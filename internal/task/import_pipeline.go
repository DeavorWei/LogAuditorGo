package task

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
	"gorm.io/gorm"

	"logauditorgo/internal/logparser"
	"logauditorgo/internal/matcher"
	"logauditorgo/internal/model"
	"logauditorgo/pkg/logger"
)

// 导入流水线的规模上限与并发参数。
//
// 这些常量此前散落在 600 行的 ImportLogsWithDevice 里（12.5 节"魔法值"问题），
// 现在集中定义，便于按实际硬件与数据规模统一调整。
const (
	// maxScanLineBytes 单行日志的最大字节数。
	// TASK-06: 超限即判定为格式非法，整个文件标记失败并显式报错，
	// 绝不像旧实现那样"记一行 warning 后继续用被截断的半截数据"。
	maxScanLineBytes = 1024 * 1024

	// importChunkLines 单批解析并落库的日志行数。
	// TASK-04: 旧实现把全部行预分配成一个大切片并塞进单一事务，
	// 超大文件既撑爆堆内存，又让 SQLite 长时间持有 WAL 锁。
	importChunkLines = 5000

	// hostnameProbeLines 用于嗅探设备 Hostname 的前置采样行数
	hostnameProbeLines = 100

	// maxRenameSeq 同名文件自动追加序号的上限 (TASK-16)。
	// 旧实现是 `for seq := 2; ; seq++` 的无界循环，极端情况下会退化成死循环。
	maxRenameSeq = 1000

	maxParseWorkers = 32
	minParseWorkers = 2

	// importJobBuffer 解析任务队列缓冲。
	// TASK-17: 旧实现 jobs/results 容量固定 2048，与批次大小无关，
	// 单条日志处理变慢时会阻塞整个 worker 池；这里让缓冲与批次对齐。
	importJobBuffer = 512
)

// devicePrefixRegex 识别文件名上已有的 "[设备名]_" 前缀。
//
// TASK-18: 旧实现只用 `strings.HasPrefix(cleanName, "[")` 判断，
// 于是不同设备的 `[A]_x.log` 与 `[B]_x.log` 都被误判为"已加前缀"而混档。
var devicePrefixRegex = regexp.MustCompile(`^\[[^\]]+\]_`)

// utf8BOM UTF-8 字节序标记
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// decodeProbeBytes 用于判定编码的探测窗口大小
const decodeProbeBytes = 64 * 1024

// fileBundle 单个待导入文件的预处理结果
type fileBundle struct {
	taskID     string
	item       FileUploadItem
	cleanName  string
	totalLines int
	deviceID   uint

	// lines 非空表示该来源无法二次读取（内存内容 / 一次性 Reader），行已被一次性载入；
	// 为空表示磁盘文件，导入阶段按块重新流式读取，内存占用与文件大小无关。
	lines []string
}

// openDecodedReader 打开一个已完成 BOM 剥离与编码转码的行读取器 (TASK-08)。
//
// 旧实现直接 `bufio.NewScanner(rc)` 后按 UTF-8 解释字节，存在两个静默错误：
//  1. UTF-8 BOM 残留在首行，导致首条日志必定解析失败；
//  2. GBK 编码的设备日志全表乱码、知识库匹配率归零。
//
// 这里先探测前 64KB：剥掉 BOM；若不是合法 UTF-8，则整流转码为 UTF-8 后再交给 Scanner。
func openDecodedReader(rc io.Reader) (io.Reader, error) {
	bufReader := bufio.NewReaderSize(rc, decodeProbeBytes)

	// 1. BOM 剥离
	if head, err := bufReader.Peek(3); err == nil && bytes.Equal(head, utf8BOM) {
		if _, err := bufReader.Discard(3); err != nil {
			return nil, fmt.Errorf("discard utf-8 bom failed: %w", err)
		}
	}

	// 2. 编码探测：读取一段样本判断是否为合法 UTF-8
	probe, err := bufReader.Peek(decodeProbeBytes)
	if err != nil && err != io.EOF && len(probe) == 0 {
		return nil, fmt.Errorf("probe file encoding failed: %w", err)
	}
	if len(probe) > 0 && !utf8.Valid(probe) {
		// GBK/GB18030 日志：整流按行转码，避免一次性载入整个文件
		return transform.NewReader(bufReader, simplifiedchinese.GB18030.NewDecoder()), nil
	}
	return bufReader, nil
}

// scanLineSource 统计有效行数并采集前 sampleN 行样本。
// 扫描错误（含单行超限）直接向上抛错，由调用方把整个文件判为失败。
func scanLineSource(r io.Reader, sampleN int) (total int, sample []string, err error) {
	scanner := bufio.NewScanner(r)
	scanBuf := make([]byte, 64*1024)
	scanner.Buffer(scanBuf, maxScanLineBytes)

	for scanner.Scan() {
		l := strings.TrimSpace(scanner.Text())
		if l == "" {
			continue
		}
		total++
		if len(sample) < sampleN {
			sample = append(sample, l)
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return 0, nil, scanErr
	}
	return total, sample, nil
}

// loadAllLines 一次性载入某个来源的全部有效行（仅用于无法二次读取的来源）
func loadAllLines(r io.Reader, sampleN int) (lines []string, sample []string, err error) {
	scanner := bufio.NewScanner(r)
	scanBuf := make([]byte, 64*1024)
	scanner.Buffer(scanBuf, maxScanLineBytes)

	for scanner.Scan() {
		l := strings.TrimSpace(scanner.Text())
		if l == "" {
			continue
		}
		lines = append(lines, l)
		if len(sample) < sampleN {
			sample = append(sample, l)
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return nil, nil, scanErr
	}
	return lines, sample, nil
}

// detectHostname 从采样行中嗅探设备主机名
func detectHostname(sample []string) string {
	for _, l := range sample {
		if n, err := logparser.ParseLine(l); err == nil && n.Hostname != "" {
			return n.Hostname
		}
	}
	return ""
}

// resolveRenameCandidate 在文件名冲突时追加序号，带上限保护 (TASK-16)
func resolveRenameCandidate(cleanName string, taken func(string) bool) (string, bool) {
	ext := filepath.Ext(cleanName)
	base := strings.TrimSuffix(cleanName, ext)
	for seq := 2; seq <= maxRenameSeq; seq++ {
		candidate := fmt.Sprintf("%s_%d%s", base, seq, ext)
		if !taken(candidate) {
			return candidate, true
		}
	}
	return "", false
}

// parseAndMatchChunk 并发解析并匹配一个批次的日志行。
//
// TASK-17: 任务队列缓冲与批次对齐，且结果按 index 就地回填，
// 单条日志处理变慢时只影响本批次，不会把整个 worker 池堵死。
func (s *Service) parseAndMatchChunk(lines []string, cleanName string, deviceID uint, deviceType, deviceVersion string) []model.LogRecord {
	records := make([]model.LogRecord, len(lines))
	if len(lines) == 0 {
		return records
	}

	workerNum := runtime.NumCPU()
	if workerNum < minParseWorkers {
		workerNum = minParseWorkers
	} else if workerNum > maxParseWorkers {
		workerNum = maxParseWorkers
	}
	if workerNum > len(lines) {
		workerNum = len(lines)
	}

	jobs := make(chan int, importJobBuffer)
	var wg sync.WaitGroup

	worker := func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				logger.Log.Errorf("[Task Service] Unexpected panic in log parse worker: %v", r)
			}
		}()
		for idx := range jobs {
			records[idx] = s.parseLogLine(lines[idx], cleanName, deviceID, deviceType, deviceVersion)
		}
	}

	wg.Add(workerNum)
	for i := 0; i < workerNum; i++ {
		go worker()
	}
	for i := range lines {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	return records
}

// parseLogLine 解析单行日志并匹配知识库，返回可直接落库的 LogRecord。
//
// 9.1: 这是 ImportLogsWithDevice 与 ReanalyzeTask 原本各自复制一份的核心逻辑（约 45 行），
// 抽取后两条链路共享同一实现，不会再出现"重分析结果与首次导入不一致"。
func (s *Service) parseLogLine(line, cleanName string, deviceID uint, deviceType, deviceVersion string) (rec model.LogRecord) {
	// 兜底：单行 panic 只影响这一行，绝不拖垮整个批次
	defer func() {
		if r := recover(); r != nil {
			logger.Log.Errorf("[Task Service] Recovered panic while parsing line: %v", r)
			rec = model.LogRecord{
				DeviceID:        deviceID,
				Module:          "UNKNOWN",
				Severity:        8,
				Brief:           "PARSE_ERROR",
				SourceFile:      cleanName,
				RawLog:          line,
				MessageBody:     line,
				ParametersJSON:  "{}",
				MatchTier:       matcher.TierUnmatch,
				MatchConfidence: 0.0,
			}
		}
	}()

	norm, err := logparser.ParseLine(line)
	if err != nil {
		norm = &model.NormalizedLog{
			RawLog:      line,
			MessageBody: line,
			Module:      "UNKNOWN",
			Brief:       "UNPARSED",
			Severity:    8, // 解析失败的日志等级设为最低级 (8)
		}
	}
	norm.SourceFile = cleanName

	var k *model.Knowledge
	var tier string
	var conf float64
	if s.matchEngine != nil {
		// PARSE-04: 版本真正透传，启用"同型号同版本优先"分档
		k, tier, conf = s.matchEngine.Match(norm, deviceType, deviceVersion)
	}
	if k != nil && k.ID > 0 {
		norm.KnowledgeID = k.ID
		norm.MatchTier = tier
		norm.MatchConfidence = conf
	} else {
		norm.KnowledgeID = 0
		norm.MatchTier = matcher.TierUnmatch
		norm.MatchConfidence = 0.0
	}

	// 兜底保障：Severity 需在 0~8 范围内（0 为 Emergency，见 PARSE-10）
	if norm.Severity < 0 || norm.Severity > 8 {
		norm.Severity = 8
	}

	paramJSONStr := "{}"
	if len(norm.Parameters) > 0 {
		if b, err := json.Marshal(norm.Parameters); err == nil {
			paramJSONStr = string(b)
		}
	}

	return model.LogRecord{
		DeviceID:        deviceID,
		Timestamp:       norm.Timestamp,
		Hostname:        norm.Hostname,
		Module:          norm.Module,
		Severity:        norm.Severity,
		Brief:           norm.Brief,
		SlotInfo:        norm.SlotInfo,
		SourceFile:      cleanName,
		RawLog:          norm.RawLog,
		MessageBody:     norm.MessageBody,
		ParametersJSON:  paramJSONStr,
		KnowledgeID:     norm.KnowledgeID,
		MatchTier:       norm.MatchTier,
		MatchConfidence: norm.MatchConfidence,
	}
}

// persistFileBundle 以"单文件"为单位完成日志落库。
//
// TASK-02 / TASK-05: 旧实现把"删旧数据"和"插入新日志"拆成两个独立事务，
// overwrite 模式下一旦插入失败，同名旧日志已被物理删除且无回滚，数据永久丢失；
// TaskFile 记录更是在入库事务之外单独 Create，失败后留下"有行无日志"的空壳，
// 后续 skip 模式命中该文件名会永久跳过，这个文件再也导不进来。
//
// 现在的边界：TaskFile 与第一批日志在同一事务内创建；
// 后续批次每 importChunkLines 行一个独立小事务（TASK-04：避免长事务持锁）；
// 任一环节失败都执行补偿清理，把 TaskFile 与该文件的已入库碎片一并删除。
func (s *Service) persistFileBundle(
	taskID string,
	taskDB *gorm.DB,
	bundle *fileBundle,
	deviceType, deviceVersion string,
	onProgress func(processed int),
) error {
	taskFile := model.TaskFile{
		TaskID:    taskID,
		FileName:  bundle.cleanName,
		FileSize:  bundle.item.FileSize,
		LineCount: bundle.totalLines,
		CreatedAt: time.Now(),
	}

	first := true
	processed := 0

	// 补偿清理：删掉该文件已入库的碎片与 TaskFile 空壳，避免留下"幽灵文件记录"
	cleanup := func() {
		if delErr := taskDB.Where("source_file = ?", bundle.cleanName).Delete(&model.LogRecord{}).Error; delErr != nil {
			logger.Log.Warnf("[Task Service] compensate delete log records of %s failed: %v", bundle.cleanName, delErr)
		}
		if taskFile.ID > 0 {
			if delErr := taskDB.Delete(&model.TaskFile{}, taskFile.ID).Error; delErr != nil {
				logger.Log.Warnf("[Task Service] compensate delete task file %s failed: %v", bundle.cleanName, delErr)
			}
		}
	}

	commit := func(records []model.LogRecord) error {
		if len(records) == 0 && !first {
			return nil
		}
		return taskDB.Transaction(func(tx *gorm.DB) error {
			if first {
				if err := tx.Create(&taskFile).Error; err != nil {
					return fmt.Errorf("create task file record failed: %w", err)
				}
			}
			if len(records) > 0 {
				if err := tx.CreateInBatches(records, 1000).Error; err != nil {
					return fmt.Errorf("batch insert log records failed: %w", err)
				}
			}
			return nil
		})
	}

	ingestChunk := func(chunk []string) error {
		records := s.parseAndMatchChunk(chunk, bundle.cleanName, bundle.deviceID, deviceType, deviceVersion)
		if err := commit(records); err != nil {
			cleanup()
			return err
		}
		processed += len(chunk)
		if onProgress != nil {
			onProgress(processed)
		}
		first = false
		return nil
	}

	// 磁盘文件：按块流式重读，内存占用与文件大小无关
	if len(bundle.lines) == 0 && bundle.item.FilePath != "" {
		f, err := os.Open(bundle.item.FilePath)
		if err != nil {
			return fmt.Errorf("open file %s for ingest failed: %w", bundle.item.FileName, err)
		}
		defer f.Close()

		dr, err := openDecodedReader(f)
		if err != nil {
			return err
		}

		chunk := make([]string, 0, importChunkLines)
		scanner := bufio.NewScanner(dr)
		scanBuf := make([]byte, 64*1024)
		scanner.Buffer(scanBuf, maxScanLineBytes)

		for scanner.Scan() {
			l := strings.TrimSpace(scanner.Text())
			if l == "" {
				continue
			}
			chunk = append(chunk, l)
			if len(chunk) >= importChunkLines {
				if err := ingestChunk(chunk); err != nil {
					return err
				}
				chunk = chunk[:0]
			}
		}
		if scanErr := scanner.Err(); scanErr != nil {
			cleanup()
			return fmt.Errorf("scan file %s failed: %w", bundle.item.FileName, scanErr)
		}
		if len(chunk) > 0 || first {
			return ingestChunk(chunk)
		}
		return nil
	}

	// 内存内容 / 一次性 Reader：按块切分已载入的行
	if len(bundle.lines) == 0 && first {
		// 空文件：仅登记 TaskFile
		return commit(nil)
	}
	for start := 0; start < len(bundle.lines); start += importChunkLines {
		end := start + importChunkLines
		if end > len(bundle.lines) {
			end = len(bundle.lines)
		}
		if err := ingestChunk(bundle.lines[start:end]); err != nil {
			return err
		}
	}
	return nil
}

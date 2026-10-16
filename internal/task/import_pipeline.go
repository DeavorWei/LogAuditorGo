package task

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
	"gorm.io/gorm"

	"logauditorgo/internal/config"
	"logauditorgo/internal/logparser"
	"logauditorgo/internal/matcher"
	"logauditorgo/internal/model"
	"logauditorgo/internal/summary"
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

// truncateIncompleteUTF8 裁切末尾可能因 Peek 截断产生的不完整 UTF-8 字符序列，
// 避免 Scanner 把半截多字节序列的最后一行误判为非法输入。
func truncateIncompleteUTF8(b []byte) []byte {
	if len(b) == 0 {
		return b
	}
	// 向后回退最多 3 个字节，定位末尾未闭合的多字节符起始点并剔除
	for i := len(b) - 1; i >= 0 && i >= len(b)-4; i-- {
		if utf8.RuneStart(b[i]) {
			c := b[i]
			expectedLen := 1
			switch {
			case c < 0x80:
				expectedLen = 1
			case c&0xE0 == 0xC0:
				expectedLen = 2
			case c&0xF0 == 0xE0:
				expectedLen = 3
			case c&0xF8 == 0xF0:
				expectedLen = 4
			}
			if len(b)-i < expectedLen {
				return b[:i]
			}
			break
		}
	}
	return b
}

// openDecodedReader 打开一个已完成 BOM 剥离与编码转码的行读取器 (TASK-08)。
//
// 这里先探测前 64KB：剥掉 BOM；若不是合法 UTF-8，则整流转码为 UTF-8 后再交给 Scanner。
// 旧实现直接 `bufio.NewScanner(rc)` 后按 UTF-8 解释字节，存在两个静默错误：
//  1. UTF-8 BOM 残留在首行，导致首条日志必定解析失败；
//  2. GBK 编码的设备日志全表乱码、知识库匹配率归零。
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
	// TASK-03: 若样本长度达到探测上限，末尾汉字多字节字符可能被截断导致 utf8.Valid 误判为 false；
	// 先行剔除末尾未完整字符后再进行合法性判定
	cleanProbe := truncateIncompleteUTF8(probe)
	if len(cleanProbe) > 0 && !utf8.Valid(cleanProbe) {
		// GBK/GB18030 日志：整流按行转码，避免一次性载入整个文件
		return transform.NewReader(bufReader, simplifiedchinese.GB18030.NewDecoder()), nil
	}
	return bufReader, nil
}

// scanLineSource 统计有效行数并采集前 sampleN 行样本。
// 扫描错误（含单行超限）直接向上抛错，由调用方把整个文件判为失败。
func scanLineSource(ctx context.Context, r io.Reader, sampleN int) (total int, sample []string, err error) {
	scanner := bufio.NewScanner(r)
	scanBuf := make([]byte, 64*1024)
	scanner.Buffer(scanBuf, maxScanLineBytes)

	for scanner.Scan() {
		if ctx != nil && ctx.Err() != nil {
			return 0, nil, ctx.Err()
		}
		text := scanner.Text()
		if logparser.IsBlankOrGarbageLine(text) {
			continue
		}
		l := logparser.SanitizeLogLine(text)
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
	if ctx != nil && ctx.Err() != nil {
		return 0, nil, ctx.Err()
	}
	return total, sample, nil
}

// loadAllLines 一次性载入某个来源的全部有效行（仅用于无法二次读取的来源）
func loadAllLines(ctx context.Context, r io.Reader, sampleN int) (lines []string, sample []string, err error) {
	scanner := bufio.NewScanner(r)
	scanBuf := make([]byte, 64*1024)
	scanner.Buffer(scanBuf, maxScanLineBytes)

	for scanner.Scan() {
		if ctx != nil && ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		text := scanner.Text()
		if logparser.IsBlankOrGarbageLine(text) {
			continue
		}
		l := logparser.SanitizeLogLine(text)
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
	if ctx != nil && ctx.Err() != nil {
		return nil, nil, ctx.Err()
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

// getEffectiveParseWorkers 计算当前有效的解析 worker 协程数。
// 优先采用全局配置 task.parse_workers；未配置时自适应当前宿主机 CPU 逻辑核心数，并钳制在 [minParseWorkers, maxParseWorkers]。
func (s *Service) getEffectiveParseWorkers() int {
	if config.GlobalConfig != nil && config.GlobalConfig.Task.ParseWorkers > 0 {
		w := config.GlobalConfig.Task.ParseWorkers
		if w < minParseWorkers {
			return minParseWorkers
		}
		if w > maxParseWorkers {
			return maxParseWorkers
		}
		return w
	}
	workerNum := runtime.NumCPU()
	if workerNum < minParseWorkers {
		return minParseWorkers
	}
	if workerNum > maxParseWorkers {
		return maxParseWorkers
	}
	return workerNum
}

// parseLogLine 解析单行日志并匹配知识库，返回可直接落库的 LogRecord。
//
// 9.1: 这是 ImportLogsWithDevice 与 ReanalyzeTask 原本各自复制一份的核心逻辑（约 45 行），
// 抽取后两条链路共享同一实现，不会再出现"重分析结果与首次导入不一致"。
func (s *Service) parseLogLine(line, cleanName string, deviceID uint, deviceType, deviceVersion string) (rec model.LogRecord) {
	// 兜底：单行 panic 只影响这一行，绝不拖垮整个批次或 Worker 协程
	// 必须置于函数第一行，确保包含后续 SanitizeLogLine 的正则及字符串分配逻辑
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

	// 纵深防御：前置确保单行必定经过清洗。SanitizeLogLine 具备 Fast-Path（纯字节扫描零内存分配），
	// 若前置 scanner 已清洗则直接瞬时幂等返回，保证 panic recover 和 ParseLine fallback 分支绝对安全。
	line = logparser.SanitizeLogLine(line)

	// 测试桩：仅在单元测试注入 hook 存在时调用，生产环境零魔数、零性能损耗 (N-A)
	if testParserPanicHook != nil {
		testParserPanicHook(line)
	}

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
	// matchedDocID 记录胜出版本映射对应的产品文档 ID，供展示层/导出直接消费，
	// 取代此前"取 Versions 中第一个非空 DocName"的猜测逻辑 (FAMILY-EXCLUSIVITY)
	var matchedDocID uint
	if s.matchEngine != nil {
		// PARSE-04: 版本真正透传，启用"同型号同版本优先"分档
		k, tier, conf = s.matchEngine.Match(norm, deviceType, deviceVersion)
	}
	if k != nil && k.ID > 0 {
		norm.KnowledgeID = k.ID
		norm.MatchTier = tier
		norm.MatchConfidence = conf
		// 同源复算：与 Match 的选优共用 bestVersionMapping，不受两级缓存短路影响
		matchedDocID = matcher.BestDocumentIDFor(k, deviceType, deviceVersion)
		if k.Message != "" {
			captured := summary.CaptureTemplateParams(k.Message, norm.MessageBody)
			if len(captured) > 0 {
				isWireless := matcher.IsWirelessLog(norm.MessageBody)
				norm.Parameters = summary.MergeCapturedParameters(norm.Parameters, captured, isWireless)
			}
		}
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
		DeviceID:          deviceID,
		Timestamp:         norm.Timestamp,
		Hostname:          norm.Hostname,
		Module:            norm.Module,
		Severity:          norm.Severity,
		Brief:             norm.Brief,
		SlotInfo:          norm.SlotInfo,
		SourceFile:        cleanName,
		RawLog:            norm.RawLog,
		MessageBody:       norm.MessageBody,
		ParametersJSON:    paramJSONStr,
		KnowledgeID:       norm.KnowledgeID,
		MatchTier:         norm.MatchTier,
		MatchConfidence:   norm.MatchConfidence,
		MatchedDocumentID: matchedDocID,
	}
}

// alignChunkTimestamps 对解析产出的日志记录进行上下文时间戳继承 (Contextual Timestamp Alignment)。
//
// 业务背景：文件头注释行（# This logfile is generated at...）、Digest 校验行、以及无法提取时间戳的残缺/注释行，
// 其解析产出的 Timestamp 均为零值（0001-01-01）。
// 若直接以零值落库，在 ORDER BY timestamp ASC 时会导致这些非正常日志全部堆积在第 1 页最前方，破坏审计体验。
// 这里对日志流维护 (lastFile, lastValidTS) 状态：
// 1. 当 SourceFile 发生切换（进入新文件）时，重置 lastValidTS，避免跨文件继承导致新文件头注释时间漂移；
// 2. 若新文件尚未确立有效时间戳（如文件开头的多行注释），向前探测借用该文件内部首条有效日志的时间；
// 3. 后续若遇到无时间戳行，继承当前文件最新的有效时间戳；
// 4. 依靠二级排序键 id asc，同一时间戳下的文件头行（id 较小）依然严格排在首条有效日志之前。
func alignChunkTimestamps(records []model.LogRecord, lastFile *string, lastValidTS *time.Time) {
	if len(records) == 0 {
		return
	}

	start := 0
	for start < len(records) {
		curFile := records[start].SourceFile
		// 寻找当前连续属于同一个 SourceFile 的切片区间 [start, end)
		end := start + 1
		for end < len(records) && records[end].SourceFile == curFile {
			end++
		}

		// 跨批次/跨分块维护文件归属状态：当进入新文件时重置时钟
		if lastFile != nil {
			if *lastFile == "" {
				*lastFile = curFile
			} else if *lastFile != curFile {
				*lastFile = curFile
				if lastValidTS != nil {
					*lastValidTS = time.Time{} // 文件边界切换，重置时钟
				}
			}
		}

		subRecords := records[start:end]

		// 1. 若当前文件尚未确立有效时间戳，先向前寻找本批次内该文件的首个有效时间戳
		if lastValidTS == nil || lastValidTS.IsZero() {
			var firstValid time.Time
			for i := range subRecords {
				if !subRecords[i].Timestamp.IsZero() && subRecords[i].Timestamp.Year() > 1970 {
					firstValid = subRecords[i].Timestamp
					break
				}
			}
			if !firstValid.IsZero() {
				for i := range subRecords {
					if subRecords[i].Timestamp.IsZero() || subRecords[i].Timestamp.Year() <= 1970 {
						subRecords[i].Timestamp = firstValid
					} else {
						break
					}
				}
				if lastValidTS != nil {
					*lastValidTS = firstValid
				}
			}
		}

		// 2. 依次向后扫描：有效行更新 lastValidTS，无时间行继承当前最新的 lastValidTS
		for i := range subRecords {
			if subRecords[i].Timestamp.IsZero() || subRecords[i].Timestamp.Year() <= 1970 {
				if lastValidTS != nil && !lastValidTS.IsZero() {
					subRecords[i].Timestamp = *lastValidTS
				}
			} else if lastValidTS != nil {
				*lastValidTS = subRecords[i].Timestamp
			}
		}

		start = end
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
	ctx context.Context,
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

	// 补偿清理：删掉该文件已入库的碎片与 TaskFile 空壳，避免留下"幽灵文件记录"
	cleanup := func() {
		_ = taskDB.Exec("DELETE FROM log_tag_relations WHERE log_id IN (SELECT id FROM log_records WHERE source_file = ?)", bundle.cleanName).Error
		if delErr := taskDB.Where("source_file = ?", bundle.cleanName).Delete(&model.LogRecord{}).Error; delErr != nil {
			logger.Log.Warnf("[Task Service] compensate delete log records of %s failed: %v", bundle.cleanName, delErr)
		}
		// 按业务主键 (task_id, file_name) 删除，不依赖局部变量 ID，天然幂等且与创建路径解耦 (M1)
		if delErr := taskDB.Where("task_id = ? AND file_name = ?", taskID, bundle.cleanName).Delete(&model.TaskFile{}).Error; delErr != nil {
			logger.Log.Warnf("[Task Service] compensate delete task file %s failed: %v", bundle.cleanName, delErr)
		}
	}

	if ctx == nil {
		ctx = context.Background()
	}

	cfg := PipelineConfig{
		WorkerNum:     s.getEffectiveParseWorkers(),
		BatchSize:     importChunkLines,
		FlushInterval: 500 * time.Millisecond,
	}

	// 1. 空文件特判（无行）：仅登记 TaskFile (P3-3 防御性检查 ctx)
	if bundle.totalLines == 0 && len(bundle.lines) == 0 {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return taskDB.Transaction(func(tx *gorm.DB) error {
			return tx.Create(&taskFile).Error
		})
	}

	var ingestErr error

	// 2. 磁盘文件：流式接入 IngestStreamingPipeline
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

		lineSource := makeFileReaderLineSource(dr)
		ingestErr = s.IngestStreamingPipeline(
			ctx,
			taskDB,
			lineSource,
			bundle,
			deviceType,
			deviceVersion,
			cfg,
			onProgress,
		)
	} else {
		// 3. 内存内容 / 一次性 Reader：切片流式接入 IngestStreamingPipeline
		lineSource := makeSliceLineSource(bundle.lines)
		ingestErr = s.IngestStreamingPipeline(
			ctx,
			taskDB,
			lineSource,
			bundle,
			deviceType,
			deviceVersion,
			cfg,
			onProgress,
		)
	}

	if ingestErr != nil {
		cleanup()
		return ingestErr
	}

	return nil
}

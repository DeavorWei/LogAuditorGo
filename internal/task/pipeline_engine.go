package task

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"runtime"
	"sync"
	"time"

	"gorm.io/gorm"

	"logauditorgo/internal/logparser"
	"logauditorgo/internal/matcher"
	"logauditorgo/internal/model"
	"logauditorgo/pkg/logger"
)

// PipelineConfig 管道参数配置
type PipelineConfig struct {
	WorkerNum     int           // 并发 Worker 数 (0 => NumCPU, 夹在 [minParseWorkers, maxParseWorkers])
	LineChanCap   int           // 原始行通道容量 (如 4096)
	RecordChanCap int           // 解析结果通道容量 (如 8192)
	BatchSize     int           // 数据库批量落库条数 (如 5000, 与现有 importChunkLines 对齐)
	FlushInterval time.Duration // 强制刷盘时间窗口 (如 500ms)
}

// indexedLine 带原始行序号的日志行。
// seq 由 Reader 单调递增分配，是"文件行序"的唯一权威来源；
// Committer 依赖它把并发乱序的解析结果重排回文件行序，
// 否则 alignChunkTimestamps 的"前序时间戳继承"语义会被破坏。
type indexedLine struct {
	seq  int
	text string
}

// seqRecord 带序号的解析结果
type seqRecord struct {
	seq int
	rec model.LogRecord
}

// 仅供包内单元测试故障注入使用；生产环境恒为 nil，避免数据路径硬编码测试魔数 (N-A)
var (
	testParserPanicHook func(line string)
	testWorkerPanicHook func(text string)
)

// IngestStreamingPipeline 执行三级流式流水线导入。
//
// 架构特征：
// 1. Stage 1 (Reader): 流式逐行扫描与清洗，分配单调自增序号 seq；
// 2. Stage 2 (Workers): NumCPU 并发协程池竞争解析，带 panic recover；
// 3. Stage 3 (Committer): 按 seq 重排缓冲（Reorder Buffer），按 batch / 定时器批量落库；
// 4. 首个 batch 与 taskFile 创建在同一事务内，保留 TASK-02/TASK-05 事务完整性；
// 5. 失败时双向取消并 drain 消费，彻底杜绝 Worker 阻塞与 Goroutine 泄漏。
func (s *Service) IngestStreamingPipeline(
	ctx context.Context,
	taskDB *gorm.DB,
	lineSource func(lineChan chan<- indexedLine, ctx context.Context) error,
	bundle *fileBundle,
	deviceType, deviceVersion string,
	cfg PipelineConfig,
	onProgress func(processed int),
) error {
	if cfg.WorkerNum <= 0 {
		cfg.WorkerNum = runtime.NumCPU()
	}
	if cfg.WorkerNum < minParseWorkers {
		cfg.WorkerNum = minParseWorkers
	} else if cfg.WorkerNum > maxParseWorkers {
		cfg.WorkerNum = maxParseWorkers
	}
	if cfg.LineChanCap <= 0 {
		cfg.LineChanCap = 4096
	}
	if cfg.RecordChanCap <= 0 {
		cfg.RecordChanCap = 8192
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = importChunkLines
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 500 * time.Millisecond
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	rawLineChan := make(chan indexedLine, cfg.LineChanCap)
	recordChan := make(chan seqRecord, cfg.RecordChanCap)

	// ---- Stage 1: Reader 协程 ----
	var readErr error
	go func() {
		defer close(rawLineChan)
		readErr = lineSource(rawLineChan, ctx)
	}()

	// ---- Stage 2: 并发 Worker 池 ----
	var workerWg sync.WaitGroup
	workerWg.Add(cfg.WorkerNum)
	for i := 0; i < cfg.WorkerNum; i++ {
		go func() {
			defer workerWg.Done()
			for il := range rawLineChan {
				var rec model.LogRecord
				func() {
					defer func() {
						if r := recover(); r != nil {
							logger.Log.Errorf("[Pipeline Worker] panic recovered on line seq %d: %v", il.seq, r)
							rec = model.LogRecord{
								DeviceID:        bundle.deviceID,
								Module:          "UNKNOWN",
								Severity:        8,
								Brief:           "PARSE_PANIC",
								SourceFile:      bundle.cleanName,
								RawLog:          il.text,
								MessageBody:     il.text,
								ParametersJSON:  "{}",
								MatchTier:       matcher.TierUnmatch,
								MatchConfidence: 0.0,
							}
						}
					}()
					if testWorkerPanicHook != nil {
						testWorkerPanicHook(il.text)
					}
					rec = s.parseLogLine(il.text, bundle.cleanName, bundle.deviceID, deviceType, deviceVersion)
				}()
				select {
				case <-ctx.Done():
					return
				case recordChan <- seqRecord{seq: il.seq, rec: rec}:
				}
			}
		}()
	}
	go func() {
		workerWg.Wait()
		close(recordChan)
	}()

	// ---- Stage 3: Committer（按 seq 重排 + 批量落库）----
	taskFile := model.TaskFile{
		TaskID:    bundle.taskID,
		FileName:  bundle.cleanName,
		FileSize:  bundle.item.FileSize,
		LineCount: bundle.totalLines,
		CreatedAt: time.Now(),
	}

	var commitErr error
	totalCommitted := 0
	first := true
	batch := make([]model.LogRecord, 0, cfg.BatchSize)
	lastFile := bundle.cleanName
	var lastValidTS time.Time

	// reorderBuf 暂存"已到达但序号尚未轮到"的记录。
	// 在途记录总数 <= LineChanCap + WorkerNum + RecordChanCap，缓冲天然有界。
	reorderBuf := make(map[int]model.LogRecord, cfg.RecordChanCap)
	nextSeq := 0

	flush := func() error {
		if len(batch) == 0 && !first {
			return nil
		}
		alignChunkTimestamps(batch, &lastFile, &lastValidTS)
		err := taskDB.Transaction(func(tx *gorm.DB) error {
			if first {
				if err := tx.Create(&taskFile).Error; err != nil {
					return fmt.Errorf("create task file record failed: %w", err)
				}
			}
			if len(batch) > 0 {
				if err := tx.CreateInBatches(batch, 1000).Error; err != nil {
					return fmt.Errorf("batch insert log records failed: %w", err)
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		totalCommitted += len(batch)
		first = false
		if onProgress != nil {
			onProgress(totalCommitted)
		}
		batch = batch[:0]
		return nil
	}

	deliver := func(rec seqRecord) {
		if rec.seq != nextSeq {
			reorderBuf[rec.seq] = rec.rec
			return
		}
		batch = append(batch, rec.rec)
		nextSeq++
		for {
			pending, ok := reorderBuf[nextSeq]
			if !ok {
				break
			}
			delete(reorderBuf, nextSeq)
			batch = append(batch, pending)
			nextSeq++
		}
	}

	ticker := time.NewTicker(cfg.FlushInterval)
	defer ticker.Stop()

consumeLoop:
	for {
		select {
		case <-ctx.Done():
			break consumeLoop
		case rec, ok := <-recordChan:
			if !ok {
				break consumeLoop
			}
			deliver(rec)
			if len(batch) >= cfg.BatchSize {
				// N2 加固 (闭环 P1-2)：若当前文件尚未获取到任何有效时间戳，且本批次内全无有效时间戳，
				// 暂缓本次满批落库，等待首条有效时间戳到达，防止文件头部无时间戳注释行因达到 BatchSize 提前刷盘
				// 而导致首批记录以 0001-01-01 零值固化落库（后续批次即使产生有效时间戳也无法回溯修正已提交记录）。
				// 为避免极端全无时间戳的病态文件导致 batch 无限增长，设置安全上限（cfg.BatchSize * 10）兜底防爆内存。
				if lastValidTS.IsZero() && !hasAnyValidTimestamp(batch) && len(batch) < cfg.BatchSize*10 {
					continue
				}
				if err := flush(); err != nil {
					commitErr = err
					break consumeLoop
				}
			}
		case <-ticker.C:
			if len(batch) > 0 {
				// N2 保护：若当前文件尚未获取到任何有效时间戳，且本批次内也全无有效时间戳，
				// 则暂缓本次定时 flush，等待首条有效时间戳到达或达到安全上限/EOF，防止头部无时间戳注释行因定时刷盘而无法完成时间戳向前借用对齐。
				if lastValidTS.IsZero() && !hasAnyValidTimestamp(batch) && len(batch) < cfg.BatchSize*10 {
					continue
				}
				if err := flush(); err != nil {
					commitErr = err
					break consumeLoop
				}
			}
		}
	}

	if commitErr != nil {
		cancel()
		for range recordChan {
		}
		return commitErr
	}

	if ctx.Err() != nil {
		cancel()
		for range recordChan {
		}
		return ctx.Err()
	}

	if readErr != nil {
		cancel()
		for range recordChan {
		}
		return readErr
	}

	if len(reorderBuf) > 0 {
		return fmt.Errorf("pipeline integrity violated: %d records stuck in reorder buffer", len(reorderBuf))
	}
	if len(batch) > 0 || first {
		if err := flush(); err != nil {
			return err
		}
	}

	return nil
}

// hasAnyValidTimestamp 检查日志切片中是否包含至少一条有效时间戳
func hasAnyValidTimestamp(records []model.LogRecord) bool {
	for i := range records {
		if !records[i].Timestamp.IsZero() && records[i].Timestamp.Year() > 1970 {
			return true
		}
	}
	return false
}

// makeFileReaderLineSource 构建从 io.Reader 流式产出 indexedLine 的源函数
func makeFileReaderLineSource(r io.Reader) func(lineChan chan<- indexedLine, ctx context.Context) error {
	return func(lineChan chan<- indexedLine, ctx context.Context) error {
		scanner := bufio.NewScanner(r)
		scanBuf := make([]byte, 64*1024)
		scanner.Buffer(scanBuf, maxScanLineBytes)

		seq := 0
		for scanner.Scan() {
			text := scanner.Text()
			if logparser.IsBlankOrGarbageLine(text) {
				continue
			}
			l := logparser.SanitizeLogLine(text)
			if l == "" {
				continue
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case lineChan <- indexedLine{seq: seq, text: l}:
				seq++
			}
		}
		if scanErr := scanner.Err(); scanErr != nil {
			return scanErr
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return nil
	}
}

// makeSliceLineSource 构建从字符串切片流式产出 indexedLine 的源函数
func makeSliceLineSource(lines []string) func(lineChan chan<- indexedLine, ctx context.Context) error {
	return func(lineChan chan<- indexedLine, ctx context.Context) error {
		seq := 0
		for _, l := range lines {
			if logparser.IsBlankOrGarbageLine(l) {
				continue
			}
			clean := logparser.SanitizeLogLine(l)
			if clean == "" {
				continue
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case lineChan <- indexedLine{seq: seq, text: clean}:
				seq++
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return nil
	}
}


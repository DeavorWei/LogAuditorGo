package knowledge

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"logauditorgo/internal/fsx"
	"logauditorgo/internal/hdx"
	"logauditorgo/internal/matcher"
	"logauditorgo/internal/model"
	"logauditorgo/internal/search"
	"logauditorgo/pkg/logger"
	"logauditorgo/pkg/progress"
)

// HDXImportStages HDX 产品文档知识库导入全流程预设阶段
var HDXImportStages = []progress.StageDef{
	// 实际运行时会按输入类型改写名称：压缩包 -> "读取压缩包索引（流式读取，无需解压）"，
	// 已解压目录 -> "文件扫描（无需解压）"
	{Key: "UPLOAD", Name: "文件扫描与索引构建"},
	{Key: "SCAN", Name: "扫描文档结构"},
	{Key: "META", Name: "解析导航与元数据"},
	{Key: "HTML_PARSE", Name: "并发解析知识条目"},
	{Key: "PERSIST", Name: "去重与数据库写入"},
	{Key: "INDEX", Name: "构建全文检索索引"},
	{Key: "COMPLETE", Name: "导入完成"},
}

type ImportStats struct {
	TotalDocuments       int           `json:"total_documents"`
	DocumentID           uint          `json:"document_id,omitempty"`
	LibID                string        `json:"lib_id,omitempty"`
	ProductType          string        `json:"product_type,omitempty"`
	ProductVersion       string        `json:"product_version,omitempty"`
	TotalTopicsInProfile int           `json:"total_topics_in_profile"`
	LeafLogCount         int           `json:"leaf_log_count"`
	LeafAlarmCount       int           `json:"leaf_alarm_count"`
	UniqueKnowledgeAdded int           `json:"unique_knowledge_added"`
	VersionMappingsAdded int           `json:"version_mappings_added"`
	Duration             time.Duration `json:"duration"`
	ImportedDocs         []string      `json:"imported_docs,omitempty"`
	SkippedDocs          []string      `json:"skipped_docs,omitempty"`
	FailedDocs           []string      `json:"failed_docs,omitempty"`
	Skipped              bool          `json:"skipped,omitempty"`
}

// ReloadableEngine 定义可重载匹配引擎接口
type ReloadableEngine interface {
	Reload()
}

type Service struct {
	db          *gorm.DB
	indexer     *search.Indexer
	matchEngine ReloadableEngine
	docLocks    sync.Map
	extractDir  string // 导入 HDX 压缩包时的解压工作目录
}

func NewService(db *gorm.DB, indexer ...*search.Indexer) *Service {
	svc := &Service{db: db}
	if len(indexer) > 0 && indexer[0] != nil {
		svc.indexer = indexer[0]
	}
	return svc
}

func (s *Service) SetIndexer(indexer *search.Indexer) {
	s.indexer = indexer
}

func (s *Service) SetMatchEngine(engine ReloadableEngine) {
	s.matchEngine = engine
}

// SetExtractDir 设置导入 HDX 压缩包时的解压工作目录（未设置时回退到系统临时目录）。
//
// KB-16: 压缩包现已默认走"读中央目录 + 内存索引"的流式读取，不再落盘，
// 该目录仅在流式读取不可用（加密包、损坏包等）回退为全量解压时才会被使用。
func (s *Service) SetExtractDir(dir string) {
	s.extractDir = dir
}

func (s *Service) getExtractDir() string {
	if s.extractDir != "" {
		return s.extractDir
	}
	return os.TempDir()
}

func (s *Service) getDocLock(key string) *sync.Mutex {
	actual, _ := s.docLocks.LoadOrStore(key, &sync.Mutex{})
	return actual.(*sync.Mutex)
}

// ImportOption 定义导入配置的 Functional Option
type ImportOption func(*ImportOptions)

// ImportOptions 导入文档配置
type ImportOptions struct {
	ConflictMode string
	Tracker      *progress.JobTracker
}

// WithConflictMode 设置文档冲突处理策略 ("overwrite" 或 "skip")
func WithConflictMode(mode string) ImportOption {
	return func(opts *ImportOptions) {
		if mode != "" {
			opts.ConflictMode = mode
		}
	}
}

// WithTracker 设置导入进度跟踪器 JobTracker
func WithTracker(tracker *progress.JobTracker) ImportOption {
	return func(opts *ImportOptions) {
		opts.Tracker = tracker
	}
}

type parsedResult struct {
	knowledge *model.Knowledge
	item      hdx.LeafNaviItem
	err       error
}

// ImportDocumentFromDir 从本地目录导入 HDX 知识库（支持进度追踪 Tracker 回调）
// 支持直接指定单个文档目录，或指定包含多个文档包的父级目录（程序自动递归发现所有文档并批量导入）
// 参数 options 支持 Functional Options (WithConflictMode, WithTracker) 以及向前兼容的 string / *progress.JobTracker
// ImportDocumentFromDir 导入单个目录下的 HDX 文档，并在结束时自动完成进度追踪
func (s *Service) ImportDocumentFromDir(dirPath string, options ...interface{}) (*ImportStats, error) {
	opts := &ImportOptions{
		ConflictMode: "overwrite",
		Tracker:      nil,
	}

	for _, opt := range options {
		switch v := opt.(type) {
		case ImportOption:
			if v != nil {
				v(opts)
			}
		case func(*ImportOptions):
			if v != nil {
				v(opts)
			}
		case *ImportOptions:
			if v != nil {
				*opts = *v
			}
		case string:
			if v != "" {
				opts.ConflictMode = v
			}
		case *progress.JobTracker:
			opts.Tracker = v
		}
	}

	return s.importFromDir(dirPath, opts.ConflictMode, opts.Tracker, true)
}

// isArchiveFile 判断路径是否为 HDX / ZIP 压缩包
func isArchiveFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".hdx" || ext == ".zip"
}

// prepareImportTargets 在实际导入前完成来源预处理：
//   - 全部为已解压目录时：把首个阶段名改写为"文件扫描（无需解压）"，直接返回目录来源
//   - 含 .hdx / .zip 压缩包时：仅读取中央目录建立内存索引，返回包内文档根来源。
//     全程不解压、不落盘；仅当压缩包无法流式读取（加密、损坏等）时才回退为全量解压。
//
// 返回值依次为：待导入的文档来源列表、预处理阶段的失败描述、清理函数。
//
// cleanup 必须由调用方通过 defer 执行，它负责：
//   - 关闭全部 ArchiveSource 句柄。Windows 下不显式 Close 会持有只读文件句柄，
//     导致导入完成后用户无法移动或重命名原始 .hdx；
//   - 清理回退解压产生的临时目录。
//
// defer 保证正常完成、报错、panic 三条路径都能第一时间释放句柄。
func (s *Service) prepareImportTargets(paths []string, tr *progress.JobTracker) ([]hdx.DocSource, []string, func()) {
	noop := func() {}

	// 解压相关接口要求非空的 tracker，此处兜底一个独立实例避免空指针
	if tr == nil {
		tr = progress.NewJobTracker("", "", "hdx", nil)
	}

	var dirs []string
	var archives []string
	for _, raw := range paths {
		clean, err := fsx.Normalize(raw)
		if err != nil {
			continue
		}
		info, err := os.Stat(fsx.LongPathSafe(clean))
		if err != nil {
			tr.AddLog("warning", "路径不存在或无法访问，已跳过: %s", clean)
			continue
		}
		if info.IsDir() {
			// 智能递归扫描：自动发现该目录下的所有 .hdx 压缩包与 profile.xml 解压文档包
			scanRes, scanErr := hdx.ScanHDXDirectory(clean)
			if scanErr != nil || scanRes.TotalCount == 0 {
				dirs = append(dirs, clean)
				continue
			}
			for _, item := range scanRes.Items {
				if item.Type == "archive" {
					archives = append(archives, item.Path)
				} else {
					dirs = append(dirs, item.Path)
				}
			}
			continue
		}
		if isArchiveFile(clean) {
			archives = append(archives, clean)
			continue
		}
		tr.AddLog("warning", "不支持的文件类型（仅支持 .hdx / .zip 压缩包或文档目录），已跳过: %s", filepath.Base(clean))
	}

	scanFailures := make([]string, 0)

	// expandDirs 把已解压目录展开为文档根来源（沿用传统目录扫描逻辑）
	expandDirs := func(list []string) []hdx.DocSource {
		out := make([]hdx.DocSource, 0, len(list))
		for _, d := range list {
			docDirs, err := hdx.FindHDXDocDirs(d)
			if err != nil {
				logger.Log.Warnf("[Knowledge Service] Scan path %s failed: %v", d, err)
				scanFailures = append(scanFailures, fmt.Sprintf("%s: %v", filepath.Base(d), err))
				tr.AddLog("warning", "路径 %s 扫描失败: %v", filepath.Base(d), err)
				continue
			}
			tr.AddLog("info", "路径 %s 中发现 %d 个 HDX 文档包", filepath.Base(d), len(docDirs))
			for _, dd := range docDirs {
				out = append(out, hdx.NewDirSource(dd))
			}
		}
		return out
	}

	// 场景一：全部为已解压目录，跳过解压环节
	if len(archives) == 0 {
		tr.SetStageName("UPLOAD", "文件扫描（无需解压）")
		tr.AddLog("info", "检测到 %d 个已解压的 HDX 文档目录，无需解压，直接进入扫描", len(dirs))
		return expandDirs(dirs), scanFailures, noop
	}

	// 场景二：存在压缩包 —— 零磁盘流式读取
	tr.SetStageName("UPLOAD", "读取压缩包索引（流式读取，无需解压）")
	tr.AddLog("info", "发现 %d 个 HDX 压缩包，正在读取中央目录并建立内存索引（不解压、不落盘）...", len(archives))

	var opened []*hdx.ArchiveSource
	var tmpDirs []string
	cleanup := func() {
		for _, a := range opened {
			_ = a.Close()
		}
		for _, d := range tmpDirs {
			_ = os.RemoveAll(d)
		}
	}

	targets := expandDirs(dirs)

	// fallbackExtract 流式读取不可用时的兜底：退回传统全量解压导入
	fallbackExtract := func(arc string) []hdx.DocSource {
		base := s.getExtractDir()
		if err := os.MkdirAll(fsx.LongPathSafe(base), 0755); err != nil {
			tr.AddLog("error", "创建解压工作目录失败: %v，跳过压缩包 %s", err, filepath.Base(arc))
			return nil
		}
		batchDir, err := os.MkdirTemp(base, "hdx_extract_")
		if err != nil {
			tr.AddLog("error", "创建解压临时目录失败: %v，跳过压缩包 %s", err, filepath.Base(arc))
			return nil
		}
		tmpDirs = append(tmpDirs, batchDir)

		// KB-13: 用系统唯一临时目录作为解压目标，避免同名压缩包互相覆盖
		dest, err := os.MkdirTemp(batchDir, "arc_")
		if err != nil {
			tr.AddLog("error", "创建解压目标目录失败: %v，跳过压缩包 %s", err, filepath.Base(arc))
			return nil
		}
		if err := hdx.UnzipConcurrent(arc, dest, tr); err != nil {
			tr.AddLog("error", "解压 %s 失败: %v", filepath.Base(arc), err)
			return nil
		}
		// 解压后的目录内可能仍嵌套压缩包，递归解压
		if err := hdx.ExtractAllArchivesWithTracker(dest, tr); err != nil {
			tr.AddLog("warning", "解压包内嵌套压缩包时出现告警: %v", err)
		}

		docDirs, err := hdx.FindHDXDocDirs(dest)
		if err != nil {
			tr.AddLog("warning", "解压后的目录 %s 中未发现 HDX 文档包: %v", filepath.Base(arc), err)
			return nil
		}
		out := make([]hdx.DocSource, 0, len(docDirs))
		for _, dd := range docDirs {
			out = append(out, hdx.NewDirSource(dd))
		}
		tr.AddLog("info", "已通过全量解压兜底导入: %s", filepath.Base(arc))
		return out
	}

	for _, arc := range archives {
		a, err := hdx.OpenArchive(arc)
		if err != nil {
			logger.Log.Warnf("[Knowledge Service] Stream open archive %s failed, fallback to full extract: %v", arc, err)
			tr.AddLog("warning", "压缩包 %s 无法流式读取 (%v)，已回退为全量解压导入", filepath.Base(arc), err)
			targets = append(targets, fallbackExtract(arc)...)
			continue
		}

		opened = append(opened, a)
		roots := a.DocRoots(tr)
		if len(roots) == 0 {
			tr.AddLog("warning", "压缩包 %s 内未发现任何 HDX 文档包（缺少 profile.xml），已跳过", filepath.Base(arc))
			continue
		}
		targets = append(targets, roots...)
		tr.AddLog("info", "压缩包索引构建完成（流式读取，零磁盘占用）: %s，包内条目 %d，文档包 %d",
			filepath.Base(arc), a.EntryCount(), len(roots))
	}

	// 读取中央目录通常在毫秒级完成，这里直接把 UPLOAD 阶段推至 100%，
	// 用户视觉重心将平滑过渡到并发解析条目的 HTML_PARSE 阶段。
	tr.UpdateProgress(int64(len(archives)), int64(len(archives)),
		fmt.Sprintf("压缩包索引构建完成（流式读取，零磁盘占用），共定位 %d 个文档包", len(targets)))

	return targets, scanFailures, cleanup
}

// ImportDocumentsFromPaths 批量导入多个路径（目录或压缩包）下的 HDX 文档，
// 统一聚合统计结果并共用同一个进度追踪器，由本函数在全部完成后统一结束进度。
func (s *Service) ImportDocumentsFromPaths(paths []string, conflictMode string, tr *progress.JobTracker) (*ImportStats, error) {
	if conflictMode == "" {
		conflictMode = "overwrite"
	}

	startTime := time.Now()

	// 路径预处理：按需解压，并同步修正首个阶段的显示名称
	if tr != nil {
		tr.SetStage("UPLOAD", "正在分析导入路径...")
	}
	// 来源预处理：压缩包走"读中央目录 + 内存索引"的流式读取（零磁盘占用），
	// 目录走传统目录扫描。cleanup 负责关闭全部压缩包句柄并清理回退解压的临时目录。
	targets, scanFailures, cleanupSources := s.prepareImportTargets(paths, tr)
	// 句柄释放必须兜底：正常完成、报错、panic 三条路径都要第一时间归还文件句柄
	defer cleanupSources()

	if len(targets) == 0 {
		err := fmt.Errorf("no valid import paths found")
		if tr != nil {
			tr.Fail(err, "没有找到可导入的有效路径")
		}
		return nil, err
	}

	// 2. 先汇总所有文档包，才能以"第 N/M 个文档包"驱动整体进度
	if tr != nil {
		tr.EnableForwardOnlyStages()
		tr.SetStage("SCAN", fmt.Sprintf("已定位 %d 个 HDX 文档包，准备逐个导入...", len(targets)))
		tr.AddLog("info", "共定位 %d 个 HDX 文档包，开始逐个导入", len(targets))
	}

	agg := &ImportStats{
		TotalDocuments: len(targets),
		ImportedDocs:   make([]string, 0, len(targets)),
		SkippedDocs:    make([]string, 0),
		FailedDocs:     make([]string, 0),
	}
	agg.FailedDocs = append(agg.FailedDocs, scanFailures...)

	var lastErr error
	successCount := 0
	for docIdx, src := range targets {
		if tr != nil {
			tr.SetOverallProgress(
				float64(docIdx)/float64(len(targets))*95,
				fmt.Sprintf("第 %d/%d 个文档包", docIdx+1, len(targets)),
			)
			tr.AddLog("info", "开始处理第 %d/%d 个文档包: %s", docIdx+1, len(targets), src.Label())
		}

		st, err := s.importSingleDocUnlocked(src, conflictMode, tr)
		if err != nil {
			logger.Log.Errorf("[Knowledge Service] Failed to import document %s: %v", src.Origin(), err)
			agg.FailedDocs = append(agg.FailedDocs, fmt.Sprintf("%s: %v", src.Label(), err))
			lastErr = err
			if tr != nil {
				tr.AddLog("error", "文档包 %s 解析失败: %v", src.Label(), err)
			}
			continue
		}

		if st.Skipped {
			docLabel := fmt.Sprintf("%s (%s %s)", st.LibID, st.ProductType, st.ProductVersion)
			agg.SkippedDocs = append(agg.SkippedDocs, docLabel)
			if tr != nil {
				tr.AddLog("warning", "已跳过已存在的文档包: %s", docLabel)
			}
			continue
		}

		successCount++
		if successCount == 1 {
			agg.DocumentID = st.DocumentID
			agg.LibID = st.LibID
			agg.ProductType = st.ProductType
			agg.ProductVersion = st.ProductVersion
		}

		docLabel := fmt.Sprintf("%s (%s %s)", st.LibID, st.ProductType, st.ProductVersion)
		agg.ImportedDocs = append(agg.ImportedDocs, docLabel)
		agg.TotalTopicsInProfile += st.TotalTopicsInProfile
		agg.LeafLogCount += st.LeafLogCount
		agg.LeafAlarmCount += st.LeafAlarmCount
		agg.UniqueKnowledgeAdded += st.UniqueKnowledgeAdded
		agg.VersionMappingsAdded += st.VersionMappingsAdded
	}

	agg.Duration = time.Since(startTime)

	imported := len(agg.ImportedDocs)
	if imported > 1 {
		agg.LibID = fmt.Sprintf("Batch (%d docs)", imported)
		agg.DocumentID = 0
		agg.ProductType = ""
		agg.ProductVersion = ""
	} else if imported == 0 && len(agg.SkippedDocs) > 0 {
		agg.LibID = fmt.Sprintf("Skipped (%d docs)", len(agg.SkippedDocs))
	}

	if imported == 0 && len(agg.SkippedDocs) == 0 {
		err := lastErr
		if err == nil {
			err = fmt.Errorf("no documents imported")
		}
		if tr != nil {
			tr.Fail(err, fmt.Sprintf("未能成功导入任何文档: %v", err))
		}
		return nil, fmt.Errorf("failed to import any documents: %w", err)
	}

	// KB-14: 全部文档都被 skip（例如重复导入同一批包）时，
	// 原实现既不 Fail 也不 Complete，前端进度弹窗会永远卡在最后一个阶段。
	if imported == 0 && tr != nil {
		tr.SetOverallProgress(100, fmt.Sprintf("共 %d 个文档包处理完毕", len(targets)))
		tr.SetStage("COMPLETE", "全部文档包均已存在，无需重复导入")
		tr.Complete(agg, fmt.Sprintf("未导入新文档：%d 个文档包均已存在于知识库中，如需重建请使用「重建索引」", len(agg.SkippedDocs)))
	}

	if imported > 0 && s.matchEngine != nil {
		s.matchEngine.Reload()
		logger.Log.Infof("[Knowledge Service] Successfully reloaded match engine after batch knowledge import")
	}

	logger.Log.Infof("Completed multi-path import of %d/%d documents (%d skipped, %d failed) across %d paths in %v",
		imported, agg.TotalDocuments, len(agg.SkippedDocs), len(agg.FailedDocs), len(paths), agg.Duration)

	// 注：imported == 0 的终态已在上面的 KB-14 分支中 Complete，此处避免重复触发终态
	if tr != nil && imported > 0 {
		tr.SetOverallProgress(100, fmt.Sprintf("共 %d 个文档包处理完毕", len(targets)))
		tr.SetStage("COMPLETE", "HDX 官方产品文档导入已全部完成")
		tr.Complete(agg, fmt.Sprintf("导入完成！已成功入库 %d 个文档包，提取叶子日志 %d 条，告警 %d 条，新增知识 %d 条",
			imported, agg.LeafLogCount, agg.LeafAlarmCount, agg.UniqueKnowledgeAdded))
	}

	return agg, nil
}

// importFromDir 导入单个路径（目录或压缩包）下的 HDX 文档。
// finalize 为 false 时不结束进度追踪，以便批量导入由调用方统一聚合与收尾。
func (s *Service) importFromDir(dirPath string, mode string, tr *progress.JobTracker, finalize bool) (*ImportStats, error) {
	startTime := time.Now()

	if tr != nil {
		tr.SetStage("UPLOAD", "正在分析导入路径...")
	}

	// 与批量导入共用同一套来源预处理：目录走扫描，压缩包走流式读取
	targets, scanFailures, cleanupSources := s.prepareImportTargets([]string{dirPath}, tr)
	// 句柄释放必须兜底：正常完成、报错、panic 三条路径都要第一时间归还文件句柄
	defer cleanupSources()

	if len(targets) == 0 {
		err := fmt.Errorf("no HDX document packages found in: %s", dirPath)
		if tr != nil {
			tr.Fail(err, fmt.Sprintf("未在 %s 中发现任何 HDX 文档包", filepath.Base(dirPath)))
		}
		return nil, err
	}

	if tr != nil {
		tr.SetStage("SCAN", fmt.Sprintf("已定位 %d 个 HDX 文档包，准备逐个导入...", len(targets)))
		tr.AddLog("info", "发现 %d 个 HDX 官方文档包", len(targets))
	}

	totalStats := &ImportStats{
		TotalDocuments: len(targets),
		ImportedDocs:   make([]string, 0, len(targets)),
		SkippedDocs:    make([]string, 0),
		FailedDocs:     make([]string, 0),
	}
	totalStats.FailedDocs = append(totalStats.FailedDocs, scanFailures...)

	var firstDocID uint
	var firstLibID, firstProductType, firstProductVersion string
	var lastErr error
	successCount := 0

	for docIdx, src := range targets {
		if tr != nil {
			tr.AddLog("info", "开始处理第 %d/%d 个文档包: %s", docIdx+1, len(targets), src.Label())
		}

		st, err := s.importSingleDocUnlocked(src, mode, tr)
		if err != nil {
			logger.Log.Errorf("[Knowledge Service] Failed to import document %s: %v", src.Origin(), err)
			totalStats.FailedDocs = append(totalStats.FailedDocs, fmt.Sprintf("%s: %v", src.Label(), err))
			lastErr = err
			if tr != nil {
				tr.AddLog("error", "文档包 %s 解析失败: %v", src.Label(), err)
			}
			continue
		}

		if st.Skipped {
			docLabel := fmt.Sprintf("%s (%s %s)", st.LibID, st.ProductType, st.ProductVersion)
			totalStats.SkippedDocs = append(totalStats.SkippedDocs, docLabel)
			if tr != nil {
				tr.AddLog("warning", "已跳过已存在的文档包: %s", docLabel)
			}
			continue
		}

		successCount++
		if successCount == 1 {
			firstDocID = st.DocumentID
			firstLibID = st.LibID
			firstProductType = st.ProductType
			firstProductVersion = st.ProductVersion
		}

		docLabel := fmt.Sprintf("%s (%s %s)", st.LibID, st.ProductType, st.ProductVersion)
		totalStats.ImportedDocs = append(totalStats.ImportedDocs, docLabel)
		totalStats.TotalTopicsInProfile += st.TotalTopicsInProfile
		totalStats.LeafLogCount += st.LeafLogCount
		totalStats.LeafAlarmCount += st.LeafAlarmCount
		totalStats.UniqueKnowledgeAdded += st.UniqueKnowledgeAdded
		totalStats.VersionMappingsAdded += st.VersionMappingsAdded
	}

	if successCount == 0 && len(totalStats.SkippedDocs) == 0 && lastErr != nil {
		if tr != nil {
			tr.Fail(lastErr, fmt.Sprintf("未能成功导入任何文档: %v", lastErr))
		}
		return nil, fmt.Errorf("failed to import any documents: %w", lastErr)
	}

	if successCount == 1 {
		totalStats.DocumentID = firstDocID
		totalStats.LibID = firstLibID
		totalStats.ProductType = firstProductType
		totalStats.ProductVersion = firstProductVersion
	} else if successCount > 1 {
		totalStats.LibID = fmt.Sprintf("Batch (%d docs)", successCount)
	} else if len(totalStats.SkippedDocs) > 0 {
		totalStats.LibID = fmt.Sprintf("Skipped (%d docs)", len(totalStats.SkippedDocs))
	}

	totalStats.Duration = time.Since(startTime)
	logger.Log.Infof("Completed batch import of %d/%d documents (%d skipped) from %s in %v: %d leaf logs, %d leaf alarms, %d unique knowledge added, %d mappings",
		successCount, len(targets), len(totalStats.SkippedDocs), dirPath, totalStats.Duration, totalStats.LeafLogCount, totalStats.LeafAlarmCount, totalStats.UniqueKnowledgeAdded, totalStats.VersionMappingsAdded)

	if successCount > 0 && s.matchEngine != nil && finalize {
		s.matchEngine.Reload()
		logger.Log.Infof("[Knowledge Service] Successfully reloaded match engine after knowledge import")
	}

	if tr != nil && finalize {
		tr.SetStage("COMPLETE", "HDX 官方产品文档导入已全部完成")
		tr.Complete(totalStats, fmt.Sprintf("导入完成！已成功入库 %d 个文档包，提取叶子日志 %d 条，告警 %d 条，新增知识 %d 条",
			successCount, totalStats.LeafLogCount, totalStats.LeafAlarmCount, totalStats.UniqueKnowledgeAdded))
	}

	return totalStats, nil
}

// maxDocumentFilePathLen model.Document.FilePath 的列宽上限（gorm size:512）
const maxDocumentFilePathLen = 512

// truncateFilePath 确保来源路径不超过列宽。
// 超长时保留尾部（压缩包名与文档根比磁盘前缀更有溯源价值），并从 UTF-8 字符边界开始，避免切出乱码。
func truncateFilePath(p string) string {
	if len(p) <= maxDocumentFilePathLen {
		return p
	}
	start := len(p) - (maxDocumentFilePathLen - len("..."))
	for start < len(p) && !utf8.RuneStart(p[start]) {
		start++
	}
	return "..." + p[start:]
}

// readDocumentMetadata 阶段 1：解析 HDX 文档的 profile.xml 与 navi.xml 元数据，并处理 skip 冲突跳过逻辑
func (s *Service) readDocumentMetadata(src hdx.DocSource, conflictMode string, tr *progress.JobTracker) (*model.Document, []hdx.LeafNaviItem, *ImportStats, bool, error) {
	if tr != nil {
		tr.SetStage("META", fmt.Sprintf("正在解析 %s 的 profile.xml 与 navi.xml 元数据...", src.Label()))
	}

	// 1. 解析 profile.xml
	doc, naviRelPath, parseErr := hdx.ParseProfileXMLFrom(src)
	if parseErr != nil {
		return nil, nil, nil, false, fmt.Errorf("parse profile.xml failed: %w", parseErr)
	}

	// KB-16: FilePath 记录用户原始来源（目录绝对路径或原始 .hdx 绝对路径），
	// 不再指向导入结束即被删除的临时解压目录，保证知识条目可溯源。
	doc.FilePath = truncateFilePath(src.Origin())

	if tr != nil {
		tr.AddLog("info", "已识别文档: LibID=%s, 产品=%s, 版本=%s, 主题数=%d", doc.LibID, doc.ProductType, doc.ProductVersion, doc.TopicNumber)
	}

	// 检查冲突策略：如果为 skip 且该文档已存在，则直接跳过，避免耗时的并发 HTML 解析
	if conflictMode == "skip" {
		var existingDoc model.Document
		if dbErr := s.db.Where("lib_id = ?", doc.LibID).First(&existingDoc).Error; dbErr == nil {
			logger.Log.Infof("[Knowledge Service] Skipping already existing document: LibID=%s, Product=%s %s", doc.LibID, doc.ProductType, doc.ProductVersion)
			return doc, nil, &ImportStats{
				TotalDocuments: 1,
				DocumentID:     existingDoc.ID,
				LibID:          doc.LibID,
				ProductType:    doc.ProductType,
				ProductVersion: doc.ProductVersion,
				Skipped:        true,
			}, true, nil
		}
	}

	// 2. 解析 navi.xml 提取所有叶子节点
	leafItems, naviErr := hdx.ParseNaviXMLFrom(src, naviRelPath)
	if naviErr != nil {
		return nil, nil, nil, false, fmt.Errorf("parse navi.xml failed: %w", naviErr)
	}

	stats := &ImportStats{
		TotalDocuments:       1,
		LibID:                doc.LibID,
		ProductType:          doc.ProductType,
		ProductVersion:       doc.ProductVersion,
		TotalTopicsInProfile: doc.TopicNumber,
	}

	// 统计叶子日志与告警数量
	for _, it := range leafItems {
		if it.EntryType == model.EntryTypeLog {
			stats.LeafLogCount++
		} else {
			stats.LeafAlarmCount++
		}
	}

	if tr != nil {
		tr.AddLog("info", "导航树提取完成: 共 %d 个叶子条目 (日志 %d 条, 告警 %d 条)", len(leafItems), stats.LeafLogCount, stats.LeafAlarmCount)
	}

	return doc, leafItems, stats, false, nil
}

// parseHTMLKnowledgeItems 阶段 2：启动并发协程池提取所有 HTML 页面的知识条目并计算 ContentHash
func (s *Service) parseHTMLKnowledgeItems(src hdx.DocSource, leafItems []hdx.LeafNaviItem, tr *progress.JobTracker) ([]parsedResult, []string) {
	if tr != nil {
		tr.SetStage("HTML_PARSE", fmt.Sprintf("正在并发解析 %d 个 HTML 知识页面...", len(leafItems)))
		tr.UpdateProgress(0, int64(len(leafItems)), fmt.Sprintf("已解析 0 / %d 个知识页面", len(leafItems)))
	}

	workerNum := runtime.NumCPU() * 2
	if workerNum < 4 {
		workerNum = 4
	} else if workerNum > 32 {
		workerNum = 32
	}
	jobs := make(chan hdx.LeafNaviItem, len(leafItems))
	results := make(chan parsedResult, len(leafItems))

	var doneCount int64
	totalLeafCount := int64(len(leafItems))

	var wg sync.WaitGroup
	for i := 0; i < workerNum; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				func(it hdx.LeafNaviItem) {
					defer func() {
						if r := recover(); r != nil {
							logger.Log.Errorf("Recovered in knowledge worker goroutine: %v", r)
							results <- parsedResult{
								knowledge: nil,
								item:      it,
								err:       fmt.Errorf("panic parsing %s: %v", it.TopicID, r),
							}
							if tr != nil {
								cur := atomic.AddInt64(&doneCount, 1)
								if cur%20 == 0 || cur == totalLeafCount {
									tr.UpdateProgress(cur, totalLeafCount, fmt.Sprintf("已并发提取 HTML 知识条目: %d / %d", cur, totalLeafCount))
								}
							}
						}
					}()

					k, parseErr := hdx.ParseHTMLKnowledgeFrom(src, it)
					results <- parsedResult{knowledge: k, item: it, err: parseErr}

					if tr != nil {
						cur := atomic.AddInt64(&doneCount, 1)
						if cur%20 == 0 || cur == totalLeafCount {
							tr.UpdateProgress(cur, totalLeafCount, fmt.Sprintf("已并发提取 HTML 知识条目: %d / %d", cur, totalLeafCount))
						}
					}
				}(item)
			}
		}()
	}

	for _, item := range leafItems {
		jobs <- item
	}
	close(jobs)

	go func() {
		wg.Wait()
		close(results)
	}()

	var resList []parsedResult
	var hashes []string
	seenHashes := make(map[string]struct{})
	for res := range results {
		if res.err != nil || res.knowledge == nil {
			logger.Log.Debugf("[Knowledge Service] Skip nil or failed item: TopicID=%s, err=%v", res.item.TopicID, res.err)
			continue
		}
		k := res.knowledge
		hash := CalculateContentHash(k)
		k.ContentHash = hash
		resList = append(resList, res)
		if _, seen := seenHashes[hash]; !seen {
			seenHashes[hash] = struct{}{}
			hashes = append(hashes, hash)
		}
	}

	return resList, hashes
}

// persistKnowledgeAndMappings 阶段 3：执行数据库事务持久化与全局去重入库
func (s *Service) persistKnowledgeAndMappings(tx *gorm.DB, doc *model.Document, resList []parsedResult, uniqueHashes []string, stats *ImportStats) ([]*model.Knowledge, int, int, error) {
	// 在事务内处理 Document 记录与旧映射清理
	var existingDoc model.Document
	if dbErr := tx.Where("lib_id = ?", doc.LibID).First(&existingDoc).Error; dbErr == nil {
		if delErr := tx.Where("document_id = ?", existingDoc.ID).Delete(&model.KnowledgeVersionMapping{}).Error; delErr != nil {
			return nil, 0, 0, fmt.Errorf("delete old version mappings failed: %w", delErr)
		}
		doc.ID = existingDoc.ID
		if saveErr := tx.Save(doc).Error; saveErr != nil {
			return nil, 0, 0, fmt.Errorf("update existing document failed: %w", saveErr)
		}
	} else {
		if createErr := tx.Create(doc).Error; createErr != nil {
			return nil, 0, 0, fmt.Errorf("save document record failed: %w", createErr)
		}
	}
	stats.DocumentID = doc.ID

	uniqueAdded := 0
	mappingsAdded := 0

	// KB-15: 原实现 `if findErr := ...; findErr == nil {` 把查重失败当成"全部不存在"，
	// 后续整批按新条目插入会直接撞 content_hash 唯一索引，导致整包导入回滚，
	// 而用户看到的错误信息与根因毫无关联。查重失败必须直接上抛。
	existingKMap := make(map[string]uint)
	for i := 0; i < len(uniqueHashes); i += 500 {
		end := i + 500
		if end > len(uniqueHashes) {
			end = len(uniqueHashes)
		}
		var existing []model.Knowledge
		if findErr := tx.Where("content_hash IN ?", uniqueHashes[i:end]).Find(&existing).Error; findErr != nil {
			return nil, 0, 0, fmt.Errorf("query existing knowledge by content_hash failed: %w", findErr)
		}
		for _, ek := range existing {
			existingKMap[ek.ContentHash] = ek.ID
		}
	}

	var newKnowledges []*model.Knowledge
	for _, res := range resList {
		if _, exists := existingKMap[res.knowledge.ContentHash]; !exists {
			existingKMap[res.knowledge.ContentHash] = 0
			newKnowledges = append(newKnowledges, res.knowledge)
		}
	}

	if len(newKnowledges) > 0 {
		if batchErr := tx.CreateInBatches(newKnowledges, 100).Error; batchErr != nil {
			return nil, 0, 0, fmt.Errorf("batch insert knowledge failed: %w", batchErr)
		}
		for _, nk := range newKnowledges {
			existingKMap[nk.ContentHash] = nk.ID
			uniqueAdded++
		}
	}

	var newMappings []*model.KnowledgeVersionMapping
	for _, res := range resList {
		kid := existingKMap[res.knowledge.ContentHash]
		if kid > 0 {
			vMap := &model.KnowledgeVersionMapping{
				KnowledgeID:    kid,
				DocumentID:     doc.ID,
				TopicID:        res.item.TopicID,
				ProductType:    doc.ProductType,
				ProductVersion: doc.ProductVersion,
				HtmlPath:       res.item.URL,
			}
			newMappings = append(newMappings, vMap)
		}
	}

	if len(newMappings) > 0 {
		if mapErr := tx.CreateInBatches(newMappings, 100).Error; mapErr != nil {
			return nil, 0, 0, fmt.Errorf("batch insert mappings failed: %w", mapErr)
		}
		mappingsAdded = len(newMappings)
	}

	// 更新文档中的统计数据
	doc.LogCount = stats.LeafLogCount
	doc.AlarmCount = stats.LeafAlarmCount
	if docSaveErr := tx.Save(doc).Error; docSaveErr != nil {
		return nil, 0, 0, fmt.Errorf("update doc counts failed: %w", docSaveErr)
	}

	return newKnowledges, uniqueAdded, mappingsAdded, nil
}

// indexDocumentKnowledge 阶段 4：自动同步建立全文检索索引 (Bleve Index)
//
// KB-01: 索引失败不再是"打一行 Warn 就当没发生"，而是把文档标记为 index_dirty，
// 前端可据此提示用户执行重建索引，让知识库具备自愈能力。
//
// KB-04: 原实现只索引本次新增的知识。已存在于库中的知识在导入新版本文档时
// 只是新增了一条版本映射，其索引里的 product_list / version_list 永远停留在首次导入的版本，
// 导致按产品过滤搜不到。这里改为"索引本文档关联的全部知识（含历史已有条目）"，
// Bleve 的 Index 是幂等的，覆盖写即可刷新版本列表。
func (s *Service) indexDocumentKnowledge(doc *model.Document, newKnowledges []*model.Knowledge, tr *progress.JobTracker) {
	if s.indexer == nil {
		return
	}

	itemsToIndex := make([]model.Knowledge, 0, len(newKnowledges))
	indexedIDs := make(map[uint]bool, len(newKnowledges))
	for _, nk := range newKnowledges {
		if nk == nil || nk.ID == 0 || indexedIDs[nk.ID] {
			continue
		}
		indexedIDs[nk.ID] = true
		item := *nk
		if len(item.Versions) == 0 {
			item.Versions = []model.KnowledgeVersionMapping{
				{ProductType: doc.ProductType, ProductVersion: doc.ProductVersion},
			}
		}
		itemsToIndex = append(itemsToIndex, item)
	}

	// 补充：本文档引用的历史已有知识，重新装载完整 Versions 后覆盖索引
	if doc != nil && doc.ID > 0 {
		existing, err := s.loadKnowledgesByDocument(doc.ID)
		if err != nil {
			logger.Log.Warnf("[Knowledge Service] load existing knowledge of document %d for reindex failed: %v", doc.ID, err)
			if tr != nil {
				tr.AddLog("warning", "装载历史知识条目用于刷新索引时出现告警: %v", err)
			}
		}
		for _, ek := range existing {
			if indexedIDs[ek.ID] {
				continue
			}
			indexedIDs[ek.ID] = true
			itemsToIndex = append(itemsToIndex, ek)
		}
	}

	if len(itemsToIndex) == 0 {
		return
	}

	if tr != nil {
		tr.SetStage("INDEX", fmt.Sprintf("正在为 %d 条知识构建/刷新 Bleve 全文检索索引...", len(itemsToIndex)))
	}

	if idxErr := s.indexer.IndexKnowledge(itemsToIndex); idxErr != nil {
		logger.Log.Errorf("[Knowledge Service] Auto-indexing into Bleve failed: %v", idxErr)
		// KB-01: 落 index_dirty 标记，为重建索引入口提供自愈依据
		s.markDocumentIndexDirty(doc, idxErr)
		if tr != nil {
			tr.AddLog("error", "全文检索索引构建失败（该文档已标记为待重建，可在文档管理中执行「重建索引」）: %v", idxErr)
		}
		return
	}

	// 索引成功：清除可能存在的脏标记
	s.clearDocumentIndexDirty(doc)

	logger.Log.Debugf("[Knowledge Service] Auto-indexed %d knowledge items to Bleve", len(itemsToIndex))
	if tr != nil {
		tr.AddLog("info", "Bleve 全文检索索引构建/刷新完成 (%d 条)", len(itemsToIndex))
	}
}

// loadKnowledgesByDocument 加载指定文档关联的全部知识（含版本映射）
func (s *Service) loadKnowledgesByDocument(docID uint) ([]model.Knowledge, error) {
	if s.db == nil || docID == 0 {
		return nil, nil
	}
	kIDs := make([]uint, 0)
	// KB-15: Pluck 必须校验 .Error 并去重，否则孤儿判定与索引刷新都会重复计算
	if err := s.db.Model(&model.KnowledgeVersionMapping{}).
		Where("document_id = ?", docID).
		Distinct("knowledge_id").
		Pluck("knowledge_id", &kIDs).Error; err != nil {
		return nil, err
	}
	if len(kIDs) == 0 {
		return nil, nil
	}
	var list []model.Knowledge
	if err := s.db.Preload("Versions").Where("id IN ?", kIDs).Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// markDocumentIndexDirty 把文档标记为"索引待重建"
func (s *Service) markDocumentIndexDirty(doc *model.Document, reason error) {
	if doc == nil || doc.ID == 0 {
		return
	}
	if err := s.db.Model(&model.Document{}).Where("id = ?", doc.ID).
		Update("index_dirty", true).Error; err != nil {
		logger.Log.Warnf("[Knowledge Service] mark document %d index_dirty failed: %v", doc.ID, err)
		return
	}
	doc.IndexDirty = true
	logger.Log.Warnf("[Knowledge Service] Document %d marked index_dirty: %v", doc.ID, reason)
}

// clearDocumentIndexDirty 清除文档的索引脏标记
func (s *Service) clearDocumentIndexDirty(doc *model.Document) {
	if doc == nil || doc.ID == 0 || !doc.IndexDirty {
		return
	}
	if err := s.db.Model(&model.Document{}).Where("id = ?", doc.ID).
		Update("index_dirty", false).Error; err != nil {
		logger.Log.Warnf("[Knowledge Service] clear document %d index_dirty failed: %v", doc.ID, err)
		return
	}
	doc.IndexDirty = false
}

// importSingleDocUnlocked 执行单个 HDX 文档包（目录或压缩包内文档根）的解析与入库
// 内部调用，自动获取文档级细粒度锁
func (s *Service) importSingleDocUnlocked(src hdx.DocSource, conflictMode string, tr *progress.JobTracker) (stats *ImportStats, err error) {
	// 锁 key 用来源唯一 ID：压缩包场景为 "绝对路径::包内文档根"，目录场景为绝对路径
	docMu := s.getDocLock(src.ID())
	docMu.Lock()
	defer docMu.Unlock()

	startTime := time.Now()

	// Stage 1: 解析文档元数据与导航树 (支持 skip 模式快速返回)
	doc, leafItems, stats, skipped, err := s.readDocumentMetadata(src, conflictMode, tr)
	if err != nil {
		return nil, err
	}
	if skipped {
		return stats, nil
	}

	// Stage 2: 并发解析 HTML 知识页面与内容哈希提取
	resList, uniqueHashes := s.parseHTMLKnowledgeItems(src, leafItems, tr)

	if tr != nil {
		tr.SetStage("PERSIST", fmt.Sprintf("正在对 %d 个条目进行全局去重与数据库事务持久化...", len(resList)))
		tr.AddLog("info", "HTML 知识页面解析完成，有效条目 %d，开始事务入库...", len(resList))
	}

	// Stage 3: 数据库事务持久化与全局去重入库
	tx := s.db.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			err = fmt.Errorf("panic during document import persistence: %v\n%s", r, debug.Stack())
			logger.Log.Errorf("[Knowledge Service] %v", err)
		}
	}()

	newKnowledges, uniqueAdded, mappingsAdded, persistErr := s.persistKnowledgeAndMappings(tx, doc, resList, uniqueHashes, stats)
	if persistErr != nil {
		tx.Rollback()
		return nil, persistErr
	}

	if commitErr := tx.Commit().Error; commitErr != nil {
		return nil, fmt.Errorf("commit transaction failed: %w", commitErr)
	}

	if tr != nil {
		tr.AddLog("info", "数据库入库成功: 新增唯一知识 %d 条, 版本映射 %d 条", uniqueAdded, mappingsAdded)
	}

	// Stage 4: 全文检索索引构建 (Bleve Index)
	s.indexDocumentKnowledge(doc, newKnowledges, tr)

	stats.UniqueKnowledgeAdded = uniqueAdded
	stats.VersionMappingsAdded = mappingsAdded
	stats.Duration = time.Since(startTime)

	logger.Log.Infof("Imported Document %s (%s %s) in %v: %d leaf logs, %d leaf alarms, %d unique knowledge added, %d mappings",
		doc.LibID, doc.ProductType, doc.ProductVersion, stats.Duration, stats.LeafLogCount, stats.LeafAlarmCount, uniqueAdded, mappingsAdded)

	return stats, nil
}

// GetDocumentList 获取所有已导入的文档
func (s *Service) GetDocumentList() ([]model.Document, error) {
	var docs []model.Document
	err := s.db.Order("imported_at desc").Find(&docs).Error
	return docs, err
}

// ScanHDXPaths 扫描指定路径列表（目录或文件）下的 HDX 文档包与压缩包，并比对知识库判断是否已存在
func (s *Service) ScanHDXPaths(paths []string) (*hdx.ScanResult, error) {
	res, err := hdx.ScanHDXPaths(paths)
	if err != nil {
		return nil, err
	}

	existingDocs, err := s.GetDocumentList()
	if err == nil && len(existingDocs) > 0 {
		for i := range res.Items {
			item := &res.Items[i]
			for _, doc := range existingDocs {
				if item.LibID != "" && strings.EqualFold(doc.LibID, item.LibID) {
					item.ExistsInKB = true
					break
				}
				if item.Name != "" && (strings.EqualFold(doc.LibName, item.Name) || strings.EqualFold(filepath.Base(doc.FilePath), item.Name)) {
					item.ExistsInKB = true
					break
				}
			}
		}
	}

	return res, nil
}

// GetKnowledgeByID 根据 ID 获取单条知识详情
func (s *Service) GetKnowledgeByID(id uint) (*model.Knowledge, error) {
	var k model.Knowledge
	err := s.db.Preload("Versions").First(&k, id).Error
	return &k, err
}

// GetKnowledgeByIDs 根据 ID 列表批量获取知识详情（含 Versions 关联），优化 N+1 查询
func (s *Service) GetKnowledgeByIDs(ids []uint) ([]model.Knowledge, error) {
	if len(ids) == 0 {
		return []model.Knowledge{}, nil
	}
	var list []model.Knowledge
	err := s.db.Preload("Versions").Where("id IN ?", ids).Find(&list).Error
	return list, err
}

// GetKnowledgeMapByIDs 根据 ID 列表批量获取知识详情并组装为 ID->*Knowledge 映射表
func (s *Service) GetKnowledgeMapByIDs(ids []uint) (map[uint]*model.Knowledge, error) {
	if len(ids) == 0 {
		return make(map[uint]*model.Knowledge), nil
	}
	list, err := s.GetKnowledgeByIDs(ids)
	if err != nil {
		return nil, err
	}
	res := make(map[uint]*model.Knowledge, len(list))
	for i := range list {
		res[list[i].ID] = &list[i]
	}
	return res, nil
}

// DeleteDocument 删除文档及关联映射，并清理孤儿知识条目与全文检索索引
//
// KB-07: 原实现把 Pluck 与两次 Delete 的错误全部丢弃（`_ =`），
// 一旦任一步失败就会出现"索引已删、DB 仍在"或"DB 已删、索引仍在"的反向不一致，
// 用户表现为"搜到一条点进去 404"。这里全部纳入错误判定：
// 先删索引、后删 DB；任一步失败都向上返回并提示可重建索引自愈。
func (s *Service) DeleteDocument(docID uint) error {
	if s.db == nil {
		return fmt.Errorf("knowledge db is not initialized")
	}

	var kIDs []uint
	// 1. 查找此文档关联的所有 knowledge_id（必须校验 Error 并去重）
	if err := s.db.Model(&model.KnowledgeVersionMapping{}).
		Where("document_id = ?", docID).
		Distinct("knowledge_id").
		Pluck("knowledge_id", &kIDs).Error; err != nil {
		return fmt.Errorf("query knowledge ids of document %d failed: %w", docID, err)
	}

	// 2. 先删映射与文档记录（DB 侧）
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("document_id = ?", docID).Delete(&model.KnowledgeVersionMapping{}).Error; err != nil {
			return fmt.Errorf("delete version mappings failed: %w", err)
		}
		res := tx.Delete(&model.Document{}, docID)
		if res.Error != nil {
			return fmt.Errorf("delete document failed: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("document %d not found", docID)
		}
		return nil
	}); err != nil {
		return err
	}

	// 3. 清理不再被任何文档引用的孤儿 Knowledge 记录与 Bleve 索引 (M-01, M-11)
	if len(kIDs) > 0 {
		var activeKIDs []uint
		if err := s.db.Model(&model.KnowledgeVersionMapping{}).
			Where("knowledge_id IN ?", kIDs).
			Distinct("knowledge_id").
			Pluck("knowledge_id", &activeKIDs).Error; err != nil {
			return fmt.Errorf("query active knowledge ids failed: %w", err)
		}
		activeSet := make(map[uint]struct{}, len(activeKIDs))
		for _, id := range activeKIDs {
			activeSet[id] = struct{}{}
		}

		orphanIDs := make([]string, 0, len(kIDs))
		orphanUintIDs := make([]uint, 0, len(kIDs))
		for _, id := range kIDs {
			if _, active := activeSet[id]; !active {
				orphanUintIDs = append(orphanUintIDs, id)
				orphanIDs = append(orphanIDs, strconv.Itoa(int(id)))
			}
		}

		if len(orphanUintIDs) > 0 {
			// 顺序：先删索引，后删 DB。
			// 反过来会短暂出现"DB 里没有但还能搜到"的幽灵条目。
			if s.indexer != nil {
				if err := s.indexer.DeleteBatch(orphanIDs); err != nil {
					logger.Log.Errorf("[Knowledge Service] delete orphan knowledge from bleve failed: %v", err)
					// 索引删除失败不阻断 DB 清理，但必须明确告知需要重建
					return fmt.Errorf("delete orphan knowledge from index failed: %w (please run a full reindex)", err)
				}
			}
			if err := s.db.Where("id IN ?", orphanUintIDs).Delete(&model.Knowledge{}).Error; err != nil {
				return fmt.Errorf("delete orphan knowledge failed: %w", err)
			}
		}
	}

	// 4. 通知匹配引擎重载 (M-13)
	if s.matchEngine != nil {
		s.matchEngine.Reload()
	}

	return nil
}

// FindBestKnowledgeMatch 实现了基于产品型号与版本的智能回退（Fallback）策略
func (s *Service) FindBestKnowledgeMatch(candidates []model.Knowledge, targetProduct, targetVersion string) *model.Knowledge {
	return matcher.FindBestKnowledgeMatch(candidates, targetProduct, targetVersion)
}

// FindBestKnowledgeMatchPtr 针对指针切片执行高效零拷贝匹配
func (s *Service) FindBestKnowledgeMatchPtr(candidates []*model.Knowledge, targetProduct, targetVersion string) *model.Knowledge {
	return matcher.FindBestKnowledgeMatchPtr(candidates, targetProduct, targetVersion)
}

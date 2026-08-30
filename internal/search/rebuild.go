package search

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/blevesearch/bleve/v2"

	"logauditorgo/internal/model"
	"logauditorgo/pkg/logger"
)

// RebuildOptions 物理重建索引的可选参数
type RebuildOptions struct {
	// TempDir 临时索引目录的父目录（留空则与正式索引同级）
	TempDir string
	// PageSize 每页从数据库读取并灌入索引的条数
	PageSize int
	// OnProgress 进度回调，参数为已处理条数与总条数
	OnProgress func(indexed, total int)
}

// RebuildFromDB 基于 knowledges 表全量重建索引并原子替换。
//
// KB-01: 这是整个知识库"可自愈"的底座。原实现在 DB 事务提交后写 Bleve，
// 失败只打一行 Warn，导致"导入显示成功但知识永远搜不到"且无任何补救手段。
//
// 实现要点（对应风险矩阵中"重建破坏历史数据"的缓解策略）：
//  1. 在临时目录 bleve_temp_<ts> 构建全新索引，绝不动老目录；
//  2. 全量分页读取 knowledges（Preload Versions）流式灌入，避免一次性读入内存；
//  3. 构建成功后关闭新旧句柄 → 老目录重命名为 .old_<ts> → 临时目录重命名为正式目录 → 重新 Open；
//  4. 任一步失败都回滚到老索引并清理临时目录，保证"要么全成功、要么原样"。
func (idx *Indexer) RebuildFromDB(loadPage func(offset, limit int) ([]model.Knowledge, error), countAll func() (int64, error), opts RebuildOptions) error {
	if idx == nil {
		return fmt.Errorf("indexer is nil")
	}
	if loadPage == nil || countAll == nil {
		return fmt.Errorf("rebuild requires loadPage and countAll callbacks")
	}

	idx.mu.RLock()
	indexPath := idx.path
	idx.mu.RUnlock()
	if indexPath == "" {
		return fmt.Errorf("indexer has no path, cannot rebuild in place")
	}

	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = 1000
	}

	total, err := countAll()
	if err != nil {
		return fmt.Errorf("count knowledge for reindex failed: %w", err)
	}

	parent := opts.TempDir
	if parent == "" {
		parent = filepath.Dir(indexPath)
	}
	tempPath := filepath.Join(parent, fmt.Sprintf("bleve_temp_%d", time.Now().UnixNano()))

	// 1. 在临时目录建立全新索引
	newIndex, err := bleve.New(tempPath, buildIndexMapping())
	if err != nil {
		_ = os.RemoveAll(tempPath)
		return fmt.Errorf("create temp index failed: %w", err)
	}
	tempIdx := &Indexer{index: newIndex, path: tempPath}
	tempIdx.ensureMappingVersion()

	fail := func(format string, args ...interface{}) error {
		_ = tempIdx.Close()
		_ = os.RemoveAll(tempPath)
		return fmt.Errorf(format, args...)
	}

	// 2. 全量分页灌入
	indexed := 0
	for offset := 0; ; offset += pageSize {
		items, err := loadPage(offset, pageSize)
		if err != nil {
			return fail("load knowledge page (offset=%d) failed: %w", offset, err)
		}
		if len(items) == 0 {
			break
		}
		tempIdx.mu.RLock()
		ok, failed := tempIdx.indexChunkLocked(items)
		tempIdx.mu.RUnlock()
		if failed > 0 {
			logger.Log.Warnf("[Bleve Indexer] reindex: %d/%d items failed to index", failed, len(items))
		}
		indexed += ok
		if opts.OnProgress != nil {
			opts.OnProgress(indexed, int(total))
		}
		if len(items) < pageSize {
			break
		}
	}

	if err := tempIdx.Close(); err != nil {
		return fail("close temp index failed: %w", err)
	}

	// 3. 原子切换：关老句柄 → 老目录改名 → 临时目录转正 → 重新 Open
	if err := idx.swapIndexDir(indexPath, tempPath); err != nil {
		_ = os.RemoveAll(tempPath)
		return err
	}

	logger.Log.Infof("[Bleve Indexer] Index rebuilt and hot-swapped successfully: %d items indexed (path: %s)", indexed, indexPath)
	return nil
}

// swapIndexDir 关闭当前索引句柄并用 tempPath 原子替换 indexPath
func (idx *Indexer) swapIndexDir(indexPath, tempPath string) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	if idx.index != nil {
		if err := idx.index.Close(); err != nil {
			return fmt.Errorf("close old index failed: %w", err)
		}
		idx.index = nil
	}

	backupPath := fmt.Sprintf("%s.old_%d", indexPath, time.Now().UnixNano())
	if err := os.Rename(indexPath, backupPath); err != nil && !os.IsNotExist(err) {
		// 老目录改名失败：尝试直接打开新索引，保证服务可用性优先
		logger.Log.Errorf("[Bleve Indexer] backup old index failed: %v", err)
		return fmt.Errorf("backup old index failed: %w", err)
	}

	if err := os.Rename(tempPath, indexPath); err != nil {
		// 转正失败，回滚老目录
		if rbErr := os.Rename(backupPath, indexPath); rbErr != nil {
			logger.Log.Errorf("[Bleve Indexer] CRITICAL: rollback old index failed: %v (manual recovery required, backup at %s)", rbErr, backupPath)
		}
		return fmt.Errorf("promote new index failed: %w", err)
	}

	// 老目录已经安全归档，删除备份释放磁盘（失败不影响主流程）
	if err := os.RemoveAll(backupPath); err != nil {
		logger.Log.Warnf("[Bleve Indexer] remove old index backup %s failed: %v", backupPath, err)
	}

	reopened, openErr := bleve.Open(indexPath)
	if openErr != nil {
		return fmt.Errorf("reopen rebuilt index failed: %w", openErr)
	}
	idx.index = reopened
	idx.path = indexPath

	// 重新注册到全局表，保证 InitIndexer 后续复用同一实例
	indexerMu.Lock()
	indexerMap[filepath.Clean(indexPath)] = idx
	indexerMu.Unlock()

	return nil
}

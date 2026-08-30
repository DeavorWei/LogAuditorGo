package knowledge

import (
	"fmt"
	"sync"
	"time"

	"logauditorgo/internal/model"
	"logauditorgo/internal/search"
	"logauditorgo/pkg/logger"
	"logauditorgo/pkg/progress"
)

// ReindexStages 重建全文检索索引的阶段定义
var ReindexStages = []progress.StageDef{
	{Key: "PREPARE", Name: "准备重建环境"},
	{Key: "REBUILD", Name: "全量重建全文检索索引"},
	{Key: "SWAP", Name: "原子切换与校验"},
	{Key: "COMPLETE", Name: "重建完成"},
}

// reindexMu 保证同一时刻只有一个重建任务在执行。
// Bleve 索引目录无法并发读写，且重建期间应考虑禁止并发导入。
var reindexMu sync.Mutex

// TryReindex 尝试以非阻塞方式启动一次重建（供后台 goroutine 使用）。
// 已有重建在跑时直接返回 false。
func (s *Service) TryReindex() bool {
	return reindexMu.TryLock()
}

// IsReindexDirty 返回是否存在索引脏标记或 mapping 过期的文档（供前端提示重建）
func (s *Service) IsReindexDirty() (bool, error) {
	if s.db == nil {
		return false, nil
	}
	var dirtyCount int64
	if err := s.db.Model(&model.Document{}).Where("index_dirty = ?", true).Count(&dirtyCount).Error; err != nil {
		return false, err
	}
	if dirtyCount > 0 {
		return true, nil
	}
	if s.indexer != nil && s.indexer.IsMappingOutdated() {
		return true, nil
	}
	return false, nil
}

// RebuildIndex 基于 knowledges 表全量重建 Bleve 索引并原子热替换。
//
// KB-01: 这是知识库"可自愈"的核心入口。原实现索引失败只打 Warn，
// 数据一旦写入 DB 就再也没有机会进索引，用户"导入成功却永远搜不到"且无法补救。
//
// 实现策略（风险矩阵"Bleve mapping 变更破坏历史数据"的缓解方案）：
//   - 临时目录构建全新索引，绝不在老目录上原地修改；
//   - 全量分页读取 knowledges（Preload Versions）流式灌入；
//   - 构建成功后关闭句柄 → 老目录归档 → 临时目录转正 → 重新 Open；
//   - 任一步失败都保留老索引并清理临时目录；
//   - 重建成功后清除全部 index_dirty 标记。
func (s *Service) RebuildIndex(tr *progress.JobTracker) (retErr error) {
	if s.indexer == nil {
		return fmt.Errorf("bleve indexer is not initialized")
	}
	if s.db == nil {
		return fmt.Errorf("knowledge db is not initialized")
	}

	if !reindexMu.TryLock() {
		return fmt.Errorf("another reindex job is already running")
	}
	defer reindexMu.Unlock()

	start := time.Now()

	defer func() {
		if r := recover(); r != nil {
			retErr = fmt.Errorf("panic during reindex: %v", r)
			logger.Log.Errorf("[Knowledge Service] %v", retErr)
			if tr != nil {
				tr.Fail(retErr, "重建索引异常中断")
			}
		}
	}()

	if tr != nil {
		tr.SetStage("PREPARE", "正在统计待重建知识条目...")
	}

	countAll := func() (int64, error) {
		var total int64
		if err := s.db.Model(&model.Knowledge{}).Count(&total).Error; err != nil {
			return 0, err
		}
		return total, nil
	}

	loadPage := func(offset, limit int) ([]model.Knowledge, error) {
		var items []model.Knowledge
		if err := s.db.Preload("Versions").
			Order("id asc").
			Offset(offset).Limit(limit).
			Find(&items).Error; err != nil {
			return nil, err
		}
		return items, nil
	}

	total, err := countAll()
	if err != nil {
		if tr != nil {
			tr.Fail(err, "统计知识条目失败")
		}
		return err
	}
	logger.Log.Infof("[Knowledge Service] Starting full reindex of %d knowledge items...", total)

	if tr != nil {
		tr.SetStage("REBUILD", fmt.Sprintf("正在重建 %d 条知识的全文检索索引...", total))
		tr.UpdateProgress(0, total, "准备重建索引...")
		tr.AddLog("info", "开始全量重建索引，共 %d 条知识", total)
	}

	lastReport := time.Now()
	err = s.indexer.RebuildFromDB(loadPage, countAll, search.RebuildOptions{OnProgress: func(indexed, total int) {
		if tr == nil {
			return
		}
		// 进度上报节流：避免百万级条目下每条都广播一次 SSE
		if time.Since(lastReport) < 200*time.Millisecond && indexed != total {
			return
		}
		lastReport = time.Now()
		tr.UpdateProgress(int64(indexed), int64(total),
			fmt.Sprintf("已重建索引: %d / %d 条", indexed, total))
	}})
	if err != nil {
		logger.Log.Errorf("[Knowledge Service] Rebuild index failed: %v", err)
		if tr != nil {
			tr.Fail(err, "重建索引失败，原索引保持不变")
		}
		return err
	}

	// 重建成功：清除全部 index_dirty 标记
	if tr != nil {
		tr.SetStage("SWAP", "正在校验索引并清理脏标记...")
	}
	if clearErr := s.db.Model(&model.Document{}).
		Where("index_dirty = ?", true).
		Update("index_dirty", false).Error; clearErr != nil {
		logger.Log.Warnf("[Knowledge Service] clear index_dirty flags failed: %v", clearErr)
		if tr != nil {
			tr.AddLog("warning", "清理索引脏标记失败: %v", clearErr)
		}
	}

	// 通知匹配引擎重载，保证内存索引与新索引一致
	if s.matchEngine != nil {
		s.matchEngine.Reload()
	}

	elapsed := time.Since(start)
	logger.Log.Infof("[Knowledge Service] Reindex completed: %d items in %v", total, elapsed)

	if tr != nil {
		tr.AddLog("success", "全文检索索引重建完成，共 %d 条知识，耗时 %.1fs", total, elapsed.Seconds())
		tr.SetStage("COMPLETE", "索引重建完成")
		tr.Complete(map[string]interface{}{
			"total":      total,
			"duration_ms": elapsed.Milliseconds(),
		}, fmt.Sprintf("索引重建完成！共重建 %d 条知识，耗时 %.1f 秒", total, elapsed.Seconds()))
	}

	return nil
}

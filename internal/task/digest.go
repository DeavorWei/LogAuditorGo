package task

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"logauditorgo/internal/model"
	"logauditorgo/internal/storage"
	"logauditorgo/pkg/logger"
)

// RefreshTaskDigest 重新生成任务级检索摘要（跨任务检索 + 未匹配热点）。
//
// 失败只告警：digest 是检索辅助索引，缺失时任务详情/摘要仍可用。
func (s *Service) RefreshTaskDigest(taskID string) error {
	if s == nil || s.globalDB == nil || !isValidTaskID(taskID) {
		return fmt.Errorf("invalid task service or task id")
	}
	summary, err := s.GetDiagnosisSummary(taskID)
	if err != nil {
		return err
	}

	taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err != nil {
		return err
	}
	defer storage.ReleaseTaskDB(taskID)

	var hostnames []string
	if err := taskDB.Model(&model.LogRecord{}).
		Where("hostname <> ''").
		Distinct("hostname").Order("hostname ASC").Limit(30).
		Pluck("hostname", &hostnames).Error; err != nil {
		logger.Log.Warnf("[Task Service] query hostnames for digest failed: %v", err)
	}

	modules := make([]string, 0, len(summary.TopModules))
	for _, item := range summary.TopModules {
		if item.Module != "" {
			modules = append(modules, item.Module)
		}
	}

	unmatchedJSON := "[]"
	if len(summary.UnmatchedTop) > 0 {
		if b, marshalErr := json.Marshal(summary.UnmatchedTop); marshalErr == nil {
			unmatchedJSON = string(b)
		}
	}

	digest := model.TaskDigest{
		TaskID:        taskID,
		Hostnames:     strings.Join(hostnames, ","),
		TopModules:    strings.Join(modules, ","),
		UnmatchedJSON: unmatchedJSON,
		LogCount:      int(summary.Overview.LogCount),
		MatchedCount:  int(summary.Overview.MatchedCount),
		UpdatedAt:     time.Now(),
	}
	if err := s.globalDB.Save(&digest).Error; err != nil {
		return fmt.Errorf("save task digest failed: %w", err)
	}
	return nil
}

// refreshTaskDigestQuiet 在导入/重分析收尾处调用，失败不影响主流程。
func (s *Service) refreshTaskDigestQuiet(taskID string) {
	if err := s.RefreshTaskDigest(taskID); err != nil {
		logger.Log.Warnf("[Task Service] refresh digest for task %s failed: %v", taskID, err)
	}
}

// GlobalUnmatchedHotspot 跨任务未匹配热点。
type GlobalUnmatchedHotspot struct {
	Module    string     `json:"module"`
	Brief     string     `json:"brief"`
	Count     int64      `json:"count"`
	TaskCount int        `json:"task_count"`
	TaskIDs   []string   `json:"task_ids"`
	LastSeen  *time.Time `json:"last_seen,omitempty"`
}

// GetGlobalUnmatchedHotspots 聚合最近 200 个案例的未匹配热点。
//
// 返回 digest 扫描数量，便于前端标注"基于最近 N 个案例的聚合"。
func (s *Service) GetGlobalUnmatchedHotspots(limit int) ([]GlobalUnmatchedHotspot, int, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	var digests []model.TaskDigest
	if err := s.globalDB.Order("updated_at DESC").Limit(200).Find(&digests).Error; err != nil {
		return nil, 0, fmt.Errorf("query task digests failed: %w", err)
	}

	type aggregate struct {
		count    int64
		taskIDs  []string
		taskSeen map[string]bool
		lastSeen *time.Time
	}
	merged := make(map[string]*aggregate)
	for _, digest := range digests {
		if strings.TrimSpace(digest.UnmatchedJSON) == "" || digest.UnmatchedJSON == "[]" {
			continue
		}
		var hotspots []DiagnosisHotspot
		if err := json.Unmarshal([]byte(digest.UnmatchedJSON), &hotspots); err != nil {
			continue
		}
		for _, item := range hotspots {
			key := item.Module + "/" + item.Brief
			agg := merged[key]
			if agg == nil {
				agg = &aggregate{taskSeen: make(map[string]bool)}
				merged[key] = agg
			}
			agg.count += item.Count
			if !agg.taskSeen[digest.TaskID] {
				agg.taskSeen[digest.TaskID] = true
				if len(agg.taskIDs) < 5 {
					agg.taskIDs = append(agg.taskIDs, digest.TaskID)
				}
			}
			if item.LastSeen != nil && (agg.lastSeen == nil || item.LastSeen.After(*agg.lastSeen)) {
				lastSeen := *item.LastSeen
				agg.lastSeen = &lastSeen
			}
		}
	}

	type kv struct {
		key  string
		item GlobalUnmatchedHotspot
	}
	list := make([]kv, 0, len(merged))
	for key, agg := range merged {
		parts := strings.SplitN(key, "/", 2)
		item := GlobalUnmatchedHotspot{
			Module:    parts[0],
			Brief:     "",
			Count:     agg.count,
			TaskCount: len(agg.taskSeen),
			TaskIDs:   agg.taskIDs,
			LastSeen:  agg.lastSeen,
		}
		if len(parts) > 1 {
			item.Brief = parts[1]
		}
		list = append(list, kv{key: key, item: item})
	}
	// 按次数降序 + key 升序，保证同数据多次查询顺序一致
	for i := 1; i < len(list); i++ {
		for j := i; j > 0; j-- {
			if list[j].item.Count > list[j-1].item.Count ||
				(list[j].item.Count == list[j-1].item.Count && list[j].key < list[j-1].key) {
				list[j], list[j-1] = list[j-1], list[j]
				continue
			}
			break
		}
	}
	result := make([]GlobalUnmatchedHotspot, 0, limit)
	for i := 0; i < len(list) && i < limit; i++ {
		result = append(result, list[i].item)
	}
	return result, len(digests), nil
}

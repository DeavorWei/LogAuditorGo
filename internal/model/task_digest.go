package model

import "time"

// TaskDigest 任务级检索摘要（v1.3）。
//
// 用途：支撑"跨任务案例检索"与"未匹配热点"两个需要跨任务库的查询。
// 数据来自任务完成/重分析后的聚合结果，属于**检索辅助索引**，
// 不是权威统计；任务详情与诊断摘要仍实时查询任务库。
type TaskDigest struct {
	TaskID        string    `gorm:"primaryKey;size:64" json:"task_id"`
	Hostnames     string    `gorm:"size:1024" json:"hostnames"`
	TopModules    string    `gorm:"size:1024" json:"top_modules"`
	UnmatchedJSON string    `gorm:"type:text" json:"unmatched_json"`
	LogCount      int       `json:"log_count"`
	MatchedCount  int       `json:"matched_count"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (TaskDigest) TableName() string {
	return "task_digests"
}

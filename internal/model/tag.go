package model

import "time"

// LogTag 任务库内的标签定义实体
type LogTag struct {
	ID        uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	Name      string    `gorm:"size:64;uniqueIndex" json:"name"`
	Color     string    `gorm:"size:16" json:"color"` // 预设色板十六进制或主题标识 (如 #409EFF)
	Remark    string    `gorm:"size:255" json:"remark"`
	CreatedAt time.Time `json:"created_at"`
	LogCount  int64     `gorm:"->" json:"log_count"`          // 动态聚合的关联日志计数（只读映射）
}

func (LogTag) TableName() string { return "log_tags" }

// LogTagRelation 标签与日志多对多关联实体
//
// 索引与级联设计（经过安全隐患审计验证）：
// 1. (TagID, LogID) 复合唯一索引：防止重复打标、支持 INSERT OR IGNORE、支撑按 tag_id 检索；
// 2. 独立 LogID 索引 (idx_tag_rel_log_id)：支撑列表分页后的批量回填 (WHERE log_id IN (...))；
// 3. LogID 外键级联 (constraint:OnDelete:CASCADE)：当 LogRecord 被物理删除时自动清理关联。
type LogTagRelation struct {
	ID        uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	TagID     uint      `gorm:"uniqueIndex:uniq_tag_log,priority:1;index" json:"tag_id"`
	LogID     uint      `gorm:"uniqueIndex:uniq_tag_log,priority:2;index:idx_tag_rel_log_id;constraint:OnDelete:CASCADE" json:"log_id"`
	CreatedAt time.Time `json:"created_at"`
}

func (LogTagRelation) TableName() string { return "log_tag_relations" }

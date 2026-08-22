package model

// ImpactEvent RCA影响事件
type ImpactEvent struct {
	LogID      uint   `json:"log_id"`
	FromLogID  uint   `json:"from_log_id,omitempty"`
	FromModule string `json:"from_module,omitempty"`
	Module     string `json:"module"`
	Brief      string `json:"brief"`
	Timestamp  string `json:"timestamp"`
	DelayMs    int64  `json:"delay_ms"`
}

// RCAEvent 根因分析事件模型
type RCAEvent struct {
	ID                uint          `gorm:"primaryKey;autoIncrement" json:"id"`
	RootLogID         uint          `gorm:"index" json:"root_log_id"`
	RootModule        string        `gorm:"size:64" json:"root_module"`
	RootBrief         string        `gorm:"size:128" json:"root_brief"`
	RootTimestamp     string        `gorm:"size:64" json:"root_timestamp"`
	RootCauseSummary  string        `gorm:"type:text" json:"root_cause_summary"`
	CorrelatedLogIDs  string        `gorm:"type:text" json:"correlated_log_ids"` // JSON 数组: [105, 108, 112]
	ImpactEventsJSON  string        `gorm:"type:text" json:"impact_events_json"` // JSON 结构体数组
	ImpactLevel       string        `gorm:"size:32" json:"impact_level"`         // CRITICAL, HIGH, MEDIUM, LOW
	Confidence        float64       `json:"confidence"`
	RecommendedAction string        `gorm:"type:text" json:"recommended_action"`
}

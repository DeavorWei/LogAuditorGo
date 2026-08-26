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

// ImpactLogDetail 富化后的衍生故障日志详细信息
type ImpactLogDetail struct {
	ImpactEvent
	Hostname        string            `json:"hostname"`
	DeviceID        uint              `json:"device_id"`
	DeviceName      string            `json:"device_name"`
	DeviceColor     string            `json:"device_color"`
	Severity        int               `json:"severity"`
	RawLog          string            `json:"raw_log"`
	Parameters      map[string]string `json:"parameters,omitempty"`
	SourceFile      string            `json:"source_file,omitempty"`
	KnowledgeID     uint              `json:"knowledge_id,omitempty"`
	MatchTier       string            `json:"match_tier,omitempty"`
	MatchConfidence float64           `json:"match_confidence,omitempty"`
}

// EnrichedRCAEvent 包含根因日志与全链条衍生日志元数据的富化 RCA 事件实体
type EnrichedRCAEvent struct {
	RCAEvent
	RootLog          *LogRecord        `json:"root_log,omitempty"`
	RootDeviceName   string            `json:"root_device_name"`
	RootDeviceColor  string            `json:"root_device_color"`
	RootHostname     string            `json:"root_hostname"`
	RootParameters   map[string]string `json:"root_parameters,omitempty"`
	ImpactDetails    []ImpactLogDetail `json:"impact_details"`
	CorrelatedCount  int               `json:"correlated_count"`
	ModulesInvolved  []string          `json:"modules_involved"`
	DevicesInvolved  []string          `json:"devices_involved"`
}

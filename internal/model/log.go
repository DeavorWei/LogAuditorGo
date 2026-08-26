package model

import "time"

// NormalizedLog 标准化日志结构
type NormalizedLog struct {
	ID          uint              `json:"id"`
	RawLog      string            `json:"raw_log"`      // 原始单行日志
	Timestamp   time.Time         `json:"timestamp"`    // 标准化时间戳
	Hostname    string            `json:"hostname"`     // 设备主机名
	DeviceType  string            `json:"device_type"`  // 如 Switch, Firewall, Router
	Module      string            `json:"module"`       // 模块名 (BGP)
	Severity    int               `json:"severity"`     // 级别 (4)
	Brief       string            `json:"brief"`        // 助记符/事件名 (BGP_AUTH_FAILED)
	LogType     string            `json:"log_type"`     // l: 日志, s: 安全日志
	Sequence    uint64            `json:"sequence"`     // 序列号
	SlotInfo    string            `json:"slot_info"`    // 插槽槽位信息 (Slot=1/1)
	SourceFile  string            `json:"source_file,omitempty"` // 来源文件名
	MessageBody string            `json:"message_body"` // 日志正文
	Parameters  map[string]string `json:"parameters"`   // 解析出的动态键值对: {"PeerID": "192.168.1.2"}

	// 匹配结果
	KnowledgeID     uint    `json:"knowledge_id,omitempty"`
	MatchTier       string  `json:"match_tier,omitempty"`       // EXACT, MNEMONIC, TEMPLATE, BLEVE, UNMATCHED
	MatchConfidence float64 `json:"match_confidence,omitempty"` // 0.0 ~ 1.0

	DeviceID uint `json:"device_id,omitempty"` // 关联设备ID
}

// LogRecord 任务数据库中的日志存储实体
type LogRecord struct {
	ID              uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	DeviceID        uint      `gorm:"index;default:0" json:"device_id"` // 所属设备ID (0表示未指定设备)
	Timestamp       time.Time `gorm:"index" json:"timestamp"`
	Hostname        string    `gorm:"size:128;index" json:"hostname"`
	Module          string    `gorm:"size:64;index" json:"module"`
	Severity        int       `gorm:"index" json:"severity"`
	Brief           string    `gorm:"size:128;index" json:"brief"`
	SlotInfo        string    `gorm:"size:64" json:"slot_info"`
	SourceFile      string    `gorm:"size:255;index" json:"source_file,omitempty"`
	RawLog          string    `gorm:"type:text" json:"raw_log"`
	MessageBody     string    `gorm:"type:text" json:"message_body"`
	ParametersJSON  string    `gorm:"type:text" json:"parameters_json"`
	KnowledgeID     uint      `gorm:"index" json:"knowledge_id"`
	MatchTier       string    `gorm:"size:32" json:"match_tier"`
	MatchConfidence float64   `json:"match_confidence"`
}


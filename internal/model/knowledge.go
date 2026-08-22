package model

import "time"

type EntryType string

const (
	EntryTypeLog   EntryType = "LOG"   // 日志知识
	EntryTypeAlarm EntryType = "ALARM" // 告警/Trap知识
)

// ParameterItem 动态参数结构
type ParameterItem struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Knowledge 故障与日志知识全局表
type Knowledge struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	EntryType   EntryType `gorm:"size:16;index" json:"entry_type"` // LOG 或 ALARM
	Module      string    `gorm:"size:64;index" json:"module"`     // 如 BGP, AAA, IFNET
	Severity    int       `gorm:"index" json:"severity"`           // 1~8
	Brief       string    `gorm:"size:128;index" json:"brief"`     // 如 BGP_AUTH_FAILED

	// 告警/Trap 专属字段
	TrapOID   string `gorm:"size:128;index" json:"trap_oid,omitempty"` // 如 1.3.6.1.4.1.2011.6.10.2.1
	MIBName   string `gorm:"size:128" json:"mib_name,omitempty"`       // 如 HUAWEI-CONFIG-MAN-MIB
	AlarmID   string `gorm:"size:64" json:"alarm_id,omitempty"`
	AlarmType string `gorm:"size:64" json:"alarm_type,omitempty"`

	// 核心内容
	Message     string `gorm:"type:text" json:"message"`     // 日志模板或 Trap Buffer 描述
	Description string `gorm:"type:text" json:"description"` // 含义解释
	Parameters  string `gorm:"type:text" json:"parameters"`  // JSON 存储: [{"name":"PeerID","description":"对等体地址"}]
	Impact      string `gorm:"type:text" json:"impact"`      // 对系统的影响
	Cause       string `gorm:"type:text" json:"cause"`       // 可能原因
	Action      string `gorm:"type:text" json:"action"`      // 处理步骤

	ContentHash string    `gorm:"size:64;uniqueIndex" json:"content_hash"` // SHA256 去重哈希
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`

	// 版本映射关联
	Versions []KnowledgeVersionMapping `gorm:"foreignKey:KnowledgeID" json:"versions,omitempty"`
}

// KnowledgeVersionMapping 知识与多文档/多版本的映射关系
type KnowledgeVersionMapping struct {
	ID             uint   `gorm:"primaryKey" json:"id"`
	KnowledgeID    uint   `gorm:"index" json:"knowledge_id"`
	DocumentID     uint   `gorm:"index" json:"document_id"`
	TopicID        string `gorm:"size:128;index" json:"topic_id"` // 如 ZH-CN_LOGREF_0000002320129730
	ProductType    string `gorm:"size:128" json:"product_type"`
	ProductVersion string `gorm:"size:64" json:"product_version"`
	HtmlPath       string `gorm:"size:255" json:"html_path"`
}

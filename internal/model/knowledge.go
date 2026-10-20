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
	ID        uint      `gorm:"primaryKey" json:"id"`
	EntryType EntryType `gorm:"size:16;index" json:"entry_type"` // LOG 或 ALARM
	Module    string    `gorm:"size:64;index" json:"module"`     // 如 BGP, AAA, IFNET
	Severity  int       `gorm:"index" json:"severity"`           // 1~8
	Brief     string    `gorm:"size:128;index" json:"brief"`     // 如 BGP_AUTH_FAILED

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
	// ReferenceURL 本地知识的参考链接（官方知识为空）。
	ReferenceURL string `gorm:"size:512" json:"reference_url,omitempty"`

	ContentHash string `gorm:"size:64;uniqueIndex" json:"content_hash"` // SHA256 去重哈希
	// Source 知识来源：official（HDX 官方文档）/ local（用户补充的本地知识）。
	// 本地知识与官方同表，复用检索/富化/版本映射；匹配优先级由 matcher 单独处理。
	Source    string    `gorm:"size:16;index;default:'official'" json:"source"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// 版本映射关联
	Versions []KnowledgeVersionMapping `gorm:"foreignKey:KnowledgeID" json:"versions,omitempty"`
}

// KnowledgeOverride 本地"匹配覆盖"：把某个 module/brief 绑定到已有官方知识。
//
// 产品约束（规划 F10）：本地覆盖优先于官方精确匹配，置信度 0.90；
// 仅对完全相同的 module+brief 生效，可启用/禁用/删除。
type KnowledgeOverride struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Module      string    `gorm:"size:64;uniqueIndex:idx_override_key" json:"module"`
	Brief       string    `gorm:"size:128;uniqueIndex:idx_override_key" json:"brief"`
	KnowledgeID uint      `gorm:"index" json:"knowledge_id"`
	Enabled     bool      `gorm:"default:true;index" json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (KnowledgeOverride) TableName() string {
	return "knowledge_overrides"
}

// KnowledgeFavorite 知识收藏。
type KnowledgeFavorite struct {
	KnowledgeID uint      `gorm:"primaryKey" json:"knowledge_id"`
	CreatedAt   time.Time `json:"created_at"`
}

func (KnowledgeFavorite) TableName() string {
	return "knowledge_favorites"
}

// KnowledgeRecent 最近浏览（最多保留 50 条，由服务层裁剪）。
type KnowledgeRecent struct {
	KnowledgeID uint      `gorm:"primaryKey" json:"knowledge_id"`
	ViewedAt    time.Time `json:"viewed_at"`
}

func (KnowledgeRecent) TableName() string {
	return "knowledge_recent"
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
	DocName        string `gorm:"-" json:"doc_name,omitempty"` // 关联的产品文档名称 (LibName)
}

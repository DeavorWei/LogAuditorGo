package model

import "time"

// Device 设备信息（存储在任务独立数据库中）
type Device struct {
	ID           uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	TaskID       string    `gorm:"size:64;index" json:"task_id"`
	DeviceName   string    `gorm:"size:255" json:"device_name"`    // 用户定义的设备名称 (如: Router-Core-01)
	DeviceType   string    `gorm:"size:128" json:"device_type"`    // 设备类型: Router, Switch, Firewall 等
	ManagementIP string    `gorm:"size:64" json:"management_ip"`   // 管理 IP
	Hostname     string    `gorm:"size:128;index" json:"hostname"` // 匹配日志中的 Hostname
	Description  string    `gorm:"type:text" json:"description"`   // 描述
	Color        string    `gorm:"size:32" json:"color"`           // 时间线标识颜色 (如 #3B82F6)
	LogCount     int       `json:"log_count"`                      // 该设备导入的日志总数
	MatchedCount int       `json:"matched_count"`                  // 知识库匹配数
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (Device) TableName() string {
	return "devices"
}

// MultiDeviceLogFilter 多设备日志联合查询过滤参数
type MultiDeviceLogFilter struct {
	DeviceIDs  []uint     `json:"device_ids"`  // 选中的设备 ID 列表 (空表示选中的所有设备)
	Modules    []string   `json:"modules"`     // 模块过滤 (如 ["OSPF", "BGP", "IFNET"])
	Briefs     []string   `json:"briefs"`      // 助记符/事件简名过滤
	Severity   *int       `json:"severity"`    // 严重级别过滤 (<= Severity)
	Keyword    string     `json:"keyword"`     // 报文关键词检索
	TimeStart  *time.Time `json:"time_start"`  // 开始时间
	TimeEnd    *time.Time `json:"time_end"`    // 结束时间
	Page       int        `json:"page"`        // 分页页码
	PageSize   int        `json:"page_size"`   // 每页数量 (-1 表示导出全量)
	AscOrder   bool       `json:"asc_order"`   // 是否按时间升序排列 (时间线通常需要升序)
}

// DeviceTimelineEvent 多设备时间线合并事件项
type DeviceTimelineEvent struct {
	LogID           uint              `json:"log_id"`
	Timestamp       time.Time         `json:"timestamp"`
	DeviceID        uint              `json:"device_id"`
	DeviceName      string            `json:"device_name"`
	DeviceColor     string            `json:"device_color"`
	Hostname        string            `json:"hostname"`
	Module          string            `json:"module"`
	Brief           string            `json:"brief"`
	Severity        int               `json:"severity"`
	RawLog          string            `json:"raw_log"`
	MessageBody     string            `json:"message_body"`
	SourceFile      string            `json:"source_file"`
	KnowledgeID     uint              `json:"knowledge_id,omitempty"`
	MatchTier       string            `json:"match_tier,omitempty"`
	MatchConfidence float64           `json:"match_confidence,omitempty"`
	Parameters      map[string]string `json:"parameters,omitempty"`
	EventSummary    string            `json:"event_summary,omitempty"`
}

// ModuleCount 模块日志统计项
type ModuleCount struct {
	Module string `json:"module"`
	Count  int    `json:"count"`
}

// DeviceStats 单设备对比统计信息
type DeviceStats struct {
	Device       Device        `json:"device"`
	LogCount     int           `json:"log_count"`
	MatchedCount int           `json:"matched_count"`
	TopModules   []ModuleCount `json:"top_modules"`
	SeverityDist map[int]int   `json:"severity_distribution"`
	FirstSeen    *time.Time    `json:"first_seen,omitempty"`
	LastSeen     *time.Time    `json:"last_seen,omitempty"`
}

// CorrelatedTimelineCluster 多设备时间窗口关联事件聚合簇
type CorrelatedTimelineCluster struct {
	StartTime time.Time             `json:"start_time"`
	EndTime   time.Time             `json:"end_time"`
	Module    string                `json:"module"`
	Devices   []string              `json:"devices"`
	Events    []DeviceTimelineEvent `json:"events"`
	Summary   string                `json:"summary"`
}

// MultiDeviceReport 多设备对比与联合分析报告模型
type MultiDeviceReport struct {
	TaskInfo     *TaskInfo                   `json:"task_info"`
	Devices      []DeviceStats               `json:"devices"`
	TotalLogs    int                         `json:"total_logs"`
	TotalMatched int                         `json:"total_matched"`
	CommonEvents []string                    `json:"common_events"` // 多台设备均出现的事件
	Clusters     []CorrelatedTimelineCluster `json:"clusters"`      // 时间窗口关联事件簇
	Timeline     []DeviceTimelineEvent       `json:"timeline"`      // 关键事件联合时间线 (前 200 条或全量关键事件)
	Conclusion   string                      `json:"conclusion"`    // 自动分析与推断结论
	ExportTime   string                      `json:"export_time"`
}

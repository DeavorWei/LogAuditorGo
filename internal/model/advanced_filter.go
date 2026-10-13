package model

import "time"

// AdvancedCondition 高级筛选单条条件
type AdvancedCondition struct {
	Field    string `json:"field"`              // 白名单字段键，如 raw_log, parameters, severity, module, tag 等
	Op       string `json:"op"`                 // 白名单操作符，如 contains, regex, kv_contains, in, lte, has_any, not_has 等
	Value    string `json:"value"`              // 统一字符串值，由 op 决定解析方式
	Disabled bool   `json:"disabled,omitempty"` // 单条条件是否处于临时禁用态
}

// AdvancedFilter 高级筛选条件组合
type AdvancedFilter struct {
	Logic      string              `json:"logic"`      // "AND" | "OR"，默认为 "AND"
	Conditions []AdvancedCondition `json:"conditions"` // 条件列表，上限扩充至 15 条
}

// LogQueryRequestBody 统一日志查询请求体 (POST /api/v1/tasks/:id/logs/query)
// 同时服务于工作台日志查询、命中预览、批量打标以及结构化报告导出
type LogQueryRequestBody struct {
	Page          int             `json:"page"`
	PageSize      int             `json:"page_size"`
	AfterID       uint            `json:"after_id,omitempty"`
	SortBy        string          `json:"sort_by,omitempty"`
	Order         string          `json:"order,omitempty"`
	Keyword       string          `json:"keyword,omitempty"`
	Severity      *int            `json:"severity,omitempty"`
	Matched       *bool           `json:"matched,omitempty"`
	DeviceID      *uint           `json:"device_id,omitempty"`
	Module        string          `json:"module,omitempty"`
	Brief         string          `json:"brief,omitempty"`
	Hostname      string          `json:"hostname,omitempty"`
	SourceFile    string          `json:"source_file,omitempty"`
	TimeStart     *CustomTime     `json:"time_start,omitempty"`
	TimeEnd       *CustomTime     `json:"time_end,omitempty"`
	Advanced      *AdvancedFilter `json:"advanced,omitempty"`
	TagIDs        []uint          `json:"tag_ids,omitempty"`
	TagLogic      string          `json:"tag_logic,omitempty"`       // "any" | "all"，默认为 "any"
	ExcludeTagIDs []uint          `json:"exclude_tag_ids,omitempty"` // 严格排除的标签ID列表
	UntaggedOnly  bool            `json:"untagged_only,omitempty"`   // 是否仅查询未打标日志
}

// TagCreateRequest 创建标签请求
type TagCreateRequest struct {
	Name   string `json:"name" binding:"required"`
	Color  string `json:"color"`
	Remark string `json:"remark"`
}

// TagUpdateRequest 更新标签请求
type TagUpdateRequest struct {
	Name   string `json:"name"`
	Color  string `json:"color"`
	Remark string `json:"remark"`
}

// SingleTagRequestBody 单条日志打标请求
type SingleTagRequestBody struct {
	TagID uint `json:"tag_id" binding:"required"`
}

// FilterPreset 任务数据库中的筛选预设实体
type FilterPreset struct {
	ID          uint                 `gorm:"primaryKey;autoIncrement" json:"id"`
	TaskID      string               `gorm:"size:64;index" json:"task_id"`
	Name        string               `gorm:"size:64;not null" json:"name"`
	Description string               `gorm:"size:255" json:"description,omitempty"`
	IsSystem    bool                 `gorm:"default:false" json:"is_system"`       // 是否为系统内置专家场景
	Scope       string               `gorm:"size:16;default:'task'" json:"scope"`  // "task" | "system"
	ConfigJSON  string               `gorm:"type:text" json:"-"`                   // 序列化存储 (已剥离 page/pageSize)
	Filters     *LogQueryRequestBody `gorm:"-" json:"filters,omitempty"`           // 反序列化只读字段
	CreatedAt   time.Time            `json:"created_at"`
	UpdatedAt   time.Time            `json:"updated_at"`
}

func (FilterPreset) TableName() string { return "filter_presets" }

// FilterPresetCreateRequest 保存筛选预设请求体
type FilterPresetCreateRequest struct {
	Name        string               `json:"name" binding:"required"`
	Description string               `json:"description"`
	Filters     LogQueryRequestBody `json:"filters" binding:"required"`
}

// FacetValueItem 字段枚举聚合项
type FacetValueItem struct {
	Value string `json:"value"`
	Count int64  `json:"count"`
}


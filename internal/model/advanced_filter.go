package model

// AdvancedCondition 高级筛选单条条件
type AdvancedCondition struct {
	Field string `json:"field"` // 白名单字段键，如 raw_log, parameters, severity, module 等
	Op    string `json:"op"`    // 白名单操作符，如 contains, regex, kv_contains, in, lte 等
	Value string `json:"value"` // 统一字符串值，由 op 决定解析方式
}

// AdvancedFilter 高级筛选条件组合
type AdvancedFilter struct {
	Logic      string              `json:"logic"`      // "AND" | "OR"，默认为 "AND"
	Conditions []AdvancedCondition `json:"conditions"` // 条件列表，上限 10 条
}

// LogQueryRequestBody 统一日志查询请求体 (POST /api/v1/tasks/:id/logs/query)
// 同时服务于工作台日志查询、命中预览、批量打标以及结构化报告导出
type LogQueryRequestBody struct {
	Page       int             `json:"page"`
	PageSize   int             `json:"page_size"`
	AfterID    uint            `json:"after_id,omitempty"`
	SortBy     string          `json:"sort_by,omitempty"`
	Order      string          `json:"order,omitempty"`
	Keyword    string          `json:"keyword,omitempty"`
	Severity   *int            `json:"severity,omitempty"`
	Matched    *bool           `json:"matched,omitempty"`
	DeviceID   *uint           `json:"device_id,omitempty"`
	Module     string          `json:"module,omitempty"`
	Brief      string          `json:"brief,omitempty"`
	Hostname   string          `json:"hostname,omitempty"`
	SourceFile string          `json:"source_file,omitempty"`
	TimeStart  *CustomTime     `json:"time_start,omitempty"`
	TimeEnd    *CustomTime     `json:"time_end,omitempty"`
	Advanced   *AdvancedFilter `json:"advanced,omitempty"`
	TagIDs     []uint          `json:"tag_ids,omitempty"`
	TagLogic   string          `json:"tag_logic,omitempty"` // "any" | "all"，默认为 "any"
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

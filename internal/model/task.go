package model

import "time"

type TaskStatus string

const (
	TaskStatusPending    TaskStatus = "PENDING"
	TaskStatusProcessing TaskStatus = "PROCESSING"
	TaskStatusCompleted  TaskStatus = "COMPLETED"
	TaskStatusFailed     TaskStatus = "FAILED"
)

// TaskInfo 任务元信息（存储在全局库以及任务库中）
type TaskInfo struct {
	TaskID       string     `gorm:"primaryKey;size:64" json:"task_id"`
	TaskName     string     `gorm:"size:255" json:"task_name"`
	DeviceType   string     `gorm:"size:128" json:"device_type"`
	// DeviceVersion 设备软件版本（如 V200R024C00）。
	//
	// PARSE-04: 新增字段。此前生产代码恒以空版本号调用 Match()，
	// 使"同型号同版本优先"档位（150 分）永不生效、偏好较新版本的排序也无从谈起，
	// 旧版本知识可能盖掉新版本正确知识。只有把版本真正透传进去，版本分档才有意义。
	DeviceVersion string    `gorm:"size:64" json:"device_version"`
	FileCount     int       `json:"file_count"`
	DeviceCount  int        `json:"device_count"` // 设备数量
	LogCount     int        `json:"log_count"`
	MatchedCount int        `json:"matched_count"`
	RcaCount     int        `json:"rca_count"`
	DBPath       string     `gorm:"size:512" json:"db_path"`
	Status       TaskStatus `gorm:"size:32" json:"status"`
	ErrorMessage string     `gorm:"type:text" json:"error_message,omitempty"`
	StartTime    time.Time  `json:"start_time"`
	FinishTime   *time.Time `json:"finish_time,omitempty"`
}

func (TaskInfo) TableName() string {
	return "task_info"
}

// TaskFile 任务中已上传的文件记录
type TaskFile struct {
	ID        uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	TaskID    string    `gorm:"size:64;index" json:"task_id"`
	FileName  string    `gorm:"size:255;index" json:"file_name"`
	FileSize  int64     `json:"file_size"`
	LineCount int       `json:"line_count"`
	CreatedAt time.Time `json:"created_at"`
}

func (TaskFile) TableName() string {
	return "task_files"
}

// LogQueryFilter 任务内日志查询过滤参数
type LogQueryFilter struct {
	Page       int        `form:"page" json:"page"`
	PageSize   int        `form:"page_size" json:"page_size"`
	DeviceID   *uint      `form:"device_id" json:"device_id"`
	Module     string     `form:"module" json:"module"`
	Severity   *int       `form:"severity" json:"severity"`
	Brief      string     `form:"brief" json:"brief"`
	Hostname   string     `form:"hostname" json:"hostname"`
	Keyword    string     `form:"keyword" json:"keyword"`
	SourceFile string     `form:"source_file" json:"source_file"`
	Matched    *bool      `form:"matched" json:"matched"`
	TimeStart  *time.Time `form:"time_start" json:"time_start"`
	TimeEnd    *time.Time `form:"time_end" json:"time_end"`

	// AfterID 游标分页锚点 (TASK-13)。
	//
	// 深翻页（offset 很大）时 SQLite 必须先扫描并丢弃 offset 之前的全部行，
	// 大表上越往后越慢。传入上一页最后一行的 ID 即可改为
	// `WHERE id > AfterID ORDER BY id ASC LIMIT n`，代价恒定。
	// 为 0 时回退到传统的 offset 分页，保证既有调用不受影响。
	AfterID uint `form:"after_id" json:"after_id"`
}




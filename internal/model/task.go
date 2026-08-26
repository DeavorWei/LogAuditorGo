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
	FileCount    int        `json:"file_count"`
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
}


package model

import "time"

// Document 文档元数据模型
type Document struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	LibID          string    `gorm:"size:64;uniqueIndex" json:"lib_id"`
	LibVersion     string    `gorm:"size:32" json:"lib_version"`
	LibName        string    `gorm:"size:255" json:"lib_name"`
	ProductType    string    `gorm:"size:128;index" json:"product_type"`
	ProductVersion string    `gorm:"size:64;index" json:"product_version"`
	IssueDate      string    `gorm:"size:32" json:"issue_date"`
	Language       string    `gorm:"size:16" json:"language"`
	TopicNumber    int       `json:"topic_number"`
	LogCount       int       `json:"log_count"`   // 叶子日志数
	AlarmCount     int       `json:"alarm_count"` // 叶子告警数
	FilePath       string    `gorm:"size:512" json:"file_path"`
	ImportedAt     time.Time `json:"imported_at"`
}

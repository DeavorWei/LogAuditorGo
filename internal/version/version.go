// Package version 集中维护产品版本号。
//
// 版本号同时用于：命令行 -version、托盘提示、系统设置"关于"、API 降级页。
// 集中管理避免多处硬编码漂移（原实现 main.go / tray / router 各写一份 "v1.0.0"）。
package version

const (
	// Number 语义化版本号（不含前缀）。
	Number = "1.1.0"
	// String 面向用户的完整版本号。
	String = "v" + Number
)

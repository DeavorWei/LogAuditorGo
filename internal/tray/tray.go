package tray

import (
	"fmt"
	"time"
)

// Config 定义托盘运行配置
type Config struct {
	Port      int       // HTTP 服务监听端口
	Host      string    // 访问主机名 (默认 "localhost")
	LogDir    string    // 日志存储目录
	DataDir   string    // 数据存储目录
	Version   string    // 系统版本
	Theme     IconTheme // 图标主题 (默认 ThemeBlueShield)
	IconPath  string    // 外部自定义图标路径 (可选)
	OnExit    func()    // 退出系统回调
}

// Tray 托盘控制器通用接口
type Tray interface {
	// Start 启动系统托盘消息循环
	Start() error
	// Stop 停止系统托盘并移除图标
	Stop()
	// ShowNotification 弹出系统原生通知
	ShowNotification(title, message string)
	// GetURL 获取指定子路径的完整访问 URL
	GetURL(subpath string) string
}

// baseTray 提供跨平台公共基础属性
type baseTray struct {
	cfg       Config
	startTime time.Time
}

// GetURL 构造访问 URL
func (b *baseTray) GetURL(subpath string) string {
	host := b.cfg.Host
	if host == "" {
		host = "localhost"
	}
	base := fmt.Sprintf("http://%s:%d", host, b.cfg.Port)
	if subpath == "" || subpath == "/" {
		return base
	}
	if subpath[0] != '/' {
		subpath = "/" + subpath
	}
	return base + subpath
}

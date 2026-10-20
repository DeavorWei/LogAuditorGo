// Package nativedialog 提供 Windows 原生文件/文件夹选择对话框。
//
// 背景（规划 F1）：桌面单机的最佳导入方式是"后端直接读取本机路径 + 原生对话框选路"，
// 而不是手填绝对路径。该包只负责把用户选择转换为绝对路径，不读取任何文件内容；
// 非 Windows 或 COM 不可用时统一返回 ErrUnsupported，由前端回退到 Web 路径选择器。
package nativedialog

import (
	"errors"
	"strings"
	"sync"
)

var (
	// ErrUnsupported 当前平台/会话不支持原生对话框。
	ErrUnsupported = errors.New("native file dialog is not supported on this platform")
	// ErrCanceled 用户取消了对话框。
	ErrCanceled = errors.New("native dialog canceled")
	// ErrBusy 已有另一个原生对话框打开（COM modal 不能并发叠加）。
	ErrBusy = errors.New("another native dialog is already open")
)

// Options 对话框选项。
type Options struct {
	Title string
	Exts  []string // 例如 ["log","txt","syslog","gz"]；为空表示不过滤
	Multi bool     // 文件模式是否允许多选
}

// Opener 原生对话框能力抽象，便于 API 层注入测试实现。
type Opener interface {
	OpenFiles(Options) ([]string, error)
	OpenFolder(Options) (string, error)
	Supported() bool
}

var (
	openerMu     sync.RWMutex
	activeOpener Opener = platformOpener{}
)

// Default 返回当前平台的默认实现。
func Default() Opener {
	openerMu.RLock()
	defer openerMu.RUnlock()
	return activeOpener
}

// SetForTest 覆盖默认实现并返回恢复函数（仅测试使用）。
func SetForTest(o Opener) func() {
	openerMu.Lock()
	previous := activeOpener
	activeOpener = o
	openerMu.Unlock()
	return func() {
		openerMu.Lock()
		activeOpener = previous
		openerMu.Unlock()
	}
}

// NormalizeExts 归一化扩展名：去点、去空白、去重、小写。
func NormalizeExts(exts []string) []string {
	result := make([]string, 0, len(exts))
	seen := make(map[string]bool, len(exts))
	for _, ext := range exts {
		item := strings.ToLower(strings.TrimSpace(ext))
		item = strings.TrimPrefix(item, ".")
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		result = append(result, item)
	}
	return result
}

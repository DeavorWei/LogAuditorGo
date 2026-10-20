//go:build !windows

package nativedialog

// platformOpener 非 Windows 平台不提供原生对话框，统一返回 ErrUnsupported，
// 前端据此回退到 Web 路径选择器（规划 F1 的回退要求）。
type platformOpener struct{}

func (platformOpener) Supported() bool { return false }

func (platformOpener) OpenFiles(Options) ([]string, error) { return nil, ErrUnsupported }

func (platformOpener) OpenFolder(Options) (string, error) { return "", ErrUnsupported }

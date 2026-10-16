//go:build windows

package browser

import (
	"fmt"

	"golang.org/x/sys/windows"
)

func openURL(url string) error {
	verbPtr, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return fmt.Errorf("invalid verb: %w", err)
	}
	urlPtr, err := windows.UTF16PtrFromString(url)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}

	// 使用 Windows 原生 ShellExecute 调用系统默认浏览器关联，静默无黑框且响应迅速
	err = windows.ShellExecute(0, verbPtr, urlPtr, nil, nil, windows.SW_SHOWNORMAL)
	if err != nil {
		return fmt.Errorf("ShellExecute failed: %w", err)
	}
	return nil
}

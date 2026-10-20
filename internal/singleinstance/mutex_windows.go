//go:build windows

package singleinstance

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// acquirePlatformLock 在 Windows 上创建命名互斥体。
//
// 命名空间使用 Local\ 而非 Global\：本工具是当前用户会话的桌面应用，
// Local 命名空间无需 SeCreateGlobalPrivilege 特权，普通用户即可运行。
// 互斥体名称由数据目录绝对路径的哈希派生，保证不同数据目录互不影响。
func acquirePlatformLock(dataDir string) (func(), error) {
	name := mutexName(dataDir)
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, fmt.Errorf("encode mutex name failed: %w", err)
	}

	handle, err := windows.CreateMutex(nil, false, namePtr)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		// 已存在同名互斥体：x/sys 同时返回有效句柄，必须关闭避免句柄泄漏。
		if handle != 0 {
			_ = windows.CloseHandle(handle)
		}
		return nil, ErrAlreadyRunning
	}
	if err != nil {
		return nil, fmt.Errorf("create instance mutex failed: %w", err)
	}

	return func() {
		if handle != 0 {
			_ = windows.CloseHandle(handle)
		}
	}, nil
}

// mutexName 由数据目录路径派生稳定的互斥体名称。
func mutexName(dataDir string) string {
	normalized := strings.ToLower(filepath.Clean(dataDir))
	sum := sha256.Sum256([]byte(normalized))
	return "Local\\LogAuditorGo-" + hex.EncodeToString(sum[:8])
}

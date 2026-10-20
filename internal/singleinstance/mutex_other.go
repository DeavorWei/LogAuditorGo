//go:build !windows

package singleinstance

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const lockFileName = "LogAuditorGo.lock"

// staleLockAge 超过该时长的锁文件视为上个进程异常退出遗留。
const staleLockAge = 24 * time.Hour

// acquirePlatformLock 在非 Windows 平台用 O_EXCL 创建锁文件实现单实例。
//
// 进程被 kill -9 时无法执行清理，因此这里对超龄锁做一次"保守回收"：
// 只有锁文件修改时间超过 24 小时才删除，避免误删正在运行的实例。
func acquirePlatformLock(dataDir string) (func(), error) {
	lockPath := filepath.Join(dataDir, lockFileName)

	if info, err := os.Stat(lockPath); err == nil {
		if time.Since(info.ModTime()) < staleLockAge {
			return nil, ErrAlreadyRunning
		}
		_ = os.Remove(lockPath)
	}

	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return nil, ErrAlreadyRunning
		}
		return nil, fmt.Errorf("create instance lock failed: %w", err)
	}
	_, _ = f.WriteString(strconv.Itoa(os.Getpid()) + "\n")
	_ = f.Close()

	return func() {
		_ = os.Remove(lockPath)
	}, nil
}

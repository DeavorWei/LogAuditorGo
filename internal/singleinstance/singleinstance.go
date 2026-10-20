// Package singleinstance 提供"同一数据目录只允许一个服务实例"的进程级保护。
//
// 背景（规划 §4.3 / UX-17）：任务库是物理 SQLite 文件，双开会让两个进程
// 同时写同一份 WAL，轻则锁等待，重则数据损坏。这里在进程启动时抢占
// 命名互斥体（Windows）或锁文件（其他平台），并落一份 instance.json，
// 让第二个实例能直接打开已在运行的 Web 控制台后退出。
package singleinstance

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrAlreadyRunning 表示同一数据目录已有实例在运行。
var ErrAlreadyRunning = errors.New("another LogAuditorGo instance is already running for this data directory")

// InstanceFile 存放运行中实例的访问信息（位于数据目录根部）。
const InstanceFile = "instance.json"

// InstanceInfo 运行中实例的元信息。
type InstanceInfo struct {
	PID       int       `json:"pid"`
	URL       string    `json:"url"`
	Version   string    `json:"version"`
	StartedAt time.Time `json:"started_at"`
}

// Lock 单实例锁。Release 幂等。
type Lock struct {
	dataDir string
	release func()
	once    bool
}

// Acquire 尝试为 dataDir 获取单实例锁。
//
// 返回 ErrAlreadyRunning 时，调用方应读取 instance.json 打开已有实例后退出。
func Acquire(dataDir string) (*Lock, error) {
	if dataDir == "" {
		dataDir = "."
	}
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, fmt.Errorf("resolve data dir failed: %w", err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir %s failed: %w", abs, err)
	}

	release, err := acquirePlatformLock(abs)
	if err != nil {
		return nil, err
	}
	return &Lock{dataDir: abs, release: release}, nil
}

// DataDir 返回锁对应的数据目录绝对路径。
func (l *Lock) DataDir() string {
	if l == nil {
		return ""
	}
	return l.dataDir
}

// Publish 写入/更新 instance.json，供第二个实例发现已在运行的访问地址。
//
// 写入失败不阻断启动：单实例保护本身仍然有效，只是第二个实例
// 无法自动打开控制台（会退化为提示用户手动访问）。
func (l *Lock) Publish(info InstanceInfo) error {
	if l == nil {
		return nil
	}
	if info.StartedAt.IsZero() {
		info.StartedAt = time.Now()
	}
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal instance info failed: %w", err)
	}
	target := filepath.Join(l.dataDir, InstanceFile)
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write instance info failed: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("publish instance info failed: %w", err)
	}
	return nil
}

// Release 释放锁并清理 instance.json（仅清理本进程写入的那份）。
func (l *Lock) Release() {
	if l == nil || l.once {
		return
	}
	l.once = true
	if info, err := ReadInstanceInfo(l.dataDir); err == nil && info.PID == os.Getpid() {
		_ = os.Remove(filepath.Join(l.dataDir, InstanceFile))
	}
	if l.release != nil {
		l.release()
	}
}

// ReadInstanceInfo 读取运行中实例信息；文件不存在或损坏时返回错误。
func ReadInstanceInfo(dataDir string) (InstanceInfo, error) {
	var info InstanceInfo
	path := filepath.Join(dataDir, InstanceFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return info, err
	}
	if err := json.Unmarshal(data, &info); err != nil {
		return info, fmt.Errorf("parse %s failed: %w", path, err)
	}
	return info, nil
}

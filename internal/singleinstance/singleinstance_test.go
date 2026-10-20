package singleinstance

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAcquireRejectsSecondInstance(t *testing.T) {
	dir := t.TempDir()

	first, err := Acquire(dir)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	defer first.Release()

	if _, err := Acquire(dir); err == nil {
		t.Fatalf("second acquire must fail with ErrAlreadyRunning")
	} else if err != ErrAlreadyRunning {
		t.Fatalf("second acquire error = %v, want ErrAlreadyRunning", err)
	}

	// 释放后应可再次获取
	first.Release()
	third, err := Acquire(dir)
	if err != nil {
		t.Fatalf("acquire after release failed: %v", err)
	}
	third.Release()
}

func TestPublishAndReadInstanceInfo(t *testing.T) {
	dir := t.TempDir()
	lock, err := Acquire(dir)
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	defer lock.Release()

	info := InstanceInfo{PID: os.Getpid(), URL: "http://127.0.0.1:8080", Version: "v1.1.0"}
	if err := lock.Publish(info); err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	got, err := ReadInstanceInfo(dir)
	if err != nil {
		t.Fatalf("read instance info failed: %v", err)
	}
	if got.PID != info.PID || got.URL != info.URL {
		t.Fatalf("instance info mismatch: %+v", got)
	}

	// Release 只清理本进程写入的信息
	lock.Release()
	if _, err := os.Stat(filepath.Join(dir, InstanceFile)); !os.IsNotExist(err) {
		t.Fatalf("instance file should be removed after release, err=%v", err)
	}
}

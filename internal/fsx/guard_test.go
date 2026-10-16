package fsx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecurePathGuard_BasicAndEnabled(t *testing.T) {
	// 空白或 nil 守卫
	var nilGuard *SecurePathGuard
	if nilGuard.Enabled() {
		t.Fatalf("expected nil guard to be disabled")
	}
	if nilGuard.Roots() != nil {
		t.Fatalf("expected nil roots for nil guard")
	}
	if err := nilGuard.Validate("C:\\any\\path"); err != nil {
		t.Fatalf("expected nil error on disabled guard, got %v", err)
	}

	emptyGuard := NewSecurePathGuard([]string{"", "   "})
	if emptyGuard.Enabled() {
		t.Fatalf("expected empty roots guard to be disabled")
	}
	if err := emptyGuard.ValidateAll([]string{"/path/one", "/path/two"}); err != nil {
		t.Fatalf("expected nil error on disabled guard ValidateAll")
	}
}

func TestSecurePathGuard_ValidateRoots(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "guard_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	subDir := filepath.Join(tempDir, "allowed_sub")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("failed to create sub dir: %v", err)
	}

	outsideDir, err := os.MkdirTemp("", "guard_outside_*")
	if err != nil {
		t.Fatalf("failed to create outside dir: %v", err)
	}
	defer os.RemoveAll(outsideDir)

	guard := NewSecurePathGuard([]string{tempDir})
	if !guard.Enabled() {
		t.Fatalf("expected guard to be enabled")
	}
	roots := guard.Roots()
	if len(roots) != 1 {
		t.Fatalf("expected 1 root, got %d", len(roots))
	}

	// 测试白名单内部子目录与文件
	validFile := filepath.Join(subDir, "log.txt")
	if err := guard.Validate(validFile); err != nil {
		t.Fatalf("expected valid file within root to pass, got: %v", err)
	}

	// 测试根目录自身
	if err := guard.Validate(tempDir); err != nil {
		t.Fatalf("expected root dir itself to pass, got: %v", err)
	}

	// 测试大小写混用路径 (Windows 兼容性)
	mixedCase := strings.ToUpper(validFile)
	if err := guard.Validate(mixedCase); err != nil {
		t.Fatalf("expected mixed case path to pass on Windows, got: %v", err)
	}

	// 测试白名单外部路径拒绝
	outsideFile := filepath.Join(outsideDir, "secret.txt")
	if err := guard.Validate(outsideFile); err == nil {
		t.Fatalf("expected outside path to be rejected")
	}

	// 测试目录遍历 ../ 绕过尝试
	traversal := filepath.Join(tempDir, "..", "secret.txt")
	if err := guard.Validate(traversal); err == nil {
		t.Fatalf("expected traversal path to be rejected")
	}

	// 测试批量校验 ValidateAll
	allValid := []string{validFile, subDir}
	if err := guard.ValidateAll(allValid); err != nil {
		t.Fatalf("expected all valid paths to pass, got: %v", err)
	}

	someInvalid := []string{validFile, outsideFile, traversal}
	if err := guard.ValidateAll(someInvalid); err == nil {
		t.Fatalf("expected ValidateAll to fail when invalid paths present")
	}

	// 测试超过 5 个非法路径的截断提示
	sixInvalid := []string{
		filepath.Join(outsideDir, "1"),
		filepath.Join(outsideDir, "2"),
		filepath.Join(outsideDir, "3"),
		filepath.Join(outsideDir, "4"),
		filepath.Join(outsideDir, "5"),
		filepath.Join(outsideDir, "6"),
	}
	errAll := guard.ValidateAll(sixInvalid)
	if errAll == nil || !strings.Contains(errAll.Error(), "共 6 个被拒绝") {
		t.Fatalf("expected '共 6 个被拒绝' in error message, got: %v", errAll)
	}
}

func TestIsWithin_Boundaries(t *testing.T) {
	if isWithin("", "C:\\test") {
		t.Fatalf("empty root should return false")
	}
	if isWithin("C:\\test", "") {
		t.Fatalf("empty target should return false")
	}

	// 验证同前缀名称伪命中防范 (如 C:\data 和 C:\database)
	if isWithin("C:\\data", "C:\\database") {
		t.Fatalf("C:\\data should NOT contain C:\\database")
	}
	if !isWithin("C:\\data", "C:\\data\\sub\\file.txt") {
		t.Fatalf("C:\\data should contain C:\\data\\sub\\file.txt")
	}
}

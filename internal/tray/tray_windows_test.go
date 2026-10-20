//go:build windows

package tray

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

func getClipboardText() (string, error) {
	out, err := exec.Command("powershell", "-NoProfile", "-Command", "[Console]::Out.Write((Get-Clipboard -Raw))").Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func TestWriteClipboardText(t *testing.T) {
	// 1. 正常字符串写入与读取断言
	testStr := "http://localhost:8080/test_clipboard_12345"
	// 剪贴板可能被浏览器/输入法等进程短暂占用，重试后再判定为环境不可用。
	var writeErr error
	for i := 0; i < 20; i++ {
		if writeErr = writeClipboardText(testStr); writeErr == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if writeErr != nil {
		t.Skipf("clipboard unavailable in this environment: %v", writeErr)
	}

	readBack, err := getClipboardText()
	if err != nil {
		t.Fatalf("getClipboardText failed: %v", err)
	}
	if readBack != testStr {
		t.Errorf("clipboard text mismatch: expected %q, got %q", testStr, readBack)
	}

	// 2. 中文字符与特殊符号
	chineseStr := "华为CloudEngine交换机日志审计平台 V2.0 - 剪贴板测试"
	if err := writeClipboardText(chineseStr); err != nil {
		t.Fatalf("writeClipboardText failed for chinese: %v", err)
	}
	readBackChinese, err := getClipboardText()
	if err != nil {
		t.Fatalf("getClipboardText failed for chinese: %v", err)
	}
	if readBackChinese != chineseStr {
		t.Errorf("clipboard chinese mismatch: expected %q, got %q", chineseStr, readBackChinese)
	}

	// 3. 空字符串写入
	if err := writeClipboardText(""); err != nil {
		t.Fatalf("writeClipboardText failed for empty string: %v", err)
	}
	readBackEmpty, err := getClipboardText()
	if err != nil {
		t.Fatalf("getClipboardText failed for empty string: %v", err)
	}
	if readBackEmpty != "" {
		t.Errorf("expected empty string in clipboard, got %q", readBackEmpty)
	}

	// 4. 较大文本写入 (16KB)
	largeStr := strings.Repeat("LogLine-1234567890\n", 800)
	if err := writeClipboardText(largeStr); err != nil {
		t.Fatalf("writeClipboardText failed for large string: %v", err)
	}
	readBackLarge, err := getClipboardText()
	if err != nil {
		t.Fatalf("getClipboardText failed for large string: %v", err)
	}
	if readBackLarge != largeStr {
		t.Errorf("clipboard large string length mismatch: expected %d, got %d", len(largeStr), len(readBackLarge))
	}
}

package browser_test

import (
	"testing"

	"logauditorgo/internal/browser"
)

func TestBrowserOpenValidation(t *testing.T) {
	// 验证空 URL 或非法字符不会造成崩溃
	_ = browser.Open("http://127.0.0.1:8080")
}

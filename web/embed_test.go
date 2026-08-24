package web_test

import (
	"io"
	"strings"
	"testing"

	"logauditorgo/web"
)

func TestEmbeddedDistFS(t *testing.T) {
	distFS := web.DistFS()

	// 验证 index.html 存在且可读
	f, err := distFS.Open("index.html")
	if err != nil {
		t.Fatalf("failed to open embedded index.html: %v", err)
	}
	defer f.Close()

	content, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("failed to read embedded index.html: %v", err)
	}

	if !strings.Contains(string(content), "LogAuditorGo") {
		t.Errorf("expected index.html to contain 'LogAuditorGo', got: %s", string(content))
	}

	// 验证 assets 目录下的静态资源可以访问
	assetsDir, err := distFS.Open("assets")
	if err != nil {
		t.Fatalf("failed to open assets dir: %v", err)
	}
	defer assetsDir.Close()
}

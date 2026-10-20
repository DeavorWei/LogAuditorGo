package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"logauditorgo/internal/config"
)

// TestFeaturesDefaultsAndToggle 验证 v1.1 功能开关的默认值、运行时切换、持久化与边界拒绝。
func TestFeaturesDefaultsAndToggle(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config failed: %v", err)
	}

	// 1. 默认值：RCA 关闭（只保留、不演进），原生对话框开启
	if cfg.Features.RCA {
		t.Fatalf("features.rca must default to false")
	}
	if !cfg.Features.NativeFileDialog {
		t.Fatalf("features.native_file_dialog must default to true")
	}
	if got := config.Features(); got.RCA {
		t.Fatalf("config.Features() must reflect default-off RCA, got %+v", got)
	}

	// 2. 打开 RCA 并落盘
	updated, err := config.UpdateFeatures(map[string]bool{"rca": true})
	if err != nil {
		t.Fatalf("update features failed: %v", err)
	}
	if !updated.RCA {
		t.Fatalf("expected rca=true after update")
	}
	if !config.Features().RCA {
		t.Fatalf("runtime snapshot must see rca=true immediately")
	}

	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read persisted config failed: %v", err)
	}
	if !strings.Contains(string(raw), "rca: true") {
		t.Fatalf("expected persisted config to contain 'rca: true', got:\n%s", string(raw))
	}

	// 3. 关闭后再次确认
	if _, err := config.UpdateFeatures(map[string]bool{"rca": false}); err != nil {
		t.Fatalf("disable rca failed: %v", err)
	}
	if config.Features().RCA {
		t.Fatalf("expected rca=false after disable")
	}

	// 4. 产品明确不做的能力必须被拒绝，而不是静默接受
	for _, key := range []string{"browser_upload", "lan_mode", "masking", "unknown_key"} {
		if _, err := config.UpdateFeatures(map[string]bool{key: true}); !errors.Is(err, config.ErrFeatureNotImplemented) {
			t.Fatalf("UpdateFeatures(%q) error = %v, want ErrFeatureNotImplemented", key, err)
		}
	}
}

package fsx_test

import (
	"os"
	"path/filepath"
	"testing"

	"logauditorgo/internal/fsx"
)

// TestBrowseCounts 验证 Browse 返回的 dir_count/file_count 为
// 过滤后、分页前的全量统计，不受 limit 分页影响。
func TestBrowseCounts(t *testing.T) {
	dir := t.TempDir()

	// 3 个子目录
	for _, name := range []string{"sub_a", "sub_b", "sub_c"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0755); err != nil {
			t.Fatalf("mkdir failed: %v", err)
		}
	}
	// 5 个匹配扩展名的文件
	for _, name := range []string{"a.log", "b.log", "c.txt", "d.syslog", "e.log"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644); err != nil {
			t.Fatalf("write file failed: %v", err)
		}
	}
	// 2 个不匹配扩展名的文件（应被过滤且不计入 file_count）
	for _, name := range []string{"ignore.bin", "ignore.dat"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644); err != nil {
			t.Fatalf("write file failed: %v", err)
		}
	}

	// 仅取 2 条，验证计数与分页解耦
	res, err := fsx.Browse(fsx.BrowseOptions{
		Path:  dir,
		Exts:  []string{".log", ".txt", ".syslog"},
		Limit: 2,
	})
	if err != nil {
		t.Fatalf("Browse failed: %v", err)
	}
	if res.DirCount != 3 {
		t.Errorf("DirCount = %d, want 3", res.DirCount)
	}
	if res.FileCount != 5 {
		t.Errorf("FileCount = %d, want 5", res.FileCount)
	}
	if len(res.Entries) != 2 {
		t.Errorf("Entries = %d, want 2 (paged)", len(res.Entries))
	}
	if res.Total != 8 {
		t.Errorf("Total = %d, want 8", res.Total)
	}
	if !res.Truncated {
		t.Error("Truncated = false, want true")
	}
}

// TestBrowseCountsDirsOnly dirs_only 模式下不返回文件，file_count 应为 0。
func TestBrowseCountsDirsOnly(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"sub_a", "sub_b"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0755); err != nil {
			t.Fatalf("mkdir failed: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "a.log"), []byte("x"), 0644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}

	res, err := fsx.Browse(fsx.BrowseOptions{Path: dir, DirsOnly: true})
	if err != nil {
		t.Fatalf("Browse failed: %v", err)
	}
	if res.DirCount != 2 {
		t.Errorf("DirCount = %d, want 2", res.DirCount)
	}
	if res.FileCount != 0 {
		t.Errorf("FileCount = %d, want 0", res.FileCount)
	}
}

// TestRootsIncludesDesktop 桌面目录存在时，快捷入口必须包含"桌面"。
func TestRootsIncludesDesktop(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot resolve home dir")
	}
	desktop := ""
	for _, name := range []string{"Desktop", "桌面"} {
		p := filepath.Join(home, name)
		if info, statErr := os.Stat(p); statErr == nil && info.IsDir() {
			desktop = filepath.Clean(p)
			break
		}
	}
	if desktop == "" {
		t.Skip("no desktop directory on this machine")
	}

	res := fsx.Roots()
	found := false
	for _, s := range res.Shortcuts {
		if s.Name == "桌面" {
			found = true
			if s.Path != desktop {
				t.Errorf("desktop shortcut path = %q, want %q", s.Path, desktop)
			}
		}
	}
	if !found {
		t.Error("shortcuts does not contain 桌面 entry")
	}
}

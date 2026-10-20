package backup_test

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"logauditorgo/internal/backup"
)

func setupBackupFixture(t *testing.T) (backup.Options, string) {
	t.Helper()
	dataDir := t.TempDir()
	knowledgeDB := filepath.Join(dataDir, "knowledge.db")
	bleveDir := filepath.Join(dataDir, "bleve", "knowledge.bleve")
	taskDir := filepath.Join(dataDir, "tasks")
	logDir := filepath.Join(dataDir, "log")
	configFile := filepath.Join(dataDir, "config.yaml")

	if err := os.WriteFile(knowledgeDB, []byte("db-v1"), 0o600); err != nil {
		t.Fatalf("write knowledge db failed: %v", err)
	}
	if err := os.MkdirAll(bleveDir, 0o755); err != nil {
		t.Fatalf("mkdir bleve failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bleveDir, "index_meta.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("write bleve index failed: %v", err)
	}
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatalf("mkdir tasks failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "task_demo.db"), []byte("task"), 0o600); err != nil {
		t.Fatalf("write task db failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "onboarding.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("write onboarding failed: %v", err)
	}
	if err := os.WriteFile(configFile, []byte("server:\n  port: 8080\n"), 0o600); err != nil {
		t.Fatalf("write config failed: %v", err)
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("mkdir log failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "log.log"), []byte("log line\n"), 0o600); err != nil {
		t.Fatalf("write log failed: %v", err)
	}

	return backup.Options{
		DataDir:     dataDir,
		KnowledgeDB: knowledgeDB,
		BleveIndex:  bleveDir,
		TaskDir:     taskDir,
		LogDir:      logDir,
		ConfigFile:  configFile,
	}, dataDir
}

func TestBackupCreateListRestore(t *testing.T) {
	opts, dataDir := setupBackupFixture(t)

	info, err := backup.Create(opts)
	if err != nil {
		t.Fatalf("create backup failed: %v", err)
	}
	if info.Size == 0 || !strings.HasSuffix(info.Name, ".zip") {
		t.Fatalf("unexpected backup info: %+v", info)
	}

	list, err := backup.List(dataDir)
	if err != nil || len(list) != 1 || list[0].Name != info.Name {
		t.Fatalf("list backups failed: %+v, err=%v", list, err)
	}

	// 归档包含知识库/索引/任务库/配置/引导状态
	reader, err := zip.OpenReader(filepath.Join(backup.Dir(dataDir), info.Name))
	if err != nil {
		t.Fatalf("open backup zip failed: %v", err)
	}
	names := map[string]bool{}
	for _, file := range reader.File {
		names[file.Name] = true
	}
	_ = reader.Close()
	for _, want := range []string{"knowledge.db", "config.yaml", "onboarding.json", "bleve/index_meta.json", "tasks/task_demo.db"} {
		if !names[want] {
			t.Fatalf("backup missing %s, got %v", want, names)
		}
	}

	if err := backup.ValidateArchive(filepath.Join(backup.Dir(dataDir), info.Name)); err != nil {
		t.Fatalf("validate archive failed: %v", err)
	}

	// 修改当前数据后恢复：下次启动应用，旧值回到 db-v1
	if err := os.WriteFile(opts.KnowledgeDB, []byte("db-v2"), 0o600); err != nil {
		t.Fatalf("modify knowledge db failed: %v", err)
	}
	if err := backup.PrepareRestore(dataDir, info.Name); err != nil {
		t.Fatalf("prepare restore failed: %v", err)
	}
	if !backup.HasPendingRestore(dataDir) {
		t.Fatalf("expected pending restore marker")
	}
	if err := backup.ApplyPendingRestore(opts, t.Logf); err != nil {
		t.Fatalf("apply restore failed: %v", err)
	}
	content, err := os.ReadFile(opts.KnowledgeDB)
	if err != nil || string(content) != "db-v1" {
		t.Fatalf("restore content = %q, err=%v", string(content), err)
	}
	if backup.HasPendingRestore(dataDir) {
		t.Fatalf("marker should be removed after restore")
	}
	matches, _ := filepath.Glob(opts.KnowledgeDB + ".pre_restore_*")
	if len(matches) == 0 {
		t.Fatalf("expected pre_restore backup of previous data")
	}
	restoreLog, err := os.ReadFile(filepath.Join(dataDir, "restore.log"))
	if err != nil || !strings.Contains(string(restoreLog), "restore applied") {
		t.Fatalf("restore.log missing: %v", err)
	}
}

func TestBackupRejectsInvalidArchives(t *testing.T) {
	opts, dataDir := setupBackupFixture(t)
	info, err := backup.Create(opts)
	if err != nil {
		t.Fatalf("create backup failed: %v", err)
	}
	archivePath := filepath.Join(backup.Dir(dataDir), info.Name)

	// 非法扩展名
	if _, err := backup.Create(backup.Options{DataDir: ""}); err == nil {
		t.Fatalf("empty data dir must be rejected")
	}

	// 路径穿越条目
	evilPath := filepath.Join(backup.Dir(dataDir), "backup_20990101_000000.zip")
	file, err := os.Create(evilPath)
	if err != nil {
		t.Fatalf("create evil zip failed: %v", err)
	}
	zw := zip.NewWriter(file)
	writer, _ := zw.Create("../evil.txt")
	_, _ = writer.Write([]byte("x"))
	_ = zw.Close()
	_ = file.Close()
	if err := backup.ValidateArchive(evilPath); err == nil {
		t.Fatalf("path traversal archive must be rejected")
	}

	// 名称白名单
	if err := backup.PrepareRestore(dataDir, "../"+info.Name); err == nil {
		t.Fatalf("illegal backup name must be rejected")
	}

	// 合法归档仍可校验
	if err := backup.ValidateArchive(archivePath); err != nil {
		t.Fatalf("valid archive rejected: %v", err)
	}
}

func TestWriteDiagnostics(t *testing.T) {
	opts, _ := setupBackupFixture(t)
	var buf strings.Builder
	err := backup.WriteDiagnostics(&buf, opts, map[string]any{"version": "v1.3.0"})
	if err != nil {
		t.Fatalf("write diagnostics failed: %v", err)
	}
	// zip 是二进制，无法用 strings.Builder 校验内容；改为写临时文件解压检查
	_ = buf
	tempFile := filepath.Join(t.TempDir(), "diagnostics.zip")
	file, err := os.Create(tempFile)
	if err != nil {
		t.Fatalf("create diagnostics file failed: %v", err)
	}
	if err := backup.WriteDiagnostics(file, opts, map[string]any{"version": "v1.3.0"}); err != nil {
		t.Fatalf("write diagnostics failed: %v", err)
	}
	_ = file.Close()
	reader, err := zip.OpenReader(tempFile)
	if err != nil {
		t.Fatalf("diagnostics is not a valid zip: %v", err)
	}
	defer reader.Close()
	found := map[string]bool{}
	for _, f := range reader.File {
		found[f.Name] = true
	}
	if !found["system.json"] || !found["README.txt"] || !found["config.yaml"] {
		t.Fatalf("diagnostics missing entries: %v", found)
	}
	for name := range found {
		if strings.HasPrefix(name, "tasks/") {
			t.Fatalf("diagnostics must not contain task logs: %s", name)
		}
	}
}

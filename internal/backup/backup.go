// Package backup 提供一键备份、恢复（下次启动生效）与诊断包。
//
// 设计约束（方案审视 §2.5/§2.6）：
//   - 备份只在长任务空闲时执行；任务库先关闭连接再复制；
//   - knowledge.db 优先使用 VACUUM INTO 生成一致快照；
//   - 恢复只写 marker，由下次启动在打开任何数据库前应用，失败可回滚；
//   - 归档条目白名单校验，拒绝绝对路径/.. 穿越。
package backup

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"logauditorgo/internal/model"
	"logauditorgo/internal/storage"
)

const (
	// DirName 备份目录名（位于数据目录下）。
	DirName = "backups"
	// KeepCount 保留最近备份份数。
	KeepCount = 3
	// MarkerFile 恢复待处理标记。
	MarkerFile = "restore.pending"
	// RestoreArchive 待应用恢复包。
	RestoreArchive = "restore.zip"

	maxArchiveEntryBytes = 20 << 30
	maxArchiveBytes      = 64 << 30
)

var backupNamePattern = regexp.MustCompile(`^backup_\d{8}_\d{6}\.zip$`)

// ErrInvalidBackup 备份名称或归档内容非法。
var ErrInvalidBackup = errors.New("invalid backup archive")

// Options 备份/恢复涉及的路径与连接。
type Options struct {
	DataDir     string
	KnowledgeDB string
	BleveIndex  string
	TaskDir     string
	LogDir      string
	ConfigFile  string
	DB          *gorm.DB
}

// Info 备份文件元信息。
type Info struct {
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
}

// Dir 返回备份目录。
func Dir(dataDir string) string {
	return filepath.Join(dataDir, DirName)
}

var backupMu sync.Mutex

// Create 生成一份备份并保留最近 KeepCount 份。
func Create(opts Options) (Info, error) {
	// 备份/恢复互斥：避免并发请求写同名 .tmp 或互相覆盖恢复 marker。
	backupMu.Lock()
	defer backupMu.Unlock()
	if strings.TrimSpace(opts.DataDir) == "" {
		return Info{}, fmt.Errorf("data dir is required")
	}
	dir := Dir(opts.DataDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Info{}, fmt.Errorf("create backup dir failed: %w", err)
	}

	name := "backup_" + time.Now().Format("20060102_150405") + ".zip"
	finalPath := filepath.Join(dir, name)
	tmpPath := finalPath + ".tmp"

	file, err := os.Create(tmpPath)
	if err != nil {
		return Info{}, fmt.Errorf("create backup file failed: %w", err)
	}
	zw := zip.NewWriter(file)

	writeErr := func() error {
		if err := addKnowledgeDB(zw, opts); err != nil {
			return err
		}
		if opts.ConfigFile != "" {
			if err := addFileIfExists(zw, opts.ConfigFile, "config.yaml"); err != nil {
				return err
			}
		}
		if err := addFileIfExists(zw, filepath.Join(opts.DataDir, "onboarding.json"), "onboarding.json"); err != nil {
			return err
		}
		if err := addDir(zw, opts.BleveIndex, "bleve"); err != nil {
			return err
		}
		if err := addTasks(zw, opts); err != nil {
			return err
		}
		return nil
	}()

	if closeErr := zw.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if closeErr := file.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		_ = os.Remove(tmpPath)
		return Info{}, writeErr
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		return Info{}, fmt.Errorf("finalize backup failed: %w", err)
	}
	// 自校验：确保 knowledge.db 等必备条目存在，避免产出“成功但不可恢复”的备份。
	if err := ValidateArchive(finalPath); err != nil {
		_ = os.Remove(finalPath)
		return Info{}, fmt.Errorf("backup validation failed: %w", err)
	}

	if err := prune(dir); err != nil {
		// 保留策略失败不影响本次备份可用性
		fmt.Fprintf(os.Stderr, "[Backup][WARN] prune old backups failed: %v\n", err)
	}

	stat, err := os.Stat(finalPath)
	if err != nil {
		return Info{}, err
	}
	return Info{Name: name, Size: stat.Size(), CreatedAt: stat.ModTime()}, nil
}

// addTasks 把任务库写入备份。
//
// 优先对每个任务执行 VACUUM INTO 生成一致快照：既避免直接复制 WAL 中的半写状态，
// 也避免 CloseAllTaskDBs 影响正在查询的工作台请求；无法查询任务列表时回退为文件复制。
func addTasks(zw *zip.Writer, opts Options) error {
	if opts.DB == nil || strings.TrimSpace(opts.TaskDir) == "" {
		return addDir(zw, opts.TaskDir, "tasks")
	}
	var taskIDs []string
	if err := opts.DB.Model(&model.TaskInfo{}).Pluck("task_id", &taskIDs).Error; err != nil {
		return addDir(zw, opts.TaskDir, "tasks")
	}
	if len(taskIDs) == 0 {
		return nil
	}
	staging, err := os.MkdirTemp("", "logauditorgo_backup_tasks_*")
	if err != nil {
		return fmt.Errorf("create task snapshot dir failed: %w", err)
	}
	defer os.RemoveAll(staging)

	for _, taskID := range taskIDs {
		db, _, err := storage.GetOrCreateTaskDB(opts.TaskDir, taskID)
		if err != nil {
			continue
		}
		snapshot := filepath.Join(staging, "task_"+taskID+".db")
		query := "VACUUM INTO '" + strings.ReplaceAll(snapshot, "'", "''") + "'"
		execErr := db.Exec(query).Error
		storage.ReleaseTaskDB(taskID)
		if execErr == nil {
			if err := addFile(zw, snapshot, path.Join("tasks", filepath.Base(snapshot))); err != nil {
				return err
			}
			_ = os.Remove(snapshot)
			continue
		}
		// 回退：直接复制主文件 + WAL/SHM（尽力而为）
		dbPath := storage.TaskDBPath(opts.TaskDir, taskID)
		if err := addFileIfExists(zw, dbPath, path.Join("tasks", filepath.Base(dbPath))); err != nil {
			return err
		}
		_ = addFileIfExists(zw, dbPath+"-wal", path.Join("tasks", filepath.Base(dbPath)+"-wal"))
		_ = addFileIfExists(zw, dbPath+"-shm", path.Join("tasks", filepath.Base(dbPath)+"-shm"))
	}
	return nil
}

func addKnowledgeDB(zw *zip.Writer, opts Options) error {
	if opts.DB != nil {
		snapshot := filepath.Join(os.TempDir(), fmt.Sprintf("logauditorgo_backup_%d.db", time.Now().UnixNano()))
		query := "VACUUM INTO '" + strings.ReplaceAll(snapshot, "'", "''") + "'"
		if err := opts.DB.Exec(query).Error; err == nil {
			defer os.Remove(snapshot)
			return addFile(zw, snapshot, "knowledge.db")
		}
		// 回退：VACUUM INTO 不可用时至少复制主文件 + WAL/SHM
		_ = os.Remove(snapshot)
	}
	if err := addFileIfExists(zw, opts.KnowledgeDB, "knowledge.db"); err != nil {
		return err
	}
	if err := addFileIfExists(zw, opts.KnowledgeDB+"-wal", "knowledge.db-wal"); err != nil {
		return err
	}
	if err := addFileIfExists(zw, opts.KnowledgeDB+"-shm", "knowledge.db-shm"); err != nil {
		return err
	}
	return nil
}

func addDir(zw *zip.Writer, srcDir, archiveRoot string) error {
	if strings.TrimSpace(srcDir) == "" {
		return nil
	}
	info, err := os.Stat(srcDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", srcDir)
	}
	return filepath.WalkDir(srcDir, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry == nil || entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(srcDir, filePath)
		if err != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("invalid relative path for %s", filePath)
		}
		return addFile(zw, filePath, path.Join(archiveRoot, filepath.ToSlash(rel)))
	})
}

func addFileIfExists(zw *zip.Writer, src, archiveName string) error {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	if _, err := os.Stat(src); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return addFile(zw, src, archiveName)
}

func addFile(zw *zip.Writer, src, archiveName string) error {
	info, err := os.Stat(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", src)
	}
	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	header.Name = archiveName
	header.Method = zip.Deflate
	writer, err := zw.CreateHeader(header)
	if err != nil {
		return err
	}
	file, err := os.Open(src)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(writer, file)
	return err
}

// List 返回按时间倒序的备份列表。
func List(dataDir string) ([]Info, error) {
	dir := Dir(dataDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []Info{}, nil
		}
		return nil, err
	}
	items := make([]Info, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !backupNamePattern.MatchString(entry.Name()) {
			continue
		}
		info, statErr := entry.Info()
		if statErr != nil {
			continue
		}
		items = append(items, Info{Name: entry.Name(), Size: info.Size(), CreatedAt: info.ModTime()})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	return items, nil
}

// Delete 删除指定备份（名称白名单）。
func Delete(dataDir, name string) error {
	filePath, err := backupPath(dataDir, name)
	if err != nil {
		return err
	}
	if err := os.Remove(filePath); err != nil {
		return err
	}
	return nil
}

func prune(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	type item struct {
		name string
		time time.Time
	}
	list := make([]item, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !backupNamePattern.MatchString(entry.Name()) {
			continue
		}
		info, statErr := entry.Info()
		if statErr != nil {
			continue
		}
		list = append(list, item{name: entry.Name(), time: info.ModTime()})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].time.After(list[j].time) })
	for i := KeepCount; i < len(list); i++ {
		_ = os.Remove(filepath.Join(dir, list[i].name))
	}
	return nil
}

func backupPath(dataDir, name string) (string, error) {
	if !backupNamePattern.MatchString(name) {
		return "", ErrInvalidBackup
	}
	return filepath.Join(Dir(dataDir), name), nil
}

// ArchivePath 返回白名单备份文件的绝对路径。
func ArchivePath(dataDir, name string) (string, error) {
	return backupPath(dataDir, name)
}

// ValidateArchive 校验归档结构（白名单 + 路径穿越 + 体积上限）。
func ValidateArchive(archivePath string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open backup archive failed: %w", err)
	}
	defer reader.Close()

	var total uint64
	hasKnowledgeDB := false
	for _, file := range reader.File {
		name := path.Clean(strings.ReplaceAll(file.Name, "\\", "/"))
		if name == "." || strings.HasPrefix(name, "../") || strings.Contains(name, "/../") || path.IsAbs(name) {
			return fmt.Errorf("%w: illegal entry %q", ErrInvalidBackup, file.Name)
		}
		if !allowedEntry(name) {
			return fmt.Errorf("%w: unexpected entry %q", ErrInvalidBackup, file.Name)
		}
		if name == "knowledge.db" {
			hasKnowledgeDB = true
		}
		if file.UncompressedSize64 > maxArchiveEntryBytes {
			return fmt.Errorf("%w: entry %q too large", ErrInvalidBackup, file.Name)
		}
		total += file.UncompressedSize64
		if total > maxArchiveBytes {
			return fmt.Errorf("%w: archive too large", ErrInvalidBackup)
		}
	}
	if !hasKnowledgeDB {
		return fmt.Errorf("%w: missing knowledge.db", ErrInvalidBackup)
	}
	return nil
}

func allowedEntry(name string) bool {
	switch {
	case name == "knowledge.db", name == "knowledge.db-wal", name == "knowledge.db-shm":
		return true
	case name == "config.yaml", name == "onboarding.json":
		return true
	case strings.HasPrefix(name, "bleve/"), strings.HasPrefix(name, "tasks/"):
		return true
	default:
		return false
	}
}

// PrepareRestore 校验备份并写入恢复标记（下次启动生效）。
func PrepareRestore(dataDir, name string) error {
	backupMu.Lock()
	defer backupMu.Unlock()
	archivePath, err := backupPath(dataDir, name)
	if err != nil {
		return err
	}
	if err := ValidateArchive(archivePath); err != nil {
		return err
	}
	if err := copyFile(archivePath, filepath.Join(dataDir, RestoreArchive)); err != nil {
		return fmt.Errorf("stage restore archive failed: %w", err)
	}
	meta := map[string]any{
		"source":      name,
		"prepared_at": time.Now().Format(time.RFC3339),
	}
	data, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(filepath.Join(dataDir, MarkerFile), data, 0o600); err != nil {
		return fmt.Errorf("write restore marker failed: %w", err)
	}
	return nil
}

// HasPendingRestore 返回是否存在待应用的恢复包。
func HasPendingRestore(dataDir string) bool {
	_, markerErr := os.Stat(filepath.Join(dataDir, MarkerFile))
	_, archiveErr := os.Stat(filepath.Join(dataDir, RestoreArchive))
	return markerErr == nil && archiveErr == nil
}

// ApplyPendingRestore 在启动阶段（打开数据库之前）应用恢复包。
//
// 步骤：解压到 staging → 现有数据改名 pre_restore → 原子替换 → 记录日志；
// 任一步失败尝试回滚，并保留 marker 以便人工介入。
func ApplyPendingRestore(opts Options, logf func(string, ...any)) error {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	dataDir := opts.DataDir
	markerPath := filepath.Join(dataDir, MarkerFile)
	archivePath := filepath.Join(dataDir, RestoreArchive)
	if !HasPendingRestore(dataDir) {
		return nil
	}
	if err := ValidateArchive(archivePath); err != nil {
		logf("[Restore] pending archive invalid: %v", err)
		return err
	}

	stamp := time.Now().Format("20060102_150405")
	staging := filepath.Join(dataDir, ".restore_staging_"+stamp)
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return err
	}
	if err := extractArchive(archivePath, staging); err != nil {
		_ = os.RemoveAll(staging)
		return fmt.Errorf("extract restore archive failed: %w", err)
	}

	// archive 名称 → 实际目标路径
	type moveItem struct {
		staged string
		target string
	}
	items := []moveItem{
		{filepath.Join(staging, "knowledge.db"), opts.KnowledgeDB},
		{filepath.Join(staging, "knowledge.db-wal"), opts.KnowledgeDB + "-wal"},
		{filepath.Join(staging, "knowledge.db-shm"), opts.KnowledgeDB + "-shm"},
		{filepath.Join(staging, "bleve"), opts.BleveIndex},
		{filepath.Join(staging, "tasks"), opts.TaskDir},
		{filepath.Join(staging, "config.yaml"), opts.ConfigFile},
		{filepath.Join(staging, "onboarding.json"), filepath.Join(dataDir, "onboarding.json")},
	}

	moved := make([]moveItem, 0, len(items))
	movedSet := make(map[string]bool, len(items))
	recordMoved := func(target string) {
		if target == "" || movedSet[target] {
			return
		}
		movedSet[target] = true
		moved = append(moved, moveItem{target: target})
	}
	rollback := func() {
		for i := len(moved) - 1; i >= 0; i-- {
			item := moved[i]
			_ = os.RemoveAll(item.target)
			if backupExists(item.target, stamp) {
				_ = os.Rename(item.target+".pre_restore_"+stamp, item.target)
			}
		}
	}

	moveAside := func(target string) error {
		if _, err := os.Stat(target); err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		backupName := target + ".pre_restore_" + stamp
		_ = os.RemoveAll(backupName)
		if err := os.Rename(target, backupName); err != nil {
			return err
		}
		recordMoved(target)
		return nil
	}

	for _, item := range items {
		if _, err := os.Stat(item.staged); err != nil {
			continue
		}
		if strings.TrimSpace(item.target) == "" {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(item.target), 0o755); err != nil {
			rollback()
			return fmt.Errorf("prepare restore target dir failed: %w", err)
		}
		// 先登记旧数据再替换：staged rename 失败时 rollback 也能恢复当前项。
		if err := moveAside(item.target); err != nil {
			rollback()
			return fmt.Errorf("backup current data failed: %w", err)
		}
		// 数据库主文件替换前清走旧 WAL/SHM，避免旧 WAL 回放进新库。
		if strings.HasSuffix(strings.ToLower(item.target), ".db") {
			for _, suffix := range []string{"-wal", "-shm"} {
				if err := moveAside(item.target + suffix); err != nil {
					rollback()
					return fmt.Errorf("backup current wal/shm failed: %w", err)
				}
			}
		}
		if err := os.Rename(item.staged, item.target); err != nil {
			rollback()
			return fmt.Errorf("apply restore item failed: %w", err)
		}
		// staged 文件已就位；若该 target 之前不存在，也要登记以便失败时移除。
		if !movedSet[item.target] {
			recordMoved(item.target)
		}
	}

	logLine := fmt.Sprintf("%s restore applied from %s, pre_restore suffix %s\n",
		time.Now().Format(time.RFC3339), archivePath, stamp)
	_ = appendFile(filepath.Join(dataDir, "restore.log"), logLine)

	_ = os.Remove(markerPath)
	_ = os.Remove(archivePath)
	_ = os.RemoveAll(staging)
	logf("[Restore] applied backup successfully (pre_restore suffix %s)", stamp)
	return nil
}

func backupExists(target, stamp string) bool {
	_, err := os.Stat(target + ".pre_restore_" + stamp)
	return err == nil
}

func extractArchive(archivePath, targetDir string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer reader.Close()
	for _, file := range reader.File {
		name := path.Clean(strings.ReplaceAll(file.Name, "\\", "/"))
		if name == "." || strings.HasPrefix(name, "../") || strings.Contains(name, "/../") || path.IsAbs(name) {
			return fmt.Errorf("%w: illegal entry %q", ErrInvalidBackup, file.Name)
		}
		target := filepath.Join(targetDir, filepath.FromSlash(name))
		rel, err := filepath.Rel(targetDir, target)
		if err != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("%w: escaped entry %q", ErrInvalidBackup, file.Name)
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := file.Open()
		if err != nil {
			return err
		}
		out, err := os.Create(target)
		if err != nil {
			_ = rc.Close()
			return err
		}
		_, copyErr := io.Copy(out, rc)
		_ = out.Close()
		_ = rc.Close()
		if copyErr != nil {
			return copyErr
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func appendFile(filePath, content string) error {
	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.WriteString(content)
	return err
}

// WriteDiagnostics 写出诊断包（配置 + 最近日志 + 运行状态），不含任务日志与知识正文。
func WriteDiagnostics(w io.Writer, opts Options, meta map[string]any) error {
	zw := zip.NewWriter(w)
	if err := addTextEntry(zw, "README.txt", diagnosticsReadme); err != nil {
		return err
	}
	if opts.ConfigFile != "" {
		data, err := os.ReadFile(opts.ConfigFile)
		if err == nil {
			if err := addTextEntry(zw, "config.yaml", string(data)); err != nil {
				return err
			}
		}
	}
	if meta != nil {
		data, err := json.MarshalIndent(meta, "", "  ")
		if err == nil {
			if err := addTextEntry(zw, "system.json", string(data)); err != nil {
				return err
			}
		}
	}
	if opts.LogDir != "" {
		entries, err := os.ReadDir(opts.LogDir)
		if err == nil {
			type logFile struct {
				path string
				time time.Time
			}
			logs := make([]logFile, 0, len(entries))
			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".log") {
					continue
				}
				info, statErr := entry.Info()
				if statErr != nil {
					continue
				}
				logs = append(logs, logFile{path: filepath.Join(opts.LogDir, entry.Name()), time: info.ModTime()})
			}
			sort.Slice(logs, func(i, j int) bool { return logs[i].time.After(logs[j].time) })
			for i := 0; i < len(logs) && i < 3; i++ {
				data, readErr := os.ReadFile(logs[i].path)
				if readErr != nil {
					continue
				}
				name := "logs/" + filepath.Base(logs[i].path)
				if err := addTextEntry(zw, name, string(data)); err != nil {
					return err
				}
			}
		}
	}
	return zw.Close()
}

func addTextEntry(zw *zip.Writer, name, content string) error {
	writer, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = writer.Write([]byte(content))
	return err
}

const diagnosticsReadme = `LogAuditorGo 诊断包
=====================

包含内容：
  config.yaml  - 当前配置（不含任何密钥）
  system.json  - 版本、运行环境、数据库/索引状态
  logs/        - 最近 3 个应用日志文件

不含内容：任务日志正文、知识条目正文、用户上传的原始文件。
如无特殊需要，请勿将诊断包公网传播。
`

package api_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"logauditorgo/internal/api"
	"logauditorgo/internal/config"
	"logauditorgo/internal/knowledge"
	"logauditorgo/internal/matcher"
	"logauditorgo/internal/nativedialog"
	"logauditorgo/internal/rootcause"
	"logauditorgo/internal/search"
	"logauditorgo/internal/storage"
	"logauditorgo/internal/task"
	"logauditorgo/pkg/logger"
)

type fakeDialogOpener struct {
	paths  []string
	folder string
	err    error
}

func (f fakeDialogOpener) Supported() bool { return true }
func (f fakeDialogOpener) OpenFiles(nativedialog.Options) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.paths, nil
}
func (f fakeDialogOpener) OpenFolder(nativedialog.Options) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.folder, nil
}

func setupPhase1Router(t *testing.T) (http.Handler, *task.Service, string) {
	t.Helper()
	logger.Init("warn", "console")

	tmpDir, err := os.MkdirTemp("", "phase1_api_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	cfg := &config.Config{
		Server: config.ServerConfig{Port: 8080, Mode: "test"},
		Storage: config.StorageConfig{
			DataDir:     tmpDir,
			KnowledgeDB: filepath.Join(tmpDir, "knowledge.db"),
			BleveIndex:  filepath.Join(tmpDir, "bleve.index"),
			TaskDir:     filepath.Join(tmpDir, "tasks"),
			UploadDir:   filepath.Join(tmpDir, "uploads"),
		},
		Log:      config.LogConfig{Dir: filepath.Join(tmpDir, "log"), Level: "warn", Format: "console", MaxSizeMB: 1, MaxDays: 1},
		Features: config.DefaultFeatures(),
	}
	config.GlobalConfig = cfg
	config.ConfigFileUsed = filepath.Join(tmpDir, "config.yaml")
	_ = os.WriteFile(config.ConfigFileUsed, []byte("features:\n  rca: false\n"), 0o600)

	storage.CloseAllTaskDBs()
	_ = storage.CloseKnowledgeDB()
	globalDB, err := storage.InitKnowledgeDB(cfg.Storage.KnowledgeDB)
	if err != nil {
		t.Fatalf("init global db failed: %v", err)
	}
	indexer, err := search.InitIndexer(cfg.Storage.BleveIndex)
	if err != nil {
		t.Fatalf("init indexer failed: %v", err)
	}
	knowledgeSvc := knowledge.NewService(globalDB, indexer)
	matchEngine := matcher.NewMatchEngine(globalDB, indexer)
	taskSvc := task.NewService(globalDB, cfg.Storage.TaskDir, matchEngine, rootcause.NewEngine(nil))
	router := api.SetupRouter(cfg, globalDB, knowledgeSvc, indexer, taskSvc)

	t.Cleanup(func() {
		storage.CloseAllTaskDBs()
		_ = indexer.Close()
		_ = storage.CloseKnowledgeDB()
		// Windows 下 WAL/句柄释放存在毫秒级延迟，删除失败时短暂重试，
		// 避免把"临时目录清理失败"误报成产品缺陷。
		for i := 0; i < 20; i++ {
			if err := os.RemoveAll(tmpDir); err == nil {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
	})
	return router, taskSvc, tmpDir
}

func TestNativeDialogEndpoint(t *testing.T) {
	router, _, tmpDir := setupPhase1Router(t)

	restore := nativedialog.SetForTest(fakeDialogOpener{paths: []string{filepath.Join(tmpDir, "a.log")}, folder: tmpDir})
	defer restore()

	// capability
	w, env := doRedRouteRequest(t, router, http.MethodGet, "/api/v1/fs/dialog/capability", "")
	var capData struct {
		Supported bool   `json:"supported"`
		Platform  string `json:"platform"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(env.Data, &capData) != nil || !capData.Supported {
		t.Fatalf("capability failed: %d %s", w.Code, w.Body.String())
	}

	// file
	w, env = doRedRouteRequest(t, router, http.MethodPost, "/api/v1/fs/dialog", `{"mode":"file","exts":["log"]}`)
	var data struct {
		Paths    []string `json:"paths"`
		Canceled bool     `json:"canceled"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(env.Data, &data) != nil || len(data.Paths) != 1 || data.Canceled {
		t.Fatalf("dialog file failed: %d %s", w.Code, w.Body.String())
	}

	// folder
	w, env = doRedRouteRequest(t, router, http.MethodPost, "/api/v1/fs/dialog", `{"mode":"folder"}`)
	if w.Code != http.StatusOK || json.Unmarshal(env.Data, &data) != nil || len(data.Paths) != 1 || data.Paths[0] != tmpDir {
		t.Fatalf("dialog folder failed: %d %s", w.Code, w.Body.String())
	}

	// canceled
	restore()
	restore = nativedialog.SetForTest(fakeDialogOpener{err: nativedialog.ErrCanceled})
	w, env = doRedRouteRequest(t, router, http.MethodPost, "/api/v1/fs/dialog", `{"mode":"file"}`)
	if w.Code != http.StatusOK || json.Unmarshal(env.Data, &data) != nil || !data.Canceled {
		t.Fatalf("canceled dialog must be success+canceled, got %d %s", w.Code, w.Body.String())
	}
	restore()

	// unsupported
	restore = nativedialog.SetForTest(fakeDialogOpener{err: nativedialog.ErrUnsupported})
	w, _ = doRedRouteRequest(t, router, http.MethodPost, "/api/v1/fs/dialog", `{"mode":"file"}`)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("unsupported dialog must return 501, got %d", w.Code)
	}
}

func TestPhase1ExportAndCase(t *testing.T) {
	router, _, _ := setupPhase1Router(t)

	body := `{"task_name":"交付案例-演示","content":"Apr 15 2026 10:00:01 CE16800-CORE %%01BGP/2/PEER_BACKWARD(l)[1]: The BGP peer went down. (PeerAddress=192.168.1.2)\n"}`
	w, env := doRedRouteRequest(t, router, http.MethodPost, "/api/v1/tasks", body)
	var created struct {
		TaskID string `json:"task_id"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(env.Data, &created) != nil || created.TaskID == "" {
		t.Fatalf("create task failed: %d %s", w.Code, w.Body.String())
	}
	taskID := created.TaskID

	// 1. 案例字段
	w, env = doRedRouteRequest(t, router, http.MethodPatch, "/api/v1/tasks/"+taskID+"/case",
		`{"case_status":"located","conclusion":"已定位为 BGP 邻居中断","notes":"现场已更换光纤","external_ref":"INC-1","case_tags":"BGP,链路"}`)
	var updated struct {
		CaseStatus  string `json:"case_status"`
		Conclusion  string `json:"conclusion"`
		CaseTags    string `json:"case_tags"`
		ExternalRef string `json:"external_ref"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(env.Data, &updated) != nil {
		t.Fatalf("update case failed: %d %s", w.Code, w.Body.String())
	}
	if updated.CaseStatus != "located" || updated.Conclusion != "已定位为 BGP 邻居中断" || updated.CaseTags != "BGP,链路" {
		t.Fatalf("case fields mismatch: %+v", updated)
	}

	// 非法状态
	w, _ = doRedRouteRequest(t, router, http.MethodPatch, "/api/v1/tasks/"+taskID+"/case", `{"case_status":"bogus"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid case status must be 400, got %d", w.Code)
	}

	// 2. 案例库按 case_status 过滤
	w, env = doRedRouteRequest(t, router, http.MethodGet, "/api/v1/tasks/query?case_status=located", "")
	var page struct {
		Total int64 `json:"total"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(env.Data, &page) != nil || page.Total != 1 {
		t.Fatalf("case_status filter failed: %d %s", w.Code, w.Body.String())
	}
	w, _ = doRedRouteRequest(t, router, http.MethodGet, "/api/v1/tasks/query?case_status=bogus", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid case_status query must be 400, got %d", w.Code)
	}

	// 3. 摘要 / 草稿 / Markdown
	w, env = doRedRouteRequest(t, router, http.MethodGet, "/api/v1/tasks/"+taskID+"/summary", "")
	var summary struct {
		Overview struct {
			LogCount int64 `json:"log_count"`
		} `json:"overview"`
		Suggestions []string `json:"suggestions"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(env.Data, &summary) != nil || summary.Overview.LogCount != 1 || len(summary.Suggestions) == 0 {
		t.Fatalf("summary failed: %d %s", w.Code, w.Body.String())
	}

	w, env = doRedRouteRequest(t, router, http.MethodGet, "/api/v1/tasks/"+taskID+"/conclusion/draft", "")
	var draft struct {
		Markdown string `json:"markdown"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(env.Data, &draft) != nil || !strings.Contains(draft.Markdown, "排障结论（草稿）") {
		t.Fatalf("conclusion draft failed: %d %s", w.Code, w.Body.String())
	}

	w, _ = doRedRouteRequest(t, router, http.MethodGet, "/api/v1/tasks/"+taskID+"/summary/markdown", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "交付案例-演示") || !strings.Contains(w.Body.String(), "已定位为 BGP 邻居中断") {
		t.Fatalf("summary markdown failed: %d %s", w.Code, w.Body.String())
	}

	// 4. 证据包 ZIP
	w, _ = doRedRouteRequest(t, router, http.MethodGet, "/api/v1/tasks/"+taskID+"/export/package", "")
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("evidence package failed: %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatalf("evidence package is not a valid zip: %v", err)
	}
	found := map[string]bool{}
	for _, f := range zr.File {
		found[f.Name] = true
	}
	for _, want := range []string{"report.html", "logs.csv", "metadata.json", "README.txt"} {
		if !found[want] {
			t.Fatalf("evidence package missing %s, got %v", want, found)
		}
	}
	for _, f := range zr.File {
		if f.Name == "report.html" {
			rc, err := f.Open()
			if err != nil {
				t.Fatalf("open report.html failed: %v", err)
			}
			content, _ := io.ReadAll(rc)
			_ = rc.Close()
			for _, want := range []string{"交付案例-演示", "执行摘要", "关键证据", "方法与局限", "已定位为 BGP 邻居中断"} {
				if !strings.Contains(string(content), want) {
					t.Fatalf("report.html missing %q", want)
				}
			}
		}
	}

	// 5. 友好文件名（含中文任务名）
	w, _ = doRedRouteRequest(t, router, http.MethodGet, "/api/v1/tasks/"+taskID+"/export?format=html", "")
	disposition := w.Header().Get("Content-Disposition")
	if !strings.Contains(disposition, "attachment") || !strings.Contains(disposition, ".html") {
		t.Fatalf("unexpected content-disposition: %q", disposition)
	}
	if !strings.Contains(disposition, "filename*=") {
		t.Fatalf("chinese filename must use RFC 5987 filename*: %q", disposition)
	}
}

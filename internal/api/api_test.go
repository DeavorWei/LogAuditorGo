package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"logauditorgo/internal/api"
	"logauditorgo/internal/config"
	"logauditorgo/internal/knowledge"
	"logauditorgo/internal/matcher"
	"logauditorgo/internal/rootcause"
	"logauditorgo/internal/search"
	"logauditorgo/internal/storage"
	"logauditorgo/internal/task"
	"logauditorgo/pkg/logger"
)

func TestAPIEndpoints(t *testing.T) {
	logger.Init("debug", "console")

	tmpDir, err := os.MkdirTemp("", "api_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Server: config.ServerConfig{Port: 8080, Mode: "test"},
		Storage: config.StorageConfig{
			DataDir:     tmpDir,
			KnowledgeDB: filepath.Join(tmpDir, "knowledge.db"),
			BleveIndex:  filepath.Join(tmpDir, "bleve.index"),
			TaskDir:     filepath.Join(tmpDir, "tasks"),
			UploadDir:   filepath.Join(tmpDir, "uploads"),
		},
	}

	globalDB, err := storage.InitKnowledgeDB(cfg.Storage.KnowledgeDB)
	if err != nil {
		t.Fatalf("init db failed: %v", err)
	}

	indexer, err := search.InitIndexer(cfg.Storage.BleveIndex)
	if err != nil {
		t.Fatalf("init indexer failed: %v", err)
	}
	defer indexer.Close()

	knowledgeSvc := knowledge.NewService(globalDB)
	matchEngine := matcher.NewMatchEngine(globalDB, indexer)
	rcaEngine := rootcause.NewEngine(nil)
	taskSvc := task.NewService(globalDB, cfg.Storage.TaskDir, matchEngine, rcaEngine)

	router := api.SetupRouter(cfg, globalDB, knowledgeSvc, indexer, taskSvc)

	// 1. 测试 GET /api/v1/system/stats
	req1, _ := http.NewRequest("GET", "/api/v1/system/stats", nil)
	w1 := httptest.NewRecorder()
	router.ServeHTTP(w1, req1)

	if w1.Code != http.StatusOK {
		t.Errorf("expected 200 for /stats, got %d", w1.Code)
	}

	var res1 struct {
		Code int `json:"code"`
		Data struct {
			TotalKnowledge int `json:"total_knowledge"`
		} `json:"data"`
	}
	json.Unmarshal(w1.Body.Bytes(), &res1)
	if res1.Code != 0 {
		t.Errorf("expected code 0, got %d", res1.Code)
	}

	// 2. 测试 GET /api/v1/documents
	req2, _ := http.NewRequest("GET", "/api/v1/documents", nil)
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Errorf("expected 200 for /documents, got %d", w2.Code)
	}

	// 3. 测试 POST /api/v1/tasks 创建任务并返回 task_id
	logPayload := `{"task_name": "TestAudit", "device_type": "CloudEngine 16800", "content": "%%01IFNET/4/IF_DOWN(l)[10]: Interface 100GE1/0/1 is down.\n%%01BFD/2/BFD_SESS_DOWN(l)[11]: BFD session state changed to DOWN. (SessionID=10)"}`
	req3, _ := http.NewRequest("POST", "/api/v1/tasks", strings.NewReader(logPayload))
	req3.Header.Set("Content-Type", "application/json")
	w3 := httptest.NewRecorder()
	router.ServeHTTP(w3, req3)

	if w3.Code != http.StatusOK {
		t.Fatalf("expected 200 for POST /tasks, got %d", w3.Code)
	}

	var res3 struct {
		Code int `json:"code"`
		Data struct {
			TaskID   string `json:"task_id"`
			LogCount int    `json:"log_count"`
			Status   string `json:"status"`
		} `json:"data"`
	}
	json.Unmarshal(w3.Body.Bytes(), &res3)
	if res3.Data.TaskID == "" {
		t.Fatalf("expected non-empty task_id in response, got %+v", res3)
	}
	taskID := res3.Data.TaskID

	// 4. 测试 GET /api/v1/tasks/:id/logs
	req4, _ := http.NewRequest("GET", "/api/v1/tasks/"+taskID+"/logs", nil)
	w4 := httptest.NewRecorder()
	router.ServeHTTP(w4, req4)
	if w4.Code != http.StatusOK {
		t.Errorf("expected 200 for /tasks/:id/logs, got %d", w4.Code)
	}

	// 5. 测试 GET /api/v1/tasks/:id/rca
	req5, _ := http.NewRequest("GET", "/api/v1/tasks/"+taskID+"/rca", nil)
	w5 := httptest.NewRecorder()
	router.ServeHTTP(w5, req5)
	if w5.Code != http.StatusOK {
		t.Errorf("expected 200 for /tasks/:id/rca, got %d", w5.Code)
	}

	// 6. 测试 GET /api/v1/tasks/:id/export?format=html
	req6, _ := http.NewRequest("GET", "/api/v1/tasks/"+taskID+"/export?format=html", nil)
	w6 := httptest.NewRecorder()
	router.ServeHTTP(w6, req6)
	if w6.Code != http.StatusOK || !strings.Contains(w6.Body.String(), "<html") {
		t.Errorf("expected 200 and html content for export, got %d", w6.Code)
	}
}

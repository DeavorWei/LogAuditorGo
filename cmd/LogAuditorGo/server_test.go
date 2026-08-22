package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"logauditorgo/internal/api"
	"logauditorgo/internal/config"
	"logauditorgo/internal/knowledge"
	"logauditorgo/internal/matcher"
	"logauditorgo/internal/model"
	"logauditorgo/internal/rootcause"
	"logauditorgo/internal/search"
	"logauditorgo/internal/storage"
	"logauditorgo/internal/task"
	"logauditorgo/pkg/logger"
)

func TestEndToEndSystemIntegration(t *testing.T) {
	logger.Init("info", "console")

	tmpDir, err := os.MkdirTemp("", "e2e_logauditor_*")
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
		t.Fatalf("init global db failed: %v", err)
	}

	indexer, err := search.InitIndexer(cfg.Storage.BleveIndex)
	if err != nil {
		t.Fatalf("init bleve indexer failed: %v", err)
	}
	defer indexer.Close()

	knowledgeSvc := knowledge.NewService(globalDB)
	matchEngine := matcher.NewMatchEngine(globalDB, indexer)
	rcaEngine := rootcause.NewEngine(nil)
	taskSvc := task.NewService(globalDB, cfg.Storage.TaskDir, matchEngine, rcaEngine)

	router := api.SetupRouter(cfg, globalDB, knowledgeSvc, indexer, taskSvc)

	// 1. 真实文档导入测试
	usgDir := filepath.FromSlash("../../原始产品文档/HiSecEngine USG6000F, USG6000G_V600R025C10_01_zh_AZQ01091")
	if _, err := os.Stat(usgDir); err == nil {
		stats, err := knowledgeSvc.ImportDocumentFromDir(usgDir)
		if err != nil {
			t.Fatalf("import USG document failed: %v", err)
		}
		t.Logf("E2E: Imported USG doc: LeafLogs=%d, LeafAlarms=%d, UniqueAdded=%d in %v",
			stats.LeafLogCount, stats.LeafAlarmCount, stats.UniqueKnowledgeAdded, stats.Duration)

		// 索引全量知识库到 Bleve
		var allKnowledge []model.Knowledge
		globalDB.Preload("Versions").Find(&allKnowledge)
		if err := indexer.IndexKnowledge(allKnowledge); err != nil {
			t.Fatalf("index knowledge to Bleve failed: %v", err)
		}
		t.Logf("E2E: Indexed %d knowledge items into Bleve", len(allKnowledge))
	}

	// 2. 模拟包含真实场景的 Syslog 报文序列
	sampleLogs := `
Apr 15 2026 14:00:01 CORE-SW-01 %%01IFNET/4/IF_DOWN(l)[101][Slot=1/1]: Interface 100GE1/0/1 state turned to DOWN. (InterfaceName=100GE1/0/1)
Apr 15 2026 14:00:02 CORE-SW-01 %%01BFD/2/BFD_SESS_DOWN(l)[102][Slot=1/1]: BFD session state changed to DOWN. (SessionID=12)
Apr 15 2026 14:00:03 CORE-SW-01 %%01BGP/2/PEER_BACKWARD(l)[103][Slot=1/1]: The BGP peer went down. (PeerAddress=192.168.1.2)
Apr 15 2026 14:05:00 USG-FW-01 %%01AAA/4/hwRadiusAuthServerDown_active(l)[201]: The communication with the RADIUS authentication server fails. (IpAddress=[10.10.10.1], Vpn-Instance=[default])
Apr 15 2026 14:05:10 USG-FW-01 %%01AAA/4/USER_AUTH_FAIL(l)[202]: User authentication failed. (UserName=testuser, UserIP=192.168.10.5)
Apr 15 2026 14:10:00 CORE-SW-02 %%01BGP/4/BGP_AUTH_FAILED(l)[301][Slot=1/2]: BGP session authentication failed. (PeerID=10.0.0.2, TcpConnSocket=15, ReturnCode=2, SourceInterface=100GE1/0/2)
`

	// 3. 执行端到端任务分析
	taskInfo, err := taskSvc.CreateAndRunTask("E2E-Integration-Task", "CloudEngine", sampleLogs)
	if err != nil {
		t.Fatalf("run task failed: %v", err)
	}

	t.Logf("E2E: Task result: Total Logs=%d, Matched=%d, RCA Incidents=%d",
		taskInfo.LogCount, taskInfo.MatchedCount, taskInfo.RcaCount)

	if taskInfo.LogCount != 6 {
		t.Errorf("expected 6 logs analyzed, got %d", taskInfo.LogCount)
	}
	if taskInfo.MatchedCount < 4 {
		t.Errorf("expected >= 4 logs matched with knowledge base, got %d", taskInfo.MatchedCount)
	}
	if taskInfo.RcaCount < 2 {
		t.Errorf("expected >= 2 RCA incidents detected (LinkDown & RadiusDown), got %d", taskInfo.RcaCount)
	}

	// 4. 测试 API 端点
	req, _ := http.NewRequest("GET", "/api/v1/tasks/"+taskInfo.TaskID+"/logs?page=1&page_size=20", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 from query logs API, got %d", w.Code)
	}

	var logRes struct {
		Code int `json:"code"`
		Data struct {
			Total   int               `json:"total"`
			Records []model.LogRecord `json:"records"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &logRes); err != nil {
		t.Fatalf("unmarshal logs response failed: %v", err)
	}
	if logRes.Data.Total != 6 {
		t.Errorf("expected 6 records in API, got %d", logRes.Data.Total)
	}

	// 5. 测试全局概览统计 API
	reqStats, _ := http.NewRequest("GET", "/api/v1/system/stats", nil)
	wStats := httptest.NewRecorder()
	router.ServeHTTP(wStats, reqStats)

	if wStats.Code != http.StatusOK {
		t.Fatalf("expected 200 from stats API, got %d", wStats.Code)
	}

	// 6. 测试 HTML 报告生成
	htmlReport, err := taskSvc.ExportTaskHTML(taskInfo.TaskID)
	if err != nil {
		t.Fatalf("export task HTML failed: %v", err)
	}
	if len(htmlReport) < 500 {
		t.Errorf("expected substantial HTML report, got %d bytes", len(htmlReport))
	}
}

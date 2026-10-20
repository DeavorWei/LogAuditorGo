package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// redRouteEnvelope 统一响应信封（红路线断言只关心 code/data）。
type redRouteEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func doRedRouteRequest(t *testing.T, router http.Handler, method, path, body string) (*httptest.ResponseRecorder, redRouteEnvelope) {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, path, reader)
	if err != nil {
		t.Fatalf("build request %s %s failed: %v", method, path, err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	// httptest 请求默认 RemoteAddr 为空，c.RemoteIP() 会判定为非回环；
	// /fs 路由有 RequireLoopback，因此统一模拟本机来源。
	req.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	var env redRouteEnvelope
	if strings.Contains(w.Header().Get("Content-Type"), "application/json") {
		_ = json.Unmarshal(w.Body.Bytes(), &env)
	}
	return w, env
}

// TestRedRoutesSmoke 覆盖四条红路线（阶段总览 Phase 0 成功标志）：
//  1. 第一次导入：创建任务 → 日志入库可见；
//  2. 第一次得到答案：命中官方知识并可解释；
//  3. 第一次交接：HTML/CSV 报告可导出；
//  4. 第一次复用：案例库按关键词/状态检索可找回。
//
// 同时验证 RCA 默认关闭（不自动运行、不产生 QUEUED）与实验室开关开启后的手动触发路径。
func TestRedRoutesSmoke(t *testing.T) {
	logger.Init("warn", "console")

	tmpDir := t.TempDir()
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
	if err := os.WriteFile(config.ConfigFileUsed, []byte("features:\n  rca: false\n"), 0o600); err != nil {
		t.Fatalf("write config file failed: %v", err)
	}

	// 同包其他用例可能已初始化全局知识库；InitKnowledgeDB 会复用现有单例，
	// 这里先关闭，确保本用例拿到独立数据库（否则会看到别的测试创建的任务）。
	storage.CloseAllTaskDBs()
	_ = storage.CloseKnowledgeDB()

	globalDB, err := storage.InitKnowledgeDB(cfg.Storage.KnowledgeDB)
	if err != nil {
		t.Fatalf("init global db failed: %v", err)
	}
	defer storage.CloseKnowledgeDB()

	indexer, err := search.InitIndexer(cfg.Storage.BleveIndex)
	if err != nil {
		t.Fatalf("init indexer failed: %v", err)
	}
	defer indexer.Close()

	knowledgeSvc := knowledge.NewService(globalDB, indexer)
	matchEngine := matcher.NewMatchEngine(globalDB, indexer)
	taskSvc := task.NewService(globalDB, cfg.Storage.TaskDir, matchEngine, rootcause.NewEngine(nil))
	task.InitRCAScheduler(2)
	// Windows 下临时目录清理要求先释放任务库句柄（defer 为后进先出，故此处晚于 CloseKnowledgeDB 注册）
	defer storage.CloseAllTaskDBs()
	router := api.SetupRouter(cfg, globalDB, knowledgeSvc, indexer, taskSvc)

	// 预置一条官方知识（BGP 邻居断开）
	kb := model.Knowledge{
		EntryType:   model.EntryTypeLog,
		Module:      "BGP",
		Severity:    2,
		Brief:       "PEER_BACKWARD",
		Message:     "The BGP peer went down.",
		Description: "BGP 邻居状态变为 Down。",
		Cause:       "链路中断或对端设备故障。",
		Action:      "检查物理链路与对端 BGP 配置。",
		ContentHash: "red-route-bgp-peer-backward",
	}
	if err := globalDB.Create(&kb).Error; err != nil {
		t.Fatalf("seed knowledge failed: %v", err)
	}
	if err := globalDB.Create(&model.KnowledgeVersionMapping{
		KnowledgeID:    kb.ID,
		ProductType:    "CE16800",
		ProductVersion: "V200R025C00",
	}).Error; err != nil {
		t.Fatalf("seed mapping failed: %v", err)
	}
	kb.Versions = []model.KnowledgeVersionMapping{{KnowledgeID: kb.ID, ProductType: "CE16800", ProductVersion: "V200R025C00"}}
	if err := indexer.IndexKnowledge([]model.Knowledge{kb}); err != nil {
		t.Fatalf("index knowledge failed: %v", err)
	}
	matchEngine.Reload()

	// ---------- 红路线 1：第一次导入 ----------
	createBody := `{"task_name":"红线-首次导入","content":"Apr 15 2026 10:00:01 CE16800-CORE %%01BGP/2/PEER_BACKWARD(l)[1]: The BGP peer went down. (PeerAddress=192.168.1.2)\n"}`
	w, env := doRedRouteRequest(t, router, http.MethodPost, "/api/v1/tasks", createBody)
	if w.Code != http.StatusOK || env.Code != 0 {
		t.Fatalf("create task failed: status=%d code=%d body=%s", w.Code, env.Code, w.Body.String())
	}
	var created struct {
		TaskID       string `json:"task_id"`
		TaskName     string `json:"task_name"`
		LogCount     int    `json:"log_count"`
		MatchedCount int    `json:"matched_count"`
		Status       string `json:"status"`
		RCAStatus    string `json:"rca_status"`
		RcaCount     int    `json:"rca_count"`
	}
	if err := json.Unmarshal(env.Data, &created); err != nil {
		t.Fatalf("decode created task failed: %v", err)
	}
	if created.TaskID == "" || created.LogCount < 1 {
		t.Fatalf("red route 1 failed: task=%+v", created)
	}
	if created.Status != string(model.TaskStatusCompleted) {
		t.Fatalf("expected COMPLETED task, got %s", created.Status)
	}
	// RCA 默认关闭：不得自动置 QUEUED / 产生事件
	if created.RCAStatus != "" && created.RCAStatus != string(model.RCAStatusPending) {
		t.Fatalf("RCA must stay PENDING when lab feature is off, got %s", created.RCAStatus)
	}
	if created.RcaCount != 0 {
		t.Fatalf("RCA must not auto-run, got rca_count=%d", created.RcaCount)
	}

	// ---------- 红路线 2：第一次得到答案 ----------
	if created.MatchedCount < 1 {
		t.Fatalf("red route 2 failed: expected matched_count >= 1, got %d", created.MatchedCount)
	}
	w, env = doRedRouteRequest(t, router, http.MethodPost, "/api/v1/tasks/"+created.TaskID+"/logs/query", `{"page":1,"page_size":10}`)
	if w.Code != http.StatusOK || env.Code != 0 {
		t.Fatalf("query logs failed: status=%d body=%s", w.Code, w.Body.String())
	}
	var logPage struct {
		Total   int64 `json:"total"`
		Records []struct {
			ID          uint             `json:"id"`
			Module      string           `json:"module"`
			Brief       string           `json:"brief"`
			KnowledgeID uint             `json:"knowledge_id"`
			MatchTier   string           `json:"match_tier"`
			Knowledge   *model.Knowledge `json:"knowledge"`
		} `json:"records"`
	}
	if err := json.Unmarshal(env.Data, &logPage); err != nil {
		t.Fatalf("decode logs failed: %v", err)
	}
	if logPage.Total < 1 || len(logPage.Records) < 1 {
		t.Fatalf("expected logs in task, got total=%d", logPage.Total)
	}
	matched := logPage.Records[0]
	if matched.KnowledgeID == 0 || matched.MatchTier != matcher.TierExact {
		t.Fatalf("expected EXACT knowledge match, got kid=%d tier=%s", matched.KnowledgeID, matched.MatchTier)
	}
	if matched.Knowledge == nil || matched.Knowledge.Brief != "PEER_BACKWARD" {
		t.Fatalf("expected knowledge payload attached to log, got %+v", matched.Knowledge)
	}

	// ---------- 红路线 3：第一次交接 ----------
	w, _ = doRedRouteRequest(t, router, http.MethodGet, "/api/v1/tasks/"+created.TaskID+"/export?format=html", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("HTML export failed: status=%d content-type=%s", w.Code, w.Header().Get("Content-Type"))
	}
	if !strings.Contains(w.Body.String(), "红线-首次导入") {
		t.Fatalf("HTML export should contain task name")
	}
	w, _ = doRedRouteRequest(t, router, http.MethodGet, "/api/v1/tasks/"+created.TaskID+"/export?format=csv", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "PEER_BACKWARD") {
		t.Fatalf("CSV export failed: status=%d body head=%s", w.Code, w.Body.String()[:min(200, w.Body.Len())])
	}

	// ---------- 红路线 4：第一次复用（案例库检索） ----------
	w, env = doRedRouteRequest(t, router, http.MethodGet, "/api/v1/tasks/query?keyword=%E7%BA%A2%E7%BA%BF", "")
	if w.Code != http.StatusOK || env.Code != 0 {
		t.Fatalf("case search failed: status=%d body=%s", w.Code, w.Body.String())
	}
	var page struct {
		Items    []model.TaskInfo `json:"items"`
		Total    int64            `json:"total"`
		Page     int              `json:"page"`
		PageSize int              `json:"page_size"`
	}
	if err := json.Unmarshal(env.Data, &page); err != nil {
		t.Fatalf("decode task page failed: %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].TaskID != created.TaskID {
		t.Fatalf("red route 4 failed: %+v", page)
	}

	// 非法状态过滤必须被拒绝，而不是静默返回全部
	w, env = doRedRouteRequest(t, router, http.MethodGet, "/api/v1/tasks/query?status=BOGUS", "")
	if w.Code != http.StatusBadRequest || env.Code == 0 {
		t.Fatalf("invalid status must return 400, got status=%d body=%s", w.Code, w.Body.String())
	}

	// 兼容契约：GET /tasks 仍返回裸数组
	w, env = doRedRouteRequest(t, router, http.MethodGet, "/api/v1/tasks", "")
	var allTasks []model.TaskInfo
	if w.Code != http.StatusOK {
		t.Fatalf("legacy GET /tasks failed: %d", w.Code)
	}
	if err := json.Unmarshal(env.Data, &allTasks); err != nil || len(allTasks) != 1 {
		t.Fatalf("legacy GET /tasks must return array, got err=%v len=%d", err, len(allTasks))
	}

	// ---------- 系统开关与存储概览 ----------
	w, env = doRedRouteRequest(t, router, http.MethodGet, "/api/v1/system/features", "")
	var featuresPayload struct {
		Features config.FeaturesConfig `json:"features"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(env.Data, &featuresPayload) != nil || featuresPayload.Features.RCA {
		t.Fatalf("features endpoint should report rca=false: status=%d body=%s", w.Code, w.Body.String())
	}

	w, env = doRedRouteRequest(t, router, http.MethodGet, "/api/v1/system/storage", "")
	var storagePayload struct {
		Paths  []map[string]interface{} `json:"paths"`
		Counts map[string]int64         `json:"counts"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(env.Data, &storagePayload) != nil || len(storagePayload.Paths) == 0 {
		t.Fatalf("storage endpoint failed: status=%d body=%s", w.Code, w.Body.String())
	}
	if storagePayload.Counts["tasks"] != 1 {
		t.Fatalf("storage counts tasks=%d, want 1", storagePayload.Counts["tasks"])
	}

	// 产品明确不做项不得被开启
	w, _ = doRedRouteRequest(t, router, http.MethodPut, "/api/v1/system/features", `{"masking":true}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("enabling masking must be rejected, got %d", w.Code)
	}

	// ---------- 实验室功能：开启后手动触发 RCA ----------
	w, env = doRedRouteRequest(t, router, http.MethodPut, "/api/v1/system/features", `{"rca":true}`)
	if w.Code != http.StatusOK || env.Code != 0 {
		t.Fatalf("enable rca failed: status=%d body=%s", w.Code, w.Body.String())
	}
	w, env = doRedRouteRequest(t, router, http.MethodPost, "/api/v1/tasks/"+created.TaskID+"/rca/reanalyze", "")
	if w.Code != http.StatusOK || env.Code != 0 {
		t.Fatalf("manual rca trigger failed: status=%d body=%s", w.Code, w.Body.String())
	}
	if !taskSvc.WaitForTaskRCA(created.TaskID, 10*time.Second) {
		t.Fatalf("manual RCA analysis did not finish in time")
	}
	w, env = doRedRouteRequest(t, router, http.MethodGet, "/api/v1/tasks/"+created.TaskID, "")
	var finalTask model.TaskInfo
	if w.Code != http.StatusOK || json.Unmarshal(env.Data, &finalTask) != nil {
		t.Fatalf("get task after RCA failed: %s", w.Body.String())
	}
	if finalTask.RCAStatus != model.RCAStatusCompleted && finalTask.RCAStatus != model.RCAStatusTimeout {
		t.Fatalf("manual RCA should reach a terminal state, got %s", finalTask.RCAStatus)
	}

	// 关闭开关，避免污染同包其他用例
	if _, err := config.UpdateFeatures(map[string]bool{"rca": false}); err != nil {
		t.Fatalf("disable rca failed: %v", err)
	}
}

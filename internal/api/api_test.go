package api_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

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

	config.ConfigFileUsed = filepath.Join(tmpDir, "config.yaml")
	config.GlobalConfig = cfg

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

	// 7. 测试 GET /api/v1/system/config
	config.GlobalConfig = cfg
	req7, _ := http.NewRequest("GET", "/api/v1/system/config", nil)
	w7 := httptest.NewRecorder()
	router.ServeHTTP(w7, req7)
	if w7.Code != http.StatusOK {
		t.Errorf("expected 200 for /system/config, got %d", w7.Code)
	}

	// 8. 测试 PUT /api/v1/system/config/log
	updateLogPayload := `{"max_size_mb": 512, "max_days": 90, "level": "info", "format": "console"}`
	req8, _ := http.NewRequest("PUT", "/api/v1/system/config/log", strings.NewReader(updateLogPayload))
	req8.Header.Set("Content-Type", "application/json")
	w8 := httptest.NewRecorder()
	router.ServeHTTP(w8, req8)
	if w8.Code != http.StatusOK {
		t.Errorf("expected 200 for PUT /system/config/log, got %d: %s", w8.Code, w8.Body.String())
	}

	// 9. 测试 GET /api/v1/system/logs
	req9, _ := http.NewRequest("GET", "/api/v1/system/logs", nil)
	w9 := httptest.NewRecorder()
	router.ServeHTTP(w9, req9)
	if w9.Code != http.StatusOK {
		t.Errorf("expected 200 for /system/logs, got %d", w9.Code)
	}

	// 10. 测试 POST /api/v1/system/logs/clean
	req10, _ := http.NewRequest("POST", "/api/v1/system/logs/clean", nil)
	w10 := httptest.NewRecorder()
	router.ServeHTTP(w10, req10)
	if w10.Code != http.StatusOK {
		t.Errorf("expected 200 for POST /system/logs/clean, got %d", w10.Code)
	}
}

func TestDocumentUploadAndConflict(t *testing.T) {
	logger.Init("debug", "console")

	tmpDir, err := os.MkdirTemp("", "doc_api_test_*")
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

	// 创建内存 Mock HDX zip
	mockZipData := createMockHDXZip(t, "DOC_TEST_001", "TestSwitch", "V100R001C00")

	// 1. 首次上传：覆盖/创建模式
	bodyBuf := &strings.Builder{}
	mpWriter := createMultipartUpload(bodyBuf, "test_doc.hdx", mockZipData, "overwrite")

	req1, _ := http.NewRequest("POST", "/api/v1/documents/upload", strings.NewReader(bodyBuf.String()))
	req1.Header.Set("Content-Type", mpWriter)
	w1 := httptest.NewRecorder()
	router.ServeHTTP(w1, req1)

	if w1.Code != http.StatusOK {
		t.Fatalf("expected 200 for upload, got %d: %s", w1.Code, w1.Body.String())
	}

	var res1 struct {
		Code int `json:"code"`
		Data struct {
			TotalDocuments       int      `json:"total_documents"`
			LeafLogCount         int      `json:"leaf_log_count"`
			UniqueKnowledgeAdded int      `json:"unique_knowledge_added"`
			ImportedDocs         []string `json:"imported_docs"`
			SkippedDocs          []string `json:"skipped_docs"`
		} `json:"data"`
	}
	json.Unmarshal(w1.Body.Bytes(), &res1)
	if res1.Code != 0 || res1.Data.TotalDocuments < 1 {
		t.Errorf("expected successful document import, got %+v", res1)
	}

	// 2. 二次上传同一文档，指定 conflict_mode = skip
	bodyBuf2 := &strings.Builder{}
	mpWriter2 := createMultipartUpload(bodyBuf2, "test_doc.hdx", mockZipData, "skip")

	req2, _ := http.NewRequest("POST", "/api/v1/documents/upload", strings.NewReader(bodyBuf2.String()))
	req2.Header.Set("Content-Type", mpWriter2)
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 for skip upload, got %d: %s", w2.Code, w2.Body.String())
	}

	var res2 struct {
		Code int `json:"code"`
		Data struct {
			SkippedDocs []string `json:"skipped_docs"`
		} `json:"data"`
	}
	json.Unmarshal(w2.Body.Bytes(), &res2)
	if len(res2.Data.SkippedDocs) != 1 {
		t.Errorf("expected 1 skipped doc, got %+v", res2)
	}

	// 3. 验证文档列表与删除
	req3, _ := http.NewRequest("GET", "/api/v1/documents", nil)
	w3 := httptest.NewRecorder()
	router.ServeHTTP(w3, req3)

	var res3 struct {
		Code int `json:"code"`
		Data []struct {
			ID    uint   `json:"id"`
			LibID string `json:"lib_id"`
		} `json:"data"`
	}
	json.Unmarshal(w3.Body.Bytes(), &res3)
	if len(res3.Data) != 1 {
		t.Fatalf("expected 1 document in list, got %d", len(res3.Data))
	}

	// 4. 删除文档
	docID := res3.Data[0].ID
	req4, _ := http.NewRequest("DELETE", fmt.Sprintf("/api/v1/documents/%d", docID), nil)
	w4 := httptest.NewRecorder()
	router.ServeHTTP(w4, req4)
	if w4.Code != http.StatusOK {
		t.Errorf("expected 200 for delete document, got %d", w4.Code)
	}
}

func createMockHDXZip(t *testing.T, libID, productType, productVer string) []byte {
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	// 1. profile.xml
	profileContent := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<profile>
  <libId>%s</libId>
  <libVersion>01</libVersion>
  <libName>Test %s Document</libName>
  <productType>%s</productType>
  <productVersion>%s</productVersion>
  <issueDate>2026-04-15</issueDate>
  <topicNumber>1</topicNumber>
  <navi>resources/navi.xml</navi>
</profile>`, libID, productType, productType, productVer)

	pw, err := zw.Create("profile.xml")
	if err != nil {
		t.Fatalf("create profile.xml in zip failed: %v", err)
	}
	pw.Write([]byte(profileContent))

	// 2. resources/navi.xml
	naviContent := `<?xml version="1.0" encoding="utf-8"?>
<topics>
  <topic id="root_01" txt="日志信息参考" url="">
    <topic id="topic_01" txt="IFNET/4/IF_DOWN" url="resources/test_log.html"/>
  </topic>
</topics>`
	nw, err := zw.Create("resources/navi.xml")
	if err != nil {
		t.Fatalf("create navi.xml in zip failed: %v", err)
	}
	nw.Write([]byte(naviContent))

	// 3. resources/test_log.html
	htmlContent := `<html>
<head><title>IFNET/4/IF_DOWN</title></head>
<body>
  <div id="body">
    <h2>日志信息</h2>
    <p>%%01IFNET/4/IF_DOWN: Interface state turned to DOWN.</p>
    <h2>日志含义</h2>
    <p>接口状态转为Down</p>
    <h2>可能原因</h2>
    <p>链路断开或对端关闭</p>
    <h2>处理步骤</h2>
    <p>检查物理网线及对端端口配置</p>
  </div>
</body>
</html>`
	hw, err := zw.Create("resources/test_log.html")
	if err != nil {
		t.Fatalf("create html in zip failed: %v", err)
	}
	hw.Write([]byte(htmlContent))

	if err := zw.Close(); err != nil {
		t.Fatalf("close zip failed: %v", err)
	}

	return buf.Bytes()
}

func createMultipartUpload(bodyBuf *strings.Builder, filename string, data []byte, conflictMode string) string {
	b := &bytes.Buffer{}
	w := multipart.NewWriter(b)

	if conflictMode != "" {
		_ = w.WriteField("conflict_mode", conflictMode)
	}

	part, _ := w.CreateFormFile("files", filename)
	part.Write(data)
	w.Close()

	bodyBuf.Reset()
	bodyBuf.WriteString(b.String())
	return w.FormDataContentType()
}

func TestFolderWithMultipleHDXZipsUpload(t *testing.T) {
	logger.Init("debug", "console")

	tmpDir, err := os.MkdirTemp("", "doc_folder_test_*")
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

	// 创建两个不同的 Mock HDX zip 数据包（使用 .hdx 后缀）
	zip1 := createMockHDXZip(t, "DOC_MULTI_01", "Switch-A", "V100R001C00")
	zip2 := createMockHDXZip(t, "DOC_MULTI_02", "Firewall-B", "V200R002C00")

	// 模拟在浏览器中选中一个包含多个 .hdx 文件的文件夹
	b := &bytes.Buffer{}
	w := multipart.NewWriter(b)
	_ = w.WriteField("conflict_mode", "overwrite")

	// 第一个文件位于 my_hdx_folder/doc1.hdx
	part1, _ := w.CreateFormFile("files", "my_hdx_folder/doc1.hdx")
	part1.Write(zip1)

	// 第二个文件位于 my_hdx_folder/sub/doc2.hdx
	part2, _ := w.CreateFormFile("files", "my_hdx_folder/sub/doc2.hdx")
	part2.Write(zip2)

	// 第三个是已解压的散装 HDX 目录结构 (profile.xml + navi.xml)
	p3, _ := w.CreateFormFile("files", "my_hdx_folder/unzipped_doc/profile.xml")
	p3.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<profile>
  <libId>DOC_UNZIPPED_03</libId>
  <libVersion>01</libVersion>
  <libName>Unzipped Document 03</libName>
  <productType>Router-C</productType>
  <productVersion>V300R003C00</productVersion>
  <issueDate>2026-04-15</issueDate>
  <topicNumber>1</topicNumber>
  <navi>resources/navi.xml</navi>
</profile>`))
	n3, _ := w.CreateFormFile("files", "my_hdx_folder/unzipped_doc/resources/navi.xml")
	n3.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<topics>
  <topic id="r_03" txt="日志" url="">
    <topic id="top_03" txt="BGP/2/DOWN" url="resources/log3.html"/>
  </topic>
</topics>`))
	h3, _ := w.CreateFormFile("files", "my_hdx_folder/unzipped_doc/resources/log3.html")
	h3.Write([]byte(`<html><body><h2>日志信息</h2><p>BGP down</p></body></html>`))

	w.Close()

	req, _ := http.NewRequest("POST", "/api/v1/documents/upload", strings.NewReader(b.String()))
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for folder with multiple HDX files, got %d: %s", rec.Code, rec.Body.String())
	}

	var res struct {
		Code int `json:"code"`
		Data struct {
			TotalDocuments int      `json:"total_documents"`
			ImportedDocs   []string `json:"imported_docs"`
		} `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &res)
	if res.Code != 0 || len(res.Data.ImportedDocs) != 3 {
		t.Fatalf("expected 3 imported documents (2 .hdx archives + 1 unzipped directory), got %+v", res)
	}
}

func TestStaticFrontendAndSPARouting(t *testing.T) {
	logger.Init("debug", "console")

	tmpDir, err := os.MkdirTemp("", "static_test_*")
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

	// 1. 测试访问根路由 / 返回 index.html
	req1, _ := http.NewRequest("GET", "/", nil)
	w1 := httptest.NewRecorder()
	router.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Errorf("expected 200 for /, got %d", w1.Code)
	}
	if !strings.Contains(w1.Body.String(), "<!DOCTYPE html>") {
		t.Errorf("expected html doctype in index.html response, got: %s", w1.Body.String())
	}

	// 2. 测试访问 SPA 前端路由（例如 /workbench, /documents 等）正常返回 index.html
	req2, _ := http.NewRequest("GET", "/workbench", nil)
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Errorf("expected 200 for /workbench SPA route, got %d", w2.Code)
	}
	if !strings.Contains(w2.Body.String(), "<div id=\"app\"></div>") {
		t.Errorf("expected SPA index.html for /workbench, got: %s", w2.Body.String())
	}

	// 3. 测试不存在的 API 路由返回 JSON 404
	req3, _ := http.NewRequest("GET", "/api/v1/not-found-endpoint", nil)
	w3 := httptest.NewRecorder()
	router.ServeHTTP(w3, req3)
	if w3.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown api route, got %d", w3.Code)
	}
	if !strings.Contains(w3.Body.String(), "API endpoint not found") {
		t.Errorf("expected json error message, got: %s", w3.Body.String())
	}
}

func TestCORSMiddleware(t *testing.T) {
	logger.Init("debug", "console")

	tmpDir, err := os.MkdirTemp("", "cors_test_*")
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

	// 1. 请求带有受信 Origin 头（如本地前端），应当返回对应的 Origin、Credentials: true 以及 Vary: Origin
	req1, _ := http.NewRequest("GET", "/api/v1/system/stats", nil)
	req1.Header.Set("Origin", "http://localhost:5173")
	w1 := httptest.NewRecorder()
	router.ServeHTTP(w1, req1)

	if w1.Header().Get("Access-Control-Allow-Origin") != "http://localhost:5173" {
		t.Errorf("expected Access-Control-Allow-Origin to match allowed origin, got %s", w1.Header().Get("Access-Control-Allow-Origin"))
	}
	if w1.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Errorf("expected Access-Control-Allow-Credentials: true when Origin is allowed, got %s", w1.Header().Get("Access-Control-Allow-Credentials"))
	}
	if !strings.Contains(w1.Header().Get("Vary"), "Origin") {
		t.Errorf("expected Vary header to contain Origin, got %s", w1.Header().Get("Vary"))
	}

	// 2. 请求带有非受信 Origin 头（如外部域），应当安全降级为 * 且不带 Credentials (M-08)
	reqExternal, _ := http.NewRequest("GET", "/api/v1/system/stats", nil)
	reqExternal.Header.Set("Origin", "http://evil.com:3000")
	wExt := httptest.NewRecorder()
	router.ServeHTTP(wExt, reqExternal)

	if wExt.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("expected Access-Control-Allow-Origin: * for untrusted origin, got %s", wExt.Header().Get("Access-Control-Allow-Origin"))
	}
	if wExt.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Errorf("expected Access-Control-Allow-Credentials to be empty for untrusted origin, got %s", wExt.Header().Get("Access-Control-Allow-Credentials"))
	}

	// 3. 请求不带 Origin 头，应当返回 Allow-Origin: * 且不设置 Allow-Credentials: true
	req2, _ := http.NewRequest("GET", "/api/v1/system/stats", nil)
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)

	if w2.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("expected Access-Control-Allow-Origin: *, got %s", w2.Header().Get("Access-Control-Allow-Origin"))
	}
	if w2.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Errorf("expected Access-Control-Allow-Credentials to be empty when no Origin, got %s", w2.Header().Get("Access-Control-Allow-Credentials"))
	}

	// 3. OPTIONS 预检请求应返回 204
	req3, _ := http.NewRequest("OPTIONS", "/api/v1/system/stats", nil)
	req3.Header.Set("Origin", "http://example.com:3000")
	w3 := httptest.NewRecorder()
	router.ServeHTTP(w3, req3)

	if w3.Code != http.StatusNoContent {
		t.Errorf("expected 204 for OPTIONS preflight, got %d", w3.Code)
	}
}

func TestUploadHDXAsyncAndPathTraversal(t *testing.T) {
	logger.Init("debug", "console")

	tmpDir, err := os.MkdirTemp("", "upload_test_*")
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

	mockZipData := createMockHDXZip(t, "DOC_ASYNC_001", "AsyncSwitch", "V100R001C00")

	// 1. 测试异步上传模式
	b := &bytes.Buffer{}
	w := multipart.NewWriter(b)
	_ = w.WriteField("conflict_mode", "overwrite")
	_ = w.WriteField("async", "true")
	part, _ := w.CreateFormFile("files", "test_async.hdx")
	part.Write(mockZipData)
	w.Close()

	req, _ := http.NewRequest("POST", "/api/v1/documents/upload", strings.NewReader(b.String()))
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for async upload, got %d: %s", rec.Code, rec.Body.String())
	}

	var res struct {
		Code int `json:"code"`
		Data struct {
			JobID   string `json:"job_id"`
			IsAsync bool   `json:"is_async"`
		} `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &res)
	if !res.Data.IsAsync || res.Data.JobID == "" {
		t.Fatalf("expected is_async=true and valid job_id, got %+v", res)
	}

	// 2. 测试路径穿越攻击文件上传（应该被安全重定向或清洗，防止逃逸）
	b2 := &bytes.Buffer{}
	w2 := multipart.NewWriter(b2)
	_ = w2.WriteField("conflict_mode", "overwrite")
	part2, _ := w2.CreateFormFile("files", "../../escape.hdx")
	part2.Write(mockZipData)
	w2.Close()

	req2, _ := http.NewRequest("POST", "/api/v1/documents/upload", strings.NewReader(b2.String()))
	req2.Header.Set("Content-Type", w2.FormDataContentType())
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200 for sanitized upload, got %d: %s", rec2.Code, rec2.Body.String())
	}

	// 验证没有文件逃逸到 uploads 外层目录
	escapedFile := filepath.Join(tmpDir, "escape.hdx")
	if _, err := os.Stat(escapedFile); !os.IsNotExist(err) {
		t.Errorf("file escaped to %s, path traversal vulnerability present!", escapedFile)
	}
}

func TestTaskAPIValidation(t *testing.T) {
	logger.Init("debug", "console")

	tmpDir, err := os.MkdirTemp("", "api_task_val_test_*")
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

	badIDs := []string{
		"short",
		"bad*id",
		"task%20spaces",
		"invalid!id",
		"task$dollar",
		"toolongtaskid123456789012345678901234567890123456789012345678901234567890",
	}

	for _, badID := range badIDs {
		endpoints := []struct {
			method string
			path   string
		}{
			{"GET", "/api/v1/tasks/" + badID},
			{"GET", "/api/v1/tasks/" + badID + "/files"},
			{"POST", "/api/v1/tasks/" + badID + "/import"},
			{"GET", "/api/v1/tasks/" + badID + "/logs"},
			{"GET", "/api/v1/tasks/" + badID + "/rca"},
			{"GET", "/api/v1/tasks/" + badID + "/export"},
			{"DELETE", "/api/v1/tasks/" + badID},
		}

		for _, ep := range endpoints {
			req, _ := http.NewRequest(ep.method, ep.path, nil)
			if ep.method == "POST" {
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Errorf("expected 400 Bad Request for %s %s, got %d", ep.method, ep.path, w.Code)
			}
		}
	}
}

func TestStatsHandlerCache(t *testing.T) {
	logger.Init("debug", "console")

	tmpDir, err := os.MkdirTemp("", "api_stats_cache_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "stats_test.db")
	db, err := storage.InitKnowledgeDB(dbPath)
	if err != nil {
		t.Fatalf("init db failed: %v", err)
	}

	handler := api.NewStatsHandler(db)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v1/system/stats", handler.GetSystemStats)

	// 第一次请求 (未命中缓存，触发 DB 查询)
	w1 := httptest.NewRecorder()
	req1, _ := http.NewRequest("GET", "/api/v1/system/stats", nil)
	r.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w1.Code)
	}

	var res1 struct {
		Data struct {
			TotalKnowledge int `json:"total_knowledge"`
		} `json:"data"`
	}
	json.Unmarshal(w1.Body.Bytes(), &res1)
	if res1.Data.TotalKnowledge != 0 {
		t.Errorf("expected total_knowledge 0, got %d", res1.Data.TotalKnowledge)
	}

	// 插入新记录，由于 15s 缓存未过期，请求依然返回缓存结果 0
	db.Create(&model.Knowledge{Module: "TEST", Brief: "TEST_BRIEF", ContentHash: "h1"})

	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET", "/api/v1/system/stats", nil)
	r.ServeHTTP(w2, req2)
	var res2 struct {
		Data struct {
			TotalKnowledge int `json:"total_knowledge"`
		} `json:"data"`
	}
	json.Unmarshal(w2.Body.Bytes(), &res2)
	if res2.Data.TotalKnowledge != 0 {
		t.Errorf("expected cached total_knowledge 0, got %d", res2.Data.TotalKnowledge)
	}

	// 清理缓存后，立即重新查询到最新值 1
	handler.InvalidateCache()
	w3 := httptest.NewRecorder()
	req3, _ := http.NewRequest("GET", "/api/v1/system/stats", nil)
	r.ServeHTTP(w3, req3)
	var res3 struct {
		Data struct {
			TotalKnowledge int `json:"total_knowledge"`
		} `json:"data"`
	}
	json.Unmarshal(w3.Body.Bytes(), &res3)
	if res3.Data.TotalKnowledge != 1 {
		t.Errorf("expected refreshed total_knowledge 1, got %d", res3.Data.TotalKnowledge)
	}
}

func TestTaskMultipartStreamingUpload(t *testing.T) {
	logger.Init("debug", "console")

	tmpDir, err := os.MkdirTemp("", "api_task_stream_upload_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	uploadDir := filepath.Join(tmpDir, "uploads")
	cfg := &config.Config{
		Server: config.ServerConfig{Port: 8080, Mode: "test"},
		Storage: config.StorageConfig{
			DataDir:     tmpDir,
			KnowledgeDB: filepath.Join(tmpDir, "knowledge.db"),
			BleveIndex:  filepath.Join(tmpDir, "bleve.index"),
			TaskDir:     filepath.Join(tmpDir, "tasks"),
			UploadDir:   uploadDir,
		},
	}
	config.ConfigFileUsed = filepath.Join(tmpDir, "config.yaml")
	config.GlobalConfig = cfg

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

	// 1. Multipart POST /api/v1/tasks
	var bodyBuf bytes.Buffer
	mw := multipart.NewWriter(&bodyBuf)
	_ = mw.WriteField("task_name", "MultipartTask")
	_ = mw.WriteField("device_type", "CloudEngine")

	part1, _ := mw.CreateFormFile("file", "sw01.log")
	part1.Write([]byte("Apr 15 2026 14:00:01 CORE-SW-01 %%01IFNET/4/IF_DOWN(l)[1]: Interface 100GE1/0/1 state turned to DOWN. (InterfaceName=100GE1/0/1)\n"))
	part2, _ := mw.CreateFormFile("file", "sw02.log")
	part2.Write([]byte("Apr 15 2026 14:00:02 CORE-SW-02 %%01BFD/2/BFD_SESS_DOWN(l)[2]: BFD session state changed to DOWN. (SessionID=10)\n"))
	mw.Close()

	req, _ := http.NewRequest("POST", "/api/v1/tasks", &bodyBuf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for multipart CreateTask, got %d: %s", rec.Code, rec.Body.String())
	}

	var res struct {
		Code int `json:"code"`
		Data struct {
			TaskID    string `json:"task_id"`
			LogCount  int    `json:"log_count"`
			FileCount int    `json:"file_count"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}

	if res.Data.LogCount != 2 || res.Data.FileCount != 2 {
		t.Errorf("expected 2 logs and 2 files, got logs=%d, files=%d", res.Data.LogCount, res.Data.FileCount)
	}

	// 2. Multipart POST /api/v1/tasks/:id/import 补充导入
	var bodyBuf2 bytes.Buffer
	mw2 := multipart.NewWriter(&bodyBuf2)
	_ = mw2.WriteField("conflict_mode", "skip")
	part3, _ := mw2.CreateFormFile("file", "fw01.log")
	part3.Write([]byte("Apr 15 2026 14:05:00 USG-FW-01 %%01AAA/4/USER_AUTH_FAIL(l)[202]: User authentication failed. (UserName=testuser, UserIP=192.168.10.5)\n"))
	mw2.Close()

	req2, _ := http.NewRequest("POST", "/api/v1/tasks/"+res.Data.TaskID+"/import", &bodyBuf2)
	req2.Header.Set("Content-Type", mw2.FormDataContentType())
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200 for multipart ImportLogs, got %d: %s", rec2.Code, rec2.Body.String())
	}

	var res2 struct {
		Code int `json:"code"`
		Data struct {
			LogCount  int `json:"log_count"`
			FileCount int `json:"file_count"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &res2); err != nil {
		t.Fatalf("unmarshal import response failed: %v", err)
	}

	if res2.Data.LogCount != 3 || res2.Data.FileCount != 3 {
		t.Errorf("expected 3 logs and 3 files after import, got logs=%d, files=%d", res2.Data.LogCount, res2.Data.FileCount)
	}
}



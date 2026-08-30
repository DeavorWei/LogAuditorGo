package api_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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

func TestDocumentImportFromDirAndConflict(t *testing.T) {
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

	// 在服务端本地磁盘上构造一个已解压的 Mock HDX 文档目录
	docRoot := filepath.Join(tmpDir, "doc_root")
	docDir := filepath.Join(docRoot, "test_doc")
	extractZipToDir(t, createMockHDXZip(t, "DOC_TEST_001", "TestSwitch", "V100R001C00"), docDir)

	// 1. 首次导入：覆盖/创建模式
	payload1, _ := json.Marshal(map[string]interface{}{
		"paths":         []string{docDir},
		"conflict_mode": "overwrite",
	})
	req1, _ := http.NewRequest("POST", "/api/v1/documents/import-dir", bytes.NewReader(payload1))
	req1.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()
	router.ServeHTTP(w1, req1)

	if w1.Code != http.StatusOK {
		t.Fatalf("expected 200 for import-dir, got %d: %s", w1.Code, w1.Body.String())
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

	// 2. 二次导入同一目录，指定 conflict_mode = skip
	payload2, _ := json.Marshal(map[string]interface{}{
		"paths":         []string{docDir},
		"conflict_mode": "skip",
	})
	req2, _ := http.NewRequest("POST", "/api/v1/documents/import-dir", bytes.NewReader(payload2))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 for skip import, got %d: %s", w2.Code, w2.Body.String())
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

// extractZipToDir 将内存中的 zip 数据包解压到指定目录，用于模拟已解压的 HDX 文档包
func extractZipToDir(t *testing.T, data []byte, destDir string) {
	t.Helper()

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open mock zip failed: %v", err)
	}
	if err := os.MkdirAll(destDir, 0755); err != nil {
		t.Fatalf("create dest dir failed: %v", err)
	}

	for _, f := range zr.File {
		target := filepath.Join(destDir, filepath.FromSlash(f.Name))
		if f.FileInfo().IsDir() {
			_ = os.MkdirAll(target, 0755)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			t.Fatalf("create sub dir failed: %v", err)
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open zip entry failed: %v", err)
		}
		out, err := os.Create(target)
		if err != nil {
			rc.Close()
			t.Fatalf("create file failed: %v", err)
		}
		_, err = io.Copy(out, rc)
		out.Close()
		rc.Close()
		if err != nil {
			t.Fatalf("write file failed: %v", err)
		}
	}
}

// writeTestFile 写入测试文件并自动创建其父目录
func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("create dir for %s failed: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write file %s failed: %v", path, err)
	}
}

func TestFolderWithMultipleHDXImport(t *testing.T) {
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

	// 构造一个包含多个文档包的父目录：2 个解压后的 HDX 包 + 1 个散装 HDX 目录
	docRoot := filepath.Join(tmpDir, "doc_root")

	zip1 := createMockHDXZip(t, "DOC_MULTI_01", "Switch-A", "V100R001C00")
	zip2 := createMockHDXZip(t, "DOC_MULTI_02", "Firewall-B", "V200R002C00")
	extractZipToDir(t, zip1, filepath.Join(docRoot, "doc1"))
	extractZipToDir(t, zip2, filepath.Join(docRoot, "sub", "doc2"))

	unzipped := filepath.Join(docRoot, "unzipped_doc")
	writeTestFile(t, filepath.Join(unzipped, "profile.xml"), `<?xml version="1.0" encoding="utf-8"?>
<profile>
  <libId>DOC_UNZIPPED_03</libId>
  <libVersion>01</libVersion>
  <libName>Unzipped Document 03</libName>
  <productType>Router-C</productType>
  <productVersion>V300R003C00</productVersion>
  <issueDate>2026-04-15</issueDate>
  <topicNumber>1</topicNumber>
  <navi>resources/navi.xml</navi>
</profile>`)
	writeTestFile(t, filepath.Join(unzipped, "resources", "navi.xml"), `<?xml version="1.0" encoding="utf-8"?>
<topics>
  <topic id="r_03" txt="日志" url="">
    <topic id="top_03" txt="BGP/2/DOWN" url="resources/log3.html"/>
  </topic>
</topics>`)
	writeTestFile(t, filepath.Join(unzipped, "resources", "log3.html"),
		`<html><body><h2>日志信息</h2><p>BGP down</p></body></html>`)

	// 写入一个真实的 .hdx 压缩包文件到 docRoot
	zipArchiveBytes := createMockHDXZip(t, "DOC_ARCHIVE_04", "Switch-D", "V400R004C00")
	if err := os.WriteFile(filepath.Join(docRoot, "Switch_D.hdx"), zipArchiveBytes, 0644); err != nil {
		t.Fatalf("write Switch_D.hdx failed: %v", err)
	}

	// 1. 测试 POST /api/v1/documents/scan 智能扫描接口
	scanPayload, _ := json.Marshal(map[string]interface{}{
		"path": docRoot,
	})
	scanReq, _ := http.NewRequest("POST", "/api/v1/documents/scan", bytes.NewReader(scanPayload))
	scanReq.Header.Set("Content-Type", "application/json")
	scanRec := httptest.NewRecorder()
	router.ServeHTTP(scanRec, scanReq)

	if scanRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for document scan, got %d: %s", scanRec.Code, scanRec.Body.String())
	}

	var scanRes struct {
		Code int `json:"code"`
		Data struct {
			TotalCount     int `json:"total_count"`
			ArchiveCount   int `json:"archive_count"`
			DirectoryCount int `json:"directory_count"`
			Items          []struct {
				Type string `json:"type"`
				Name string `json:"name"`
				Path string `json:"path"`
			} `json:"items"`
		} `json:"data"`
	}
	json.Unmarshal(scanRec.Body.Bytes(), &scanRes)
	if scanRes.Code != 0 || scanRes.Data.TotalCount != 4 {
		t.Fatalf("expected 4 scanned items (1 archive + 3 dirs), got %+v", scanRes)
	}
	if scanRes.Data.ArchiveCount != 1 || scanRes.Data.DirectoryCount != 3 {
		t.Fatalf("expected 1 archive and 3 directories, got archive=%d, dir=%d", scanRes.Data.ArchiveCount, scanRes.Data.DirectoryCount)
	}

	// 2. 直接将父目录路径提交给服务端，由服务端递归发现并一次性导入其中所有文档包（含压缩包与解压目录）
	payload, _ := json.Marshal(map[string]interface{}{
		"paths":         []string{docRoot},
		"conflict_mode": "overwrite",
	})
	req, _ := http.NewRequest("POST", "/api/v1/documents/import-dir", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
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
	if res.Code != 0 || len(res.Data.ImportedDocs) != 4 {
		t.Fatalf("expected 4 imported documents (1 .hdx archive + 3 unzipped directories), got %+v", res)
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

	// 2. 请求带有非受信 Origin 头（如外部域），必须**完全不返回** Access-Control-Allow-Origin (ARCH-08)。
	//
	//    原实现在此处兜底返回 `*`，等于对任意网站放开了本服务全部数据（任务、知识库、配置）的读写，
	//    配合"文件系统浏览接口"可形成本地文件泄露链。正确做法是不设置该响应头，
	//    浏览器会按同源策略直接拦截响应。
	reqExternal, _ := http.NewRequest("GET", "/api/v1/system/stats", nil)
	reqExternal.Header.Set("Origin", "http://evil.com:3000")
	wExt := httptest.NewRecorder()
	router.ServeHTTP(wExt, reqExternal)

	if got := wExt.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("ARCH-08: expected NO Access-Control-Allow-Origin for untrusted origin, got %q", got)
	}
	if wExt.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Errorf("expected Access-Control-Allow-Credentials to be empty for untrusted origin, got %s", wExt.Header().Get("Access-Control-Allow-Credentials"))
	}

	// 3. 请求不带 Origin 头（同源请求或非浏览器客户端），同样不设置 CORS 相关响应头
	req2, _ := http.NewRequest("GET", "/api/v1/system/stats", nil)
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)

	if got := w2.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("ARCH-08: expected NO Access-Control-Allow-Origin when request has no Origin, got %q", got)
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

func TestImportDirAsyncAndPathValidation(t *testing.T) {
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

	docRoot := filepath.Join(tmpDir, "doc_root")
	docDir := filepath.Join(docRoot, "async_doc")
	extractZipToDir(t, createMockHDXZip(t, "DOC_ASYNC_001", "AsyncSwitch", "V100R001C00"), docDir)

	// 1. 测试异步导入模式：应立即返回 job_id
	payload, _ := json.Marshal(map[string]interface{}{
		"paths":         []string{docDir},
		"conflict_mode": "overwrite",
		"async":         true,
	})
	req, _ := http.NewRequest("POST", "/api/v1/documents/import-dir", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for async import, got %d: %s", rec.Code, rec.Body.String())
	}

	var res struct {
		Code int `json:"code"`
		Data struct {
			JobID     string `json:"job_id"`
			IsAsync   bool   `json:"is_async"`
			PathCount int    `json:"path_count"`
		} `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &res)
	if !res.Data.IsAsync || res.Data.JobID == "" || res.Data.PathCount != 1 {
		t.Fatalf("expected is_async=true, valid job_id and path_count=1, got %+v", res)
	}

	// 2. 不存在的路径应在预检阶段被拒绝
	payload2, _ := json.Marshal(map[string]interface{}{
		"paths": []string{filepath.Join(tmpDir, "definitely_not_exist_dir")},
	})
	req2, _ := http.NewRequest("POST", "/api/v1/documents/import-dir", bytes.NewReader(payload2))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for non-existent path, got %d: %s", rec2.Code, rec2.Body.String())
	}

	// 3. 空路径列表应被拒绝
	req3, _ := http.NewRequest("POST", "/api/v1/documents/import-dir", strings.NewReader(`{"paths":[]}`))
	req3.Header.Set("Content-Type", "application/json")
	rec3 := httptest.NewRecorder()
	router.ServeHTTP(rec3, req3)

	if rec3.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty paths, got %d: %s", rec3.Code, rec3.Body.String())
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

func TestTaskPathImport(t *testing.T) {
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

	// 1. 在服务端本地磁盘准备日志文件，HTTP 请求只提交路径
	logDir := filepath.Join(tmpDir, "device_logs")
	writeTestFile(t, filepath.Join(logDir, "sw01.log"),
		"Apr 15 2026 14:00:01 CORE-SW-01 %%01IFNET/4/IF_DOWN(l)[1]: Interface 100GE1/0/1 state turned to DOWN. (InterfaceName=100GE1/0/1)\n")
	writeTestFile(t, filepath.Join(logDir, "sw02.log"),
		"Apr 15 2026 14:00:02 CORE-SW-02 %%01BFD/2/BFD_SESS_DOWN(l)[2]: BFD session state changed to DOWN. (SessionID=10)\n")

	payload, _ := json.Marshal(map[string]interface{}{
		"task_name":   "PathImportTask",
		"device_type": "CloudEngine",
		"paths":       []string{logDir},
		"exts":        []string{".log"},
		"recursive":   true,
	})
	req, _ := http.NewRequest("POST", "/api/v1/tasks", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for path-based CreateTask, got %d: %s", rec.Code, rec.Body.String())
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

	// 2. 基于路径向已有任务补充导入
	extraDir := filepath.Join(tmpDir, "extra_logs")
	writeTestFile(t, filepath.Join(extraDir, "fw01.log"),
		"Apr 15 2026 14:05:00 USG-FW-01 %%01AAA/4/USER_AUTH_FAIL(l)[202]: User authentication failed. (UserName=testuser, UserIP=192.168.10.5)\n")

	payload2, _ := json.Marshal(map[string]interface{}{
		"paths":         []string{extraDir},
		"exts":          []string{".log"},
		"conflict_mode": "skip",
	})
	req2, _ := http.NewRequest("POST", "/api/v1/tasks/"+res.Data.TaskID+"/import", bytes.NewReader(payload2))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200 for path-based ImportLogs, got %d: %s", rec2.Code, rec2.Body.String())
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



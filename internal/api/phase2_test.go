package api_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"logauditorgo/internal/model"
	"logauditorgo/internal/storage"
)

// TestOnboardingAPI 验证首启引导状态持久化与跳过/关闭语义。
func TestOnboardingAPI(t *testing.T) {
	router, _, tmpDir := setupPhase1Router(t)

	w, env := doRedRouteRequest(t, router, http.MethodGet, "/api/v1/system/onboarding", "")
	var initState struct {
		State struct {
			CompletedSteps []int `json:"completed_steps"`
			Skipped        bool  `json:"skipped"`
		} `json:"state"`
		ShouldShow bool `json:"should_show"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(env.Data, &initState) != nil || !initState.ShouldShow {
		t.Fatalf("initial onboarding state failed: %d %s", w.Code, w.Body.String())
	}

	w, env = doRedRouteRequest(t, router, http.MethodPut, "/api/v1/system/onboarding", `{"completed_steps":[1,2],"skipped":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("update onboarding failed: %d %s", w.Code, w.Body.String())
	}
	var updated struct {
		State struct {
			CompletedSteps []int `json:"completed_steps"`
			Skipped        bool  `json:"skipped"`
		} `json:"state"`
		ShouldShow bool `json:"should_show"`
	}
	if json.Unmarshal(env.Data, &updated) != nil || len(updated.State.CompletedSteps) != 2 || !updated.State.Skipped || updated.ShouldShow {
		t.Fatalf("unexpected onboarding state: %s", w.Body.String())
	}

	// 持久化到数据目录，重新读取仍为 skipped
	if _, err := filepath.Glob(filepath.Join(tmpDir, "onboarding.json")); err != nil {
		t.Fatalf("onboarding file glob failed: %v", err)
	}
	w, env = doRedRouteRequest(t, router, http.MethodGet, "/api/v1/system/onboarding", "")
	if json.Unmarshal(env.Data, &initState) != nil || initState.ShouldShow {
		t.Fatalf("onboarding state not persisted: %s", w.Body.String())
	}

	// 非法步骤拒绝
	w, _ = doRedRouteRequest(t, router, http.MethodPut, "/api/v1/system/onboarding", `{"completed_steps":[5]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid step must be 400, got %d", w.Code)
	}
}

// TestBackupAndDiagnosticsAPI 验证备份创建/列表/下载/删除、恢复准备与诊断包。
func TestBackupAndDiagnosticsAPI(t *testing.T) {
	router, _, tmpDir := setupPhase1Router(t)

	w, env := doRedRouteRequest(t, router, http.MethodPost, "/api/v1/system/backup", "")
	var created struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(env.Data, &created) != nil || created.Name == "" {
		t.Fatalf("create backup failed: %d %s", w.Code, w.Body.String())
	}

	w, env = doRedRouteRequest(t, router, http.MethodGet, "/api/v1/system/backups", "")
	var backups []map[string]interface{}
	if w.Code != http.StatusOK || json.Unmarshal(env.Data, &backups) != nil || len(backups) != 1 {
		t.Fatalf("list backups failed: %d %s", w.Code, w.Body.String())
	}

	w, _ = doRedRouteRequest(t, router, http.MethodGet, "/api/v1/system/backups/"+created.Name+"/download", "")
	if w.Code != http.StatusOK || w.Body.Len() == 0 {
		t.Fatalf("download backup failed: %d", w.Code)
	}
	// 下载内容应为合法 zip
	if _, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len())); err != nil {
		t.Fatalf("downloaded backup is not a zip: %v", err)
	}

	// 非法备份名
	w, _ = doRedRouteRequest(t, router, http.MethodGet, "/api/v1/system/backups/evil.zip/download", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("illegal backup name must 404, got %d", w.Code)
	}

	// 恢复准备：写 marker，重启后生效
	w, env = doRedRouteRequest(t, router, http.MethodPost, "/api/v1/system/restore", `{"name":"`+created.Name+`"}`)
	var restore struct {
		Pending bool `json:"pending"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(env.Data, &restore) != nil || !restore.Pending {
		t.Fatalf("prepare restore failed: %d %s", w.Code, w.Body.String())
	}
	marker, err := filepath.Glob(filepath.Join(tmpDir, "restore.pending"))
	if err != nil || len(marker) != 1 {
		t.Fatalf("restore marker missing: %v", err)
	}

	// 诊断包
	w, _ = doRedRouteRequest(t, router, http.MethodGet, "/api/v1/system/diagnostics", "")
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("diagnostics failed: %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatalf("diagnostics is not a zip: %v", err)
	}
	foundSystem := false
	for _, f := range zr.File {
		if f.Name == "system.json" {
			foundSystem = true
			rc, _ := f.Open()
			content, _ := io.ReadAll(rc)
			_ = rc.Close()
			if !strings.Contains(string(content), "version") {
				t.Fatalf("system.json missing version: %s", content)
			}
		}
	}
	if !foundSystem {
		t.Fatalf("diagnostics missing system.json")
	}

	// 删除备份
	w, _ = doRedRouteRequest(t, router, http.MethodDelete, "/api/v1/system/backups/"+created.Name, "")
	if w.Code != http.StatusOK {
		t.Fatalf("delete backup failed: %d %s", w.Code, w.Body.String())
	}
}

// TestLocalKnowledgeAndOverrideAPI 验证本地知识 CRUD/CSV 与覆盖绑定。
func TestLocalKnowledgeAndOverrideAPI(t *testing.T) {
	router, _, _ := setupPhase1Router(t)

	createBody := `{"module":"BGP","brief":"PEER_BACKWARD","product_family":"CE16800","severity":2,"description":"本地补充","cause":"链路","action":"检查链路","reference":"https://example.local"}`
	w, env := doRedRouteRequest(t, router, http.MethodPost, "/api/v1/knowledge/local", createBody)
	var local struct {
		ID     uint   `json:"id"`
		Source string `json:"source"`
		Module string `json:"module"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(env.Data, &local) != nil || local.ID == 0 || local.Source != "local" || local.Module != "BGP" {
		t.Fatalf("create local knowledge failed: %d %s", w.Code, w.Body.String())
	}

	w, env = doRedRouteRequest(t, router, http.MethodGet, "/api/v1/knowledge/local", "")
	var locals []map[string]interface{}
	if w.Code != http.StatusOK || json.Unmarshal(env.Data, &locals) != nil || len(locals) != 1 {
		t.Fatalf("list local knowledge failed: %d %s", w.Code, w.Body.String())
	}

	// 更新
	w, env = doRedRouteRequest(t, router, http.MethodPut, "/api/v1/knowledge/local/"+jsonNumber(local.ID), `{"module":"BGP","brief":"PEER_BACKWARD","severity":1,"description":"更新后的本地知识"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("update local knowledge failed: %d %s", w.Code, w.Body.String())
	}

	// CSV 导出/导入
	w, _ = doRedRouteRequest(t, router, http.MethodGet, "/api/v1/knowledge/local/export", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "PEER_BACKWARD") {
		t.Fatalf("export local csv failed: %d", w.Code)
	}
	csvBody := `{"content":"module,brief,severity,description\nOSPF,NBR_CHG,3,本地 OSPF\n"}`
	w, env = doRedRouteRequest(t, router, http.MethodPost, "/api/v1/knowledge/local/import", csvBody)
	var importResult struct {
		Created int `json:"created"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(env.Data, &importResult) != nil || importResult.Created != 1 {
		t.Fatalf("import local csv failed: %d %s", w.Code, w.Body.String())
	}

	// 覆盖：目标必须是官方知识
	official := model.Knowledge{
		EntryType:   model.EntryTypeLog,
		Module:      "IFNET",
		Brief:       "IF_DOWN",
		Severity:    4,
		Description: "官方 IF_DOWN",
		ContentHash: "official-api-local-test",
		Source:      "official",
	}
	if err := storage.GlobalKnowledgeDB.Create(&official).Error; err != nil {
		t.Fatalf("seed official knowledge failed: %v", err)
	}
	w, env = doRedRouteRequest(t, router, http.MethodPost, "/api/v1/knowledge/overrides", `{"module":"IFNET","brief":"IF_DOWN","knowledge_id":`+jsonNumber(official.ID)+`}`)
	var override struct {
		ID          uint `json:"id"`
		KnowledgeID uint `json:"knowledge_id"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(env.Data, &override) != nil || override.KnowledgeID != official.ID {
		t.Fatalf("create override failed: %d %s", w.Code, w.Body.String())
	}
	w, env = doRedRouteRequest(t, router, http.MethodGet, "/api/v1/knowledge/overrides", "")
	var overrides []map[string]interface{}
	if w.Code != http.StatusOK || json.Unmarshal(env.Data, &overrides) != nil || len(overrides) != 1 {
		t.Fatalf("list overrides failed: %d %s", w.Code, w.Body.String())
	}
	// 本地知识不能作为覆盖目标
	w, _ = doRedRouteRequest(t, router, http.MethodPost, "/api/v1/knowledge/overrides", `{"module":"BGP","brief":"X","knowledge_id":`+jsonNumber(local.ID)+`}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("local knowledge as override target must be 400, got %d", w.Code)
	}
	// 删除本地知识（官方删除走别的接口，这里只删本地）
	w, _ = doRedRouteRequest(t, router, http.MethodDelete, "/api/v1/knowledge/local/"+jsonNumber(local.ID), "")
	if w.Code != http.StatusOK {
		t.Fatalf("delete local knowledge failed: %d %s", w.Code, w.Body.String())
	}
}

func jsonNumber(v uint) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// TestKnowledgeFacetsFavoritesRecentAPI 验证分面、收藏与最近浏览。
func TestKnowledgeFacetsFavoritesRecentAPI(t *testing.T) {
	router, _, _ := setupPhase1Router(t)

	official := model.Knowledge{
		EntryType:   model.EntryTypeLog,
		Module:      "BGP",
		Brief:       "PEER_BACKWARD",
		Severity:    3,
		Description: "BGP 邻居断开",
		ContentHash: "facet-favorite-test",
		Source:      "official",
	}
	if err := storage.GlobalKnowledgeDB.Create(&official).Error; err != nil {
		t.Fatalf("seed knowledge failed: %v", err)
	}
	if err := storage.GlobalKnowledgeDB.Create(&model.KnowledgeVersionMapping{
		KnowledgeID:    official.ID,
		ProductType:    "CE16800",
		ProductVersion: "V200R025C00",
	}).Error; err != nil {
		t.Fatalf("seed mapping failed: %v", err)
	}

	w, env := doRedRouteRequest(t, router, http.MethodGet, "/api/v1/knowledge/facets", "")
	var facets struct {
		Products   []map[string]interface{} `json:"products"`
		Severities []map[string]interface{} `json:"severities"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(env.Data, &facets) != nil || len(facets.Severities) == 0 || len(facets.Products) == 0 {
		t.Fatalf("facets failed: %d %s", w.Code, w.Body.String())
	}

	w, _ = doRedRouteRequest(t, router, http.MethodPost, "/api/v1/knowledge/"+jsonNumber(official.ID)+"/favorite", "")
	if w.Code != http.StatusOK {
		t.Fatalf("favorite failed: %d %s", w.Code, w.Body.String())
	}
	w, env = doRedRouteRequest(t, router, http.MethodGet, "/api/v1/knowledge/favorites", "")
	var favorites []model.Knowledge
	if w.Code != http.StatusOK || json.Unmarshal(env.Data, &favorites) != nil || len(favorites) != 1 || favorites[0].ID != official.ID {
		t.Fatalf("favorites failed: %d %s", w.Code, w.Body.String())
	}

	// 详情访问写入最近浏览
	w, _ = doRedRouteRequest(t, router, http.MethodGet, "/api/v1/knowledge/"+jsonNumber(official.ID), "")
	if w.Code != http.StatusOK {
		t.Fatalf("detail failed: %d", w.Code)
	}
	w, env = doRedRouteRequest(t, router, http.MethodGet, "/api/v1/knowledge/recent", "")
	var recent []model.Knowledge
	if w.Code != http.StatusOK || json.Unmarshal(env.Data, &recent) != nil || len(recent) != 1 {
		t.Fatalf("recent failed: %d %s", w.Code, w.Body.String())
	}

	w, _ = doRedRouteRequest(t, router, http.MethodDelete, "/api/v1/knowledge/"+jsonNumber(official.ID)+"/favorite", "")
	if w.Code != http.StatusOK {
		t.Fatalf("unfavorite failed: %d", w.Code)
	}
	w, env = doRedRouteRequest(t, router, http.MethodGet, "/api/v1/knowledge/favorites", "")
	favorites = nil
	if w.Code != http.StatusOK || json.Unmarshal(env.Data, &favorites) != nil || len(favorites) != 0 {
		t.Fatalf("favorites should be empty: %s", w.Body.String())
	}
}

package api_test

import (
	"net/http"
	"path/filepath"
	"testing"
)

// TestInsightsRequireExistingTask 验证直方图/聚合/注释计数不会为不存在的任务创建幽灵库。
func TestInsightsRequireExistingTask(t *testing.T) {
	router, _, tmpDir := setupPhase1Router(t)
	fakeID := "deadbeefdeadbeef"

	cases := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/api/v1/tasks/" + fakeID + "/logs/histogram", `{}`},
		{http.MethodPost, "/api/v1/tasks/" + fakeID + "/logs/aggregated", `{}`},
		{http.MethodGet, "/api/v1/tasks/" + fakeID + "/logs/comments/count", ""},
	}
	for _, item := range cases {
		w, _ := doRedRouteRequest(t, router, item.method, item.path, item.body)
		if w.Code != http.StatusNotFound {
			t.Fatalf("%s %s = %d, want 404 (body=%s)", item.method, item.path, w.Code, w.Body.String())
		}
	}
	files, _ := filepath.Glob(filepath.Join(tmpDir, "tasks", "task_"+fakeID+"*.db"))
	if len(files) != 0 {
		t.Fatalf("phantom task db created: %v", files)
	}
}

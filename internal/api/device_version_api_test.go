package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"logauditorgo/internal/model"
)

// TestAPI_DeviceVersion_RoundTrip 守护"设备型号 + 软件版本"的 API 契约。
//
// 设备行的 device_type / device_version 是日志匹配上下文的**唯一**权威来源
// （本方案 §4.5 / §6.1），因此这两项必须能从 API 完整写入并读回，
// 前端 DeviceManager 的"设备型号 / 软件版本"输入才不是摆设。
func TestAPI_DeviceVersion_RoundTrip(t *testing.T) {
	router, taskID, _ := setupTestRouterWithTask(t)

	// 1. 创建设备：型号用具体型号（可命中版本分档），版本显式指定
	createJSON := `{"device_name":"WLC-01","device_type":"AC6508","device_version":"V200R024C00","hostname":"AC6508"}`
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/tasks/"+taskID+"/devices", strings.NewReader(createJSON))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create device expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var created struct {
		Code int          `json:"code"`
		Data model.Device `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create device response failed: %v", err)
	}
	if created.Code != 0 || created.Data.ID == 0 {
		t.Fatalf("unexpected create response: code=%d id=%d", created.Code, created.Data.ID)
	}
	if created.Data.DeviceType != "AC6508" {
		t.Errorf("device_type = %q, want %q", created.Data.DeviceType, "AC6508")
	}
	if created.Data.DeviceVersion != "V200R024C00" {
		t.Errorf("device_version = %q, want %q", created.Data.DeviceVersion, "V200R024C00")
	}

	devicePath := "/api/v1/tasks/" + taskID + "/devices/" + strconv.FormatUint(uint64(created.Data.ID), 10)

	// 2. 读回：版本必须持久化，而不是被请求 DTO 静默丢弃
	reqGet, _ := http.NewRequest(http.MethodGet, devicePath, nil)
	wGet := httptest.NewRecorder()
	router.ServeHTTP(wGet, reqGet)
	if wGet.Code != http.StatusOK {
		t.Fatalf("get device expected 200, got %d: %s", wGet.Code, wGet.Body.String())
	}
	var got struct {
		Data model.Device `json:"data"`
	}
	if err := json.Unmarshal(wGet.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode get device response failed: %v", err)
	}
	if got.Data.DeviceVersion != "V200R024C00" {
		t.Fatalf("persisted device_version = %q, want %q", got.Data.DeviceVersion, "V200R024C00")
	}

	// 3. 更新：型号改挂到另一产品族，版本同步刷新（改完重新分析即生效）
	updateJSON := `{"device_type":"CloudEngine","device_version":"V200R033C00"}`
	reqPut, _ := http.NewRequest(http.MethodPut, devicePath, strings.NewReader(updateJSON))
	reqPut.Header.Set("Content-Type", "application/json")
	wPut := httptest.NewRecorder()
	router.ServeHTTP(wPut, reqPut)
	if wPut.Code != http.StatusOK {
		t.Fatalf("update device expected 200, got %d: %s", wPut.Code, wPut.Body.String())
	}

	reqGet2, _ := http.NewRequest(http.MethodGet, devicePath, nil)
	wGet2 := httptest.NewRecorder()
	router.ServeHTTP(wGet2, reqGet2)
	var got2 struct {
		Data model.Device `json:"data"`
	}
	if err := json.Unmarshal(wGet2.Body.Bytes(), &got2); err != nil {
		t.Fatalf("decode get device response failed: %v", err)
	}
	if got2.Data.DeviceType != "CloudEngine" {
		t.Errorf("updated device_type = %q, want %q", got2.Data.DeviceType, "CloudEngine")
	}
	if got2.Data.DeviceVersion != "V200R033C00" {
		t.Errorf("updated device_version = %q, want %q", got2.Data.DeviceVersion, "V200R033C00")
	}
}

// TestAPI_LegacyTaskLevelDeviceFields_NotRejected 守护 API 兼容：
// 老客户端仍会在 POST /tasks 与 /reanalyze 中携带任务级 device_type / device_version，
// 服务端必须接受（**绝不 400**），但这两个字段不得再影响任何日志匹配
// （匹配上下文由设备行决定，见 TestResolveLineContext）。
//
// 采用"无日志内容"的空任务路径，避免异步导入/RCA 后台协程与 t.TempDir 清理竞争。
func TestAPI_LegacyTaskLevelDeviceFields_NotRejected(t *testing.T) {
	router, _, _ := setupTestRouterWithTask(t)

	// 1. POST /tasks 携带遗留任务级型号 + 版本
	payload := `{"task_name":"LegacyClientTask","device_type":"CloudEngine 16800","device_version":"V200R019C00"}`
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("legacy task-level device fields must never be rejected, got %d: %s", w.Code, w.Body.String())
	}

	var res struct {
		Code int `json:"code"`
		Data struct {
			TaskID     string `json:"task_id"`
			DeviceType string `json:"device_type"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if res.Code != 0 || res.Data.TaskID == "" {
		t.Fatalf("task not created: code=%d task_id=%q", res.Code, res.Data.TaskID)
	}

	// 兼容契约：遗留任务级型号仍按老契约落库并回显（老客户端 UI 不炸），
	// 但它已完全不参与匹配——匹配上下文只认设备行。
	if res.Data.DeviceType != "CloudEngine 16800" {
		t.Errorf("legacy device_type must still be persisted for display, got %q", res.Data.DeviceType)
	}
}

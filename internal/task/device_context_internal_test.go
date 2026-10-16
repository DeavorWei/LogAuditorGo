package task

import (
	"context"
	"os"
	"testing"

	"logauditorgo/internal/matcher"
	"logauditorgo/internal/model"
	"logauditorgo/internal/storage"
)

// TestResolveLineContext 守护重分析链路的上下文决议优先级（本方案 §4.5 / §6.2）：
// 设备行库内属性 > 主机名嗅探 > 调用方显式覆盖 > 中性兜底，
// 且型号与版本**独立**决议（型号为空不影响版本生效）。
//
// 这是"任务级单一型号被当作全局上下文"（1.1 节 D1）不再复现的核心保证。
func TestResolveLineContext(t *testing.T) {
	devMap := map[uint]model.Device{
		7: {ID: 7, DeviceType: "AC6508", DeviceVersion: "V200R024C00"},
		8: {ID: 8, DeviceType: "CloudEngine"},
		9: {ID: 9, DeviceVersion: "V200R019C00"},
		// 历史遗留：DeviceType 被旧版本写死为形态分类，应按"型号未配置"处理
		11: {ID: 11, DeviceType: "Router", DeviceVersion: "V200R019C00"},
		12: {ID: 12, DeviceType: " switch "},
	}

	cases := []struct {
		name        string
		rec         model.LogRecord
		overrideP   string
		overrideV   string
		wantProduct string
		wantVersion string
	}{
		{
			name:        "已归属：设备行型号与版本即权威，覆盖值不生效",
			rec:         model.LogRecord{DeviceID: 7, Hostname: "IGNORED-HOST"},
			overrideP:   "Campus-Switch",
			overrideV:   "V100R001",
			wantProduct: "AC6508",
			wantVersion: "V200R024C00",
		},
		{
			name:        "已归属但设备无版本：型号仍权威，版本为空",
			rec:         model.LogRecord{DeviceID: 8, Hostname: "IGNORED-HOST"},
			overrideP:   "WLAN",
			wantProduct: "CloudEngine",
			wantVersion: "",
		},
		{
			name:        "脏 device_id 未命中设备表：回落主机名嗅探，覆盖仍不生效",
			rec:         model.LogRecord{DeviceID: 10, Hostname: "CE16800"},
			overrideP:   "Campus-Switch",
			overrideV:   "V200R001",
			wantProduct: "CE16800",
			wantVersion: "",
		},
		{
			name:        "未归属 + 可嗅探主机名：嗅探优先于显式覆盖",
			rec:         model.LogRecord{DeviceID: 0, Hostname: "S12700"},
			overrideP:   "CloudEngine",
			overrideV:   "V100",
			wantProduct: "S12700",
			wantVersion: "",
		},
		{
			name:        "未归属 + 无法嗅探：显式覆盖生效（ReanalyzeOptions 语义收敛点）",
			rec:         model.LogRecord{DeviceID: 0, Hostname: "CORE-SW-01"},
			overrideP:   "WLAN",
			overrideV:   "V200R019C00",
			wantProduct: "WLAN",
			wantVersion: "V200R019C00",
		},
		{
			name:        "未归属 + 无法嗅探 + 无覆盖：中性兜底",
			rec:         model.LogRecord{DeviceID: 0},
			wantProduct: matcher.NeutralProductName,
			wantVersion: "",
		},
		{
			name:        "设备仅有版本无型号：型号中性兜底，版本独立保留",
			rec:         model.LogRecord{DeviceID: 9},
			wantProduct: matcher.NeutralProductName,
			wantVersion: "V200R019C00",
		},
		{
			name:        "历史遗留 Router 型号 + 可嗅探主机名：视作未配置并恢复真实上下文",
			rec:         model.LogRecord{DeviceID: 11, Hostname: "AC6508"},
			wantProduct: "AC6508",
			wantVersion: "V200R019C00",
		},
		{
			name:        "历史遗留 Switch 型号（含空白）+ 无法嗅探：中性兜底",
			rec:         model.LogRecord{DeviceID: 12, Hostname: "CORE-SW-01"},
			wantProduct: matcher.NeutralProductName,
			wantVersion: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, v := resolveLineContext(&tc.rec, devMap, tc.overrideP, tc.overrideV)
			if p != tc.wantProduct || v != tc.wantVersion {
				t.Fatalf("resolveLineContext() = (%q, %q), want (%q, %q)",
					p, v, tc.wantProduct, tc.wantVersion)
			}
		})
	}
}

// TestNormalizeDeviceProduct 守护"历史形态分类不算型号"的归一规则。
//
// 该规则决定存量任务能否在重新分析后自动恢复真实匹配上下文：
// AutoAssignDevices 旧版本把 device_type 写死为 Router，若不做归一，
// 这批设备会永远拿不到同型号/同族版本分档（本方案 §9.1 的读路径等价替代）。
func TestNormalizeDeviceProduct(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"   ", ""},
		{"Router", ""},
		{"router", ""},
		{" switch ", ""},
		{"FIREWALL", ""},
		{"Other", ""},
		{"AC6508", "AC6508"},
		{"CloudEngine", "CloudEngine"},
		{" Huawei-VRP ", "Huawei-VRP"},
	}
	for _, tc := range cases {
		if got := normalizeDeviceProduct(tc.in); got != tc.want {
			t.Fatalf("normalizeDeviceProduct(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestPrepareBundles_DeviceContext 守护导入链路的上下文决议（本方案 §6.1）：
// 文件采样嗅探到 AC6508 主机名时，
//  1. fileBundle.targetProduct 指向**具体型号**而非任务级默认值；
//  2. 自动创建的设备行直接落库真实型号（绝不写死 "Router"）；
//  3. bundle.deviceID 指向该设备，保证日志归属与匹配上下文同源。
func TestPrepareBundles_DeviceContext(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "bundle_ctx_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	const taskID = "bundle-ctx-test"
	taskDB, _, err := storage.GetOrCreateTaskDB(tmpDir, taskID)
	if err != nil {
		t.Fatalf("init task db failed: %v", err)
	}
	defer storage.ReleaseTaskDB(taskID)

	content := "Apr 15 2026 14:00:01 AC6508 %%01IFNET/4/IF_DOWN(l)[1]: Interface down.\n"
	items := []FileUploadItem{{
		FileName: "ac6508.log",
		FileSize: int64(len(content)),
		Content:  content,
	}}

	svc := &Service{}
	bundles, totalLines, failed, err := svc.prepareBundles(
		context.Background(), taskID, 0, items, "overwrite", taskDB,
		map[string]model.TaskFile{}, map[string]bool{}, nil)
	if err != nil {
		t.Fatalf("prepareBundles failed: %v", err)
	}
	if len(failed) != 0 {
		t.Fatalf("unexpected failed files: %v", failed)
	}
	if len(bundles) != 1 {
		t.Fatalf("expected 1 bundle, got %d", len(bundles))
	}
	if totalLines != 1 {
		t.Fatalf("totalLines = %d, want 1", totalLines)
	}

	b := bundles[0]
	if b.targetProduct != "AC6508" {
		t.Fatalf("bundle.targetProduct = %q, want %q", b.targetProduct, "AC6508")
	}
	if b.targetVersion != "" {
		t.Fatalf("bundle.targetVersion = %q, want empty (未录入软件版本时应保持空)", b.targetVersion)
	}

	var dev model.Device
	if err := taskDB.Where("device_name = ?", "AC6508").First(&dev).Error; err != nil {
		t.Fatalf("query auto-created device failed: %v", err)
	}
	if dev.DeviceType != "AC6508" {
		t.Fatalf("device.DeviceType = %q, want %q (绝不写死 Router)", dev.DeviceType, "AC6508")
	}
	if b.deviceID != dev.ID {
		t.Fatalf("bundle.deviceID = %d, want %d", b.deviceID, dev.ID)
	}
}

// TestPrepareBundles_LegacyDeviceTypeRecovered 守护存量设备的上下文自愈：
// 旧版本把 device_type 写死为 "Router"，这批设备行不做人工订正也应能重新拿到
// 真实匹配上下文（型号归一为空 → 主机名嗅探接管），否则存量任务的版本分档
// 将永久停留在跨产品通用档（本方案 §9.1 的意图，此处以无副作用读路径实现）。
func TestPrepareBundles_LegacyDeviceTypeRecovered(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "bundle_legacy_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	const taskID = "bundle-legacy-type-test"
	taskDB, _, err := storage.GetOrCreateTaskDB(tmpDir, taskID)
	if err != nil {
		t.Fatalf("init task db failed: %v", err)
	}
	defer storage.ReleaseTaskDB(taskID)

	// 预置历史遗留设备：型号是形态分类，主机名才是真实型号信息
	legacy := model.Device{
		TaskID:     taskID,
		DeviceName: "AC6508",
		DeviceType: "Router",
		Hostname:   "AC6508",
	}
	if err := taskDB.Create(&legacy).Error; err != nil {
		t.Fatalf("seed legacy device failed: %v", err)
	}

	content := "Apr 15 2026 14:00:01 AC6508 %%01IFNET/4/IF_DOWN(l)[1]: Interface down.\n"
	items := []FileUploadItem{{
		FileName: "ac6508.log",
		FileSize: int64(len(content)),
		Content:  content,
	}}

	svc := &Service{}
	bundles, _, failed, err := svc.prepareBundles(
		context.Background(), taskID, 0, items, "overwrite", taskDB,
		map[string]model.TaskFile{}, map[string]bool{}, nil)
	if err != nil {
		t.Fatalf("prepareBundles failed: %v", err)
	}
	if len(failed) != 0 {
		t.Fatalf("unexpected failed files: %v", failed)
	}
	if len(bundles) != 1 {
		t.Fatalf("expected 1 bundle, got %d", len(bundles))
	}

	b := bundles[0]
	if b.targetProduct != "AC6508" {
		t.Fatalf("legacy device must recover real context, got targetProduct=%q, want %q", b.targetProduct, "AC6508")
	}
	if b.deviceID != legacy.ID {
		t.Fatalf("bundle.deviceID = %d, want existing legacy device %d (不得重复建设备)", b.deviceID, legacy.ID)
	}
}

package summary

import (
	"reflect"
	"testing"
)

func TestCaptureTemplateParams_FiveCases(t *testing.T) {
	// 报文 #1: active
	t.Run("Message #1 active - Positional Fallback due to misspelling", func(t *testing.T) {
		tpl := "FWD/4/hwEntityExtCpuUsageNotfication_active: The cpu usage exceeds the threshold value. (forwarding type = [hwCpuUsageTrapType], slot id = [hwCpuUsageTrapSlot], cpu id = [hwCpuUsageTrapCpu], current cpu usage = [hwCpuUsageCurrentUsage], threashold = [hwCpuUsageThreashold])"
		body := "CID=0x0-alarmID=0x00f103b4;The CPU usage exceeds the threshold value. (forwarding type=1, slot id=0, CPU id=0, current CPU usage=97, threshold=90)"

		params, prov := CaptureTemplateParamsWithProvenance(tpl, body)
		if prov != "positional" {
			t.Errorf("expected provenance 'positional', got '%s'", prov)
		}
		expected := map[string]string{
			"hwCpuUsageTrapType":     "1",
			"hwCpuUsageTrapSlot":     "0",
			"hwCpuUsageTrapCpu":      "0",
			"hwCpuUsageCurrentUsage": "97",
			"hwCpuUsageThreashold":   "90",
		}
		if !reflect.DeepEqual(params, expected) {
			t.Errorf("expected %v, got %v", expected, params)
		}
	})

	// 报文 #2: clear
	t.Run("Message #2 clear - Positional Fallback", func(t *testing.T) {
		tpl := "FWD/4/hwEntityExtCpuUsageNotfication_clear: The cpu usage fell below the threshold value. The lower threshold is 0.9 times the upper threshold. (forwarding type = [hwCpuUsageTrapType], slot id = [hwCpuUsageTrapSlot], cpu id = [hwCpuUsageTrapCpu], current cpu usage = [hwCpuUsageCurrentUsage], threashold = [hwCpuUsageThreashold])"
		body := "CID=0x0-alarmID=0x00f103b4-clearType=service_resume;The CPU usage fell below the threshold value. The lower threshold is 0.9 times the upper threshold. (forwarding type=1, slot id=0, CPU id=0, current CPU usage=55, threshold=81)"

		params, prov := CaptureTemplateParamsWithProvenance(tpl, body)
		if prov != "positional" {
			t.Errorf("expected provenance 'positional', got '%s'", prov)
		}
		expected := map[string]string{
			"hwCpuUsageTrapType":     "1",
			"hwCpuUsageTrapSlot":     "0",
			"hwCpuUsageTrapCpu":      "0",
			"hwCpuUsageCurrentUsage": "55",
			"hwCpuUsageThreashold":   "81",
		}
		if !reflect.DeepEqual(params, expected) {
			t.Errorf("expected %v, got %v", expected, params)
		}
	})

	// 报文 #3: DROP_LOG
	t.Run("Message #3 DROP_LOG - Strict Regex with space in value", func(t *testing.T) {
		tpl := "FWD/4/SYS_STAT_DROP_LOG: The forwarding engine detects packet loss. (Slot=[slotId], CPU=[cpuId], Drop reason=[dropReason], Drop count=[dropCount])"
		body := "Service=hppd[10177]0;The forwarding engine detects packet loss. (Slot=0, CPU=0, Drop reason=TTL exceed packets discarded, Drop count=28298)"

		params, prov := CaptureTemplateParamsWithProvenance(tpl, body)
		if prov != "regex" {
			t.Errorf("expected provenance 'regex', got '%s'", prov)
		}
		expected := map[string]string{
			"slotId":     "0",
			"cpuId":      "0",
			"dropReason": "TTL exceed packets discarded",
			"dropCount":  "28298",
		}
		if !reflect.DeepEqual(params, expected) {
			t.Errorf("expected %v, got %v", expected, params)
		}
	})

	// 报文 #4: SESSCTRLEND
	t.Run("Message #4 SESSCTRLEND - Strict Regex with hyphenated param keys", func(t *testing.T) {
		tpl := "FWD/4/SESSCTRLEND: Session creation control ended, SLOT [slot-id],CPU [cpu-id],The CPU usage was [cpu-usage]. In the process, [permitted-packets-num] packets were permitted and [blocked-packets-num] packets were blocked."
		body := "Service=hppd[10177]0;Session creation control ended, SLOT 0,CPU 0,The CPU usage was 100. In the process, 0 packets were permitted and 0 packets were blocked."

		params, prov := CaptureTemplateParamsWithProvenance(tpl, body)
		if prov != "regex" {
			t.Errorf("expected provenance 'regex', got '%s'", prov)
		}
		expected := map[string]string{
			"slot-id":               "0",
			"cpu-id":                "0",
			"cpu-usage":             "100",
			"permitted-packets-num": "0",
			"blocked-packets-num":   "0",
		}
		if !reflect.DeepEqual(params, expected) {
			t.Errorf("expected %v, got %v", expected, params)
		}
	})

	// 报文 #5: SuddenChange
	t.Run("Message #5 SuddenChange - Strict Regex with percent delimiters", func(t *testing.T) {
		tpl := "ENTEXT/4/hwEntityExtCpuUsageSuddenChangeNotification_active: The CPU usage on SPU [hwEntitySlotID] CPU [hwEntityCpuID] is suddenly changed from [hwEntityPreviousValue]% to [hwEntityCurrentValue]%, and the change value is [hwEntityChangeValue]%, exceeding threshold value [hwEntityChangeValueThreshold]%."
		body := "CID=0x814f042e-alarmID=0x00f10334;The CPU usage on SPU 0 CPU 0 is suddenly changed from 98% to 1%, and the change value is 97%, exceeding threshold value 40%."

		params, prov := CaptureTemplateParamsWithProvenance(tpl, body)
		if prov != "regex" {
			t.Errorf("expected provenance 'regex', got '%s'", prov)
		}
		expected := map[string]string{
			"hwEntitySlotID":               "0",
			"hwEntityCpuID":                "0",
			"hwEntityPreviousValue":        "98",
			"hwEntityCurrentValue":         "1",
			"hwEntityChangeValue":          "97",
			"hwEntityChangeValueThreshold": "40",
		}
		if !reflect.DeepEqual(params, expected) {
			t.Errorf("expected %v, got %v", expected, params)
		}
	})
}

func TestCaptureTemplateParams_PositionalEdgeCases(t *testing.T) {
	// 使用带错拼词 threashold 的模板，使得一级严格正则必然失配，专门测试二级位置映射的降级与拒绝机制
	tpl := "FWD/4/TEST: Alert (slot id = [slotId], threashold = [cpuUsage])"

	t.Run("Item count mismatch", func(t *testing.T) {
		body := "Alert (slot id=0)"
		res := CaptureTemplateParams(tpl, body)
		if len(res) != 0 {
			t.Errorf("expected empty result for count mismatch, got %v", res)
		}
	})

	t.Run("Words mismatch", func(t *testing.T) {
		body := "Alert (interface=GigabitEthernet0/0/1, threshold=50)"
		res := CaptureTemplateParams(tpl, body)
		if len(res) != 0 {
			t.Errorf("expected empty result for words mismatch, got %v", res)
		}
	})

	t.Run("Edit distance > 1", func(t *testing.T) {
		// slot vs slxx (dist 2)
		body := "Alert (slxx id=0, threshold=50)"
		res := CaptureTemplateParams(tpl, body)
		if len(res) != 0 {
			t.Errorf("expected empty result for edit distance > 1, got %v", res)
		}
	})

	t.Run("Numeric weak validation failure", func(t *testing.T) {
		// threashold/threshold 暗示数值，传入非数值应拒绝位置映射
		body := "Alert (slot id=0, threshold=invalid-usage)"
		res := CaptureTemplateParams(tpl, body)
		if len(res) != 0 {
			t.Errorf("expected empty result for non-numeric usage, got %v", res)
		}
	})

	t.Run("Empty or punctuation only value", func(t *testing.T) {
		body := "Alert (slot id=..., threshold=90)"
		res := CaptureTemplateParams(tpl, body)
		if len(res) != 0 {
			t.Errorf("expected empty result for pure punctuation value, got %v", res)
		}
	})
}

func TestCaptureTemplateParams_Cache(t *testing.T) {
	tpl := "FWD/4/SYS_STAT_DROP_LOG: The forwarding engine detects packet loss. (Slot=[slotId], CPU=[cpuId], Drop reason=[dropReason], Drop count=[dropCount])"
	body1 := "The forwarding engine detects packet loss. (Slot=1, CPU=2, Drop reason=buffer full, Drop count=100)"
	body2 := "The forwarding engine detects packet loss. (Slot=3, CPU=4, Drop reason=timeout, Drop count=200)"

	res1 := CaptureTemplateParams(tpl, body1)
	if res1["slotId"] != "1" || res1["dropReason"] != "buffer full" {
		t.Fatalf("unexpected res1: %v", res1)
	}

	res2 := CaptureTemplateParams(tpl, body2)
	if res2["slotId"] != "3" || res2["dropReason"] != "timeout" {
		t.Fatalf("unexpected res2: %v", res2)
	}
}

func TestCaptureTemplateParams_AuditB1_TrailingPlaceholder(t *testing.T) {
	t.Run("Trailing placeholder in multi-variable template", func(t *testing.T) {
		tpl := "X/1/Y: A [a] B [b]"
		body := "A 1 B 2"
		res := CaptureTemplateParams(tpl, body)
		if res["a"] != "1" || res["b"] != "2" {
			t.Fatalf("expected {a:1, b:2}, got %v", res)
		}
	})

	t.Run("Single trailing placeholder in entire template", func(t *testing.T) {
		tpl := "X/1/Y: [a]"
		body := "hello"
		res := CaptureTemplateParams(tpl, body)
		if res["a"] != "hello" {
			t.Fatalf("expected {a:hello}, got %v", res)
		}
	})

	t.Run("Common network state trailing placeholder", func(t *testing.T) {
		tpl := "BGP/6/STATE: The BGP peer 192.168.1.1 changed to [state]"
		body := "The BGP peer 192.168.1.1 changed to Down"
		res := CaptureTemplateParams(tpl, body)
		if res["state"] != "Down" {
			t.Fatalf("expected {state:Down}, got %v", res)
		}
	})
}

func TestCaptureTemplateParams_AuditB2_HeaderWithL(t *testing.T) {
	// 带 (l) 级别的模板，在二级位置映射下成功提取
	tpl := "FWD/4/hwCpuOver(l): The cpu usage is high. (slot id = [slotId], current cpu usage = [usage])"
	body := "The cpu usage is high. (slot id=3, current cpu usage=90)"
	res := CaptureTemplateParams(tpl, body)
	if res["slotId"] != "3" || res["usage"] != "90" {
		t.Fatalf("expected {slotId:3, usage:90}, got %v", res)
	}
}

func TestCaptureTemplateParams_AuditB5_EqualSignTolerance(t *testing.T) {
	// 模板等号两侧有空格，正文等号无空格，一级正则必须容差命中
	tpl := "FWD/4/TEST: Alert (slot id = [slotId])"
	body := "Alert (slot id=0)"
	params, prov := CaptureTemplateParamsWithProvenance(tpl, body)
	if prov != "regex" {
		t.Errorf("expected provenance 'regex', got '%s'", prov)
	}
	if params["slotId"] != "0" {
		t.Errorf("expected slotId:0, got %v", params)
	}
}

func TestCaptureTemplateParams_AuditB10_DuplicateParamMerge(t *testing.T) {
	tpl := "IFNET/4/PORT: Port [port] to [port] changed"
	body := "Port 1 to 2 changed"
	res := CaptureTemplateParams(tpl, body)
	if res["port"] != `["1","2"]` {
		t.Errorf("expected JSON array [\"1\",\"2\"], got %s", res["port"])
	}
}

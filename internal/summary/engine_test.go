package summary_test

import (
	"strings"
	"testing"

	"logauditorgo/internal/model"
	"logauditorgo/internal/summary"
)

func TestKnowledgeDrivenSummary(t *testing.T) {
	// 场景 1: 命中知识库，包含官方中文 Description 和官方 Message 模板
	kb1 := &model.Knowledge{
		Module:      "EVPN",
		Brief:       "hwEvpnMacDupVpnAlarm_active",
		Message:     "MAC addresses were suppressed in an EVPN instance [EVPNInstanceName] due to frequent MAC duplication.",
		Description: "该告警表示在EVPN实例中因为频繁发生MAC地址重复，导致该MAC地址被抑制。可能是网络中存在二层环路导致。",
	}
	params1 := map[string]string{
		"EVPNInstanceName": "9",
		"MAC":              "02bf-0a09-09fa",
		"InterfaceName1":   "Eth-Trunk9.25",
		"IPAddress1":       "10.0.4.188",
	}

	summary1 := summary.GenerateSummary("EVPN", "hwEvpnMacDupVpnAlarm_active", 2, "", params1, kb1)
	t.Logf("Generated Summary 1: %s", summary1)
	if !strings.Contains(summary1, "EVPN实例中因为频繁发生MAC地址重复") {
		t.Errorf("expected official description core title, got: %s", summary1)
	}
	// RCA-15: 关键参数按"原因 > 接口 > 对端 > 其他"的诊断优先级截断，
	// 因此本例中 接口 / IP / 实例 这三个高价值字段必须全部保留。
	if !strings.Contains(summary1, "接口: Eth-Trunk9.25") || !strings.Contains(summary1, "IP: 10.0.4.188") {
		t.Errorf("expected high-priority core parameters (接口/IP) to survive truncation, got: %s", summary1)
	}

	// RCA-15 核心回归：参数超过 3 个时，"原因"必须被保留（旧实现会最先砍掉它）
	paramsWithReason := map[string]string{
		"Reason":         "Loss of signal",
		"InterfaceName1": "GE1/0/1",
		"MAC":            "02bf-0a09-09fa",
		"TrunkName":      "Eth-Trunk1",
		"State":          "Down",
	}
	summaryReason := summary.ExtractCoreContextParams(summary.BuildNormalizedMap(paramsWithReason))
	if !strings.Contains(summaryReason, "原因: Loss of signal") {
		t.Errorf("expected 原因 to survive truncation (RCA-15), got: %s", summaryReason)
	}
	if !strings.Contains(summaryReason, "接口: GE1/0/1") {
		t.Errorf("expected 接口 to survive truncation (RCA-15), got: %s", summaryReason)
	}

	// 场景 2: 命中知识库，但只有官方英文 Message 模板 (如某些无中文说明的老版本日志)
	kb2 := &model.Knowledge{
		Module:  "BGP",
		Brief:   "BGP_STATE_BACKWARD",
		Message: "The BGP peer [bgpPeerRemoteAddr] changed state to Down because [reason].",
	}
	params2 := map[string]string{
		"bgpPeerRemoteAddr": "192.168.10.1",
		"reason":            "HoldTimer expired",
	}

	summary2 := summary.GenerateSummary("BGP", "BGP_STATE_BACKWARD", 2, "", params2, kb2)
	t.Logf("Generated Summary 2: %s", summary2)
	if !strings.Contains(summary2, "192.168.10.1") || !strings.Contains(summary2, "HoldTimer expired") {
		t.Errorf("expected template rendering, got: %s", summary2)
	}

	// 场景 3: 未匹配知识库 (kb == nil)，通用自适应语义提取 (CLI 场景)
	params3 := map[string]string{
		"Task":    "VTY1",
		"Ip":      "10.17.11.26",
		"User":    "admin",
		"Command": "display clock",
	}
	summary3 := summary.GenerateSummary("CLI", "CMDRECORD", 5, "", params3, nil)
	t.Logf("Generated Summary 3: %s", summary3)
	if !strings.Contains(summary3, "admin") || !strings.Contains(summary3, "display clock") {
		t.Errorf("expected adaptive user command extraction, got: %s", summary3)
	}

	// 场景 4: 未匹配知识库 (kb == nil)，通用自适应接口与原因提取
	params4 := map[string]string{
		"InterfaceName": "GE1/0/1",
		"Reason":        "Loss of signal",
	}
	summary4 := summary.GenerateSummary("CUSTOM_MOD", "PORT_ALERT", 3, "", params4, nil)
	t.Logf("Generated Summary 4: %s", summary4)
	if !strings.Contains(summary4, "接口: GE1/0/1") || !strings.Contains(summary4, "原因: Loss of signal") {
		t.Errorf("expected generic adaptive extraction, got: %s", summary4)
	}

	// 场景 5: 完全无参数无知识库，原始报文截断兜底
	summary5 := summary.GenerateSummary("UNKNOWN", "EVENT", 4, "System initialized successfully without parameters.", nil, nil)
	t.Logf("Generated Summary 5: %s", summary5)
	if !strings.Contains(summary5, "System initialized successfully") {
		t.Errorf("expected raw message fallback, got: %s", summary5)
	}
}

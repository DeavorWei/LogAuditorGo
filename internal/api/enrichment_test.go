package api

import (
	"encoding/json"
	"testing"

	"logauditorgo/internal/model"
)

func TestEnrichParameters(t *testing.T) {
	kb := &model.Knowledge{
		Parameters: `[
			{"name": "PeerID", "description": "BGP对等体IP地址"},
			{"name": "Interface_Name", "description": "物理或逻辑接口名称"},
			{"name": "vpn-instance", "description": "VPN实例名称"}
		]`,
	}

	params := map[string]string{
		"PeerID":        "192.168.1.1",
		"interface_name": "GE1/0/1",
		"VpnInstance":   "vpn_test",
		"UnknownParam":  "12345",
	}
	paramsJSON, _ := json.Marshal(params)

	enriched := EnrichParameters(string(paramsJSON), kb)
	if len(enriched) != 4 {
		t.Fatalf("expected 4 enriched parameters, got %d", len(enriched))
	}

	lookup := make(map[string]EnrichedParameter)
	for _, p := range enriched {
		lookup[p.Name] = p
	}

	// 1. 精确匹配
	p1, ok := lookup["PeerID"]
	if !ok || !p1.Matched || p1.Description != "BGP对等体IP地址" || p1.Value != "192.168.1.1" {
		t.Errorf("PeerID match failed: %+v", p1)
	}

	// 2. 忽略大小写匹配 (interface_name vs Interface_Name)
	p2, ok := lookup["interface_name"]
	if !ok || !p2.Matched || p2.Description != "物理或逻辑接口名称" || p2.Value != "GE1/0/1" {
		t.Errorf("interface_name match failed: %+v", p2)
	}

	// 3. 规范化去除短横线/下划线匹配 (VpnInstance vs vpn-instance)
	p3, ok := lookup["VpnInstance"]
	if !ok || !p3.Matched || p3.Description != "VPN实例名称" || p3.Value != "vpn_test" {
		t.Errorf("VpnInstance match failed: %+v", p3)
	}

	// 4. 未匹配项降级
	p4, ok := lookup["UnknownParam"]
	if !ok || p4.Matched || p4.Description != "" || p4.Value != "12345" {
		t.Errorf("UnknownParam should be unmatched: %+v", p4)
	}
}

func TestRenderMessageTemplate(t *testing.T) {
	tpl := "The BGP peer [PeerID] of interface <Interface_Name> in vpn %vpn-instance% entered state {State}."
	params := map[string]string{
		"PeerID":        "10.0.0.1",
		"interface_name": "100GE1/0/1",
		"VpnInstance":   "default_vpn",
		"State":         "Established",
	}

	rendered := RenderMessageTemplate(tpl, params)
	expected := "The BGP peer 10.0.0.1 of interface 100GE1/0/1 in vpn default_vpn entered state Established."
	if rendered != expected {
		t.Errorf("RenderMessageTemplate mismatch:\ngot:      %s\nexpected: %s", rendered, expected)
	}
}

func TestContextualizeText(t *testing.T) {
	actionText := "请执行 display bgp peer [PeerID] 检查对等体 [PeerID] 状态，并确认接口 [Interface_Name] 物理连接正常。"
	params := map[string]string{
		"PeerID":        "172.16.0.2",
		"interface_name": "GE2/0/0",
	}

	rendered := ContextualizeText(actionText, params)
	expected := "请执行 display bgp peer 172.16.0.2 检查对等体 172.16.0.2 状态，并确认接口 GE2/0/0 物理连接正常。"
	if rendered != expected {
		t.Errorf("ContextualizeText mismatch:\ngot:      %s\nexpected: %s", rendered, expected)
	}
}

func TestContextualizeKnowledge(t *testing.T) {
	kb := &model.Knowledge{
		Description: "BGP对等体 [PeerID] 认证失败",
		Cause:       "接口 [Interface_Name] 上的密码配置不匹配",
		Action:      "登录设备检查对等体 [PeerID] 的认证密钥",
		Impact:      "与对等体 [PeerID] 的邻居关系无法建立",
	}
	paramsJSON := `{"PeerID":"10.1.1.1","Interface_Name":"100GE1/0/1"}`

	ctxKB := ContextualizeKnowledge(kb, paramsJSON)
	if ctxKB == nil {
		t.Fatal("expected non-nil ContextualizedKnowledge")
	}

	if ctxKB.Description != "BGP对等体 10.1.1.1 认证失败" {
		t.Errorf("Description contextualization mismatch: %s", ctxKB.Description)
	}
	if ctxKB.Cause != "接口 100GE1/0/1 上的密码配置不匹配" {
		t.Errorf("Cause contextualization mismatch: %s", ctxKB.Cause)
	}
	if ctxKB.Action != "登录设备检查对等体 10.1.1.1 的认证密钥" {
		t.Errorf("Action contextualization mismatch: %s", ctxKB.Action)
	}
	if ctxKB.Impact != "与对等体 10.1.1.1 的邻居关系无法建立" {
		t.Errorf("Impact contextualization mismatch: %s", ctxKB.Impact)
	}
}


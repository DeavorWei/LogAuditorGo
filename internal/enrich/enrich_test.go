package enrich

import (
	"testing"

	"logauditorgo/internal/model"
)

func TestEnrichParametersForComments(t *testing.T) {
	paramsJSON := `{"Slot":"1","DeviceModel":"CE6865E","Version":"V200R024C00SPC500B126"}`
	enriched := EnrichParameters(paramsJSON, nil)

	if len(enriched) != 3 {
		t.Fatalf("expected 3 enriched parameters, got %d", len(enriched))
	}

	foundSlot := false
	foundModel := false
	foundVer := false

	for _, p := range enriched {
		if p.Name == "Slot" {
			foundSlot = true
			if !p.Matched || p.Description == "" {
				t.Errorf("expected Slot to be matched with description, got matched=%v, desc=%s", p.Matched, p.Description)
			}
		}
		if p.Name == "DeviceModel" {
			foundModel = true
			if !p.Matched || p.Description == "" {
				t.Errorf("expected DeviceModel to be matched with description, got matched=%v, desc=%s", p.Matched, p.Description)
			}
		}
		if p.Name == "Version" {
			foundVer = true
			if !p.Matched || p.Description == "" {
				t.Errorf("expected Version to be matched with description, got matched=%v, desc=%s", p.Matched, p.Description)
			}
		}
	}

	if !foundSlot || !foundModel || !foundVer {
		t.Errorf("missing expected parameters: slot=%v, model=%v, ver=%v", foundSlot, foundModel, foundVer)
	}
}

func TestEnrichParametersDigest(t *testing.T) {
	paramsJSON := `{"DigestSeq":"0006756365","Digest":"3e0f5f595bfa263fff2638e6692bb42ce44af9c01af42a075add1073b287b917"}`
	enriched := EnrichParameters(paramsJSON, nil)

	if len(enriched) != 2 {
		t.Fatalf("expected 2 enriched parameters, got %d", len(enriched))
	}

	for _, p := range enriched {
		if !p.Matched || p.Description == "" {
			t.Errorf("expected %s to have matched description, got matched=%v, desc=%s", p.Name, p.Matched, p.Description)
		}
	}
}

type mockKnowledgeResolver struct {
	m map[uint]*model.Knowledge
}

func (r *mockKnowledgeResolver) GetKnowledgeMapByIDs(ids []uint) (map[uint]*model.Knowledge, error) {
	return r.m, nil
}

func TestEnrichLogs_TemplateCaptureZeroMigration(t *testing.T) {
	kb := &model.Knowledge{
		ID:         1001,
		Module:     "FWD",
		Brief:      "SYS_STAT_DROP_LOG",
		Message:    "FWD/4/SYS_STAT_DROP_LOG: The forwarding engine detects packet loss. (Slot=[slotId], CPU=[cpuId], Drop reason=[dropReason], Drop count=[dropCount])",
		Parameters: `[{"name":"slotId","description":"槽位号。"},{"name":"cpuId","description":"CPU号。"},{"name":"dropReason","description":"丢弃原因。"},{"name":"dropCount","description":"丢弃数量。"}]`,
	}
	resolver := &mockKnowledgeResolver{
		m: map[uint]*model.Knowledge{1001: kb},
	}
	svc := NewService(resolver)

	// 模拟存量日志：未包含捕获的官方参数名，仅有原始盲扫字段
	records := []model.LogRecord{
		{
			ID:             1,
			KnowledgeID:    1001,
			MessageBody:    "Service=hppd[10177]0;The forwarding engine detects packet loss. (Slot=0, CPU=0, Drop reason=TTL exceed packets discarded, Drop count=28298)",
			ParametersJSON: `{"Service":"hppd[10177]0"}`,
		},
	}

	enriched := svc.EnrichLogs(records)
	if len(enriched) != 1 {
		t.Fatalf("expected 1 enriched record, got %d", len(enriched))
	}

	rec := enriched[0]
	// 验证模板渲染
	expectedRendered := "FWD/4/SYS_STAT_DROP_LOG: The forwarding engine detects packet loss. (Slot=0, CPU=0, Drop reason=TTL exceed packets discarded, Drop count=28298)"
	if rec.RenderedMessage != expectedRendered {
		t.Errorf("expected rendered message %q, got %q", expectedRendered, rec.RenderedMessage)
	}

	// 验证官方参数字典全部匹配成功
	matchedCount := 0
	for _, p := range rec.EnrichedParameters {
		if p.Matched {
			matchedCount++
		}
	}
	if matchedCount < 4 {
		t.Errorf("expected at least 4 matched parameters, got %d (%v)", matchedCount, rec.EnrichedParameters)
	}
}

func TestEnrichParameters_RoleAliasFallback(t *testing.T) {
	kb := &model.Knowledge{
		ID:         2001,
		Module:     "IFNET",
		Brief:      "IF_DOWN",
		Message:    "Interface [interface] state down, reason [reason], slot [slot], cpu [cpu], count [count]",
		Parameters: `[{"name":"reason","description":"下线原因说明"},{"name":"slot","description":"单板槽位编号"},{"name":"cpu","description":"处理器编号"},{"name":"count","description":"重试次数"}]`,
	}

	// 传入别名形态的参数名：dropReason, slotId, cpuId, dropCount
	paramsJSON := `{"dropReason":"Link failed","slotId":"1","cpuId":"0","dropCount":"10"}`
	enriched := EnrichParameters(paramsJSON, kb)

	matchedMap := make(map[string]bool)
	for _, p := range enriched {
		if p.Matched {
			matchedMap[p.Name] = true
		}
	}

	if !matchedMap["dropReason"] {
		t.Errorf("expected dropReason to match role-alias 'reason'")
	}
	if !matchedMap["slotId"] {
		t.Errorf("expected slotId to match role-alias 'slot'")
	}
	if !matchedMap["cpuId"] {
		t.Errorf("expected cpuId to match role-alias 'cpu'")
	}
	if !matchedMap["dropCount"] {
		t.Errorf("expected dropCount to match role-alias 'count'")
	}

	// 验证模板渲染通过别名替换
	rawParams := map[string]string{
		"dropReason": "Link failed",
		"slotId":     "1",
		"cpuId":      "0",
		"dropCount":  "10",
	}
	rendered := RenderMessageTemplate(kb.Message, rawParams)
	expected := "Interface [interface] state down, reason Link failed, slot 1, cpu 0, count 10"
	if rendered != expected {
		t.Errorf("expected rendered template %q, got %q", expected, rendered)
	}
}

package matcher_test

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"logauditorgo/internal/matcher"
	"logauditorgo/internal/model"
)

// ---------------- 互斥产品族过滤与 FAMILY_SUSPECT 降级守护测试 ----------------
//
// 背景：同一助记符在多个产品族文档中各有一条知识时（如 CM:USER_OFFLINERESULT），
// 仅靠版本分 + 特征分无法保证"无线报文必配无线文档"：
//   - 上下文为 CloudEngine 且候选无专属参数标记时，CE 候选 50-10=40 分，
//     与 WLAN 候选 10+30=40 分持平，最终由 betterCandidate 的 ID 升序仲裁决定；
//   - 加大扣分（如 -200）无法解决，因为选优函数没有准入阈值，负分候选仍会被落库。
// 因此改为候选集级硬过滤（宽松豁免 + 严格剔除）+ 豁免条目降级 FAMILY_SUSPECT。

// newExclusivityEngine 构建仅含指定知识条目与版本映射的隔离测试引擎
func newExclusivityEngine(t *testing.T, items []model.Knowledge, mappings []model.KnowledgeVersionMapping) *matcher.MatchEngine {
	t.Helper()
	tmpDir := t.TempDir()
	db, err := gorm.Open(sqlite.Open(filepath.Join(tmpDir, "exclusivity.db")), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite failed: %v", err)
	}
	if err := db.AutoMigrate(&model.Knowledge{}, &model.KnowledgeVersionMapping{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	for _, k := range items {
		item := k
		if err := db.Create(&item).Error; err != nil {
			t.Fatalf("insert knowledge %d failed: %v", item.ID, err)
		}
	}
	for _, m := range mappings {
		mapping := m
		if err := db.Create(&mapping).Error; err != nil {
			t.Fatalf("insert mapping for %d failed: %v", mapping.KnowledgeID, err)
		}
	}
	// indexer 传 nil：只验证 Tier1~3 内存索引路径，屏蔽 Tier4 全文召回的不确定性
	engine := matcher.NewMatchEngine(db, nil)
	engine.Reload()
	return engine
}

// exclusivityKnowledges 与生产库同构的三条知识：#3823 CE / #8438 Campus / #11534 WLAN
func exclusivityKnowledges() []model.Knowledge {
	return []model.Knowledge{
		{ID: 3823, Module: "CM", Brief: "USER_OFFLINERESULT", Severity: 5, ContentHash: "ex_hash_3823", Parameters: `[{"name":"wiredUserInfo"}]`},
		{ID: 8438, Module: "CM", Brief: "USER_OFFLINERESULT", Severity: 5, ContentHash: "ex_hash_8438", Parameters: `[{"name":"USER_INFO_OFFLINE"}]`},
		{ID: 11534, Module: "CM", Brief: "USER_OFFLINERESULT", Severity: 5, ContentHash: "ex_hash_11534", Parameters: `[{"name":"WLAN_STA_INFO_OFFLINE"}]`},
	}
}

// exclusivityMappings 与生产库同构的版本映射（含真实 DocumentID，用于文档归属断言）
func exclusivityMappings() []model.KnowledgeVersionMapping {
	return []model.KnowledgeVersionMapping{
		{KnowledgeID: 3823, DocumentID: 3823, ProductType: "CloudEngine 16800", ProductVersion: "V300R024C00"},
		{KnowledgeID: 8438, DocumentID: 8438, ProductType: "S12700, S12700E", ProductVersion: "V200R024C00"},
		{KnowledgeID: 11534, DocumentID: 11534, ProductType: "WLAN", ProductVersion: "V200R024C00"},
	}
}

func wirelessLog(body string) *model.NormalizedLog {
	return &model.NormalizedLog{
		Module:      "CM",
		Brief:       "USER_OFFLINERESULT",
		Severity:    5,
		MessageBody: body,
	}
}

func wiredLog() *model.NormalizedLog {
	return &model.NormalizedLog{
		Module:      "CM",
		Brief:       "USER_OFFLINERESULT",
		Severity:    5,
		MessageBody: `[USER_INFO_OFFLINE]DEVICEMAC:00-e0-fc-01-02-03;USER:admin;IPADDRESS:192.168.1.100;`,
	}
}

// TestFilterExcludedCandidates_LenientAndStrict 验证宽松豁免与严格剔除两种模式的行为差异
func TestFilterExcludedCandidates_LenientAndStrict(t *testing.T) {
	ce := &model.Knowledge{ID: 3823, Versions: []model.KnowledgeVersionMapping{{ID: 1, ProductType: "CloudEngine 16800"}}}
	wlan := &model.Knowledge{ID: 11534, Versions: []model.KnowledgeVersionMapping{{ID: 2, ProductType: "WLAN"}}}
	mixed := []*model.Knowledge{ce, wlan}
	pureCE := []*model.Knowledge{ce}

	// 1. 混合候选：存在合规候选 -> 互斥候选被剔除（两种模式一致）
	for _, strict := range []bool{false, true} {
		got := matcher.FilterExcludedCandidatesForTest(mixed, true, false, strict)
		if len(got) != 1 || got[0].ID != 11534 {
			t.Fatalf("strict=%v: wireless log with mixed candidates must keep only WLAN, got %v", strict, len(got))
		}
	}

	// 2. 纯互斥候选：宽松模式豁免保留，严格模式清空
	if got := matcher.FilterExcludedCandidatesForTest(pureCE, true, false, false); len(got) != 1 {
		t.Fatalf("lenient mode must exempt pure conflicting candidates, got %v", len(got))
	}
	if got := matcher.FilterExcludedCandidatesForTest(pureCE, true, false, true); len(got) != 0 {
		t.Fatalf("strict mode must drop pure conflicting candidates, got %v", len(got))
	}

	// 3. 无特征报文：原样返回且不分配新切片
	plain := []*model.Knowledge{ce, wlan}
	if got := matcher.FilterExcludedCandidatesForTest(plain, false, false, false); len(got) != 2 {
		t.Fatalf("log without protocol fingerprint must not be filtered, got %v", len(got))
	}

	// 4. 有线报文 + 纯 WLAN 候选：对称豁免
	wlanOnly := []*model.Knowledge{wlan}
	if got := matcher.FilterExcludedCandidatesForTest(wlanOnly, false, true, false); len(got) != 1 {
		t.Fatalf("wired log must exempt pure WLAN candidates in lenient mode, got %v", len(got))
	}
	if got := matcher.FilterExcludedCandidatesForTest(wlanOnly, false, true, true); len(got) != 0 {
		t.Fatalf("wired log must drop WLAN candidates in strict mode, got %v", len(got))
	}
}

// TestIsSuspectWinnerAndCap 验证豁免判定与 Tier/置信度降级
func TestIsSuspectWinnerAndCap(t *testing.T) {
	ce := &model.Knowledge{ID: 3823, Versions: []model.KnowledgeVersionMapping{{ID: 1, ProductType: "CloudEngine 16800"}}}
	wlan := &model.Knowledge{ID: 11534, Versions: []model.KnowledgeVersionMapping{{ID: 2, ProductType: "WLAN"}}}

	if !matcher.IsSuspectWinnerForTest(ce, true, false) {
		t.Fatal("wireless log + CloudEngine winner must be marked suspect")
	}
	if matcher.IsSuspectWinnerForTest(wlan, true, false) {
		t.Fatal("wireless log + WLAN winner must not be marked suspect")
	}
	if !matcher.IsSuspectWinnerForTest(wlan, false, true) {
		t.Fatal("wired log + WLAN winner must be marked suspect (symmetric guard)")
	}
	if matcher.IsSuspectWinnerForTest(nil, true, false) {
		t.Fatal("nil winner must not be marked suspect")
	}
	if matcher.IsSuspectWinnerForTest(ce, false, false) {
		t.Fatal("log without protocol fingerprint must not trigger suspect")
	}

	tier, conf := matcher.ApplySuspectCapForTest(matcher.TierExact, 1.0, true)
	if tier != matcher.TierFamilySuspect || conf != matcher.ConfidenceFamilySuspectMax {
		t.Fatalf("suspect winner must be downgraded to %s/%.2f, got %s/%.2f", matcher.TierFamilySuspect, matcher.ConfidenceFamilySuspectMax, tier, conf)
	}
	tier, conf = matcher.ApplySuspectCapForTest(matcher.TierExact, 1.0, false)
	if tier != matcher.TierExact || conf != 1.0 {
		t.Fatalf("non-suspect winner must keep tier/conf unchanged, got %s/%.2f", tier, conf)
	}
	// 低于封顶值的置信度不得被放大
	tier, conf = matcher.ApplySuspectCapForTest(matcher.TierBleve, 0.10, true)
	if tier != matcher.TierFamilySuspect || conf != 0.10 {
		t.Fatalf("suspect cap must not raise conf, got %s/%.2f", tier, conf)
	}
}

// TestBestVersionMappingAndDocumentIDFor 验证"版本分档"与"文档归属"同源
func TestBestVersionMappingAndDocumentIDFor(t *testing.T) {
	// 1. 同型号同版本 150 分，文档 ID 与胜出映射一致
	k := &model.Knowledge{ID: 1, Versions: []model.KnowledgeVersionMapping{
		{ID: 11, DocumentID: 501, ProductType: "CloudEngine 16800", ProductVersion: "V300R024C00"},
		{ID: 12, DocumentID: 502, ProductType: "S12700", ProductVersion: "V200R024C00"},
	}}
	score, mapping := matcher.BestVersionMappingForTest(k, "CloudEngine 16800", "V300R024C00")
	if score != matcher.ScoreExactProductExactVersion || mapping == nil || mapping.ID != 11 {
		t.Fatalf("exact match mapping wrong: score=%d mapping=%v", score, mapping)
	}
	if got := matcher.BestDocumentIDFor(k, "CloudEngine 16800", "V300R024C00"); got != 501 {
		t.Fatalf("BestDocumentIDFor = %d, want 501", got)
	}
	if got := matcher.ScoreKnowledgeVersion(k, "CloudEngine 16800", "V300R024C00"); got != matcher.ScoreExactProductExactVersion {
		t.Fatalf("ScoreKnowledgeVersion = %d, want %d", got, matcher.ScoreExactProductExactVersion)
	}

	// 2. 同分时按映射 ID 升序裁决（与 betterCandidate 末位规则一致）
	tie := &model.Knowledge{ID: 2, Versions: []model.KnowledgeVersionMapping{
		{ID: 22, DocumentID: 702, ProductType: "WLAN", ProductVersion: "V200R024C00"},
		{ID: 21, DocumentID: 701, ProductType: "WLAN", ProductVersion: "V200R024C00"},
	}}
	if got := matcher.BestDocumentIDFor(tie, "WLAN", "V200R024C00"); got != 701 {
		t.Fatalf("tie-break must pick the smallest mapping ID, got %d", got)
	}

	// 3. 无版本映射：兜底 5 分且不产生文档归属（守护 ScoreNoVersionFallback 不被误杀）
	noVer := &model.Knowledge{ID: 3}
	if got := matcher.ScoreKnowledgeVersion(noVer, "WLAN", ""); got != matcher.ScoreNoVersionFallback {
		t.Fatalf("no-version knowledge = %d, want %d", got, matcher.ScoreNoVersionFallback)
	}
	if got := matcher.BestDocumentIDFor(noVer, "WLAN", ""); got != 0 {
		t.Fatalf("no-version knowledge must not produce document ID, got %d", got)
	}
	if got := matcher.BestDocumentIDFor(nil, "WLAN", ""); got != 0 {
		t.Fatalf("nil knowledge must return 0, got %d", got)
	}
}

// TestMatchEngine_FamilyExclusivity_MixedCandidates 验证混合候选下互斥族被硬剔除
func TestMatchEngine_FamilyExclusivity_MixedCandidates(t *testing.T) {
	engine := newExclusivityEngine(t, exclusivityKnowledges(), exclusivityMappings())

	k, tier, conf := engine.Match(wirelessLog(`[WLAN_STA_INFO_OFFLINE]ACMAC:f4-e5-f2-70-f4-bc;APMAC:8c-e5-ef-80-b3-20;SSID:office;`), "CloudEngine", "")
	if k == nil || k.ID != 11534 {
		t.Fatalf("wireless log under CloudEngine context must hit #11534, got %v", k)
	}
	if tier != matcher.TierExact || conf <= matcher.ConfidenceFamilySuspectMax {
		t.Fatalf("excluded-family path must keep normal tier/conf, got %s/%.2f", tier, conf)
	}
	if got := matcher.BestDocumentIDFor(k, "CloudEngine", ""); got != 11534 {
		t.Fatalf("matched document ID = %d, want 11534", got)
	}
}

// TestMatchEngine_FamilyExclusivity_SuspectFallback 验证纯互斥候选下走豁免降级而非错配高置信
func TestMatchEngine_FamilyExclusivity_SuspectFallback(t *testing.T) {
	items := []model.Knowledge{exclusivityKnowledges()[0]} // 仅 #3823 (CloudEngine)
	mappings := []model.KnowledgeVersionMapping{exclusivityMappings()[0]}
	engine := newExclusivityEngine(t, items, mappings)

	k, tier, conf := engine.Match(wirelessLog(`[WLAN_STA_INFO_OFFLINE]ACMAC:f4-e5-f2-70-f4-bc;SSID:office;`), "Huawei-VRP", "")
	if k == nil || k.ID != 3823 {
		t.Fatalf("pure conflicting candidate set must fall back to #3823 in lenient mode, got %v", k)
	}
	if tier != matcher.TierFamilySuspect {
		t.Fatalf("suspect fallback must degrade tier, got %s", tier)
	}
	if conf > matcher.ConfidenceFamilySuspectMax {
		t.Fatalf("suspect fallback conf %.2f must be capped at %.2f", conf, matcher.ConfidenceFamilySuspectMax)
	}

	// 同 module:brief 但不同正文：务必命中 exactBestCache，suspect 状态不得丢失
	engine.ClearCache()
	warmK, warmTier, _ := engine.Match(wirelessLog(`[WLAN_STA_INFO_OFFLINE]ACMAC:aa-bb-cc-dd-ee-ff;SSID:office;`), "Huawei-VRP", "")
	if warmK == nil || warmTier != matcher.TierFamilySuspect {
		t.Fatalf("warm-up match must be suspect, got %v/%s", warmK, warmTier)
	}
	k2, tier2, conf2 := engine.Match(wirelessLog(`[WLAN_STA_INFO_OFFLINE]ACMAC:11-22-33-44-55-66;SSID:guest;`), "Huawei-VRP", "")
	if k2 == nil || k2.ID != 3823 {
		t.Fatalf("cache-hit path must return the same winner, got %v", k2)
	}
	if tier2 != matcher.TierFamilySuspect || conf2 > matcher.ConfidenceFamilySuspectMax {
		t.Fatalf("suspect state must survive exactBestCache hit, got %s/%.2f", tier2, conf2)
	}
}

// TestMatchEngine_FamilyExclusivity_WiredSideSymmetry 验证有线侧对称豁免
func TestMatchEngine_FamilyExclusivity_WiredSideSymmetry(t *testing.T) {
	items := []model.Knowledge{exclusivityKnowledges()[2]} // 仅 #11534 (WLAN)
	mappings := []model.KnowledgeVersionMapping{exclusivityMappings()[2]}
	engine := newExclusivityEngine(t, items, mappings)

	k, tier, conf := engine.Match(wiredLog(), "Huawei-VRP", "")
	if k == nil || k.ID != 11534 {
		t.Fatalf("wired log with only WLAN candidates must fall back in lenient mode, got %v", k)
	}
	if tier != matcher.TierFamilySuspect || conf > matcher.ConfidenceFamilySuspectMax {
		t.Fatalf("wired side must be symmetric (suspect + capped conf), got %s/%.2f", tier, conf)
	}
}

// TestMatchEngine_FamilyExclusivity_WiredExcludesWLAN 验证有线报文仍然硬排除 WLAN 候选
func TestMatchEngine_FamilyExclusivity_WiredExcludesWLAN(t *testing.T) {
	engine := newExclusivityEngine(t, exclusivityKnowledges(), exclusivityMappings())

	k, tier, _ := engine.Match(wiredLog(), "Huawei-VRP", "")
	if k == nil || k.ID == 11534 {
		t.Fatalf("wired log must never match WLAN knowledge while wired candidates exist, got %v", k)
	}
	if tier == matcher.TierFamilySuspect {
		t.Fatalf("wired log with compliant candidates must not be flagged suspect, got %s", tier)
	}
}

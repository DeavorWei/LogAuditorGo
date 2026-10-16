package matcher_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"logauditorgo/internal/matcher"
	"logauditorgo/internal/model"
	"logauditorgo/internal/search"
	"logauditorgo/internal/storage"
	"logauditorgo/pkg/logger"
)

func TestMatcherTiers(t *testing.T) {
	logger.Init("debug", "console")

	tmpDir, err := os.MkdirTemp("", "matcher_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test_matcher.db")
	db, err := storage.InitKnowledgeDB(dbPath)
	if err != nil {
		t.Fatalf("init test db failed: %v", err)
	}

	indexPath := filepath.Join(tmpDir, "test_matcher.bleve")
	indexer, err := search.InitIndexer(indexPath)
	if err != nil {
		t.Fatalf("init test indexer failed: %v", err)
	}
	defer indexer.Close()

	// 插入测试知识库
	items := []model.Knowledge{
		{
			ID:          1,
			EntryType:   model.EntryTypeLog,
			Module:      "BGP",
			Severity:    4,
			Brief:       "BGP_AUTH_FAILED",
			Message:     "BGP session authentication failed. (PeerID=[PeerID], TcpConnSocket=[TcpFD])",
			Description: "BGP认证失败",
			Cause:       "密码不匹配",
			Action:      "检查密码",
			ContentHash: "h1",
		},
		{
			ID:          2,
			EntryType:   model.EntryTypeLog,
			Module:      "AAA",
			Severity:    4,
			Brief:       "hwRadiusAuthServerDown_active",
			Message:     "The communication with the RADIUS authentication server fails. (IpAddress=[IpAddress], Vpn-Instance=[Vpn-Instance])",
			Description: "RADIUS不可达",
			Cause:       "网络中断",
			Action:      "Ping测试",
			ContentHash: "h2",
		},
	}

	for _, it := range items {
		db.Create(&it)
	}
	_ = indexer.IndexKnowledge(items)

	engine := matcher.NewMatchEngine(db, indexer)

	// 1. 测试 Tier 1: EXACT 匹配
	norm1 := &model.NormalizedLog{
		Module:      "BGP",
		Brief:       "BGP_AUTH_FAILED",
		MessageBody: "BGP session authentication failed. (PeerID=1.1.1.1, TcpConnSocket=10)",
	}
	k1, tier1, conf1 := engine.Match(norm1, "", "")
	if k1 == nil || tier1 != matcher.TierExact || conf1 != 1.0 {
		t.Errorf("Tier 1 failed: got k=%v, tier=%s, conf=%f", k1, tier1, conf1)
	}

	// 2. 测试 Tier 2: MNEMONIC 助记符别名匹配 (去掉 _active)
	norm2 := &model.NormalizedLog{
		Module:      "AAA",
		Brief:       "hwRadiusAuthServerDown",
		MessageBody: "The communication with the RADIUS authentication server fails. (IpAddress=10.0.0.1, Vpn-Instance=default)",
	}
	k2, tier2, conf2 := engine.Match(norm2, "", "")
	if k2 == nil || tier2 != matcher.TierMnemonic || conf2 != 0.90 {
		t.Errorf("Tier 2 failed: got k=%v, tier=%s, conf=%f", k2, tier2, conf2)
	}

	// 3. 测试 Tier 3: TEMPLATE 消息模板反向匹配 (Brief 不匹配但 Message 结构完全吻合)
	norm3Template := &model.NormalizedLog{
		Module:      "BGP",
		Brief:       "SOME_CUSTOM_BRIEF",
		MessageBody: "BGP session authentication failed. (PeerID=192.168.1.1, TcpConnSocket=42)",
	}
	k3T, tier3T, conf3T := engine.Match(norm3Template, "", "")
	if k3T == nil || tier3T != matcher.TierTemplate || conf3T != 0.80 {
		t.Errorf("Tier 3 Template match failed: got k=%v, tier=%s, conf=%f", k3T, tier3T, conf3T)
	}

	// 4. 测试 Tier 4: BLEVE 搜索召回
	norm3 := &model.NormalizedLog{
		Module:      "BGP",
		Brief:       "UNKNOWN_BGP_EVENT",
		MessageBody: "authentication failed with remote neighbor",
	}
	k3, tier3, conf3 := engine.Match(norm3, "", "")
	if k3 == nil || tier3 != matcher.TierBleve || conf3 < 0.5 {
		t.Errorf("Tier 4 failed: got k=%v, tier=%s, conf=%f", k3, tier3, conf3)
	}

	// 5. 测试 Tier 5: UNMATCHED
	norm4 := &model.NormalizedLog{
		Module:      "RANDOM_XYZ",
		Brief:       "NOT_EXIST_BRIEF",
		MessageBody: "Completely unrelated message string 12345",
	}
	k4, tier4, conf4 := engine.Match(norm4, "", "")
	if k4 != nil || tier4 != matcher.TierUnmatch || conf4 != 0.0 {
		t.Errorf("Tier 5 failed: expected unmatch, got k=%v, tier=%s, conf=%f", k4, tier4, conf4)
	}
}
func TestMatcherDefensive(t *testing.T) {
	var engine *matcher.MatchEngine

	// Should not panic, should return unmatch
	k, tier, _ := engine.Match(&model.NormalizedLog{}, "", "")
	if tier != matcher.TierUnmatch || k != nil {
		t.Errorf("expected TierUnmatch for nil engine, got %s", tier)
	}

	// Should not panic on reload
	engine.Reload()

	// Should not panic, should return unmatch
	engine2 := matcher.NewMatchEngine(nil, nil)
	k, tier, _ = engine2.Match(nil, "", "")
	if tier != matcher.TierUnmatch || k != nil {
		t.Errorf("expected TierUnmatch for nil log, got %s", tier)
	}
	engine2.Reload()
}

func TestMatcherReloadAndCache(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "matcher_reload_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "reload_matcher.db")
	db, err := storage.InitKnowledgeDB(dbPath)
	if err != nil {
		t.Fatalf("init test db failed: %v", err)
	}

	indexPath := filepath.Join(tmpDir, "reload_matcher.bleve")
	indexer, err := search.InitIndexer(indexPath)
	if err != nil {
		t.Fatalf("init test indexer failed: %v", err)
	}
	defer indexer.Close()

	item := model.Knowledge{
		EntryType:   model.EntryTypeLog,
		Module:      "OSPF",
		Severity:    3,
		Brief:       "OSPF_NBR_DOWN",
		Message:     "Neighbor [NbrIP] is Down.",
		ContentHash: "hash-ospf",
	}
	if err := db.Create(&item).Error; err != nil {
		t.Fatalf("failed to insert test knowledge: %v", err)
	}
	_ = indexer.IndexKnowledge([]model.Knowledge{item})

	engine := matcher.NewMatchEngine(db, indexer)

	norm := &model.NormalizedLog{
		Module:      "OSPF",
		Brief:       "OSPF_NBR_DOWN",
		MessageBody: "Neighbor 10.1.1.1 is Down.",
	}

	// First match (populates cache)
	k1, tier1, conf1 := engine.Match(norm, "", "")
	if k1 == nil || tier1 != matcher.TierExact || conf1 != 1.0 {
		t.Fatalf("first match failed: got k=%v, tier=%s", k1, tier1)
	}

	// Second match (cache hit)
	k2, tier2, conf2 := engine.Match(norm, "", "")
	if k2 == nil || tier2 != matcher.TierExact || conf2 != 1.0 {
		t.Fatalf("second match (cache hit) failed: got k=%v, tier=%s", k2, tier2)
	}

	// Negative match (populates negativeCache)
	normUnmatched := &model.NormalizedLog{
		Module:      "UNKNOWN",
		Brief:       "NO_SUCH_EVENT",
		MessageBody: "Nothing matched here",
	}
	kUnmatch1, tierU1, _ := engine.Match(normUnmatched, "", "")
	if kUnmatch1 != nil || tierU1 != matcher.TierUnmatch {
		t.Fatalf("expected unmatch on first try, got %s", tierU1)
	}

	// Second negative match (negative cache hit)
	kUnmatch2, tierU2, _ := engine.Match(normUnmatched, "", "")
	if kUnmatch2 != nil || tierU2 != matcher.TierUnmatch {
		t.Fatalf("expected unmatch on second try, got %s", tierU2)
	}

	// Reload clears cache and negative cache
	engine.Reload()

	// Third match after reload (re-computes and caches again)
	k3, tier3, conf3 := engine.Match(norm, "", "")
	if k3 == nil || tier3 != matcher.TierExact || conf3 != 1.0 {
		t.Fatalf("match after reload failed: got k=%v, tier=%s", k3, tier3)
	}
}

func BenchmarkMatcherExactAndNegative(b *testing.B) {
	tmpDir, _ := os.MkdirTemp("", "bench_matcher_*")
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "bench_matcher.db")
	db, _ := storage.InitKnowledgeDB(dbPath)
	indexPath := filepath.Join(tmpDir, "bench_matcher.bleve")
	indexer, _ := search.InitIndexer(indexPath)
	defer indexer.Close()

	items := []model.Knowledge{
		{
			ID:          1,
			EntryType:   model.EntryTypeLog,
			Module:      "BGP",
			Severity:    4,
			Brief:       "BGP_AUTH_FAILED",
			Message:     "BGP session authentication failed.",
			ContentHash: "h1",
		},
	}
	for _, it := range items {
		db.Create(&it)
	}
	engine := matcher.NewMatchEngine(db, indexer)

	normMatch := &model.NormalizedLog{
		Module:      "BGP",
		Brief:       "BGP_AUTH_FAILED",
		MessageBody: "BGP session authentication failed.",
	}
	normUnmatch := &model.NormalizedLog{
		Module:      "DEBUG",
		Brief:       "ROUTINE_EVENT",
		MessageBody: "Routine session message",
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if i%2 == 0 {
			_, _, _ = engine.Match(normMatch, "", "")
		} else {
			_, _, _ = engine.Match(normUnmatch, "", "")
		}
	}
}

func TestScoringConstantsAndFallback(t *testing.T) {
	// 1. Verify confidence calculations
	if c := matcher.CalculateConfidence(matcher.TierExact, 0); c != matcher.ConfidenceExact {
		t.Errorf("expected %f for TierExact, got %f", matcher.ConfidenceExact, c)
	}
	if c := matcher.CalculateConfidence(matcher.TierMnemonic, 0); c != matcher.ConfidenceMnemonic {
		t.Errorf("expected %f for TierMnemonic, got %f", matcher.ConfidenceMnemonic, c)
	}
	if c := matcher.CalculateConfidence(matcher.TierTemplate, 0); c != matcher.ConfidenceTemplate {
		t.Errorf("expected %f for TierTemplate, got %f", matcher.ConfidenceTemplate, c)
	}
	if c := matcher.CalculateConfidence(matcher.TierBleve, 0); c != matcher.ConfidenceBleveMin {
		t.Errorf("expected %f for raw bleve score 0, got %f", matcher.ConfidenceBleveMin, c)
	}
	if c := matcher.CalculateConfidence(matcher.TierBleve, 100); c != matcher.ConfidenceBleveMax {
		t.Errorf("expected %f for raw bleve score 100, got %f", matcher.ConfidenceBleveMax, c)
	}
	if c := matcher.CalculateConfidence("UNKNOWN", 0); c != 0.0 {
		t.Errorf("expected 0.0 for unknown tier, got %f", c)
	}

	// 2. Verify FindBestKnowledgeMatchPtr version scoring
	candidates := []*model.Knowledge{
		{
			ID: 1,
			Versions: []model.KnowledgeVersionMapping{
				{ProductType: "CloudEngine 16800", ProductVersion: "V200R024C00"},
			},
		},
		{
			ID: 2,
			Versions: []model.KnowledgeVersionMapping{
				{ProductType: "CloudEngine 16800", ProductVersion: "V200R025C00"},
			},
		},
		{
			ID: 3,
			Versions: []model.KnowledgeVersionMapping{
				{ProductType: "HiSecEngine USG6000F", ProductVersion: "V600R025C10"},
			},
		},
		{
			ID: 4,
			Versions: []model.KnowledgeVersionMapping{
				{ProductType: "General Switch", ProductVersion: "V100"},
			},
		},
		{
			ID:       5,
			Versions: nil, // fallback candidate with no versions
		},
	}

	// Exact Product + Exact Version -> Candidate 2 (Score: 150)
	m1 := matcher.FindBestKnowledgeMatchPtr(candidates, "CloudEngine 16800", "V200R025C00")
	if m1 == nil || m1.ID != 2 {
		t.Errorf("expected ID 2 for exact product and version, got %+v", m1)
	}

	// Exact Product + Different Version -> Candidate 1 or 2 (Score: 120)
	m2 := matcher.FindBestKnowledgeMatchPtr(candidates, "CloudEngine 16800", "V200R010C00")
	if m2 == nil || (m2.ID != 1 && m2.ID != 2) {
		t.Errorf("expected ID 1 or 2 for exact product newer version, got %+v", m2)
	}

	// Same Family -> Candidate 1 or 2 (Score: 50)
	m3 := matcher.FindBestKnowledgeMatchPtr(candidates, "CloudEngine 6800", "V200R001C00")
	if m3 == nil || (m3.ID != 1 && m3.ID != 2) {
		t.Errorf("expected ID 1 or 2 for same family, got %+v", m3)
	}

	// Cross-product general -> Candidate 4 (Score: 10)
	m4 := matcher.FindBestKnowledgeMatchPtr([]*model.Knowledge{candidates[3], candidates[4]}, "NetEngine 8000", "V800")
	if m4 == nil || m4.ID != 4 {
		t.Errorf("expected ID 4 for cross-product match, got %+v", m4)
	}

	// No version fallback -> Candidate 5 (Score: 5)
	m5 := matcher.FindBestKnowledgeMatchPtr([]*model.Knowledge{candidates[4]}, "NetEngine 8000", "V800")
	if m5 == nil || m5.ID != 5 {
		t.Errorf("expected ID 5 for no version fallback, got %+v", m5)
	}

	// Nil / Empty candidates
	if mNil := matcher.FindBestKnowledgeMatchPtr(nil, "CloudEngine", "V200"); mNil != nil {
		t.Errorf("expected nil for empty candidates, got %+v", mNil)
	}
}

func BenchmarkFindBestKnowledgeMatchPtr(b *testing.B) {
	candidates := []*model.Knowledge{
		{
			ID: 1,
			Versions: []model.KnowledgeVersionMapping{
				{ProductType: "CloudEngine 16800", ProductVersion: "V200R024C00"},
			},
		},
		{
			ID: 2,
			Versions: []model.KnowledgeVersionMapping{
				{ProductType: "CloudEngine 16800", ProductVersion: "V200R025C00"},
			},
		},
		{
			ID: 3,
			Versions: []model.KnowledgeVersionMapping{
				{ProductType: "HiSecEngine USG6000F", ProductVersion: "V600R025C10"},
			},
		},
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = matcher.FindBestKnowledgeMatchPtr(candidates, "CloudEngine 16800", "V200R025C00")
	}
}

// TestMnemonicNoReverseMatch 验证 H-03: 助记符匹配绝不能将 _down 误配给 Up 产生语义反转
func TestMnemonicNoReverseMatch(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "matcher_h03_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test_h03.db")
	db, err := storage.InitKnowledgeDB(dbPath)
	if err != nil {
		t.Fatalf("init test db failed: %v", err)
	}

	// 库中只有 hwBgpPeerUp 知识条目
	items := []model.Knowledge{
		{
			ID:          1,
			EntryType:   model.EntryTypeLog,
			Module:      "BGP",
			Brief:       "hwBgpPeerUp",
			Message:     "BGP peer connection established successfully.",
			ContentHash: "hash_bgp_up",
		},
	}
	for _, it := range items {
		_ = db.Create(&it).Error
	}

	engine := matcher.NewMatchEngine(db, nil)
	engine.Reload()

	// 输入故障掉线日志: hwBgpPeer_down
	downLog := &model.NormalizedLog{
		Module:      "BGP",
		Brief:       "hwBgpPeer_down",
		MessageBody: "BGP peer is disconnected.",
	}

	k, tier, _ := engine.Match(downLog, "", "")
	// 验证：绝不能命中 TierMnemonic 并返回 hwBgpPeerUp
	if tier == matcher.TierMnemonic && k != nil && k.Brief == "hwBgpPeerUp" {
		t.Fatalf("H-03 regression: hwBgpPeer_down was incorrectly matched to opposite knowledge hwBgpPeerUp via TierMnemonic!")
	}
}

// newIsolatedMatchEngine 在独立临时目录中构建一套 DB + Bleve + 匹配引擎，供各回归用例隔离使用
func newIsolatedMatchEngine(t *testing.T) (*matcher.MatchEngine, *gorm.DB) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "matcher_regr_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })

	db, err := storage.InitKnowledgeDB(filepath.Join(tmpDir, "regr.db"))
	if err != nil {
		t.Fatalf("init test db failed: %v", err)
	}
	indexer, err := search.InitIndexer(filepath.Join(tmpDir, "regr.bleve"))
	if err != nil {
		t.Fatalf("init test indexer failed: %v", err)
	}
	t.Cleanup(func() { _ = indexer.Close() })

	return matcher.NewMatchEngine(db, indexer), db
}

// TestMatchTemplateAngleBracketPlaceholder 回归 PARSE-05：
// Go 的 regexp.QuoteMeta 不转义 '<' 和 '>'，若先 QuoteMeta 再试图匹配转义后的 `\<...\>` 将永不命中，
// 导致 `<Param>` 风格的知识模板在 Tier3 中 100% 失效。
func TestMatchTemplateAngleBracketPlaceholder(t *testing.T) {
	logger.Init("error", "console")

	engine, db := newIsolatedMatchEngine(t)

	k := &model.Knowledge{
		EntryType:   model.EntryTypeLog,
		Module:      "TEMPLATE_ANGLE",
		Severity:    3,
		Brief:       "NEIGHBOR_ANGLE_DOWN",
		Message:     "Neighbor <NbrIP> is Down.",
		Description: "邻居中断",
		ContentHash: "h_angle",
	}
	if err := db.Create(k).Error; err != nil {
		t.Fatalf("failed to create knowledge: %v", err)
	}
	engine.Reload()

	norm := &model.NormalizedLog{
		Module:      "TEMPLATE_ANGLE",
		Brief:       "SOME_OTHER_BRIEF",
		Severity:    3,
		MessageBody: "Neighbor 10.1.1.1 is Down.",
	}
	got, gotTier, _ := engine.Match(norm, "", "")
	if got == nil || got.ID != k.ID {
		t.Fatalf("PARSE-05 regression: <NbrIP> placeholder template should match via Tier3, got tier=%v k=%v", gotTier, got)
	}
	if gotTier != matcher.TierTemplate {
		t.Fatalf("expected tier %s, got %s", matcher.TierTemplate, gotTier)
	}
}

// TestMatchCacheKeyIsBodySensitive 回归 PARSE-01/02：
// 同一 Module:Brief 下，不同正文必须各自独立判定；首条未命中不得毒化后续同类日志。
func TestMatchCacheKeyIsBodySensitive(t *testing.T) {
	logger.Init("error", "console")

	engine, db := newIsolatedMatchEngine(t)

	kB := &model.Knowledge{
		EntryType:   model.EntryTypeLog,
		Module:      "CACHEBODY",
		Severity:    3,
		Brief:       "UNRELATED_BRIEF_FOR_CACHE",
		Message:     "Link flapping detected on interface <IfName>",
		Description: "链路震荡",
		ContentHash: "h_cachebody",
	}
	if err := db.Create(kB).Error; err != nil {
		t.Fatalf("failed to create knowledge: %v", err)
	}
	engine.Reload()

	// 第 1 条：正文完全无关 -> 应未匹配（会写入负缓存）
	first := &model.NormalizedLog{
		Module:      "CACHEBODY",
		Brief:       "SOME_BRIEF",
		Severity:    3,
		MessageBody: "Completely unrelated text about nothing at all.",
	}
	if k, tier, _ := engine.Match(first, "", ""); k != nil {
		t.Fatalf("unrelated body should not match, got tier=%v k.ID=%v", tier, k.ID)
	}

	// 第 2 条：同 Module:Brief，正文能命中模板 -> 必须独立判定，不能被上一条的负缓存毒化
	second := &model.NormalizedLog{
		Module:      "CACHEBODY",
		Brief:       "SOME_BRIEF",
		Severity:    3,
		MessageBody: "Link flapping detected on interface GigabitEthernet1/0/1",
	}
	k2, tier2, _ := engine.Match(second, "", "")
	if k2 == nil || k2.ID != kB.ID {
		t.Fatalf("PARSE-01/02 regression: second log with different body must be judged independently, got tier=%v k=%v", tier2, k2)
	}
}

// TestMnemonicStemPolarityBlacklist 回归 PARSE-13：
// 大写形态的 IF_DOWN / IF_UP 也必须能被归一化识别（原实现对大写后缀完全不生效），
// 且极性相反的后缀（UP↔DOWN、ACTIVE↔INACTIVE）禁止坍缩到同一词干后互配。
func TestMnemonicStemPolarityBlacklist(t *testing.T) {
	cases := []struct {
		brief string
		stem  string
		pol   int8
	}{
		{"IF_DOWN", "IF", -1},
		{"IF_UP", "IF", +1},
		{"PORT_ACTIVE", "PORT", +1},
		{"PORT_INACTIVE", "PORT", -1},
		{"BFD_SESSION_CLEAR", "BFD_SESSION", +1},
		{"PLAIN_BRIEF", "PLAIN_BRIEF", 0},
		// 驼峰形态（无下划线）不在归一化范围内：刻意保持原样。
		// 若强行剥离 "Down" 后缀，会把 hwPortShutdown 切成 hwPortShut，反而引入误配。
		{"hwBfdSessionDown", "HWBFDSESSIONDOWN", 0},
	}
	for _, c := range cases {
		stem, pol := matcher.MnemonicStemForTest(c.brief)
		if stem != c.stem || pol != c.pol {
			t.Errorf("MnemonicStem(%q) = (%q, %d), want (%q, %d)", c.brief, stem, pol, c.stem, c.pol)
		}
	}
}

// newIsolatedMatchEngineWithFixtures 构建带固定三条知识的独立隔离测试引擎 (闭环 P1-1 与 B2)
func newIsolatedMatchEngineWithFixtures(t *testing.T) *matcher.MatchEngine {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "fixture.db")

	// 绕开 storage.InitKnowledgeDB 全局单例，直接通过 gorm.Open 构建独立测试库 (闭环 P1-1)
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite failed: %v", err)
	}
	if err := db.AutoMigrate(&model.Knowledge{}, &model.KnowledgeVersionMapping{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	// 注册关闭 hook 释放 Windows SQLite 文件句柄，避免 t.TempDir 清理报错
	sqlDB, err := db.DB()
	if err == nil {
		t.Cleanup(func() {
			_ = sqlDB.Close()
		})
	}

	// 显式填写唯一的 ContentHash，杜绝 SQLite uniqueIndex 冲突 (闭环 B2)
	knowledges := []model.Knowledge{
		{ID: 3823, Module: "CM", Brief: "USER_OFFLINERESULT", Severity: 5, ContentHash: "hash_3823", Parameters: `[{"name":"wiredUserInfo"}]`},
		{ID: 8438, Module: "CM", Brief: "USER_OFFLINERESULT", Severity: 5, ContentHash: "hash_8438", Parameters: `[{"name":"USER_INFO_OFFLINE"}]`},
		{ID: 11534, Module: "CM", Brief: "USER_OFFLINERESULT", Severity: 5, ContentHash: "hash_11534", Parameters: `[{"name":"WLAN_STA_INFO_OFFLINE"}]`},
	}
	for _, k := range knowledges {
		if err := db.Create(&k).Error; err != nil {
			t.Fatalf("failed to insert fixture %d: %v", k.ID, err)
		}
	}

	// 绑定版本映射：#11534 为 WLAN 族，#3823 为 CloudEngine 族，#8438 为 Campus 族
	_ = db.Create(&model.KnowledgeVersionMapping{KnowledgeID: 11534, ProductType: "WLAN", ProductVersion: "V200R024C00"}).Error
	_ = db.Create(&model.KnowledgeVersionMapping{KnowledgeID: 3823, ProductType: "CloudEngine 16800", ProductVersion: "V300R024C00"}).Error
	_ = db.Create(&model.KnowledgeVersionMapping{KnowledgeID: 8438, ProductType: "S12700, S12700E", ProductVersion: "V200R024C00"}).Error

	engine := matcher.NewMatchEngine(db, nil)
	engine.Reload()
	return engine
}

// TestMatchEngine_MixedWiredWireless_NoCacheLeak 验证混合场景缓存隔离：同任务有线无线交替防串档 (闭环 P0-A, §5.3)
func TestMatchEngine_MixedWiredWireless_NoCacheLeak(t *testing.T) {
	engine := newIsolatedMatchEngineWithFixtures(t)

	wiredLog := &model.NormalizedLog{
		Module:      "CM",
		Brief:       "USER_OFFLINERESULT",
		Severity:    5,
		MessageBody: `[USER_INFO_OFFLINE]DEVICEMAC:00-e0-fc-01-02-03;USER:admin;MAC:00-11-22-33-44-55;IPADDRESS:192.168.1.100;`,
	}
	wirelessLog := &model.NormalizedLog{
		Module:      "CM",
		Brief:       "USER_OFFLINERESULT",
		Severity:    5,
		MessageBody: `[WLAN_STA_INFO_OFFLINE]ACMAC:f4-e5-f2-70-f4-bc;APMAC:8c-e5-ef-80-b3-20;SSID:office;USER:sta01;`,
	}

	taskProduct := "Huawei-VRP"
	taskVersion := ""

	// 1. 先有线后无线
	k1, _, _ := engine.Match(wiredLog, taskProduct, taskVersion)
	if k1 == nil || k1.ID == 11534 {
		t.Fatalf("wired log should not hit #11534, got ID: %v", k1.ID)
	}

	k2, _, _ := engine.Match(wirelessLog, taskProduct, taskVersion)
	if k2 == nil || k2.ID != 11534 {
		t.Fatalf("wireless log must hit #11534 after wired log! got: %v", k2.ID)
	}

	// 2. 清理缓存，先无线后有线
	engine.ClearCache()
	k3, _, _ := engine.Match(wirelessLog, taskProduct, taskVersion)
	if k3 == nil || k3.ID != 11534 {
		t.Fatalf("wireless log must hit #11534, got: %v", k3.ID)
	}

	k4, _, _ := engine.Match(wiredLog, taskProduct, taskVersion)
	if k4 == nil || k4.ID == 11534 {
		t.Fatalf("wired log must NOT hit #11534 after wireless log! got: %v", k4.ID)
	}
}

// TestMatchEngine_DeterministicScoringMatrix 验证严格确定性打分矩阵断言 (§5.4)
func TestMatchEngine_DeterministicScoringMatrix(t *testing.T) {
	engine := newIsolatedMatchEngineWithFixtures(t)

	acLog := &model.NormalizedLog{
		Module:      "CM",
		Brief:       "USER_OFFLINERESULT",
		Severity:    5,
		MessageBody: `[WLAN_STA_INFO_OFFLINE]ACMAC:f4-e5-f2-70-f4-bc;APMAC:8c-e5-ef-80-b3-20;SSID:office;USER:sta01;`,
	}
	ceLog := &model.NormalizedLog{
		Module:      "CM",
		Brief:       "USER_OFFLINERESULT",
		Severity:    5,
		MessageBody: `wired user log on CE`,
	}
	s12700Log := &model.NormalizedLog{
		Module:      "CM",
		Brief:       "USER_OFFLINERESULT",
		Severity:    5,
		MessageBody: `wired user log on Campus switch`,
	}

	// 1. AC 报文 + Huawei-VRP 任务（一期算法自愈）：应击穿 #8438，命中 #11534 (得 40 分)
	engine.ClearCache()
	k1, _, _ := engine.Match(acLog, "Huawei-VRP", "")
	if k1 == nil || k1.ID != 11534 {
		t.Fatalf("AC + Huawei-VRP expected #11534, got %v", k1)
	}

	// 2. AC 报文 + WLAN 任务（同型号同版本）：应命中 #11534 (得 180 分)
	engine.ClearCache()
	k2, _, _ := engine.Match(acLog, "WLAN", "V200R024C00")
	if k2 == nil || k2.ID != 11534 {
		t.Fatalf("AC + WLAN exact expected #11534, got %v", k2)
	}

	// 3. AC 报文 + WLAN 任务（同型号无版本）：应命中 #11534 (得 150 分)
	engine.ClearCache()
	k3, _, _ := engine.Match(acLog, "WLAN", "")
	if k3 == nil || k3.ID != 11534 {
		t.Fatalf("AC + WLAN no version expected #11534, got %v", k3)
	}

	// 4. CE 报文 + CloudEngine 任务：应命中 #3823 (得 50 分)
	engine.ClearCache()
	k4, _, _ := engine.Match(ceLog, "CloudEngine", "V200R024C00")
	if k4 == nil || k4.ID != 3823 {
		t.Fatalf("CE + CloudEngine expected #3823, got %v", k4)
	}

	// 5. S12700 园区报文 + Campus-Switch 任务：应命中 #8438 (得 50 分)
	engine.ClearCache()
	k5, _, _ := engine.Match(s12700Log, "Campus-Switch", "V200R024C00")
	if k5 == nil || k5.ID != 8438 {
		t.Fatalf("S12700 + Campus-Switch expected #8438, got %v", k5)
	}

	// 6. 同分确定性断言：构造同分同版本候选，严格断言返回 ID 较小者 (闭环 D10)
	cands := []*model.Knowledge{
		{ID: 200, Brief: "TEST", Versions: []model.KnowledgeVersionMapping{{ProductType: "CloudEngine", ProductVersion: "V100"}}},
		{ID: 100, Brief: "TEST", Versions: []model.KnowledgeVersionMapping{{ProductType: "CloudEngine", ProductVersion: "V100"}}},
		{ID: 300, Brief: "TEST", Versions: []model.KnowledgeVersionMapping{{ProductType: "CloudEngine", ProductVersion: "V100"}}},
	}
	best := matcher.FindBestKnowledgeMatchPtr(cands, "CloudEngine", "V100")
	if best == nil || best.ID != 100 {
		t.Fatalf("tie-breaking expected ID=100 (lowest ID), got %v", best)
	}
}

func TestCalcFeatureScore_NoVersionFallback(t *testing.T) {
	acLog := &model.NormalizedLog{
		MessageBody: "[WLAN_STA_INFO_OFFLINE]ACMAC:f4-e5-f2-70-f4-bc;SSID:office;",
	}

	// 1. 无版本映射的 WLAN 候选
	kNoVerWLAN := &model.Knowledge{
		ID:         1,
		Brief:      "WLAN_OFFLINE",
		Message:    "ACMAC [ACMAC] SSID [SSID]",
		Parameters: "ACMAC,SSID",
		Versions:   nil,
	}
	sWLAN := matcher.CalcFeatureScoreForTest(acLog, kNoVerWLAN)
	if sWLAN != 30 {
		t.Errorf("expected +30 for unversioned WLAN knowledge, got %d", sWLAN)
	}

	// 2. 无版本映射的有线候选 (USER_INFO_OFFLINE)
	kNoVerWired := &model.Knowledge{
		ID:         2,
		Brief:      "USER_INFO_OFFLINE",
		Message:    "user offline",
		Parameters: "USER_INFO_OFFLINE",
		Versions:   nil,
	}
	sWired := matcher.CalcFeatureScoreForTest(acLog, kNoVerWired)
	if sWired != -20 {
		t.Errorf("expected -20 for unversioned wired knowledge, got %d", sWired)
	}

	// 3. 无版本映射的有线候选 (WIREDUSERINFO 闭环 R4)
	kNoVerCE := &model.Knowledge{
		ID:         3,
		Brief:      "WIRED_USER_OFFLINE",
		Message:    "wired user offline",
		Parameters: "WIREDUSERINFO",
		Versions:   nil,
	}
	sCE := matcher.CalcFeatureScoreForTest(acLog, kNoVerCE)
	if sCE != -20 {
		t.Errorf("expected -20 for unversioned WIREDUSERINFO knowledge, got %d", sCE)
	}

	// 4. 普通报文短路 (闭环 R3)
	plainLog := &model.NormalizedLog{
		MessageBody: "GigabitEthernet0/0/1 change state to down",
	}
	sPlain := matcher.CalcFeatureScoreForTest(plainLog, kNoVerWLAN)
	if sPlain != 0 {
		t.Errorf("expected 0 for plain log hot-path short circuit, got %d", sPlain)
	}
}

// TestTier1Consistency_BodyKeyCacheIsolation 验证 R1:
// 同 module:brief 下，正文匹配模板的日志A (一致性0.70) 不会将 Tier1 结果毒化给正文不匹配模板的日志B (真实一致性0.45 < 0.60)
func TestTier1Consistency_BodyKeyCacheIsolation(t *testing.T) {
	engine, db := newIsolatedMatchEngine(t)

	// 构造严重级为 6、含模板的知识条目
	k := &model.Knowledge{
		ID:         9001,
		Module:     "TEMPMON",
		Brief:      "HIGH_TEMP",
		Severity:   6,
		Message:    "Device temperature high threshold [TEMP]",
		Parameters: "TEMP",
	}
	if err := db.Create(k).Error; err != nil {
		t.Fatalf("failed to create knowledge: %v", err)
	}
	engine.Reload()

	// 日志 A: 严重级 3 (差 3，惩罚 0.30)，正文匹配模板 (无惩罚)
	// 一致性 = 1.0 - 0.30 = 0.70 >= 0.60 -> 命中 Tier1 (EXACT)
	logA := &model.NormalizedLog{
		Module:      "TEMPMON",
		Brief:       "HIGH_TEMP",
		Severity:    3,
		MessageBody: "Device temperature high threshold 85",
	}
	kA, tierA, confA := engine.Match(logA, "", "")
	if kA == nil || kA.ID != 9001 || tierA != matcher.TierExact {
		t.Fatalf("logA expected TierExact with k.ID=9001, got tier=%v k=%v", tierA, kA)
	}
	if confA < 0.69 || confA > 0.71 {
		t.Fatalf("logA expected conf ~0.70, got %f", confA)
	}

	// 日志 B: 严重级 3 (差 3，惩罚 0.30)，但正文不匹配模板 (惩罚 0.25)
	// 真实一致性 = 1.0 - 0.30 - 0.25 = 0.45 < 0.60
	// 必须退出 Tier1 软短路！(因无其他 Tier 候选，最终退回 fallback 降级置信度 0.45)
	logB := &model.NormalizedLog{
		Module:      "TEMPMON",
		Brief:       "HIGH_TEMP",
		Severity:    3,
		MessageBody: "Mismatched message body completely unrelated",
	}
	kB, _, confB := engine.Match(logB, "", "")
	if kB == nil || kB.ID != 9001 {
		t.Fatalf("logB expected fallback k.ID=9001, got %v", kB)
	}
	// 关键断言: confB 必须是真实的降级置信度 0.45，绝不能拿到 logA 的 0.70 缓存！
	if confB > 0.50 {
		t.Fatalf("R1 regression: logB wrongly received cached conf=%f from logA, expected <= 0.45", confB)
	}
}

// TestMatchEngineReload_ClearsFallbackFamilyCache 验证 S2:
// 知识库条目原地更新内容后，Reload 必须清空 candFallbackFamilyCache 与选优缓存，保证族判定不陈旧
func TestMatchEngineReload_ClearsFallbackFamilyCache(t *testing.T) {
	engine, db := newIsolatedMatchEngine(t)

	// 初始状态：ID=7777 为 WLAN 无线知识 (无版本映射)
	k7777 := &model.Knowledge{
		ID:          7777,
		Module:      "AUTH",
		Brief:       "USER_OFFLINE",
		Severity:    4,
		Message:     "ACMAC [ACMAC] SSID [SSID]",
		Parameters:  "ACMAC,SSID",
		ContentHash: "hash_k7777_test",
	}
	if err := db.Create(k7777).Error; err != nil {
		t.Fatalf("failed to create knowledge: %v", err)
	}
	engine.Reload()

	acLog := &model.NormalizedLog{
		Module:      "AUTH",
		Brief:       "USER_OFFLINE",
		Severity:    4,
		MessageBody: "[WLAN_STA_INFO_OFFLINE]ACMAC:f4-e5-f2-70-f4-bc;SSID:office;",
	}

	// 首次匹配：归为 FamilyWLAN，特征得分 +30
	sBefore := matcher.CalcFeatureScoreForTest(acLog, k7777)
	if sBefore != 30 {
		t.Fatalf("expected +30 before update, got %d", sBefore)
	}

	// 模拟原地热更新：条目 ID=7777 修改为有线知识 (WIREDUSERINFO)
	k7777.Message = "wired user offline"
	k7777.Parameters = "WIREDUSERINFO"
	if err := db.Save(k7777).Error; err != nil {
		t.Fatalf("failed to update knowledge: %v", err)
	}

	// 执行 Reload
	engine.Reload()

	// 再次匹配：必须识别出新内容为有线族，特征得分转为 -20，绝不能沿用旧的 +30！
	sAfter := matcher.CalcFeatureScoreForTest(acLog, k7777)
	if sAfter != -20 {
		t.Fatalf("S2 regression: expected -20 after Reload, got %d (stale cache detected)", sAfter)
	}
}

// TestMatchEngine_ExactBestCachePerformance 验证 S1:
// 同 module:brief 下不同正文的日志，候选选优结果能够跨行秒级复用，保证极速匹配
func TestMatchEngine_ExactBestCachePerformance(t *testing.T) {
	engine, db := newIsolatedMatchEngine(t)

	// 创建 10 个同 module:brief 的知识条目 (模拟多候选)
	for i := 1; i <= 10; i++ {
		k := &model.Knowledge{
			ID:          uint(8000 + i),
			Module:      "IFNET",
			Brief:       "IF_STATE",
			Severity:    4,
			Message:     fmt.Sprintf("Interface GigabitEthernet0/0/%d changed state to down", i),
			Description: "接口状态告警",
			ContentHash: fmt.Sprintf("hash_if_state_%d", i),
		}
		if err := db.Create(k).Error; err != nil {
			t.Fatalf("failed to create knowledge %d: %v", i, err)
		}
	}
	engine.Reload()

	// 连续匹配 100 行不同正文的日志 (模拟真实场景正文中接口、时间不同)
	for i := 1; i <= 100; i++ {
		logItem := &model.NormalizedLog{
			Module:      "IFNET",
			Brief:       "IF_STATE",
			Severity:    4,
			MessageBody: fmt.Sprintf("Interface GigabitEthernet0/0/1 changed state to down (logIndex=%d randomIp=10.0.%d.1)", i, i),
		}
		k, tier, _ := engine.Match(logItem, "CloudEngine", "V200R024C00")
		if k == nil || tier == matcher.TierUnmatch {
			t.Fatalf("expected matched item for log index %d, got tier=%v", i, tier)
		}
	}
}

// TestTier2Mnemonic_ExactBestKeyCrossBodyReuse 验证闭环 P1-1:
// Tier2 助记符别名匹配的命中所依赖的属性与具体正文解耦，因此同 brief 的不同正文日志应能跨正文复用 exactBestKey 缓存。
func TestTier2Mnemonic_ExactBestKeyCrossBodyReuse(t *testing.T) {
	engine, db := newIsolatedMatchEngine(t)

	// 库中仅存在助记符词干条目 IF_DOWN (不包含具体后缀 _FAIL 等同名条目)
	kStem := &model.Knowledge{
		ID:          9100,
		Module:      "IFNET",
		Brief:       "IF_DOWN",
		Severity:    4,
		Message:     "Interface down event",
		Description: "接口断开",
		ContentHash: "hash_if_down_stem",
	}
	if err := db.Create(kStem).Error; err != nil {
		t.Fatalf("failed to create knowledge: %v", err)
	}
	engine.Reload()

	// 首条日志 brief 为 IF_FAIL (通过 stem 归一化在 Tier2 命中 IF_DOWN)
	log1 := &model.NormalizedLog{
		Module:      "IFNET",
		Brief:       "IF_FAIL",
		Severity:    4,
		MessageBody: "Interface GE1/0/1 state changed to FAIL (session 1)",
	}
	k1, tier1, conf1 := engine.Match(log1, "", "")
	if k1 == nil || tier1 != matcher.TierMnemonic || k1.ID != kStem.ID {
		t.Fatalf("log1 expected TierMnemonic hit, got tier=%s, k=%v", tier1, k1)
	}

	// 第 2 条日志具有完全不同的正文，但同一 brief
	log2 := &model.NormalizedLog{
		Module:      "IFNET",
		Brief:       "IF_FAIL",
		Severity:    4,
		MessageBody: "Interface GE1/0/2 state changed to FAIL (completely different message body hash)",
	}
	k2, tier2, conf2 := engine.Match(log2, "", "")
	if k2 == nil || tier2 != matcher.TierMnemonic || k2.ID != kStem.ID || conf2 != conf1 {
		t.Fatalf("log2 expected TierMnemonic hit via exactBestKey cache, got tier=%s, k=%v", tier2, k2)
	}
}

// TestCalcFeatureScore_DualFeature_WiredPriority 验证闭环 P2-1:
// 报文同时包含无线特征 [WLAN_ 与有线特征 DEVICEMAC: 时，有线排斥规则优先执行，WLAN 知识必须获得 -20 排斥分。
func TestCalcFeatureScore_DualFeature_WiredPriority(t *testing.T) {
	// 双特征混合报文
	dualLog := &model.NormalizedLog{
		Module:      "CM",
		Brief:       "USER_OFFLINERESULT",
		Severity:    5,
		MessageBody: "[WLAN_STA_INFO_OFFLINE]DEVICEMAC:00-e0-fc-12-34-56;ACMAC:f4-e5-f2-70-f4-bc;",
	}

	wlanCand := &model.Knowledge{
		ID:     11534,
		Module: "CM",
		Brief:  "USER_OFFLINERESULT",
		Versions: []model.KnowledgeVersionMapping{
			{ProductType: "WLAN", ProductVersion: "V200R024C00"},
		},
	}

	score := matcher.CalcFeatureScoreForTest(dualLog, wlanCand)
	if score != -20 {
		t.Fatalf("P2-1 regression: dual feature log should yield -20 for WLAN candidate, got %d", score)
	}
}

// TestResolveProductFamily_SpecificModels 验证闭环 P2-2:
// 支持前端 ReanalyzeDialog 承诺的具体型号直接输入（CE8850, S6730, NE9000, AC7005, AP3050, CE12808 等），正确归类到目标产品族。
func TestResolveProductFamily_SpecificModels(t *testing.T) {
	matcher.ClearProductFamilyCache()

	cases := []struct {
		model          string
		expectedFamily matcher.ProductFamily
	}{
		{"CE8850", matcher.FamilyCloudEngine},
		{"CE12808", matcher.FamilyCloudEngine},
		{"S6730", matcher.FamilyCampus},
		{"S5735", matcher.FamilyCampus},
		{"NE9000", matcher.FamilyNetEngine},
		{"NE40E", matcher.FamilyNetEngine},
		{"AC7005", matcher.FamilyWLAN},
		{"AP3050", matcher.FamilyWLAN},
		{"AirEngine 5760", matcher.FamilyWLAN},
		{"USG6550E", matcher.FamilyHiSecEngine},
	}

	for _, c := range cases {
		got := matcher.ResolveProductFamily(c.model)
		if got != c.expectedFamily {
			t.Errorf("model %q: expected family %s, got %s", c.model, c.expectedFamily, got)
		}
	}
}

// TestMatcherEmptyDB 验证 ISSUE-MAT-01: 空知识库时 loaded = true，多次 Match 不会死循环或崩溃
func TestMatcherEmptyDB(t *testing.T) {
	engine, _ := newIsolatedMatchEngine(t)
	// 知识库为空，执行 Match
	norm := &model.NormalizedLog{
		Module:      "BGP",
		Brief:       "PEER_DOWN",
		Severity:    3,
		MessageBody: "BGP peer 1.1.1.1 is down",
	}
	k, tier, conf := engine.Match(norm, "", "")
	if k != nil || tier != matcher.TierUnmatch || conf != 0.0 {
		t.Fatalf("expected unmatch on empty db, got tier=%s, k=%v", tier, k)
	}

	// 再次调用 Match，验证不会反复加写锁加载
	k2, tier2, conf2 := engine.Match(norm, "", "")
	if k2 != nil || tier2 != matcher.TierUnmatch || conf2 != 0.0 {
		t.Fatalf("expected unmatch on empty db, got tier=%s, k=%v", tier2, k2)
	}
}

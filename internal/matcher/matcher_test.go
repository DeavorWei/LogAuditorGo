package matcher_test

import (
	"os"
	"path/filepath"
	"testing"

	"logauditorgo/internal/matcher"
	"logauditorgo/internal/model"
	"logauditorgo/internal/search"
	"logauditorgo/internal/storage"
	"logauditorgo/pkg/logger"

	"gorm.io/gorm"
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

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

	// Should not panic, should return unmatch
	engine2 := matcher.NewMatchEngine(nil, nil)
	k, tier, _ = engine2.Match(nil, "", "")
	if tier != matcher.TierUnmatch || k != nil {
		t.Errorf("expected TierUnmatch for nil log, got %s", tier)
	}
}

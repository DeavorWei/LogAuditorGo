package knowledge_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"logauditorgo/internal/knowledge"
	"logauditorgo/internal/matcher"
	"logauditorgo/internal/model"
	"logauditorgo/internal/search"
	"logauditorgo/internal/storage"
)

func setupLocalKnowledge(t *testing.T) (*knowledge.Service, *matcher.MatchEngine) {
	t.Helper()
	dir := t.TempDir()
	storage.CloseAllTaskDBs()
	_ = storage.CloseKnowledgeDB()
	db, err := storage.InitKnowledgeDB(filepath.Join(dir, "knowledge.db"))
	if err != nil {
		t.Fatalf("init knowledge db failed: %v", err)
	}
	indexer, err := search.InitIndexer(filepath.Join(dir, "index.bleve"))
	if err != nil {
		t.Fatalf("init indexer failed: %v", err)
	}
	svc := knowledge.NewService(db, indexer)
	engine := matcher.NewMatchEngine(db, indexer)
	svc.SetMatchEngine(engine)
	t.Cleanup(func() {
		_ = indexer.Close()
		_ = storage.CloseKnowledgeDB()
		// Windows 下 Bleve 段文件句柄释放有毫秒级延迟，重试删除避免误报清理失败
		for i := 0; i < 20; i++ {
			if err := os.RemoveAll(dir); err == nil {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
	})
	return svc, engine
}

func localNorm(module, brief, body string) *model.NormalizedLog {
	return &model.NormalizedLog{
		Module:      module,
		Brief:       brief,
		Severity:    4,
		MessageBody: body,
		RawLog:      module + "/" + brief + " " + body,
	}
}

// TestLocalKnowledgePriorityAndCSV 验证本地知识优先匹配、覆盖绑定与 CSV 往返。
func TestLocalKnowledgePriorityAndCSV(t *testing.T) {
	svc, engine := setupLocalKnowledge(t)

	entity, err := svc.UpsertLocalKnowledge(knowledge.LocalKnowledgeInput{
		Module:         "bgp",
		Brief:          "PEER_BACKWARD",
		ProductFamily:  "CE16800",
		ProductVersion: "V200R025C00",
		Severity:       2,
		Description:    "本地补充：BGP 邻居中断",
		Cause:          "链路或配置问题",
		Action:         "检查物理链路",
		Reference:      "https://example.local/bgp",
	})
	if err != nil {
		t.Fatalf("upsert local knowledge failed: %v", err)
	}
	if entity.ID == 0 || entity.Source != "local" {
		t.Fatalf("unexpected local knowledge: %+v", entity)
	}

	// 1. LOCAL_EXACT：本地知识优先于任何官方候选
	got, tier, conf := engine.Match(localNorm("BGP", "PEER_BACKWARD", "The BGP peer went down."), "CE16800", "V200R025C00")
	if got == nil || got.ID != entity.ID {
		t.Fatalf("expected local knowledge match, got %+v", got)
	}
	if tier != matcher.TierLocalExact || conf != matcher.ConfidenceLocalExact {
		t.Fatalf("tier=%s conf=%v, want LOCAL_EXACT/0.95", tier, conf)
	}

	// 2. 官方知识 + 覆盖绑定 → LOCAL_MAPPING（0.90）
	official := model.Knowledge{
		EntryType:   model.EntryTypeLog,
		Module:      "IFNET",
		Brief:       "IF_DOWN",
		Severity:    4,
		Message:     "Interface state turned to DOWN.",
		Description: "接口物理链路中断",
		ContentHash: "official-if-down-local-test",
		Source:      "official",
	}
	if err := storage.GlobalKnowledgeDB.Create(&official).Error; err != nil {
		t.Fatalf("create official knowledge failed: %v", err)
	}
	if _, err := svc.CreateOverride("ifnet", "IF_DOWN", official.ID); err != nil {
		t.Fatalf("create override failed: %v", err)
	}
	got, tier, conf = engine.Match(localNorm("IFNET", "IF_DOWN", "Interface down."), "", "")
	if got == nil || got.ID != official.ID || tier != matcher.TierLocalMapping || conf != matcher.ConfidenceLocalMapping {
		t.Fatalf("override match = %+v tier=%s conf=%v", got, tier, conf)
	}

	// 3. CSV 导出 + 导入（新增一行）
	csvText, err := svc.ExportLocalCSV()
	if err != nil {
		t.Fatalf("export csv failed: %v", err)
	}
	if !strings.Contains(csvText, "PEER_BACKWARD") || !strings.Contains(csvText, "module,brief,product") {
		t.Fatalf("unexpected csv: %s", csvText)
	}
	// 版本映射必须随列表/导出返回，否则 CSV 往返会丢产品/版本范围
	if !strings.Contains(csvText, "CE16800") || !strings.Contains(csvText, "V200R025C00") {
		t.Fatalf("csv lost version mapping: %s", csvText)
	}
	importCSV := "module,brief,product,version,severity,entry_type,message,description,cause,action,impact,reference\n" +
		"OSPF,NBR_CHG,通用,,3,LOG,,本地 OSPF 邻居变化,邻居震荡,检查链路,,https://example.local/ospf\n" +
		"BROKEN,,通用,,4,LOG,,,,,,,"
	result, err := svc.ImportLocalCSV(importCSV)
	if err != nil {
		t.Fatalf("import csv failed: %v", err)
	}
	if result.Created != 1 || result.Failed != 1 || result.Updated != 0 {
		t.Fatalf("unexpected import result: %+v", result)
	}

	// 4. 删除本地知识后不再命中
	if err := svc.DeleteLocalKnowledge(entity.ID); err != nil {
		t.Fatalf("delete local knowledge failed: %v", err)
	}
	got, tier, _ = engine.Match(localNorm("BGP", "PEER_BACKWARD", "The BGP peer went down."), "", "")
	if got != nil {
		t.Fatalf("local knowledge should not match after delete, got %+v tier=%s", got, tier)
	}
}

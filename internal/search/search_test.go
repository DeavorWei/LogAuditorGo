package search_test

import (
	"os"
	"path/filepath"
	"testing"

	"logauditorgo/internal/model"
	"logauditorgo/internal/search"
	"logauditorgo/pkg/logger"
)

func TestBleveIndexerAndSearch(t *testing.T) {
	logger.Init("debug", "console")

	tmpDir, err := os.MkdirTemp("", "bleve_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	indexPath := filepath.Join(tmpDir, "test.bleve")
	indexer, err := search.InitIndexer(indexPath)
	if err != nil {
		t.Fatalf("init indexer failed: %v", err)
	}
	defer indexer.Close()

	items := []model.Knowledge{
		{
			ID:          1,
			EntryType:   model.EntryTypeLog,
			Module:      "BGP",
			Severity:    4,
			Brief:       "BGP_AUTH_FAILED",
			Message:     "BGP session authentication failed. (PeerID=192.168.1.2)",
			Description: "显示BGP会话认证失败的信息",
			Cause:       "BGP会话两端的安全配置不对称，密码错误",
			Action:      "请检查两端keychain或MD5认证配置是否一致",
			Versions: []model.KnowledgeVersionMapping{
				{ProductType: "CloudEngine 16800", ProductVersion: "V200R025C00"},
			},
		},
		{
			ID:          2,
			EntryType:   model.EntryTypeLog,
			Module:      "IFNET",
			Severity:    4,
			Brief:       "IF_DOWN",
			Message:     "Interface 100GE1/0/1 state turned to DOWN.",
			Description: "接口物理链路中断",
			Cause:       "光纤松动或光模块故障",
			Action:      "检查光纤物理连接及收发光功率",
			Versions: []model.KnowledgeVersionMapping{
				{ProductType: "CloudEngine 16800", ProductVersion: "V200R025C00"},
			},
		},
		{
			ID:          3,
			EntryType:   model.EntryTypeAlarm,
			Module:      "AAA",
			Severity:    2,
			Brief:       "hwRadiusAuthServerDown",
			TrapOID:     "1.3.6.1.4.1.2011.5.25.40.15.2.2.1.2",
			MIBName:     "HUAWEI-AAA-MIB",
			Message:     "The RADIUS authentication server was down.",
			Description: "RADIUS认证服务器不可达",
			Cause:       "网络中断或RADIUS服务器服务宕机",
			Action:      "在设备上ping服务器IP测试连通性",
			Versions: []model.KnowledgeVersionMapping{
				{ProductType: "HiSecEngine USG6000F", ProductVersion: "V600R025C10"},
			},
		},
	}

	if err := indexer.IndexKnowledge(items); err != nil {
		t.Fatalf("index knowledge failed: %v", err)
	}

	// 1. 测试关键词全文检索
	res1, err := indexer.Search(search.SearchFilter{Keyword: "认证"})
	if err != nil {
		t.Fatalf("search keyword failed: %v", err)
	}
	if res1.Total != 2 {
		t.Errorf("expected 2 results for keyword '认证', got %d", res1.Total)
	}

	// 2. 测试模块过滤
	res2, err := indexer.Search(search.SearchFilter{Module: "BGP"})
	if err != nil {
		t.Fatalf("search module failed: %v", err)
	}
	if res2.Total != 1 || res2.Hits[0].KnowledgeID != 1 {
		t.Errorf("expected knowledge ID 1 for module BGP, got %+v", res2)
	}

	// 3. 测试级别过滤
	//
	// KB-10: 语义由"范围过滤(1~N)"改为"精确等于"。
	// 原实现查询 severity=4 会返回 1~4 全部，与 UI 的单选语义不符。
	// 这里分别验证"等于 2 命中 ID 3"与"等于 4 命中 ID 1/2 但不含 ID 3"。
	sevExact := 2
	res3, err := indexer.Search(search.SearchFilter{Severity: &sevExact})
	if err != nil {
		t.Fatalf("search severity failed: %v", err)
	}
	if res3.Total != 1 || res3.Hits[0].KnowledgeID != 3 {
		t.Errorf("expected knowledge ID 3 for severity == 2, got %+v", res3)
	}

	sevFour := 4
	res3b, err := indexer.Search(search.SearchFilter{Severity: &sevFour})
	if err != nil {
		t.Fatalf("search severity failed: %v", err)
	}
	if res3b.Total != 2 {
		t.Errorf("expected 2 results for severity == 4, got %d", res3b.Total)
	}
	for _, h := range res3b.Hits {
		if h.KnowledgeID == 3 {
			t.Errorf("severity==4 must NOT match knowledge ID 3 (severity=2), got %+v", res3b)
		}
	}

	// 4'. KB-02: 命中的 Document 字段必须被填充，调用方无需再按 ID 回查数据库
	if len(res3.Hits) == 1 {
		doc := res3.Hits[0].Document
		if doc.ID != "3" {
			t.Errorf("expected hit Document.ID == '3', got %q", doc.ID)
		}
		if doc.Brief != "hwRadiusAuthServerDown" {
			t.Errorf("expected hit Document.Brief == 'hwRadiusAuthServerDown', got %q", doc.Brief)
		}
		if doc.Description != "RADIUS认证服务器不可达" {
			t.Errorf("expected hit Document.Description to be populated, got %q", doc.Description)
		}
		if doc.Severity != 2 {
			t.Errorf("expected hit Document.Severity == 2, got %v", doc.Severity)
		}
		if len(doc.ProductList) != 1 || doc.ProductList[0] != "HiSecEngine USG6000F" {
			t.Errorf("expected hit Document.ProductList == [HiSecEngine USG6000F], got %v", doc.ProductList)
		}
	} else {
		t.Fatalf("expected exactly 1 hit for severity filter, got %d", len(res3.Hits))
	}

	// 4. 测试告警类型过滤
	res4, err := indexer.Search(search.SearchFilter{EntryType: "ALARM"})
	if err != nil {
		t.Fatalf("search entry_type failed: %v", err)
	}
	if res4.Total != 1 || res4.Hits[0].KnowledgeID != 3 {
		t.Errorf("expected knowledge ID 3 for entry_type ALARM, got %+v", res4)
	}
}
func TestBleveIndexerDefensive(t *testing.T) {
	var idx *search.Indexer
	err := idx.Close()
	if err != nil {
		t.Errorf("expected nil error on nil indexer Close")
	}

	err = idx.IndexKnowledge(nil)
	if err == nil {
		t.Errorf("expected error on nil indexer IndexKnowledge")
	}

	_, err = idx.Search(search.SearchFilter{})
	if err == nil {
		t.Errorf("expected error on nil indexer Search")
	}
}

func TestBleveIndexerConcurrentCloseAndInit(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "bleve_concurrent_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	indexPath := filepath.Join(tmpDir, "test.bleve")

	for i := 0; i < 20; i++ {
		idx, err := search.InitIndexer(indexPath)
		if err != nil {
			t.Fatalf("InitIndexer failed on iteration %d: %v", i, err)
		}

		done := make(chan struct{})
		go func() {
			_ = idx.Close()
			close(done)
		}()

		go func() {
			_, _ = search.InitIndexer(indexPath)
		}()

		<-done
		_ = idx.Close()
	}
}

func TestBleveIndexerChunkedBatching(t *testing.T) {
	logger.Init("debug", "console")

	tmpDir, err := os.MkdirTemp("", "bleve_chunk_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	indexPath := filepath.Join(tmpDir, "test_chunk.bleve")
	indexer, err := search.InitIndexer(indexPath)
	if err != nil {
		t.Fatalf("init indexer failed: %v", err)
	}
	defer indexer.Close()

	// 构造 1200 条知识，跨越多个 500-item batch
	items := make([]model.Knowledge, 1200)
	for i := 0; i < 1200; i++ {
		items[i] = model.Knowledge{
			ID:          uint(i + 1),
			EntryType:   model.EntryTypeLog,
			Module:      "SYSTEM",
			Severity:    4,
			Brief:       "SYS_CHUNK_TEST",
			Message:     "System chunk batching test item.",
			Description: "Chunking verification doc",
		}
	}

	if err := indexer.IndexKnowledge(items); err != nil {
		t.Fatalf("IndexKnowledge failed for 1200 items: %v", err)
	}

	res, err := indexer.Search(search.SearchFilter{
		Module:   "SYSTEM",
		PageSize: 10,
	})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if res.Total != 1200 {
		t.Errorf("expected total 1200 hits, got %d", res.Total)
	}
}


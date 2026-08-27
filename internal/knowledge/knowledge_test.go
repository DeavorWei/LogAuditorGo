package knowledge_test

import (
	"os"
	"path/filepath"
	"testing"

	"logauditorgo/internal/hdx"
	"logauditorgo/internal/knowledge"
	"logauditorgo/internal/model"
	"logauditorgo/internal/storage"
	"logauditorgo/pkg/logger"
	"logauditorgo/pkg/progress"
)

func TestRealHDXImportAndDeduplication(t *testing.T) {
	logger.Init("debug", "console")

	tmpDir, err := os.MkdirTemp("", "knowledge_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test_knowledge.db")
	db, err := storage.InitKnowledgeDB(dbPath)
	if err != nil {
		t.Fatalf("init test db failed: %v", err)
	}

	service := knowledge.NewService(db)

	// 1. 测试真实 USG6000F/G 文档导入
	usgDir := filepath.FromSlash("../../原始产品文档/HiSecEngine USG6000F, USG6000G_V600R025C10_01_zh_AZQ01091")
	if _, err := os.Stat(usgDir); os.IsNotExist(err) {
		t.Skipf("sample doc dir not found at %s, skipping test", usgDir)
	}

	stats1, err := service.ImportDocumentFromDir(usgDir)
	if err != nil {
		t.Fatalf("import USG doc failed: %v", err)
	}
	t.Logf("USG6000F/G Stats: Leaf Logs=%d, Leaf Alarms=%d, Unique Knowledge=%d, Duration=%v",
		stats1.LeafLogCount, stats1.LeafAlarmCount, stats1.UniqueKnowledgeAdded, stats1.Duration)

	if stats1.LeafLogCount < 2000 {
		t.Errorf("expected > 2000 leaf logs for USG6000F/G, got %d", stats1.LeafLogCount)
	}
	if stats1.LeafAlarmCount < 1000 {
		t.Errorf("expected > 1000 leaf alarms for USG6000F/G, got %d", stats1.LeafAlarmCount)
	}
	if stats1.UniqueKnowledgeAdded == 0 {
		t.Errorf("expected > 0 unique knowledge added")
	}

	// 2. 测试第二个文档导入并验证去重 (CloudEngine 16800 V200R025C00)
	ceDir := filepath.FromSlash("../../原始产品文档/CloudEngine 16800_V200R025C00_05_zh_AZP10147")
	if _, err := os.Stat(ceDir); err == nil {
		stats2, err := service.ImportDocumentFromDir(ceDir)
		if err != nil {
			t.Fatalf("import CE doc failed: %v", err)
		}
		t.Logf("CE 16800 Stats: Leaf Logs=%d, Leaf Alarms=%d, Unique Knowledge=%d, Duration=%v",
			stats2.LeafLogCount, stats2.LeafAlarmCount, stats2.UniqueKnowledgeAdded, stats2.Duration)

		if stats2.LeafLogCount < 1400 {
			t.Errorf("expected > 1400 leaf logs for CE16800, got %d", stats2.LeafLogCount)
		}
	}

	// 3. 验证文档列表查询
	docs, err := service.GetDocumentList()
	if err != nil {
		t.Fatalf("get doc list failed: %v", err)
	}
	if len(docs) < 1 {
		t.Errorf("expected at least 1 doc, got %d", len(docs))
	}
}

func TestNaviParsingEdgeCases(t *testing.T) {
	// 测试 GBK 转码
	gbkText := []byte{0xC4, 0xE3, 0xBA, 0xC3} // "你好" in GBK
	utf8Text, err := hdx.DecodeGBK(gbkText)
	if err != nil {
		t.Fatalf("decode gbk failed: %v", err)
	}
	if utf8Text != "你好" {
		t.Errorf("expected '你好', got '%s'", utf8Text)
	}
}

func TestCalculateContentHash(t *testing.T) {
	// 1. nil 防御
	if hash := knowledge.CalculateContentHash(nil); hash != "" {
		t.Errorf("expected empty string for nil knowledge, got %s", hash)
	}

	// 2. 确定性与空白符修剪
	k1 := &model.Knowledge{
		EntryType: model.EntryTypeLog,
		Module:    "BGP",
		Brief:     "BGP_AUTH_FAILED",
		Message:   "BGP authentication failed.",
		Cause:     "Password mismatch",
		Action:    "Check passwords",
	}
	k2 := &model.Knowledge{
		EntryType: model.EntryTypeLog,
		Module:    " BGP ",
		Brief:     "BGP_AUTH_FAILED ",
		Message:   "BGP authentication failed.",
		Cause:     "Password mismatch",
		Action:    "Check passwords",
	}

	h1 := knowledge.CalculateContentHash(k1)
	h2 := knowledge.CalculateContentHash(k2)
	if h1 == "" || h1 != h2 {
		t.Errorf("expected matching hash for trimmed fields, got h1=%s, h2=%s", h1, h2)
	}

	// 3. 验证 Severity 敏感性 (H-01: 不同告警级别不能被合并)
	kSev2 := &model.Knowledge{
		EntryType: model.EntryTypeLog,
		Module:    "BGP",
		Brief:     "BGP_AUTH_FAILED",
		Severity:  2,
	}
	kSev6 := &model.Knowledge{
		EntryType: model.EntryTypeLog,
		Module:    "BGP",
		Brief:     "BGP_AUTH_FAILED",
		Severity:  6,
	}
	if knowledge.CalculateContentHash(kSev2) == knowledge.CalculateContentHash(kSev6) {
		t.Errorf("expected different hashes for different severity levels")
	}

	// 4. 验证 TrapOID 敏感性 (H-01: 跨产品不同 OID 不能被误合并)
	kTrap1 := &model.Knowledge{
		EntryType: model.EntryTypeAlarm,
		Module:    "BGP",
		Brief:     "BGP_PEER_DOWN",
		TrapOID:   "1.3.6.1.4.1.2011.5.25.177.1.1",
	}
	kTrap2 := &model.Knowledge{
		EntryType: model.EntryTypeAlarm,
		Module:    "BGP",
		Brief:     "BGP_PEER_DOWN",
		TrapOID:   "1.3.6.1.4.1.2011.5.25.177.1.2",
	}
	if knowledge.CalculateContentHash(kTrap1) == knowledge.CalculateContentHash(kTrap2) {
		t.Errorf("expected different hashes for different TrapOIDs")
	}
}

func TestFindBestKnowledgeMatch(t *testing.T) {
	service := knowledge.NewService(nil)

	candidates := []model.Knowledge{
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

	// 1. 完全精确命中 (Product + Version)
	match := service.FindBestKnowledgeMatch(candidates, "CloudEngine 16800", "V200R025C00")
	if match == nil || match.ID != 2 {
		t.Fatalf("expected candidate ID 2 for exact match, got %+v", match)
	}

	// 2. 同型号降级命中 (Product 命中，Version 不匹配)
	match2 := service.FindBestKnowledgeMatch(candidates, "CloudEngine 16800", "V200R020C00")
	if match2 == nil || (match2.ID != 1 && match2.ID != 2) {
		t.Fatalf("expected candidate ID 1 or 2 for model match, got %+v", match2)
	}

	// 3. 同产品族相近系列匹配
	match3 := service.FindBestKnowledgeMatch(candidates, "CloudEngine 6800", "V200R025C00")
	if match3 == nil || (match3.ID != 1 && match3.ID != 2) {
		t.Fatalf("expected CloudEngine family match, got %+v", match3)
	}

	// 4. 空候选
	if matchNil := service.FindBestKnowledgeMatch(nil, "CE16800", "V200"); matchNil != nil {
		t.Errorf("expected nil for empty candidates, got %+v", matchNil)
	}
}

func TestBatchDirectoryImport(t *testing.T) {
	logger.Init("debug", "console")

	docRoot := filepath.FromSlash("../../原始产品文档")
	if _, err := os.Stat(docRoot); os.IsNotExist(err) {
		t.Skipf("sample doc root not found at %s, skipping test", docRoot)
	}

	tmpDir, err := os.MkdirTemp("", "knowledge_batch_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "batch_knowledge.db")
	db, err := storage.InitKnowledgeDB(dbPath)
	if err != nil {
		t.Fatalf("init test db failed: %v", err)
	}

	service := knowledge.NewService(db)

	// 传入包含 10 个 HDX 子文档的父级目录
	stats, err := service.ImportDocumentFromDir(docRoot)
	if err != nil {
		t.Fatalf("ImportDocumentFromDir failed on parent dir: %v", err)
	}

	t.Logf("Batch Import Stats: TotalDocs=%d, LeafLogs=%d, LeafAlarms=%d, UniqueKnowledge=%d, VersionMappings=%d, Duration=%v",
		stats.TotalDocuments, stats.LeafLogCount, stats.LeafAlarmCount, stats.UniqueKnowledgeAdded, stats.VersionMappingsAdded, stats.Duration)

	if stats.TotalDocuments <= 0 {
		t.Errorf("expected > 0 documents imported, got %d", stats.TotalDocuments)
	}
	if stats.LeafLogCount <= 0 {
		t.Errorf("expected > 0 leaf logs")
	}
	if stats.LeafAlarmCount <= 0 {
		t.Errorf("expected > 0 leaf alarms")
	}
	if stats.UniqueKnowledgeAdded <= 0 {
		t.Errorf("expected > 0 unique knowledge added")
	}
	if len(stats.ImportedDocs) != stats.TotalDocuments {
		t.Errorf("expected %d imported docs in list, got %d", stats.TotalDocuments, len(stats.ImportedDocs))
	}

	// 验证所有导入的文档已写入 Document 列表
	docs, err := service.GetDocumentList()
	if err != nil {
		t.Fatalf("GetDocumentList failed: %v", err)
	}
	if len(docs) != stats.TotalDocuments {
		t.Errorf("expected %d documents in db, got %d", stats.TotalDocuments, len(docs))
	}
}

func TestConcurrentDocumentImport(t *testing.T) {
	logger.Init("debug", "console")

	usgDir := filepath.FromSlash("../../原始产品文档/HiSecEngine USG6000F, USG6000G_V600R025C10_01_zh_AZQ01091")
	ceDir := filepath.FromSlash("../../原始产品文档/CloudEngine 16800_V200R025C00_05_zh_AZP10147")
	if _, err := os.Stat(usgDir); os.IsNotExist(err) {
		t.Skipf("sample doc dir not found, skipping test")
	}
	if _, err := os.Stat(ceDir); os.IsNotExist(err) {
		t.Skipf("sample doc dir not found, skipping test")
	}

	tmpDir, err := os.MkdirTemp("", "knowledge_concurrent_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "concurrent_knowledge.db")
	db, err := storage.InitKnowledgeDB(dbPath)
	if err != nil {
		t.Fatalf("init test db failed: %v", err)
	}

	service := knowledge.NewService(db)

	stat1Ch := make(chan *knowledge.ImportStats, 1)
	stat2Ch := make(chan *knowledge.ImportStats, 1)
	errCh := make(chan error, 2)

	go func() {
		st, err := service.ImportDocumentFromDir(usgDir)
		stat1Ch <- st
		errCh <- err
	}()
	go func() {
		st, err := service.ImportDocumentFromDir(ceDir)
		stat2Ch <- st
		errCh <- err
	}()

	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("concurrent import failed: %v", err)
		}
	}

	st1 := <-stat1Ch
	st2 := <-stat2Ch
	if st1 == nil || st1.TotalDocuments != 1 {
		t.Errorf("expected 1 doc for st1, got %+v", st1)
	}
	if st2 == nil || st2.TotalDocuments != 1 {
		t.Errorf("expected 1 doc for st2, got %+v", st2)
	}
}

func TestBatchKnowledgeQueries(t *testing.T) {
	logger.Init("debug", "console")

	tmpDir, err := os.MkdirTemp("", "knowledge_batch_query_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "batch_query_knowledge.db")
	db, err := storage.InitKnowledgeDB(dbPath)
	if err != nil {
		t.Fatalf("init test db failed: %v", err)
	}

	service := knowledge.NewService(db)

	// 1. 空 ID 列表
	list0, err := service.GetKnowledgeByIDs(nil)
	if err != nil || len(list0) != 0 {
		t.Errorf("expected empty slice, got err=%v, len=%d", err, len(list0))
	}
	map0, err := service.GetKnowledgeMapByIDs([]uint{})
	if err != nil || len(map0) != 0 {
		t.Errorf("expected empty map, got err=%v, len=%d", err, len(map0))
	}

	// 2. 插入测试数据
	k1 := &model.Knowledge{
		Module:      "BGP",
		Brief:       "BGP_STATE_CHANGE",
		ContentHash: "hash1",
		Versions: []model.KnowledgeVersionMapping{
			{ProductType: "CE16800", ProductVersion: "V200"},
		},
	}
	k2 := &model.Knowledge{
		Module:      "OSPF",
		Brief:       "OSPF_NBR_DOWN",
		ContentHash: "hash2",
		Versions: []model.KnowledgeVersionMapping{
			{ProductType: "CE16800", ProductVersion: "V200"},
		},
	}
	db.Create(k1)
	db.Create(k2)

	// 3. 批量查询
	list, err := service.GetKnowledgeByIDs([]uint{k1.ID, k2.ID, 9999})
	if err != nil {
		t.Fatalf("GetKnowledgeByIDs failed: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("expected 2 items, got %d", len(list))
	}

	// 4. 批量 Map 查询
	kMap, err := service.GetKnowledgeMapByIDs([]uint{k1.ID, k2.ID, 9999})
	if err != nil {
		t.Fatalf("GetKnowledgeMapByIDs failed: %v", err)
	}
	if len(kMap) != 2 {
		t.Errorf("expected 2 items in map, got %d", len(kMap))
	}
	if kMap[k1.ID] == nil || len(kMap[k1.ID].Versions) == 0 {
		t.Errorf("expected k1 preloaded with versions, got %+v", kMap[k1.ID])
	}
	if kMap[k2.ID] == nil || kMap[k2.ID].Brief != "OSPF_NBR_DOWN" {
		t.Errorf("expected k2 brief OSPF_NBR_DOWN, got %+v", kMap[k2.ID])
	}
}

func TestImportFunctionalOptions(t *testing.T) {
	logger.Init("debug", "console")

	tmpDir, err := os.MkdirTemp("", "knowledge_opts_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "opts_knowledge.db")
	db, err := storage.InitKnowledgeDB(dbPath)
	if err != nil {
		t.Fatalf("init test db failed: %v", err)
	}

	service := knowledge.NewService(db)

	usgDir := filepath.FromSlash("../../原始产品文档/HiSecEngine USG6000F, USG6000G_V600R025C10_01_zh_AZQ01091")
	if _, err := os.Stat(usgDir); os.IsNotExist(err) {
		t.Skipf("sample doc dir not found, skipping test")
	}

	// 1. 使用 Functional Options 导入 (WithTracker + WithConflictMode)
	tracker1 := progress.NewJobTracker("test_job_1", "task_1", "hdx", knowledge.HDXImportStages)
	stats1, err := service.ImportDocumentFromDir(usgDir, knowledge.WithConflictMode("overwrite"), knowledge.WithTracker(tracker1))
	if err != nil {
		t.Fatalf("import with functional options failed: %v", err)
	}
	if stats1.LeafLogCount <= 0 {
		t.Errorf("expected > 0 leaf logs, got %d", stats1.LeafLogCount)
	}
	snap1 := tracker1.GetSnapshot()
	if snap1.Status != progress.JobCompleted {
		t.Errorf("expected tracker status %s, got %s", progress.JobCompleted, snap1.Status)
	}

	// 2. 测试 WithConflictMode("skip") 跳过已导入文档
	tracker2 := progress.NewJobTracker("test_job_2", "task_2", "hdx", knowledge.HDXImportStages)
	stats2, err := service.ImportDocumentFromDir(usgDir, knowledge.WithConflictMode("skip"), knowledge.WithTracker(tracker2))
	if err != nil {
		t.Fatalf("import with skip option failed: %v", err)
	}
	if len(stats2.SkippedDocs) != 1 {
		t.Errorf("expected 1 skipped doc, got %d", len(stats2.SkippedDocs))
	}

	// 3. 测试向前兼容的传统参数传入方式 (string + *progress.JobTracker)
	tracker3 := progress.NewJobTracker("test_job_3", "task_3", "hdx", knowledge.HDXImportStages)
	stats3, err := service.ImportDocumentFromDir(usgDir, "skip", tracker3)
	if err != nil {
		t.Fatalf("import with legacy args failed: %v", err)
	}
	if len(stats3.SkippedDocs) != 1 {
		t.Errorf("expected 1 skipped doc with legacy args, got %d", len(stats3.SkippedDocs))
	}
}




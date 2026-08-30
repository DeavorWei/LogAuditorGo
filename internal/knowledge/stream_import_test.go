package knowledge_test

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"logauditorgo/internal/knowledge"
	"logauditorgo/internal/model"
	"logauditorgo/pkg/logger"
	"logauditorgo/pkg/progress"
)

// ---------------------------------------------------------------------------
// 最小可用的 HDX 文档包素材
// ---------------------------------------------------------------------------

const streamProfileXML = `<?xml version="1.0" encoding="UTF-8"?>
<profile>
  <libId>AZN1024P</libId>
  <libVersion>07</libVersion>
  <libName>Stream Import Test Doc</libName>
  <productType>CloudEngine 16800</productType>
  <productVersion>V200R024C00</productVersion>
  <language>zh</language>
  <topicNumber>3</topicNumber>
  <navi>resources/navi.xml</navi>
</profile>`

const streamNaviXML = `<?xml version="1.0" encoding="UTF-8"?>
<topics>
  <topic id="ZH-CN_LOGREF_ROOT" txt="BGP日志">
    <topic id="ZH-CN_LOGREF_0001" txt="BGP/4/BGP_AUTH_FAILED" url="log_bgp.html" />
  </topic>
  <topic id="ZH-CN_ALARM_ROOT" txt="AAA告警">
    <topic id="ZH-CN_ALARMREF_0002" txt="hwRadiusAuthServerDown" url="alarm_radius.html#anchor1" />
  </topic>
</topics>`

const streamLogHTML = `<!DOCTYPE html>
<html>
<head>
  <meta name="DC.Title" content="BGP/4/BGP_AUTH_FAILED" />
  <meta name="DC.subject" content="BGP_AUTH_FAILED" />
  <meta name="DC.Creator" content="BGP" />
</head>
<body>
  <div class="logRefMessage"><div class="logRefMessagebody">BGP session authentication failed. (PeerID=[PeerIP])</div></div>
  <div class="logRefDesc"><div class="logRefDescbody">显示BGP会话认证失败的信息。</div></div>
  <div class="logRefCause"><div class="logRefCausebody">BGP会话两端的安全配置不一致。</div></div>
  <div class="section"><h4 class="sectiontitle">处理步骤</h4><p>执行 display bgp peer 检查对等体状态。</p></div>
</body>
</html>`

const streamAlarmHTML = `<!DOCTYPE html>
<html>
<head>
  <meta name="DC.Title" content="hwRadiusAuthServerDown" />
</head>
<body>
  <div class="section"><h4 class="sectiontitle">Trap Buffer 解释</h4><p>RADIUS 认证服务器无响应或已宕机。</p></div>
  <div class="section">
    <h4 class="sectiontitle">Trap属性</h4>
    <table>
      <tr><td>OID</td><td>1.3.6.1.4.1.2011.5.25.40.15.2.2.1.2</td></tr>
      <tr><td>MIB</td><td>HUAWEI-AAA-MIB</td></tr>
      <tr><td>告警级别</td><td>重要 (Major)</td></tr>
    </table>
  </div>
  <div class="impactonsystem"><div class="impactonsystembody">终端用户将无法正常通过 RADIUS 进行身份鉴权。</div></div>
  <div class="section"><h4 class="sectiontitle">处理步骤</h4><p>在交换机上 ping 服务器 IP 测试连通性。</p></div>
</body>
</html>`

func sampleDocFiles() map[string]string {
	return map[string]string{
		"profile.xml":                 streamProfileXML,
		"resources/navi.xml":          streamNaviXML,
		"resources/log_bgp.html":      streamLogHTML,
		"resources/alarm_radius.html": streamAlarmHTML,
	}
}

func writeSampleDir(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatalf("mkdir for %s failed: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatalf("write %s failed: %v", rel, err)
		}
	}
	return root
}

func writeSampleArchive(t *testing.T, files map[string]string) string {
	t.Helper()
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create zip entry %s failed: %v", name, err)
		}
		if _, err := w.Write([]byte(files[name])); err != nil {
			t.Fatalf("write zip entry %s failed: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip writer failed: %v", err)
	}

	path := filepath.Join(t.TempDir(), "sample.hdx")
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatalf("write archive failed: %v", err)
	}
	return path
}

// streamTestDBSeq 为每个测试库生成唯一的名字，保证内存库之间互不共享
var streamTestDBSeq int64

// newStreamTestService 建立一个完全独立的测试数据库。
//
// 这里刻意绕过 storage.InitKnowledgeDB：它是"全局单例"，多次调用返回同一个 *gorm.DB，
// 无法用于需要对比两个独立库（目录导入 vs 压缩包导入）的等价性验证。
//
// 使用带唯一名的私有内存库，避免 SQLite 的 -wal / -shm 残留文件在 Windows 上
// 干扰 t.TempDir() 的自动清理。
func newStreamTestService(t *testing.T) (*knowledge.Service, *gorm.DB) {
	t.Helper()

	id := atomic.AddInt64(&streamTestDBSeq, 1)
	dsn := fmt.Sprintf("file:stream_test_%d?mode=memory&cache=private&_pragma=busy_timeout(10000)&_pragma=foreign_keys(1)", id)

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db failed: %v", err)
	}
	if err := db.AutoMigrate(
		&model.Document{},
		&model.Knowledge{},
		&model.KnowledgeVersionMapping{},
		&model.TaskInfo{},
	); err != nil {
		t.Fatalf("auto migrate test db failed: %v", err)
	}

	// 显式关闭底层 sqlite 句柄：否则 TempDir 清理时 db 文件仍被占用（Windows 下会失败）
	t.Cleanup(func() {
		if sqlDB, cerr := db.DB(); cerr == nil {
			_ = sqlDB.Close()
		}
	})
	return knowledge.NewService(db), db
}

func contentHashes(t *testing.T, db *gorm.DB) []string {
	t.Helper()
	var hashes []string
	if err := db.Model(&model.Knowledge{}).Order("content_hash asc").Pluck("content_hash", &hashes).Error; err != nil {
		t.Fatalf("query content hashes failed: %v", err)
	}
	return hashes
}

// TestStreamImportFromArchive 压缩包零磁盘流式导入：结果正确、来源可溯源、句柄及时释放
func TestStreamImportFromArchive(t *testing.T) {
	logger.Init("error", "console")

	svc, db := newStreamTestService(t)
	// 用独立的解压工作目录，便于断言"零磁盘占用"
	extractDir := t.TempDir()
	svc.SetExtractDir(extractDir)

	arcPath := writeSampleArchive(t, sampleDocFiles())

	tr := progress.NewJobTracker("stream_import", "", "hdx", knowledge.HDXImportStages)
	stats, err := svc.ImportDocumentsFromPaths([]string{arcPath}, "overwrite", tr)
	if err != nil {
		t.Fatalf("stream import from archive failed: %v", err)
	}

	if stats.LeafLogCount != 1 || stats.LeafAlarmCount != 1 {
		t.Errorf("expected 1 leaf log and 1 leaf alarm, got %d/%d", stats.LeafLogCount, stats.LeafAlarmCount)
	}
	if stats.UniqueKnowledgeAdded != 2 {
		t.Errorf("expected 2 unique knowledge added, got %d", stats.UniqueKnowledgeAdded)
	}

	// KB-16: FilePath 必须记录原始 .hdx 绝对路径，而不是失效的临时解压目录
	var doc model.Document
	if err := db.Where("lib_id = ?", "AZN1024P").First(&doc).Error; err != nil {
		t.Fatalf("query imported document failed: %v", err)
	}
	if doc.FilePath != arcPath {
		t.Errorf("expected FilePath %s, got %s", arcPath, doc.FilePath)
	}

	// 零磁盘占用：整个流式导入不应在解压工作目录留下任何文件
	if entries, err := os.ReadDir(extractDir); err != nil || len(entries) != 0 {
		t.Errorf("expected empty extract dir (zero disk usage), got %d entries (err: %v)", len(entries), err)
	}

	// 生命周期保障：导入结束后原始压缩包必须可自由重命名（Windows 下句柄未释放会失败）
	renamed := arcPath + ".renamed"
	if err := os.Rename(arcPath, renamed); err != nil {
		t.Errorf("archive handle was not released after import: %v", err)
	}

	// 进度阶段体感：UPLOAD 阶段应改写为流式读取文案，并直接推至 100%
	snap := tr.GetSnapshot()
	stageRenamed := false
	indexLogFound := false
	for _, s := range snap.Stages {
		if s.Key == "UPLOAD" && strings.Contains(s.Name, "流式读取") {
			stageRenamed = true
		}
	}
	for _, l := range snap.Logs {
		if strings.Contains(l.Message, "零磁盘占用") {
			indexLogFound = true
		}
	}
	if !stageRenamed {
		t.Errorf("expected UPLOAD stage renamed to streaming wording, got stages: %+v", snap.Stages)
	}
	if !indexLogFound {
		t.Errorf("expected log about zero-disk index building, got logs: %+v", snap.Logs)
	}
}

// TestStreamImportMatchesExtractedImport 核心回归：
// 同一份文档素材，压缩包流式导入与已解压目录导入必须产出完全相同的知识条目
func TestStreamImportMatchesExtractedImport(t *testing.T) {
	logger.Init("error", "console")

	files := sampleDocFiles()

	// A: 已解压目录导入（传统链路）
	svcDir, dbDir := newStreamTestService(t)
	statsDir, err := svcDir.ImportDocumentFromDir(writeSampleDir(t, files))
	if err != nil {
		t.Fatalf("import from extracted dir failed: %v", err)
	}

	// B: 压缩包流式导入
	svcArc, dbArc := newStreamTestService(t)
	extractDir := t.TempDir()
	svcArc.SetExtractDir(extractDir)

	statsArc, err := svcArc.ImportDocumentFromDir(writeSampleArchive(t, files))
	if err != nil {
		t.Fatalf("import from archive failed: %v", err)
	}

	if statsDir.LeafLogCount != statsArc.LeafLogCount || statsDir.LeafAlarmCount != statsArc.LeafAlarmCount {
		t.Errorf("leaf counts mismatch: dir=(%d logs, %d alarms), archive=(%d logs, %d alarms)",
			statsDir.LeafLogCount, statsDir.LeafAlarmCount, statsArc.LeafLogCount, statsArc.LeafAlarmCount)
	}
	if statsDir.UniqueKnowledgeAdded != statsArc.UniqueKnowledgeAdded {
		t.Errorf("unique knowledge mismatch: dir=%d, archive=%d",
			statsDir.UniqueKnowledgeAdded, statsArc.UniqueKnowledgeAdded)
	}

	hashDir := contentHashes(t, dbDir)
	hashArc := contentHashes(t, dbArc)
	if !reflect.DeepEqual(hashDir, hashArc) {
		t.Errorf("content hash sets mismatch:\n dir=%v\n archive=%v", hashDir, hashArc)
	}
}

// TestStreamImportRealSampleArchive 用仓库内的真实 HDX 样本做端到端验证（样本不存在时跳过）
func TestStreamImportRealSampleArchive(t *testing.T) {
	logger.Init("error", "console")

	matches, _ := filepath.Glob(filepath.FromSlash("../../原始产品文档/*.hdx"))
	if len(matches) == 0 {
		t.Skipf("no sample .hdx found under 原始产品文档, skipping")
	}
	arcPath := matches[0]

	svc, db := newStreamTestService(t)
	extractDir := t.TempDir()
	svc.SetExtractDir(extractDir)

	tr := progress.NewJobTracker("stream_real", "", "hdx", knowledge.HDXImportStages)
	stats, err := svc.ImportDocumentsFromPaths([]string{arcPath}, "overwrite", tr)
	if err != nil {
		t.Fatalf("stream import of real sample %s failed: %v", filepath.Base(arcPath), err)
	}

	t.Logf("real sample imported: %s, leaf logs=%d, leaf alarms=%d, unique knowledge=%d, duration=%v",
		filepath.Base(arcPath), stats.LeafLogCount, stats.LeafAlarmCount, stats.UniqueKnowledgeAdded, stats.Duration)

	if stats.LeafLogCount == 0 {
		t.Errorf("expected > 0 leaf logs from real sample, got 0")
	}

	var doc model.Document
	if err := db.Order("id desc").First(&doc).Error; err != nil {
		t.Fatalf("query imported document failed: %v", err)
	}
	if !strings.HasSuffix(strings.ToLower(doc.FilePath), ".hdx") {
		t.Errorf("expected FilePath to point at the original .hdx, got %s", doc.FilePath)
	}

	// 真实大包同样必须零磁盘占用
	if entries, err := os.ReadDir(extractDir); err != nil || len(entries) != 0 {
		t.Errorf("expected empty extract dir for real sample (zero disk usage), got %d entries (err: %v)", len(entries), err)
	}
}

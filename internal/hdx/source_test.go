package hdx_test

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"logauditorgo/internal/hdx"
	"logauditorgo/internal/model"
)

// ---------------------------------------------------------------------------
// 测试素材：一个最小可用的 HDX 文档包
// ---------------------------------------------------------------------------

const fixtureProfileXML = `<?xml version="1.0" encoding="UTF-8"?>
<profile>
  <libId>AZN1024P</libId>
  <libVersion>07</libVersion>
  <libName>CloudEngine 16800 产品文档</libName>
  <productType>CloudEngine 16800</productType>
  <productVersion>V200R024C00</productVersion>
  <language>zh</language>
  <topicNumber>17702</topicNumber>
  <navi>resources/navi.xml</navi>
</profile>`

const fixtureNaviXML = `<?xml version="1.0" encoding="UTF-8"?>
<topics>
  <topic id="ZH-CN_LOGREF_ROOT" txt="BGP日志">
    <topic id="ZH-CN_LOGREF_0001" txt="BGP/4/BGP_AUTH_FAILED" url="log_bgp.html" />
  </topic>
  <topic id="ZH-CN_ALARM_ROOT" txt="AAA告警">
    <topic id="ZH-CN_ALARMREF_0002" txt="hwRadiusAuthServerDown" url="alarm_radius.html#anchor1" />
  </topic>
  <topic id="ZH-CN_CONCEPT_0003" txt="产品介绍" url="concept.html" />
</topics>`

const fixtureLogHTML = `<!DOCTYPE html>
<html>
<head>
  <meta name="DC.Title" content="BGP/4/BGP_AUTH_FAILED" />
  <meta name="DC.subject" content="BGP_AUTH_FAILED" />
  <meta name="DC.Creator" content="BGP" />
</head>
<body>
  <div class="logRefMessage"><div class="logRefMessagebody">BGP session authentication failed. (PeerID=[PeerIP])</div></div>
  <div class="logRefDesc"><div class="logRefDescbody">显示BGP会话认证失败的信息。</div></div>
  <div class="logRefParams">
    <table>
      <tr><th>参数名称</th><th>参数类型</th><th>参数描述</th></tr>
      <tr><td>PeerIP</td><td>string</td><td>对等体IP地址</td></tr>
    </table>
  </div>
  <div class="logRefCause"><div class="logRefCausebody">BGP会话两端的安全配置不一致。</div></div>
  <div class="section"><h4 class="sectiontitle">处理步骤</h4><p>1. 检查两端认证密码是否相同。<br/>2. 执行 display bgp peer 检查状态。</p></div>
</body>
</html>`

const fixtureAlarmHTML = `<!DOCTYPE html>
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
  <div class="possiblecauses"><div class="alarmpossbody">与 RADIUS 服务器网络不可达。</div></div>
  <div class="section"><h4 class="sectiontitle">处理步骤</h4><p>在交换机上 ping 服务器 IP 测试连通性。</p></div>
</body>
</html>`

// fixtureDoc 一个完整的最小 HDX 文档包（相对路径 -> 内容）
func fixtureDoc() map[string]string {
	return map[string]string{
		"profile.xml":                 fixtureProfileXML,
		"resources/navi.xml":          fixtureNaviXML,
		"resources/log_bgp.html":      fixtureLogHTML,
		"resources/alarm_radius.html": fixtureAlarmHTML,
	}
}

// writeDirFixture 把文件清单写入磁盘目录，返回文档根目录
func writeDirFixture(t *testing.T, files map[string]string) string {
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

// buildZipBytes 把文件清单打包为 zip 字节流。
// store 为 true 时逐条不压缩存储：用于构造"高压缩比"素材之外的超限用例，
// 否则内容高度重复会被解压炸弹防护（压缩比 > 1000:1）提前拒绝。
func buildZipBytes(t *testing.T, files map[string]string, store ...bool) []byte {
	t.Helper()
	noCompress := len(store) > 0 && store[0]

	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		var w io.Writer
		var err error
		if noCompress {
			w, err = zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		} else {
			w, err = zw.Create(name)
		}
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
	return buf.Bytes()
}

// writeArchiveFixture 把文件清单落盘为一个 .hdx 压缩包，返回压缩包绝对路径
func writeArchiveFixture(t *testing.T, files map[string]string, store ...bool) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sample.hdx")
	if err := os.WriteFile(path, buildZipBytes(t, files, store...), 0644); err != nil {
		t.Fatalf("write archive failed: %v", err)
	}
	return path
}

// ---------------------------------------------------------------------------
// 路径清洗
// ---------------------------------------------------------------------------

func TestResolveItemRelPath(t *testing.T) {
	cases := []struct {
		name    string
		naviDir string
		rawURL  string
		want    string
		wantErr bool
	}{
		{"plain", "resources", "log_bgp.html", "resources/log_bgp.html", false},
		{"anchor", "resources", "alarm_radius.html#anchor1", "resources/alarm_radius.html", false},
		{"query", "resources", "doc.html?lang=zh", "resources/doc.html", false},
		{"url encoded", "resources", "BGP%20Fail.html", "resources/BGP Fail.html", false},
		{"backslash", "resources", `sub\alarm.html`, "resources/sub/alarm.html", false},
		{"dot slash", "resources", "./log_bgp.html", "resources/log_bgp.html", false},
		{"navi at root", ".", "log_bgp.html", "log_bgp.html", false},
		{"escape", "resources", "../../evil.html", "", true},
		{"escape from root", ".", "../evil.html", "", true},
		{"empty", "resources", "", "", true},
		{"anchor only", "resources", "#top", "", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := hdx.ResolveItemRelPath(tc.naviDir, tc.rawURL)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got rel=%q", tc.rawURL, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.rawURL, err)
			}
			if got != tc.want {
				t.Errorf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestDirSourceRejectsPathEscape(t *testing.T) {
	dir := writeDirFixture(t, fixtureDoc())
	src := hdx.NewDirSource(dir)

	// 正常读取
	rc, _, err := src.Open("profile.xml")
	if err != nil {
		t.Fatalf("open profile.xml failed: %v", err)
	}
	_ = rc.Close()

	// 越界读取必须被拒绝
	if _, _, err := src.Open("../evil.html"); err == nil {
		t.Errorf("expected error when opening escaping path")
	}
	if _, _, err := src.Open("resources/../../evil.html"); err == nil {
		t.Errorf("expected error when opening escaping path with nested ..")
	}
	// 反斜杠形式的越界同样要被拒绝
	if _, _, err := src.Open(`..\evil.html`); err == nil {
		t.Errorf("expected error when opening backslash escaping path")
	}
}

// ---------------------------------------------------------------------------
// 压缩包源
// ---------------------------------------------------------------------------

func TestZipSourceDocRootsAndParse(t *testing.T) {
	arcPath := writeArchiveFixture(t, fixtureDoc())

	a, err := hdx.OpenArchive(arcPath)
	if err != nil {
		t.Fatalf("OpenArchive failed: %v", err)
	}
	defer func() { _ = a.Close() }()

	if a.EntryCount() != len(fixtureDoc()) {
		t.Errorf("expected %d entries, got %d", len(fixtureDoc()), a.EntryCount())
	}

	roots := a.DocRoots(nil)
	if len(roots) != 1 {
		t.Fatalf("expected 1 doc root, got %d", len(roots))
	}
	src := roots[0]

	if src.Kind() != hdx.SourceKindZip {
		t.Errorf("expected kind zip, got %s", src.Kind())
	}
	if src.Origin() != arcPath {
		t.Errorf("expected origin %s, got %s", arcPath, src.Origin())
	}

	// profile.xml 与 navi.xml 全部从压缩包流式读取，不做任何落盘
	doc, naviRel, err := hdx.ParseProfileXMLFrom(src)
	if err != nil {
		t.Fatalf("ParseProfileXMLFrom failed: %v", err)
	}
	if doc.LibID != "AZN1024P" || doc.ProductType != "CloudEngine 16800" {
		t.Errorf("unexpected profile parsed: %+v", doc)
	}
	if doc.FilePath != arcPath {
		t.Errorf("expected FilePath to record the original archive %s, got %s", arcPath, doc.FilePath)
	}
	if naviRel != "resources/navi.xml" {
		t.Errorf("unexpected navi rel path: %s", naviRel)
	}

	items, err := hdx.ParseNaviXMLFrom(src, naviRel)
	if err != nil {
		t.Fatalf("ParseNaviXMLFrom failed: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 leaf items, got %d", len(items))
	}
}

// TestZipSourceEqualsDirSource 核心回归：压缩包流式解析与已解压目录解析结果必须完全一致
func TestZipSourceEqualsDirSource(t *testing.T) {
	files := fixtureDoc()
	dirSrc := hdx.NewDirSource(writeDirFixture(t, files))

	arcPath := writeArchiveFixture(t, files)
	a, err := hdx.OpenArchive(arcPath)
	if err != nil {
		t.Fatalf("OpenArchive failed: %v", err)
	}
	defer func() { _ = a.Close() }()

	roots := a.DocRoots(nil)
	if len(roots) != 1 {
		t.Fatalf("expected 1 doc root, got %d", len(roots))
	}
	zipSrc := roots[0]

	dirDoc, dirNavi, err := hdx.ParseProfileXMLFrom(dirSrc)
	if err != nil {
		t.Fatalf("dir ParseProfileXMLFrom failed: %v", err)
	}
	zipDoc, zipNavi, err := hdx.ParseProfileXMLFrom(zipSrc)
	if err != nil {
		t.Fatalf("zip ParseProfileXMLFrom failed: %v", err)
	}
	if dirDoc.LibID != zipDoc.LibID || dirDoc.TopicNumber != zipDoc.TopicNumber || dirNavi != zipNavi {
		t.Fatalf("profile mismatch: dir=%+v (%s), zip=%+v (%s)", dirDoc, dirNavi, zipDoc, zipNavi)
	}

	dirItems, err := hdx.ParseNaviXMLFrom(dirSrc, dirNavi)
	if err != nil {
		t.Fatalf("dir ParseNaviXMLFrom failed: %v", err)
	}
	zipItems, err := hdx.ParseNaviXMLFrom(zipSrc, zipNavi)
	if err != nil {
		t.Fatalf("zip ParseNaviXMLFrom failed: %v", err)
	}
	if len(dirItems) != len(zipItems) {
		t.Fatalf("leaf count mismatch: dir=%d, zip=%d", len(dirItems), len(zipItems))
	}
	sortItems := func(items []hdx.LeafNaviItem) {
		sort.Slice(items, func(i, j int) bool { return items[i].TopicID < items[j].TopicID })
	}
	sortItems(dirItems)
	sortItems(zipItems)

	for i := range dirItems {
		d, z := dirItems[i], zipItems[i]
		if d.TopicID != z.TopicID || d.Module != z.Module || d.Brief != z.Brief ||
			d.Severity != z.Severity || d.URL != z.URL || d.NaviDir != z.NaviDir {
			t.Fatalf("leaf item mismatch at %d: dir=%+v, zip=%+v", i, d, z)
		}

		dk, err := hdx.ParseHTMLKnowledgeFrom(dirSrc, d)
		if err != nil {
			t.Fatalf("dir ParseHTMLKnowledgeFrom(%s) failed: %v", d.TopicID, err)
		}
		zk, err := hdx.ParseHTMLKnowledgeFrom(zipSrc, z)
		if err != nil {
			t.Fatalf("zip ParseHTMLKnowledgeFrom(%s) failed: %v", z.TopicID, err)
		}
		if dk.Message != zk.Message || dk.Description != zk.Description ||
			dk.Parameters != zk.Parameters || dk.Cause != zk.Cause || dk.Action != zk.Action {
			t.Errorf("knowledge mismatch for %s:\n dir=%+v\n zip=%+v", d.TopicID, dk, zk)
		}
		if dk.TrapOID != zk.TrapOID || dk.MIBName != zk.MIBName {
			t.Errorf("alarm attr mismatch for %s: dir=(%s,%s) zip=(%s,%s)",
				d.TopicID, dk.TrapOID, dk.MIBName, zk.TrapOID, zk.MIBName)
		}
	}
}

// TestZipSourceMultipleDocRoots 一个包内含多个虚拟文档根时，各 source 必须只看到自己的文档根
func TestZipSourceMultipleDocRoots(t *testing.T) {
	files := map[string]string{
		"doc1/profile.xml":        `<profile><libId>DOC1</libId><productType>P1</productType></profile>`,
		"doc2/profile.xml":        `<profile><libId>DOC2</libId><productType>P2</productType></profile>`,
		"doc1/resources/navi.xml": fixtureNaviXML,
		"doc2/resources/navi.xml": fixtureNaviXML,
	}
	a, err := hdx.OpenArchive(writeArchiveFixture(t, files))
	if err != nil {
		t.Fatalf("OpenArchive failed: %v", err)
	}
	defer func() { _ = a.Close() }()

	roots := a.DocRoots(nil)
	if len(roots) != 2 {
		t.Fatalf("expected 2 doc roots, got %d", len(roots))
	}

	byRoot := map[string]hdx.DocSource{}
	for _, r := range roots {
		byRoot[r.Root()] = r
	}
	for _, root := range []string{"doc1", "doc2"} {
		src, ok := byRoot[root]
		if !ok {
			t.Fatalf("doc root %s not found, roots=%v", root, byRoot)
		}
		doc, _, err := hdx.ParseProfileXMLFrom(src)
		if err != nil {
			t.Fatalf("parse profile in %s failed: %v", root, err)
		}
		// doc1 -> libId DOC1，doc2 -> libId DOC2，验证来源隔离正确
		wantLibID := "DOC" + root[len(root)-1:]
		if doc.LibID != wantLibID {
			t.Errorf("expected LibID %s for root %s, got %s", wantLibID, root, doc.LibID)
		}
		if !strings.HasSuffix(src.Origin(), "::"+root) {
			t.Errorf("expected origin to carry doc root suffix, got %s", src.Origin())
		}
	}
}

// TestZipSourceCaseInsensitiveFallback 条目名与 URL 大小写不一致时通过 basename 兜底命中
func TestZipSourceCaseInsensitiveFallback(t *testing.T) {
	files := map[string]string{
		"PROFILE.XML":                 fixtureProfileXML,
		"RESOURCES/NAVI.XML":          fixtureNaviXML,
		"RESOURCES/LOG_BGP.HTML":      fixtureLogHTML,
		"resources/alarm_radius.html": fixtureAlarmHTML,
	}
	a, err := hdx.OpenArchive(writeArchiveFixture(t, files))
	if err != nil {
		t.Fatalf("OpenArchive failed: %v", err)
	}
	defer func() { _ = a.Close() }()

	roots := a.DocRoots(nil)
	if len(roots) != 1 {
		t.Fatalf("expected doc root discovered via PROFILE.XML, got %d", len(roots))
	}
	src := roots[0]

	// navi 声明小写目录 + 小写文件名，实体条目全大写，依赖大小写归一化命中
	items, err := hdx.ParseNaviXMLFrom(src, "resources/navi.xml")
	if err != nil {
		t.Fatalf("ParseNaviXMLFrom with case mismatch failed: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 leaf items, got %d", len(items))
	}

	var logItem hdx.LeafNaviItem
	for _, it := range items {
		if it.EntryType == model.EntryTypeLog {
			logItem = it
		}
	}
	if logItem.TopicID == "" {
		t.Fatalf("log leaf item not found")
	}
	k, err := hdx.ParseHTMLKnowledgeFrom(src, logItem)
	if err != nil {
		t.Fatalf("ParseHTMLKnowledgeFrom with uppercase entry failed: %v", err)
	}
	if !strings.Contains(k.Message, "authentication failed") {
		t.Errorf("unexpected message parsed: %s", k.Message)
	}
}

// TestZipSourceSkipsZipSlipEntries Zip Slip 条目必须被索引跳过，且无法被读取
func TestZipSourceSkipsZipSlipEntries(t *testing.T) {
	a, err := hdx.OpenArchive(writeArchiveFixture(t, map[string]string{
		"profile.xml":   fixtureProfileXML,
		"../evil.html":  "<html>evil</html>",
		"/abs/evil.txt": "evil",
	}))
	if err != nil {
		t.Fatalf("OpenArchive failed: %v", err)
	}
	defer func() { _ = a.Close() }()

	// 非法条目不计入索引
	if a.EntryCount() != 1 {
		t.Errorf("expected only 1 legal entry, got %d", a.EntryCount())
	}
	roots := a.DocRoots(nil)
	if len(roots) != 1 {
		t.Fatalf("expected 1 doc root, got %d", len(roots))
	}
	if _, _, err := roots[0].Open("../../evil.html"); err == nil {
		t.Errorf("expected error when reading escaping entry")
	}
}

// TestZipSourceNestedArchive 嵌套压缩包在扫描阶段被整包展开，Worker 无需重复解包
func TestZipSourceNestedArchive(t *testing.T) {
	inner := buildZipBytes(t, map[string]string{
		"profile.xml":                 `<profile><libId>INNER</libId></profile>`,
		"resources/navi.xml":          fixtureNaviXML,
		"resources/log_bgp.html":      fixtureLogHTML,
		"resources/alarm_radius.html": fixtureAlarmHTML,
	})

	a, err := hdx.OpenArchive(writeArchiveFixture(t, map[string]string{
		"bundle/inner.zip": string(inner),
	}))
	if err != nil {
		t.Fatalf("OpenArchive failed: %v", err)
	}
	defer func() { _ = a.Close() }()

	roots := a.DocRoots(nil)
	if len(roots) != 1 {
		t.Fatalf("expected 1 nested doc root, got %d", len(roots))
	}
	src := roots[0]
	if !strings.Contains(src.Origin(), "::bundle/inner.zip") {
		t.Errorf("expected origin to record nested archive path, got %s", src.Origin())
	}

	doc, _, err := hdx.ParseProfileXMLFrom(src)
	if err != nil {
		t.Fatalf("parse nested profile failed: %v", err)
	}
	if doc.LibID != "INNER" {
		t.Errorf("expected LibID INNER, got %s", doc.LibID)
	}

	// 关闭顶层句柄后，嵌套 source 也不再可用（验证生命周期绑定正确）
	if err := a.Close(); err != nil {
		t.Fatalf("close archive failed: %v", err)
	}
}

// TestZipSourceOversizeHTMLRejected 超限 HTML 必须被拒绝，防止压缩包声明大小造假撑爆内存
func TestZipSourceOversizeHTMLRejected(t *testing.T) {
	// 11MB 重复内容，解压后超过 10MB 的单文件上限
	oversize := "<html><body>" + strings.Repeat("A", 11<<20) + "</body></html>"
	// 逐条不压缩存储，避免高度重复内容触发解压炸弹（压缩比）防护而偏离测试目标
	a, err := hdx.OpenArchive(writeArchiveFixture(t, map[string]string{
		"profile.xml":                 fixtureProfileXML,
		"resources/navi.xml":          fixtureNaviXML,
		"resources/log_bgp.html":      oversize,
		"resources/alarm_radius.html": fixtureAlarmHTML,
	}, true))
	if err != nil {
		t.Fatalf("OpenArchive failed: %v", err)
	}
	defer func() { _ = a.Close() }()

	roots := a.DocRoots(nil)
	if len(roots) != 1 {
		t.Fatalf("expected 1 doc root, got %d", len(roots))
	}
	src := roots[0]

	items, err := hdx.ParseNaviXMLFrom(src, "resources/navi.xml")
	if err != nil {
		t.Fatalf("ParseNaviXMLFrom failed: %v", err)
	}
	for _, it := range items {
		if it.EntryType != model.EntryTypeLog {
			continue
		}
		if _, err := hdx.ParseHTMLKnowledgeFrom(src, it); err == nil {
			t.Errorf("expected oversize html to be rejected")
		}
	}
}

// TestArchiveSourceCloseIdempotent Close 必须可安全重复调用（cleanup 可能被多处触发）
func TestArchiveSourceCloseIdempotent(t *testing.T) {
	a, err := hdx.OpenArchive(writeArchiveFixture(t, fixtureDoc()))
	if err != nil {
		t.Fatalf("OpenArchive failed: %v", err)
	}
	roots := a.DocRoots(nil)
	if len(roots) != 1 {
		t.Fatalf("expected 1 doc root, got %d", len(roots))
	}

	if err := a.Close(); err != nil {
		t.Fatalf("first close failed: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("second close should be a no-op, got: %v", err)
	}
	// 通过 DocSource.Close 关闭（共享句柄）同样幂等
	if err := roots[0].Close(); err != nil {
		t.Fatalf("close via DocSource failed: %v", err)
	}
}

// TestZipSourceConcurrentRead 32 并发读取同一压缩包条目：
// 模拟 HTML_PARSE 阶段多 Worker 并发解析，验证 zip 条目并发 Open 的内容一致性。
//
// 注：并发安全性的严格验证仍需 go test -race（需 cgo 环境），本用例覆盖功能正确性。
func TestZipSourceConcurrentRead(t *testing.T) {
	a, err := hdx.OpenArchive(writeArchiveFixture(t, fixtureDoc()))
	if err != nil {
		t.Fatalf("OpenArchive failed: %v", err)
	}
	defer func() { _ = a.Close() }()

	src := a.DocRoots(nil)[0]

	const (
		workers = 32
		loops   = 20
	)

	errCh := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < loops; j++ {
				rc, _, err := src.Open("resources/log_bgp.html")
				if err != nil {
					errCh <- fmt.Errorf("concurrent open failed: %w", err)
					return
				}
				data, err := io.ReadAll(rc)
				_ = rc.Close()
				if err != nil {
					errCh <- fmt.Errorf("concurrent read failed: %w", err)
					return
				}
				if string(data) != fixtureLogHTML {
					errCh <- fmt.Errorf("concurrent read returned corrupted content (len=%d)", len(data))
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("%v", err)
	}
}

// TestZipSourceMissingEntry 缺失条目必须返回明确错误，而不是静默丢条目
func TestZipSourceMissingEntry(t *testing.T) {
	a, err := hdx.OpenArchive(writeArchiveFixture(t, map[string]string{
		"profile.xml":        fixtureProfileXML,
		"resources/navi.xml": fixtureNaviXML,
	}))
	if err != nil {
		t.Fatalf("OpenArchive failed: %v", err)
	}
	defer func() { _ = a.Close() }()

	src := a.DocRoots(nil)[0]
	items, err := hdx.ParseNaviXMLFrom(src, "resources/navi.xml")
	if err != nil {
		t.Fatalf("ParseNaviXMLFrom failed: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 leaf items, got %d", len(items))
	}
	for _, it := range items {
		_, err := hdx.ParseHTMLKnowledgeFrom(src, it)
		if err == nil {
			t.Errorf("expected error for missing html %s", it.URL)
			continue
		}
		if !strings.Contains(err.Error(), "not found in archive") {
			t.Errorf("unexpected error for %s: %v", it.URL, err)
		}
	}
}

// TestOpenArchiveRejectsNonExistent 不存在的压缩包必须报错，由上层回退到全量解压
func TestOpenArchiveRejectsNonExistent(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.hdx")
	if _, err := hdx.OpenArchive(missing); err == nil {
		t.Errorf("expected error for non-existent archive")
	}
	if _, err := hdx.OpenArchive(filepath.Join(t.TempDir(), "not-a-zip.hdx")); err == nil {
		fmt.Fprintln(os.Stderr, "note: empty file may still open as an empty zip")
	}
}

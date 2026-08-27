package hdx_test

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"logauditorgo/internal/hdx"
	"logauditorgo/internal/model"
	"logauditorgo/pkg/progress"
)

func TestCharsetDecode(t *testing.T) {
	// 1. UTF-8
	utf8Str := "华为CloudEngine交换机日志"
	decoded, err := hdx.DecodeGBK([]byte(utf8Str))
	if err != nil {
		t.Fatalf("decode utf-8 failed: %v", err)
	}
	if decoded != utf8Str {
		t.Errorf("expected '%s', got '%s'", utf8Str, decoded)
	}

	// 2. GBK "华为"
	gbkBytes := []byte{0xbb, 0xaa, 0xce, 0xaa}
	decodedGBK, err := hdx.DecodeGBK(gbkBytes)
	if err != nil {
		t.Fatalf("decode gbk failed: %v", err)
	}
	if decodedGBK != "华为" {
		t.Errorf("expected '华为', got '%s'", decodedGBK)
	}

	// 3. 空字节处理
	emptyDecoded, err := hdx.DecodeGBK(nil)
	if err != nil || emptyDecoded != "" {
		t.Errorf("expected empty string without error, got '%s', err: %v", emptyDecoded, err)
	}
}

func TestParseProfileXML(t *testing.T) {
	tmpDir := t.TempDir()

	xmlContent := `<?xml version="1.0" encoding="UTF-8"?>
<profile>
  <libId>AZN1024P</libId>
  <libVersion>07</libVersion>
  <libName>CloudEngine 16800 产品文档</libName>
  <productType>CloudEngine 16800</productType>
  <productVersion>V200R024C00</productVersion>
  <issueDate>2026-07-27</issueDate>
  <language>zh</language>
  <topicNumber>17702</topicNumber>
  <navi>resources/navi.xml</navi>
</profile>`

	profilePath := filepath.Join(tmpDir, "profile.xml")
	if err := os.WriteFile(profilePath, []byte(xmlContent), 0644); err != nil {
		t.Fatalf("write profile.xml failed: %v", err)
	}

	doc, naviRelPath, err := hdx.ParseProfileXML(tmpDir)
	if err != nil {
		t.Fatalf("ParseProfileXML failed: %v", err)
	}

	if doc.LibID != "AZN1024P" {
		t.Errorf("expected LibID AZN1024P, got %s", doc.LibID)
	}
	if doc.ProductType != "CloudEngine 16800" {
		t.Errorf("expected ProductType CloudEngine 16800, got %s", doc.ProductType)
	}
	if doc.TopicNumber != 17702 {
		t.Errorf("expected TopicNumber 17702, got %d", doc.TopicNumber)
	}
	if naviRelPath != "resources/navi.xml" {
		t.Errorf("expected navi path resources/navi.xml, got %s", naviRelPath)
	}
}

func TestParseNaviXMLAndLeafFiltering(t *testing.T) {
	tmpDir := t.TempDir()
	resDir := filepath.Join(tmpDir, "resources")
	if err := os.MkdirAll(resDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	naviContent := `<?xml version="1.0" encoding="UTF-8"?>
<topics>
  <!-- 容器节点：有子 topic，即便 ID 包含 LOGREF 也必须被当作容器跳过 -->
  <topic id="ZH-CN_LOGREF_0000000001" txt="局域网与城域网接入日志" url="dc_lan_log.html">
    <!-- 有效叶子日志节点 -->
    <topic id="ZH-CN_LOGREF_0000000002" txt="BGP/4/BGP_AUTH_FAILED" url="dc_bgp_auth_fail.html" />
    <!-- 非法级别叶子节点 (Config 不是 1~8) 应被过滤 -->
    <topic id="ZH-CN_LOGREF_0000000003" txt="BGP/Config/OVERVIEW" url="dc_bgp_overview.html" />
    <!-- 空 URL 叶子节点应被过滤 -->
    <topic id="ZH-CN_LOGREF_0000000004" txt="BGP/4/BGP_PEER_DOWN" url="" />
  </topic>
  <!-- 容器告警节点 -->
  <topic id="ZH-CN_ALARM_ROOT" txt="AAA告警">
    <!-- 有效告警节点 -->
    <topic id="ZH-CN_ALARMREF_0000000005" txt="hwRadiusAuthServerDown" url="alarm_radius_down.html#anchor1" />
  </topic>
  <!-- 普通说明节点（非 LOGREF 非 ALARMREF），应被跳过 -->
  <topic id="ZH-CN_CONCEPT_0001" txt="产品介绍" url="concept.html" />
</topics>`

	naviPath := filepath.Join(resDir, "navi.xml")
	if err := os.WriteFile(naviPath, []byte(naviContent), 0644); err != nil {
		t.Fatalf("write navi.xml failed: %v", err)
	}

	items, err := hdx.ParseNaviXML(tmpDir, "resources/navi.xml")
	if err != nil {
		t.Fatalf("ParseNaviXML failed: %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("expected exactly 2 valid leaf items, got %d", len(items))
	}

	// 校验第 1 条日志节点
	logItem := items[0]
	if logItem.EntryType != model.EntryTypeLog {
		t.Errorf("expected EntryTypeLog, got %v", logItem.EntryType)
	}
	if logItem.Module != "BGP" || logItem.Severity != 4 || logItem.Brief != "BGP_AUTH_FAILED" {
		t.Errorf("unexpected log item parsed: %+v", logItem)
	}
	if logItem.URL != "dc_bgp_auth_fail.html" {
		t.Errorf("unexpected URL: %s", logItem.URL)
	}

	// 校验第 2 条告警节点
	alarmItem := items[1]
	if alarmItem.EntryType != model.EntryTypeAlarm {
		t.Errorf("expected EntryTypeAlarm, got %v", alarmItem.EntryType)
	}
	if alarmItem.Module != "AAA" {
		t.Errorf("expected inferred module AAA, got %s", alarmItem.Module)
	}
	if alarmItem.Brief != "hwRadiusAuthServerDown" {
		t.Errorf("expected brief hwRadiusAuthServerDown, got %s", alarmItem.Brief)
	}
}

func TestParseHTMLKnowledgeLogAndAlarm(t *testing.T) {
	tmpDir := t.TempDir()
	resDir := filepath.Join(tmpDir, "resources")
	if err := os.MkdirAll(resDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	// 1. 创建 LogRef HTML
	logHTML := `<!DOCTYPE html>
<html>
<head>
  <meta name="DC.Type" content="logRef" />
  <meta name="DC.Title" content="BGP/4/BGP_AUTH_FAILED" />
  <meta name="DC.subject" content="BGP_AUTH_FAILED" />
  <meta name="DC.Creator" content="BGP" />
</head>
<body>
  <div class="logRefMessage"><div class="logRefMessagebody">BGP session authentication failed. (PeerID=[PeerIP], ReturnCode=[Code])</div></div>
  <div class="logRefDesc"><div class="logRefDescbody">显示BGP会话认证失败的信息。</div></div>
  <div class="logRefParams">
    <table>
      <tr><th>参数名称</th><th>参数类型</th><th>参数描述</th></tr>
      <tr><td>PeerIP</td><td>string</td><td>对等体IP地址</td></tr>
      <tr><td>Code</td><td>int</td><td>认证错误代码</td></tr>
    </table>
  </div>
  <div class="logRefCause"><div class="logRefCausebody">BGP会话两端的安全配置不一致。</div></div>
  <div class="section"><h4 class="sectiontitle">处理步骤</h4><p>1. 检查两端 MD5 或 Keychain 认证密码是否相同。<br/>2. 执行 display bgp peer 检查对等体状态。</p></div>
</body>
</html>`

	logPath := filepath.Join(resDir, "log_bgp_auth.html")
	if err := os.WriteFile(logPath, []byte(logHTML), 0644); err != nil {
		t.Fatalf("write log html failed: %v", err)
	}

	leafLog := hdx.LeafNaviItem{
		EntryType: model.EntryTypeLog,
		TopicID:   "ZH-CN_LOGREF_0001",
		Module:    "BGP",
		Severity:  4,
		Brief:     "BGP_AUTH_FAILED",
		URL:       "log_bgp_auth.html",
		NaviDir:   "resources",
	}

	kLog, err := hdx.ParseHTMLKnowledge(tmpDir, leafLog)
	if err != nil {
		t.Fatalf("ParseHTMLKnowledge for Log failed: %v", err)
	}

	if kLog.Module != "BGP" || kLog.Brief != "BGP_AUTH_FAILED" || kLog.Severity != 4 {
		t.Errorf("mismatched log metadata: %+v", kLog)
	}
	if !strings.Contains(kLog.Message, "authentication failed") {
		t.Errorf("message not extracted: %s", kLog.Message)
	}
	if !strings.Contains(kLog.Parameters, "对等体IP地址") {
		t.Errorf("3-column parameters not extracted correctly: %s", kLog.Parameters)
	}
	if !strings.Contains(kLog.Action, "display bgp peer") {
		t.Errorf("action not extracted: %s", kLog.Action)
	}

	// 2. 创建 AlarmRef HTML
	alarmHTML := `<!DOCTYPE html>
<html>
<head>
  <meta name="DC.Type" content="alarmref" />
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
      <tr><td>告警ID</td><td>0x08520001</td></tr>
    </table>
  </div>
  <div class="impactonsystem"><div class="impactonsystembody">终端用户将无法正常通过 RADIUS 进行身份鉴权。</div></div>
  <div class="possiblecauses"><div class="alarmpossbody">与 RADIUS 服务器网络不可达，或认证服务进程停止。</div></div>
  <div class="section"><h4 class="sectiontitle">处理步骤</h4><p>在交换机上 ping 服务器 IP 测试连通性。</p></div>
</body>
</html>`

	alarmPath := filepath.Join(resDir, "alarm_radius.html")
	if err := os.WriteFile(alarmPath, []byte(alarmHTML), 0644); err != nil {
		t.Fatalf("write alarm html failed: %v", err)
	}

	leafAlarm := hdx.LeafNaviItem{
		EntryType: model.EntryTypeAlarm,
		TopicID:   "ZH-CN_ALARMREF_0002",
		Module:    "AAA",
		Severity:  4,
		Brief:     "hwRadiusAuthServerDown",
		URL:       "alarm_radius.html#anchor1",
		NaviDir:   "resources",
	}

	kAlarm, err := hdx.ParseHTMLKnowledge(tmpDir, leafAlarm)
	if err != nil {
		t.Fatalf("ParseHTMLKnowledge for Alarm failed: %v", err)
	}

	if kAlarm.TrapOID != "1.3.6.1.4.1.2011.5.25.40.15.2.2.1.2" {
		t.Errorf("expected TrapOID, got %s", kAlarm.TrapOID)
	}
	if kAlarm.MIBName != "HUAWEI-AAA-MIB" {
		t.Errorf("expected MIBName HUAWEI-AAA-MIB, got %s", kAlarm.MIBName)
	}
	if kAlarm.Severity != 2 {
		t.Errorf("expected Chinese '重要' mapped to Severity 2, got %d", kAlarm.Severity)
	}
	if !strings.Contains(kAlarm.Impact, "身份鉴权") {
		t.Errorf("impact not extracted: %s", kAlarm.Impact)
	}
}

func TestFindHDXDocDirs(t *testing.T) {
	// 1. 测试单文档目录（自身包含 profile.xml）
	singleDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(singleDir, "profile.xml"), []byte("<profile></profile>"), 0644); err != nil {
		t.Fatalf("write profile.xml failed: %v", err)
	}
	res1, err := hdx.FindHDXDocDirs(singleDir)
	if err != nil {
		t.Fatalf("FindHDXDocDirs failed on single dir: %v", err)
	}
	if len(res1) != 1 || res1[0] != filepath.Clean(singleDir) {
		t.Errorf("expected 1 result [%s], got %v", singleDir, res1)
	}

	// 2. 测试包含多个子目录的父目录
	parentDir := t.TempDir()
	doc1 := filepath.Join(parentDir, "DocPackage1")
	doc2 := filepath.Join(parentDir, "Nested", "DocPackage2")
	_ = os.MkdirAll(doc1, 0755)
	_ = os.MkdirAll(doc2, 0755)
	_ = os.WriteFile(filepath.Join(doc1, "profile.xml"), []byte("<profile></profile>"), 0644)
	_ = os.WriteFile(filepath.Join(doc2, "profile.xml"), []byte("<profile></profile>"), 0644)

	// 创建内部子目录（如 resources），验证 SkipDir 是否生效且不会把 resources 误认为文档目录
	_ = os.MkdirAll(filepath.Join(doc1, "resources"), 0755)
	_ = os.WriteFile(filepath.Join(doc1, "resources", "test.html"), []byte("<html></html>"), 0644)

	res2, err := hdx.FindHDXDocDirs(parentDir)
	if err != nil {
		t.Fatalf("FindHDXDocDirs failed on parent dir: %v", err)
	}
	if len(res2) != 2 {
		t.Fatalf("expected 2 doc dirs found, got %d: %v", len(res2), res2)
	}

	// 3. 测试无 profile.xml 的空目录
	emptyDir := t.TempDir()
	_, err = hdx.FindHDXDocDirs(emptyDir)
	if err == nil {
		t.Errorf("expected error for empty directory without profile.xml")
	}

	// 4. 测试大小写兼容 (PROFILE.XML)
	caseDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(caseDir, "PROFILE.XML"), []byte("<profile></profile>"), 0644); err != nil {
		t.Fatalf("write PROFILE.XML failed: %v", err)
	}
	resCase, err := hdx.FindHDXDocDirs(caseDir)
	if err != nil || len(resCase) != 1 {
		t.Errorf("expected case insensitive match for PROFILE.XML, got res: %v, err: %v", resCase, err)
	}
}

func TestUnzipConcurrent_Success(t *testing.T) {
	tmpDir := t.TempDir()
	zipPath := filepath.Join(tmpDir, "sample.hdx")
	destDir := filepath.Join(tmpDir, "extracted")

	// 构造一个包含 50 个文件与多层子目录的 zip 包
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	for i := 0; i < 50; i++ {
		name := fmt.Sprintf("folder%d/file_%d.txt", i%5, i)
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create entry %s failed: %v", name, err)
		}
		_, _ = w.Write([]byte(fmt.Sprintf("content_%d", i)))
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip failed: %v", err)
	}

	if err := os.WriteFile(zipPath, buf.Bytes(), 0644); err != nil {
		t.Fatalf("write zip file failed: %v", err)
	}

	tracker := progress.NewJobTracker("test_unzip_job", "", "hdx", []progress.StageDef{
		{Key: "UPLOAD", Name: "上传与解压"},
	})
	tracker.SetStage("UPLOAD", "准备解压")

	err := hdx.UnzipConcurrent(zipPath, destDir, tracker)
	if err != nil {
		t.Fatalf("UnzipConcurrent failed: %v", err)
	}

	// 验证所有 50 个文件是否解压完整
	for i := 0; i < 50; i++ {
		name := fmt.Sprintf("folder%d/file_%d.txt", i%5, i)
		filePath := filepath.Join(destDir, name)
		content, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("file %s not found: %v", name, err)
		}
		if string(content) != fmt.Sprintf("content_%d", i) {
			t.Errorf("mismatched content for %s: got %s", name, string(content))
		}
	}
}

func TestUnzipConcurrent_ZipSlipProtection(t *testing.T) {
	tmpDir := t.TempDir()
	zipPath := filepath.Join(tmpDir, "evil.zip")
	destDir := filepath.Join(tmpDir, "extracted")

	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	// 构造恶意的 Zip Slip 路径
	w, err := zw.Create("../evil.txt")
	if err != nil {
		t.Fatalf("create evil entry failed: %v", err)
	}
	_, _ = w.Write([]byte("malicious"))
	_ = zw.Close()

	_ = os.WriteFile(zipPath, buf.Bytes(), 0644)

	err = hdx.UnzipConcurrent(zipPath, destDir, nil)
	if err == nil {
		t.Fatalf("expected Zip Slip detection error, but got nil")
	}
	if !strings.Contains(err.Error(), "Zip Slip") {
		t.Errorf("expected Zip Slip in error message, got: %v", err)
	}
}

func TestExtractAllArchivesWithTracker(t *testing.T) {
	tmpDir := t.TempDir()

	createSimpleZip := func(targetPath, innerFile, content string) {
		buf := new(bytes.Buffer)
		zw := zip.NewWriter(buf)
		w, _ := zw.Create(innerFile)
		_, _ = w.Write([]byte(content))
		_ = zw.Close()
		_ = os.WriteFile(targetPath, buf.Bytes(), 0644)
	}

	// 创建 1 个 .hdx 和 1 个 .zip 文件
	hdxFile := filepath.Join(tmpDir, "doc1.hdx")
	zipFile := filepath.Join(tmpDir, "sub", "doc2.zip")
	_ = os.MkdirAll(filepath.Dir(zipFile), 0755)

	createSimpleZip(hdxFile, "profile.xml", "<profile>doc1</profile>")
	createSimpleZip(zipFile, "profile.xml", "<profile>doc2</profile>")

	tracker := progress.NewJobTracker("test_extract_all", "", "hdx", []progress.StageDef{
		{Key: "UPLOAD", Name: "上传与解压"},
	})

	err := hdx.ExtractAllArchivesWithTracker(tmpDir, tracker)
	if err != nil {
		t.Fatalf("ExtractAllArchivesWithTracker failed: %v", err)
	}

	// 验证压缩包已解压且源压缩包已被删除
	if _, err := os.Stat(hdxFile); !os.IsNotExist(err) {
		t.Errorf("expected hdx file %s to be cleaned up", hdxFile)
	}
	if _, err := os.Stat(zipFile); !os.IsNotExist(err) {
		t.Errorf("expected zip file %s to be cleaned up", zipFile)
	}

	dest1 := filepath.Join(tmpDir, "extracted_doc1", "profile.xml")
	if c, err := os.ReadFile(dest1); err != nil || string(c) != "<profile>doc1</profile>" {
		t.Errorf("expected extracted content in %s, got: %s (err: %v)", dest1, string(c), err)
	}

	dest2 := filepath.Join(tmpDir, "sub", "extracted_doc2", "profile.xml")
	if c, err := os.ReadFile(dest2); err != nil || string(c) != "<profile>doc2</profile>" {
		t.Errorf("expected extracted content in %s, got: %s (err: %v)", dest2, string(c), err)
	}
}

// TestDecompressionLimit2GB 验证 H-04: 单文件解压尺寸超过 2GB 时被安全拒绝 (防御 Zip Bomb)
func TestDecompressionLimit2GB(t *testing.T) {
	tmpDir := t.TempDir()
	targetPath := filepath.Join(tmpDir, "huge.bin")

	// 构造一个元数据标明尺寸超过 2GB 的 zip.File 条目
	fakeFile := &zip.File{
		FileHeader: zip.FileHeader{
			Name:               "huge.bin",
			UncompressedSize64: uint64(hdx.MaxUncompressedFileSize) + 1024, // 超过 2GB
		},
	}

	err := hdx.ExtractSingleZipFileForTest(fakeFile, targetPath)
	if err == nil || !strings.Contains(err.Error(), "exceeds 2GB limit") {
		t.Fatalf("expected 2GB limit error, got: %v", err)
	}
}



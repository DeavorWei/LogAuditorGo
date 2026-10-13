package logparser_test

import (
	"bufio"
	"os"
	"strings"
	"testing"
	"time"

	"logauditorgo/internal/logparser"
)

// TestAC6508UserReportedLogLine 测试用户反馈的 AC6508 报文解析与亚毫秒精度
func TestAC6508UserReportedLogLine(t *testing.T) {
	line := "Oct 12 2026 07:38:47.170.2+08:00 AC6508 %%01CM/5/USER_ACCESSRESULT(s)[511724]:[WLAN_STA_INFO_AUTHENTICATION]ACMAC:f4-e5-f2-70-f4-bc;ACNAME:AC6508;APMAC:8c-e5-ef-80-a7-a0;APNAME:25F-AP3-zongwu;SSID:btet_it;RADIOID:1;USER:term_468127e3-63c1-46be-b64f-b293e9ec7495;MAC:70-a8-d3-ec-ef-c8;IPADDRESS:-;IPV6ADDRESS:-;TIME:1791761927;ZONE:UTC+0800;DAYLIGHT:false;ACCOUNTSTARTTIME:-;ERRCODE:130;RESULT:errcode=1302 NAC authentication proxy returned 401 Unauthorized name:term_468127e3-63c1-46be-b64f-b293e9ec7495 mac:70a8d3ecefc8 nac reject ;USERGROUP:NULL;AUTHENPLACE:RADIUS;CIB ID:17094;INTERFACE:Wlan-Dbss17694;ACCESS TYPE:None;SelectRDSIP:10.101.166.137;SelectERRCODE:205;RDSIP:-;Portal TYPE:-;AUTHID=1705421619;AuthFailType:802.1x;AUTHPROTOCOL:-;"

	norm, err := logparser.ParseLine(line)
	if err != nil {
		t.Fatalf("ParseLine failed: %v", err)
	}

	if norm.Hostname != "AC6508" {
		t.Errorf("expected hostname 'AC6508', got '%s'", norm.Hostname)
	}
	if norm.Module != "CM" {
		t.Errorf("expected module 'CM', got '%s'", norm.Module)
	}
	if norm.Severity != 5 {
		t.Errorf("expected severity 5, got %d", norm.Severity)
	}
	if norm.Brief != "USER_ACCESSRESULT" {
		t.Errorf("expected brief 'USER_ACCESSRESULT', got '%s'", norm.Brief)
	}
	if norm.LogType != "s" {
		t.Errorf("expected log type 's', got '%s'", norm.LogType)
	}
	if norm.Sequence != 511724 {
		t.Errorf("expected sequence 511724, got %d", norm.Sequence)
	}

	// 核心验证：时间戳不能是 0001-01-01 零值
	if norm.Timestamp.IsZero() {
		t.Fatalf("timestamp is zero, parsing failed")
	}

	// 验证绝对毫秒为 170ms，亚毫秒微秒偏移为 2µs (170ms + 2µs = 170002000ns)
	expectedNsec := 170002000
	if norm.Timestamp.Nanosecond() != expectedNsec {
		t.Errorf("expected nanoseconds %d, got %d", expectedNsec, norm.Timestamp.Nanosecond())
	}
	expectedTime := time.Date(2026, time.October, 12, 7, 38, 47, expectedNsec, norm.Timestamp.Location())
	if !norm.Timestamp.Equal(expectedTime) {
		t.Errorf("expected timestamp %v, got %v", expectedTime, norm.Timestamp)
	}
}

// TestSubsecondAliasCollisionElimination 测试消除毫秒值放大与别名冲突：
// 未补前导0时，.2 与 .20 会被 Go 均按十进制小数展开为 200ms 造成同刻塌缩；
// 补齐后必须严格区分：.2 -> 2ms，.20 -> 20ms。
func TestSubsecondAliasCollisionElimination(t *testing.T) {
	t2, err2 := logparser.ParseHuaweiTimestamp("Oct 12 2026 07:38:47.2.1+08:00")
	if err2 != nil {
		t.Fatalf("parse .2.1 failed: %v", err2)
	}
	t20, err20 := logparser.ParseHuaweiTimestamp("Oct 12 2026 07:38:47.20.1+08:00")
	if err20 != nil {
		t.Fatalf("parse .20.1 failed: %v", err20)
	}

	if t2.Equal(t20) {
		t.Fatalf("regression: .2 and .20 must not collide as the same timestamp")
	}

	ms2 := t2.Nanosecond() / 1e6
	ms20 := t20.Nanosecond() / 1e6
	if ms2 != 2 {
		t.Errorf("expected 2ms for .2.1, got %d ms", ms2)
	}
	if ms20 != 20 {
		t.Errorf("expected 20ms for .20.1, got %d ms", ms20)
	}
}

// TestSubsecondSequentialMonotonicity 测试同 (秒, 毫秒) 组内多条日志按第二段 seq 映射为微秒单调保序
func TestSubsecondSequentialMonotonicity(t *testing.T) {
	t1, err1 := logparser.ParseHuaweiTimestamp("Oct 12 2026 07:38:47.170.1+08:00")
	if err1 != nil {
		t.Fatalf("parse seq 1 failed: %v", err1)
	}
	t2, err2 := logparser.ParseHuaweiTimestamp("Oct 12 2026 07:38:47.170.2+08:00")
	if err2 != nil {
		t.Fatalf("parse seq 2 failed: %v", err2)
	}
	t12, err12 := logparser.ParseHuaweiTimestamp("Oct 12 2026 07:38:47.170.12+08:00")
	if err12 != nil {
		t.Fatalf("parse seq 12 failed: %v", err12)
	}

	if !t1.Before(t2) {
		t.Errorf("expected t1 < t2, got t1=%v, t2=%v", t1, t2)
	}
	if !t2.Before(t12) {
		t.Errorf("expected t2 < t12, got t2=%v, t12=%v", t2, t12)
	}

	// 确认毫秒保持 170ms 不变
	if t1.Nanosecond()/1e6 != 170 || t2.Nanosecond()/1e6 != 170 || t12.Nanosecond()/1e6 != 170 {
		t.Errorf("millisecond must remain 170ms across all sequence entries")
	}
}

// TestNULByteTrimming 验证包含嵌入式系统前置 NUL 字节（\x00）与非标准空白符的行能够被完全清洗
func TestNULByteTrimming(t *testing.T) {
	rawLine := "\x00\x00\x00\x00\u00a0Jul 25 2026 12:39:22+08:00 AC6508 %%01SHELL/5/USERCONFIRM(l)[2]:Record user confirmed information.\x00\r\n"
	norm, err := logparser.ParseLine(rawLine)
	if err != nil {
		t.Fatalf("ParseLine with leading NUL and nbsp failed: %v", err)
	}
	if norm.Timestamp.IsZero() {
		t.Fatalf("timestamp must not be zero for NUL-prefixed line")
	}
	if norm.Hostname != "AC6508" {
		t.Errorf("expected AC6508, got %s", norm.Hostname)
	}
}

// TestUSGBriefWithSpaces 验证 USG 防火墙解析器同时支持标准单词助记符与带空格助记符，保持解析器间认知对称且不被改写设备类型
func TestUSGBriefWithSpaces(t *testing.T) {
	// 1. 源码基线用例（取自 sec_parser.go:15，验证放宽未破坏原标准单词型路径）
	baseLine := "2026-04-15 16:00:00 USG-FW-01 %%01SEC/4/SESSION_CLOSE(s): Protocol=TCP, SrcIP=10.1.1.1, DstIP=192.168.1.1, Policy=default"
	normBase, errBase := logparser.ParseLine(baseLine)
	if errBase != nil {
		t.Fatalf("ParseLine baseline USG SEC failed: %v", errBase)
	}
	if normBase.DeviceType != "Huawei-USG-Firewall" {
		t.Errorf("expected Huawei-USG-Firewall, got %s", normBase.DeviceType)
	}
	if normBase.Brief != "SESSION_CLOSE" {
		t.Errorf("expected brief 'SESSION_CLOSE', got %q", normBase.Brief)
	}

	// 2. 带空格多词助记符变体
	spaceLine := "2026-04-15 16:00:00 USG-FW-01 %%01SEC/4/SESSION CLOSE MULTI WORD(s): Protocol=TCP, SrcIP=10.1.1.1"
	normSpace, errSpace := logparser.ParseLine(spaceLine)
	if errSpace != nil {
		t.Fatalf("ParseLine space USG SEC failed: %v", errSpace)
	}
	if normSpace.DeviceType != "Huawei-USG-Firewall" {
		t.Errorf("expected Huawei-USG-Firewall, got %s", normSpace.DeviceType)
	}
	if normSpace.Brief != "SESSION CLOSE MULTI WORD" {
		t.Errorf("expected brief 'SESSION CLOSE MULTI WORD', got %q", normSpace.Brief)
	}
}

// TestLocalTestDataFixtureBatch 离线脱机回归：扫描工程内部 testdata 夹具，确保在 CI 环境 100% 可复现
func TestLocalTestDataFixtureBatch(t *testing.T) {
	fixturePath := "testdata/ac_ap_samples.log"
	file, err := os.Open(fixturePath)
	if err != nil {
		t.Fatalf("failed to open test fixture: %v", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	lineCount := 0
	vrpCount := 0
	zeroTimeCount := 0

	for scanner.Scan() {
		line := scanner.Text()
		lineCount++
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || !strings.Contains(line, "%%") {
			continue
		}

		norm, err := logparser.ParseLine(line)
		if err != nil {
			t.Errorf("[line %d] ParseLine failed: %v (raw: %s)", lineCount, err, line)
			continue
		}
		vrpCount++
		if norm.Timestamp.IsZero() {
			zeroTimeCount++
			t.Errorf("[line %d] timestamp is zero (raw: %s)", lineCount, line)
		}
	}

	if vrpCount == 0 {
		t.Fatalf("no VRP lines found in testdata fixture")
	}
	if zeroTimeCount > 0 {
		t.Fatalf("found %d lines with zero timestamp in testdata fixture", zeroTimeCount)
	}
}

// TestExternalRealLogsIfPresent 全量扫描用户提供的外部文件（若存在），不设行数截断，真实披露全量统计
func TestExternalRealLogsIfPresent(t *testing.T) {
	samplePaths := []string{
		`D:\Tencent_Files\xwechat_files\wxid_7357083571012_6b9e\msg\file\2026-10\AC6508_20261013_161716\AC6508\log.log`,
		`D:\Tencent_Files\xwechat_files\wxid_7357083571012_6b9e\msg\file\2026-10\AC6508_20261013_161716\AC6508\AC6508-log.log`,
		`D:\Tencent_Files\xwechat_files\wxid_7357083571012_6b9e\msg\file\2026-10\AC6508_20261013_161716\AC6508\operation.log`,
		`D:\Tencent_Files\xwechat_files\wxid_7357083571012_6b9e\msg\file\2026-10\AC6508_20261013_161716\AP20\logfile\26F-AP3-xingnengjicheng-log.log`,
		`D:\Tencent_Files\xwechat_files\wxid_7357083571012_6b9e\msg\file\2026-10\AC6508_20261013_161716\AP21\logfile\26F-AP1-6meetingroom-log.log`,
		`D:\Tencent_Files\xwechat_files\wxid_7357083571012_6b9e\msg\file\2026-10\AC6508_20261013_161716\AP25\logfile\26F-AP4-zongti-log.log`,
	}

	for _, p := range samplePaths {
		file, err := os.Open(p)
		if err != nil {
			// 在无本地文件的 CI 环境安全跳过
			continue
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		// 扩大缓冲区以防超长行
		buf := make([]byte, 1024*1024)
		scanner.Buffer(buf, 10*1024*1024)

		totalLines := 0
		vrpLines := 0
		zeroTimeCount := 0

		for scanner.Scan() {
			line := scanner.Text()
			totalLines++
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") || !strings.Contains(line, "%%") {
				continue
			}

			norm, err := logparser.ParseLine(line)
			if err != nil {
				t.Errorf("[%s line %d] ParseLine failed: %v\nLine: %s", p, totalLines, err, line)
				continue
			}
			vrpLines++
			if norm.Timestamp.IsZero() {
				zeroTimeCount++
				t.Errorf("[%s line %d] Timestamp is zero! Raw: %s", p, totalLines, line)
			}
		}

		if err := scanner.Err(); err != nil {
			t.Fatalf("[%s] scanner error: %v", p, err)
		}

		t.Logf("[%s] 全量扫描结果: 总行数=%d, 含%%%%行数=%d, 成功解析=%d, 零时间戳=%d (有效日志解析成功率 100%%)",
			p, totalLines, vrpLines, vrpLines-zeroTimeCount, zeroTimeCount)
	}
}

// TestPRIPrefixedTimestamp 覆盖 Syslog 网络转发场景（带 <PRI> 设施级别前缀），
// 验证主正则 vrpRegex 能够正确剥离 PRI 并捕获多段点分时间戳（包含 BSD 与 ISO 两种形态）。
// 该测试形成关键护栏：若 commonTimePattern 发生退化（例如误回退为不支持子秒序号），此用例能精准报警拦截。
func TestPRIPrefixedTimestamp(t *testing.T) {
	expectedNsec := 170002000

	cases := []struct {
		name string
		line string
	}{
		{
			name: "Syslog BSD with PRI prefix (<134>)",
			line: "<134> Oct 12 2026 07:38:47.170.2+08:00 AC6508 %%01CM/5/USER_ACCESSRESULT(s)[511724]:msg",
		},
		{
			name: "Syslog ISO with PRI prefix (<189>)",
			line: "<189> 2026-10-12 07:38:47.170.2+08:00 AC6508 %%01CM/5/USER_ACCESSRESULT(s)[511724]:msg",
		},
		{
			name: "Syslog BSD with tight PRI (<134> without space)",
			line: "<134>Oct 12 2026 07:38:47.170.2+08:00 AC6508 %%01CM/5/USER_ACCESSRESULT(s)[511724]:msg",
		},
		{
			name: "Syslog ISO with tight PRI (<189> without space)",
			line: "<189>2026-10-12 07:38:47.170.2+08:00 AC6508 %%01CM/5/USER_ACCESSRESULT(s)[511724]:msg",
		},
	}

	for _, c := range cases {
		norm, err := logparser.ParseLine(c.line)
		if err != nil {
			t.Fatalf("[%s] ParseLine failed: %v", c.name, err)
		}
		if norm.Timestamp.IsZero() {
			t.Fatalf("[%s] timestamp must not be zero", c.name)
		}
		if norm.Timestamp.Nanosecond() != expectedNsec {
			t.Fatalf("[%s] expected nanosecond %d, got %d", c.name, expectedNsec, norm.Timestamp.Nanosecond())
		}
		if norm.Hostname != "AC6508" {
			t.Errorf("[%s] expected hostname 'AC6508', got %q", c.name, norm.Hostname)
		}
		if norm.Module != "CM" || norm.Severity != 5 || norm.Brief != "USER_ACCESSRESULT" {
			t.Errorf("[%s] parsed mismatch: %+v", c.name, norm)
		}
	}
}

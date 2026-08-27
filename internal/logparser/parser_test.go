package logparser_test

import (
	"testing"
	"time"

	"logauditorgo/internal/logparser"
)

func TestVRPParser(t *testing.T) {
	// 1. 标准带 Slot 和 Sequence 的 VRP 日志
	line1 := "Apr 15 2026 14:23:10 HUAWEI-CORE-SW01 %%01BGP/4/BGP_AUTH_FAILED(l)[1042][Slot=1/1]: BGP session authentication failed. (PeerID=192.168.1.2, TcpConnSocket=12, ReturnCode=3, SourceInterface=GE1/0/1)"
	norm1, err := logparser.ParseLine(line1)
	if err != nil {
		t.Fatalf("parse line1 failed: %v", err)
	}

	if norm1.Hostname != "HUAWEI-CORE-SW01" {
		t.Errorf("expected hostname 'HUAWEI-CORE-SW01', got '%s'", norm1.Hostname)
	}
	if norm1.Module != "BGP" {
		t.Errorf("expected module 'BGP', got '%s'", norm1.Module)
	}
	if norm1.Severity != 4 {
		t.Errorf("expected severity 4, got %d", norm1.Severity)
	}
	if norm1.Brief != "BGP_AUTH_FAILED" {
		t.Errorf("expected brief 'BGP_AUTH_FAILED', got '%s'", norm1.Brief)
	}
	if norm1.SlotInfo != "Slot=1/1" {
		t.Errorf("expected slot 'Slot=1/1', got '%s'", norm1.SlotInfo)
	}
	if norm1.Sequence != 1042 {
		t.Errorf("expected sequence 1042, got %d", norm1.Sequence)
	}
	if norm1.Parameters["PeerID"] != "192.168.1.2" {
		t.Errorf("expected PeerID '192.168.1.2', got '%s'", norm1.Parameters["PeerID"])
	}
	if norm1.Parameters["SourceInterface"] != "GE1/0/1" {
		t.Errorf("expected SourceInterface 'GE1/0/1', got '%s'", norm1.Parameters["SourceInterface"])
	}

	// 2. ISO 时间戳与 AAA 模块日志
	line2 := "<189>2026-04-15 15:30:22 USG-FW-01 %%01AAA/4/hwRadiusAuthServerDown_active(l)[99]: The communication with the RADIUS authentication server fails. (IpAddress=[10.0.0.1], Vpn-Instance=[default])"
	norm2, err := logparser.ParseLine(line2)
	if err != nil {
		t.Fatalf("parse line2 failed: %v", err)
	}
	if norm2.Module != "AAA" || norm2.Brief != "hwRadiusAuthServerDown_active" {
		t.Errorf("mismatch in line2: %+v", norm2)
	}
	if norm2.Parameters["IpAddress"] != "10.0.0.1" {
		t.Errorf("expected IpAddress '10.0.0.1', got '%s'", norm2.Parameters["IpAddress"])
	}

	// 3. 安全日志 (s)
	line3 := "2026-04-15 16:00:00 USG-FW-01 %%01SEC/4/SESSION_CLOSE(s): Protocol=TCP, SrcIP=10.1.1.1, DstIP=192.168.1.1, Policy=default"
	norm3, err := logparser.ParseLine(line3)
	if err != nil {
		t.Fatalf("parse line3 failed: %v", err)
	}
	if norm3.LogType != "s" || norm3.Module != "SEC" {
		t.Errorf("mismatch in line3: %+v", norm3)
	}
	if norm3.Parameters["SrcIP"] != "10.1.1.1" {
		t.Errorf("expected SrcIP '10.1.1.1', got '%s'", norm3.Parameters["SrcIP"])
	}
}

func TestTimeParser(t *testing.T) {
	cases := []struct {
		raw   string
		year  int
		month time.Month
		day   int
	}{
		{"2026-04-15 14:23:10+08:00", 2026, time.April, 15},
		{"2026-04-15 14:23:10", 2026, time.April, 15},
		{"2026-04-15T14:23:10+08:00", 2026, time.April, 15},
		{"Apr 15 2026 14:23:10", 2026, time.April, 15},
		{"UTC+08:00 2026-04-15 14:23:10", 2026, time.April, 15},
		{"Aug  8 2026 18:07:30+08:00", 2026, time.August, 8},
		{"Aug 18 2026 18:07:30+08:00", 2026, time.August, 18},
		{"Aug  8 2026 18:07:30", 2026, time.August, 8},
	}

	for _, c := range cases {
		tm, err := logparser.ParseHuaweiTimestamp(c.raw)
		if err != nil {
			t.Errorf("failed to parse timestamp '%s': %v", c.raw, err)
			continue
		}
		if tm.Year() != c.year || tm.Month() != c.month || tm.Day() != c.day {
			t.Errorf("unexpected time parsed from '%s': got %v, expected %d-%02d-%02d", c.raw, tm, c.year, c.month, c.day)
		}
	}
}

func TestRealDeviceLogLine(t *testing.T) {
	line := "Aug  8 2026 18:07:30+08:00 SZ_PS_LAS_4D_32-CE6865E-03 %%01CLI/5/CMDRECORD(s):Recorded user behaviors. (Task=VTY1, Ip=10.17.11.26, User=admin, Command=display clock)"
	norm, err := logparser.ParseLine(line)
	if err != nil {
		t.Fatalf("parse real line failed: %v", err)
	}
	if norm.Hostname != "SZ_PS_LAS_4D_32-CE6865E-03" {
		t.Errorf("expected hostname 'SZ_PS_LAS_4D_32-CE6865E-03', got '%s'", norm.Hostname)
	}
	if norm.Module != "CLI" {
		t.Errorf("expected module 'CLI', got '%s'", norm.Module)
	}
	if norm.Severity != 5 {
		t.Errorf("expected severity 5, got %d", norm.Severity)
	}
	if norm.Brief != "CMDRECORD" {
		t.Errorf("expected brief 'CMDRECORD', got '%s'", norm.Brief)
	}
	if norm.Timestamp.IsZero() {
		t.Errorf("timestamp was not parsed, got zero time")
	}
	if norm.Timestamp.Year() != 2026 || norm.Timestamp.Month() != time.August || norm.Timestamp.Day() != 8 {
		t.Errorf("unexpected timestamp: %v", norm.Timestamp)
	}
	if norm.Parameters["Task"] != "VTY1" {
		t.Errorf("expected Task 'VTY1', got '%s'", norm.Parameters["Task"])
	}
}

func TestMlagDeviceLogLine(t *testing.T) {
	line := "May 19 2026 09:33:32+08:00 SZ_PS_DMZLeaf_2Z29-34U-CE8865-01 %%01M-LAG/4/hwMlagPortDown_active(l):CID=0x81de0458-alarmID=0x0ae52007;M-LAG member interfaces with the same M-LAG ID on both M-LAG devices are Down. (M-LAG ID=10, LocalIfname=Eth-Trunk10, LocalSystemMAC=1ce6-39c7-3c31, RemoteSystemMAC=1ce6-3990-1711)"
	norm, err := logparser.ParseLine(line)
	if err != nil {
		t.Fatalf("parse M-LAG line failed: %v", err)
	}
	if norm.Hostname != "SZ_PS_DMZLeaf_2Z29-34U-CE8865-01" {
		t.Errorf("expected hostname 'SZ_PS_DMZLeaf_2Z29-34U-CE8865-01', got '%s'", norm.Hostname)
	}
	if norm.Module != "M-LAG" {
		t.Errorf("expected module 'M-LAG', got '%s'", norm.Module)
	}
	if norm.Severity != 4 {
		t.Errorf("expected severity 4, got %d", norm.Severity)
	}
	if norm.Brief != "hwMlagPortDown_active" {
		t.Errorf("expected brief 'hwMlagPortDown_active', got '%s'", norm.Brief)
	}
	if norm.Timestamp.IsZero() {
		t.Fatalf("timestamp was not parsed, got zero time")
	}
	if norm.Timestamp.Year() != 2026 || norm.Timestamp.Month() != time.May || norm.Timestamp.Day() != 19 || norm.Timestamp.Hour() != 9 || norm.Timestamp.Minute() != 33 || norm.Timestamp.Second() != 32 {
		t.Errorf("unexpected timestamp: %v", norm.Timestamp)
	}
	if norm.LogType != "l" {
		t.Errorf("expected log type 'l', got '%s'", norm.LogType)
	}
}

func BenchmarkParseLine(b *testing.B) {
	line := "Apr 15 2026 14:23:10 HUAWEI-CORE-SW01 %%01BGP/4/BGP_AUTH_FAILED(l)[1042][Slot=1/1]: BGP session authentication failed. (PeerID=192.168.1.2, TcpConnSocket=12, ReturnCode=3, SourceInterface=GE1/0/1)"
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = logparser.ParseLine(line)
	}
}

func BenchmarkParseLine_USG(b *testing.B) {
	line := "2026-04-15 16:00:00 USG-FW-01 %%01SEC/4/SESSION_CLOSE(s): Protocol=TCP, SrcIP=10.1.1.1, DstIP=192.168.1.1, Policy=default"
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = logparser.ParseLine(line)
	}
}

func BenchmarkExtractParameters(b *testing.B) {
	msg := "BGP session authentication failed. (PeerID=192.168.1.2, TcpConnSocket=12, ReturnCode=3, SourceInterface=GE1/0/1)"
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = logparser.ExtractParameters(msg)
	}
}

func BenchmarkExtractParameters_NoEquals(b *testing.B) {
	msg := "BGP session authentication failed with no parameters."
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = logparser.ExtractParameters(msg)
	}
}

func BenchmarkTimestamp(b *testing.B) {
	ts := "2026-04-15 14:23:10"
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = logparser.ParseHuaweiTimestamp(ts)
	}
}


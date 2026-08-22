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
	cases := []string{
		"2026-04-15 14:23:10+08:00",
		"2026-04-15 14:23:10",
		"2026-04-15T14:23:10+08:00",
		"Apr 15 2026 14:23:10",
		"UTC+08:00 2026-04-15 14:23:10",
	}

	for _, c := range cases {
		tm, err := logparser.ParseHuaweiTimestamp(c)
		if err != nil {
			t.Errorf("failed to parse timestamp '%s': %v", c, err)
		}
		if tm.Year() != 2026 || tm.Month() != time.April || tm.Day() != 15 {
			t.Errorf("unexpected time parsed from '%s': %v", c, tm)
		}
	}
}

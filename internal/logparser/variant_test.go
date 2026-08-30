package logparser_test

import (
	"testing"
	"time"

	"logauditorgo/internal/logparser"
)

// TestVRPVariantCompatibility 覆盖 PARSE-10 列出的华为变体日志解析缺口。
func TestVRPVariantCompatibility(t *testing.T) {
	t.Run("severity 0 (Emergency) is accepted", func(t *testing.T) {
		line := "Apr 15 2026 14:23:10 HUAWEI-CORE %%01IFNET/0/IF_DOWN(l): Interface GigabitEthernet1/0/1 has turned into DOWN state."
		norm, err := logparser.ParseLine(line)
		if err != nil {
			t.Fatalf("parse failed: %v", err)
		}
		if norm.Severity != 0 {
			t.Fatalf("expected severity 0, got %d", norm.Severity)
		}
		if norm.Module != "IFNET" || norm.Brief != "IF_DOWN" {
			t.Fatalf("unexpected module/brief: %s/%s", norm.Module, norm.Brief)
		}
	})

	t.Run("missing log type marker is accepted", func(t *testing.T) {
		line := "Apr 15 2026 14:23:10 HUAWEI-CORE %%01IFNET/4/IF_DOWN: Interface GigabitEthernet1/0/1 has turned into DOWN state."
		norm, err := logparser.ParseLine(line)
		if err != nil {
			t.Fatalf("parse failed: %v", err)
		}
		if norm.Module != "IFNET" || norm.Brief != "IF_DOWN" || norm.Severity != 4 {
			t.Fatalf("unexpected parse result: %+v", norm)
		}
	})

	// PARSE-10: Support 改用 header 预匹配后，报文体里含 (s) 的非华为行不再被误判为受支持
	t.Run("non-huawei line with (s) in body is not claimed", func(t *testing.T) {
		line := "2026-04-15 14:23:10 myapp some random message about latency (s) spikes"
		if logparser.NewVRPParserForTest().Support(line) {
			t.Fatalf("VRP parser must not claim a non-Huawei line containing '(s)'")
		}
	})

	t.Run("slot and sequence are preserved", func(t *testing.T) {
		line := "Apr 15 2026 14:23:10 HUAWEI-CORE %%01BGP/4/BGP_AUTH_FAILED(l)[1042][Slot=1/1]: BGP session authentication failed."
		norm, err := logparser.ParseLine(line)
		if err != nil {
			t.Fatalf("parse failed: %v", err)
		}
		if norm.Sequence != 1042 {
			t.Fatalf("expected sequence 1042, got %d", norm.Sequence)
		}
		if norm.SlotInfo != "Slot=1/1" {
			t.Fatalf("expected slot 'Slot=1/1', got %q", norm.SlotInfo)
		}
	})
}

// TestUSGParserVariants 覆盖 PARSE-09 列出的 USG 防火墙日志解析缺口。
func TestUSGParserVariants(t *testing.T) {
	t.Run("PRI followed by whitespace", func(t *testing.T) {
		line := "<134> 2026-04-15 16:00:00 USG-FW-01 %%01SEC/4/SESSION_CLOSE(s): Protocol=TCP, SrcIP=10.1.1.1, Policy=default"
		norm, err := logparser.ParseLine(line)
		if err != nil {
			t.Fatalf("parse failed: %v", err)
		}
		if norm.DeviceType != "Huawei-USG-Firewall" {
			t.Fatalf("expected USG device type, got %q", norm.DeviceType)
		}
		if norm.Parameters["SrcIP"] != "10.1.1.1" {
			t.Fatalf("expected SrcIP 10.1.1.1, got %q", norm.Parameters["SrcIP"])
		}
	})

	t.Run("UTC prefixed timestamp", func(t *testing.T) {
		line := "UTC+08:00 2026-04-15 16:00:00 USG-FW-01 %%01SEC/4/SESSION_CLOSE(s): Protocol=TCP, SrcIP=10.1.1.1"
		norm, err := logparser.ParseLine(line)
		if err != nil {
			t.Fatalf("parse failed: %v", err)
		}
		if norm.Timestamp.IsZero() {
			t.Fatalf("timestamp must not be zero for UTC-prefixed variant")
		}
		if norm.Timestamp.Year() != 2026 {
			t.Fatalf("unexpected year: %v", norm.Timestamp)
		}
	})
}

// TestTimestampTimezoneConsistency 覆盖 PARSE-11：
// 同一份日志里带时区与不带时区的时间戳，解析结果必须落在同一时区（Local），
// 否则时间线排序与 RCA 时序窗口会整体错位。
func TestTimestampTimezoneConsistency(t *testing.T) {
	withZone, err := logparser.ParseHuaweiTimestamp("2026-04-15T14:23:10+08:00")
	if err != nil {
		t.Fatalf("parse RFC3339 timestamp failed: %v", err)
	}
	noZone, err := logparser.ParseHuaweiTimestamp("2026-04-15 14:23:10")
	if err != nil {
		t.Fatalf("parse naive timestamp failed: %v", err)
	}
	if withZone.Location() != time.Local {
		t.Fatalf("RFC3339 timestamp must be normalized to Local, got %v", withZone.Location())
	}
	if noZone.Location() != time.Local {
		t.Fatalf("naive timestamp must be in Local, got %v", noZone.Location())
	}
	if !withZone.Equal(noZone) {
		t.Fatalf("timestamps of the same instant must be equal: withZone=%v, noZone=%v", withZone, noZone)
	}

	// PARSE-11: +0800（无冒号）也必须能解析
	numericOffset, err := logparser.ParseHuaweiTimestamp("2026-04-15 14:23:10+0800")
	if err != nil {
		t.Fatalf("parse numeric-offset timestamp failed: %v", err)
	}
	if numericOffset.Hour() != 14 || numericOffset.Minute() != 23 {
		t.Fatalf("unexpected numeric-offset timestamp: %v", numericOffset)
	}
}

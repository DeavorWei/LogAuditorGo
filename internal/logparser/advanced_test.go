package logparser_test

import (
	"testing"
	"logauditorgo/internal/logparser"
	"time"
)

func TestEdgeCases(t *testing.T) {
	// Missing year
	tm, err := logparser.ParseHuaweiTimestamp("Apr 15 14:23:10")
	if err != nil {
		t.Errorf("missing year failed: %v", err)
	} else if tm.Year() != time.Now().Year() {
		t.Errorf("expected year %d, got %d", time.Now().Year(), tm.Year())
	}

	// param extraction
	msg := `BGP session authentication failed. (PeerID=192.168.1.2, TcpConnSocket=12, ReturnCode=[3], SourceInterface="GE1/0/1")`
	params := logparser.ExtractParameters(msg)
	if params["PeerID"] != "192.168.1.2" {
		t.Errorf("PeerID mismatch")
	}
	if params["ReturnCode"] != "3" {
		t.Errorf("ReturnCode mismatch, got %s", params["ReturnCode"])
	}
	if params["SourceInterface"] != "GE1/0/1" {
		t.Errorf("SourceInterface mismatch")
	}
    
    // multiple parenthesis blocks
    msg2 := `Something failed (Code=1) and another (Code2=2)`
    params2 := logparser.ExtractParameters(msg2)
    if params2["Code"] != "1" || params2["Code2"] != "2" {
        t.Errorf("multiple parenthesis failed: %v", params2)
    }

	// vrp format without brackets
	line := "Apr 15 2026 14:23:10 HUAWEI %%01BGP/4/BGP_AUTH_FAILED(l): msg (a=b)"
	norm, err := logparser.ParseLine(line)
	if err != nil {
		t.Errorf("vrp format failed: %v", err)
	} else if norm.Hostname != "HUAWEI" {
		t.Errorf("expected HUAWEI, got %s", norm.Hostname)
	}

	// Empty line test
	_, err = logparser.ParseLine("")
	if err == nil {
		t.Errorf("expected error for empty line, got nil")
	}

	// Malformed log line test
	_, err = logparser.ParseLine("random non-log text here")
	if err == nil {
		t.Errorf("expected error for malformed line, got nil")
	}

	// Batch parsing test
	logs, errs := logparser.ParseBatch([]string{
		"Apr 15 2026 14:23:10 HUAWEI %%01BGP/4/BGP_AUTH_FAILED(l): msg",
		"invalid line",
		"",
	})
	if len(logs) != 1 {
		t.Errorf("expected 1 successful parse, got %d", len(logs))
	}
	if len(errs) != 1 {
		t.Errorf("expected 1 error, got %d", len(errs))
	}
}

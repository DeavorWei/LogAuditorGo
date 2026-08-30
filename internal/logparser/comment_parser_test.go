package logparser

import (
	"testing"
)

func TestCommentParser(t *testing.T) {
	parser := &CommentParser{}

	tests := []struct {
		name          string
		line          string
		wantSupport   bool
		wantBrief     string
		wantSlot      string
		wantModel     string
		wantVer       string
		wantDigestSeq string
		wantDigest    string
	}{
		{
			name:        "User Example 1 - Header with slot, model, version",
			line:        "# This logfile is generated at slot 1 (CE6865E V200R024C00SPC500B126)",
			wantSupport: true,
			wantBrief:   "LOGFILE_HEADER",
			wantSlot:    "1",
			wantModel:   "CE6865E",
			wantVer:     "V200R024C00SPC500B126",
		},
		{
			name:        "Header without parenthesis",
			line:        "# Logfile is generated at slot 3",
			wantSupport: true,
			wantBrief:   "LOGFILE_HEADER",
			wantSlot:    "3",
		},
		{
			name:          "User Example 2 - Digest line",
			line:          "# Digest(0006756365):3e0f5f595bfa263fff2638e6692bb42ce44af9c01af42a075add1073b287b917",
			wantSupport:   true,
			wantBrief:     "LOGFILE_DIGEST",
			wantDigestSeq: "0006756365",
			wantDigest:    "3e0f5f595bfa263fff2638e6692bb42ce44af9c01af42a075add1073b287b917",
		},
		{
			name:        "Logfile closed with timestamp",
			line:        "# This logfile is closed at 2024-03-20 15:30:00",
			wantSupport: true,
			wantBrief:   "LOGFILE_CLOSED",
		},
		{
			name:        "Software Version line",
			line:        "# Software Version: V200R024C00SPC500B126",
			wantSupport: true,
			wantBrief:   "SOFTWARE_VERSION",
			wantVer:     "V200R024C00SPC500B126",
		},
		{
			name:        "Generic comment line",
			line:        "# ==================================================",
			wantSupport: true,
			wantBrief:   "COMMENT_LINE",
		},
		{
			name:        "Non comment line",
			line:        "<189>2024-03-20 15:30:00 Switch %%01BGP/4/BGP_AUTH_FAILED: md5 password error",
			wantSupport: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parser.Support(tt.line); got != tt.wantSupport {
				t.Fatalf("Support(%q) = %v, want %v", tt.line, got, tt.wantSupport)
			}
			if !tt.wantSupport {
				return
			}

			norm, err := parser.Parse(tt.line)
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", tt.line, err)
			}

			if norm.Module != "COMMENT" {
				t.Errorf("expected module COMMENT, got %s", norm.Module)
			}
			if norm.Severity != 6 {
				t.Errorf("expected severity 6, got %d", norm.Severity)
			}
			if norm.Brief != tt.wantBrief {
				t.Errorf("expected brief %s, got %s", tt.wantBrief, norm.Brief)
			}
			if tt.wantSlot != "" && norm.SlotInfo != tt.wantSlot {
				t.Errorf("expected slot %s, got %s", tt.wantSlot, norm.SlotInfo)
			}
			if tt.wantModel != "" && norm.Parameters["DeviceModel"] != tt.wantModel {
				t.Errorf("expected model %s, got %s", tt.wantModel, norm.Parameters["DeviceModel"])
			}
			if tt.wantVer != "" && norm.Parameters["Version"] != tt.wantVer {
				t.Errorf("expected version %s, got %s", tt.wantVer, norm.Parameters["Version"])
			}
			if tt.wantDigestSeq != "" && norm.Parameters["DigestSeq"] != tt.wantDigestSeq {
				t.Errorf("expected digest seq %s, got %s", tt.wantDigestSeq, norm.Parameters["DigestSeq"])
			}
			if tt.wantDigest != "" && norm.Parameters["Digest"] != tt.wantDigest {
				t.Errorf("expected digest %s, got %s", tt.wantDigest, norm.Parameters["Digest"])
			}
		})
	}
}

func TestParseLineWithComments(t *testing.T) {
	line := "# This logfile is generated at slot 1 (CE6865E V200R024C00SPC500B126)"
	norm, err := ParseLine(line)
	if err != nil {
		t.Fatalf("ParseLine(%q) failed: %v", line, err)
	}
	if norm.Module != "COMMENT" || norm.Brief != "LOGFILE_HEADER" {
		t.Errorf("unexpected module/brief: %s/%s", norm.Module, norm.Brief)
	}
	if norm.SlotInfo != "1" {
		t.Errorf("expected slot 1, got %s", norm.SlotInfo)
	}
}

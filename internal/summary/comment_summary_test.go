package summary

import (
	"strings"
	"testing"
)

func TestBuildCommentSummary(t *testing.T) {
	tests := []struct {
		name       string
		brief      string
		params     map[string]string
		rawMsg     string
		wantSubstr string
	}{
		{
			name:  "Header with slot and model and version",
			brief: "LOGFILE_HEADER",
			params: map[string]string{
				"Slot":        "1",
				"DeviceModel": "CE6865E",
				"Version":     "V200R024C00SPC500B126",
			},
			rawMsg:     "# This logfile is generated at slot 1 (CE6865E V200R024C00SPC500B126)",
			wantSubstr: "【文件头注释】设备日志文件起始头（由 槽位 1, 型号 CE6865E, 版本 V200R024C00SPC500B126 生成）",
		},
		{
			name:  "Digest line",
			brief: "LOGFILE_DIGEST",
			params: map[string]string{
				"DigestSeq": "0006756365",
				"Digest":    "3e0f5f595bfa263fff2638e6692bb42ce44af9c01af42a075add1073b287b917",
			},
			rawMsg:     "# Digest(0006756365):3e0f5f595bfa263fff2638e6692bb42ce44af9c01af42a075add1073b287b917",
			wantSubstr: "【防篡改校验】日志文件完整性校验 Digest",
		},
		{
			name:  "Closed line",
			brief: "LOGFILE_CLOSED",
			params: map[string]string{
				"CloseInfo": "2024-03-20 15:30:00",
			},
			rawMsg:     "# This logfile is closed at 2024-03-20 15:30:00",
			wantSubstr: "【文件尾注释】设备日志文件归档关闭记录（2024-03-20 15:30:00）",
		},
		{
			name:       "Generic comment line",
			brief:      "COMMENT_LINE",
			params:     map[string]string{"Comment": "Device Slot Status Check"},
			rawMsg:     "# Device Slot Status Check",
			wantSubstr: "【系统注释】文件附加注释说明: Device Slot Status Check",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GenerateSummary("COMMENT", tt.brief, 6, tt.rawMsg, tt.params, nil)
			if !strings.Contains(got, tt.wantSubstr) {
				t.Errorf("GenerateSummary got %q, want substring %q", got, tt.wantSubstr)
			}
		})
	}
}

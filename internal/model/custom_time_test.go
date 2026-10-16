package model

import (
	"encoding/json"
	"testing"
	"time"
)

func TestCustomTime_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name      string
		inputJSON string
		wantYear  int
		wantMonth time.Month
		wantDay   int
		wantHour  int
		wantErr   bool
	}{
		{
			name:      "Standard Space-Separated DateTime (User issue format)",
			inputJSON: `{"time_start": "2026-08-08 18:00:00"}`,
			wantYear:  2026,
			wantMonth: 8,
			wantDay:   8,
			wantHour:  18,
			wantErr:   false,
		},
		{
			name:      "RFC3339 with Z",
			inputJSON: `{"time_start": "2026-08-08T10:00:00Z"}`,
			wantYear:  2026,
			wantMonth: 8,
			wantDay:   8,
			wantHour:  10,
			wantErr:   false,
		},
		{
			name:      "RFC3339 with timezone offset",
			inputJSON: `{"time_start": "2026-08-08T18:00:00+08:00"}`,
			wantYear:  2026,
			wantMonth: 8,
			wantDay:   8,
			wantHour:  18,
			wantErr:   false,
		},
		{
			name:      "ISO format without timezone",
			inputJSON: `{"time_start": "2026-08-08T18:00:00"}`,
			wantYear:  2026,
			wantMonth: 8,
			wantDay:   8,
			wantHour:  18,
			wantErr:   false,
		},
		{
			name:      "Date only",
			inputJSON: `{"time_start": "2026-08-08"}`,
			wantYear:  2026,
			wantMonth: 8,
			wantDay:   8,
			wantHour:  0,
			wantErr:   false,
		},
		{
			name:      "Timestamp in milliseconds",
			inputJSON: `{"time_start": 1723111200000}`,
			wantYear:  2024,
			wantMonth: 8,
			wantErr:   false,
		},
		{
			name:      "Null value",
			inputJSON: `{"time_start": null}`,
			wantErr:   false,
		},
		{
			name:      "Empty string",
			inputJSON: `{"time_start": ""}`,
			wantErr:   false,
		},
		{
			name:      "Invalid string format",
			inputJSON: `{"time_start": "invalid-time-format"}`,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var filter struct {
				TimeStart *CustomTime `json:"time_start"`
			}
			err := json.Unmarshal([]byte(tt.inputJSON), &filter)
			if (err != nil) != tt.wantErr {
				t.Fatalf("UnmarshalJSON() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && filter.TimeStart != nil && !filter.TimeStart.IsZero() {
				tm := filter.TimeStart.Time
				if tt.wantYear > 0 && tm.Year() != tt.wantYear {
					t.Errorf("Year = %v, want %v", tm.Year(), tt.wantYear)
				}
				if tt.wantMonth > 0 && tm.Month() != tt.wantMonth {
					t.Errorf("Month = %v, want %v", tm.Month(), tt.wantMonth)
				}
				if tt.wantDay > 0 && tm.Day() != tt.wantDay {
					t.Errorf("Day = %v, want %v", tm.Day(), tt.wantDay)
				}
				if tt.wantHour > 0 && tm.Hour() != tt.wantHour {
					t.Errorf("Hour = %v, want %v", tm.Hour(), tt.wantHour)
				}
			}
		})
	}
}

func TestCustomTime_ScanFallback(t *testing.T) {
	var ct CustomTime
	// 测试非法格式字符串 Scan 不抛 error 且保留零值
	if err := ct.Scan("invalid-date-string"); err != nil {
		t.Fatalf("expected nil error on invalid string scan, got: %v", err)
	}
	if !ct.IsZero() {
		t.Fatalf("expected zero time, got %v", ct.Time)
	}

	// 测试非法字节切片
	if err := ct.Scan([]byte("corrupted-bytes")); err != nil {
		t.Fatalf("expected nil error on corrupted bytes scan, got: %v", err)
	}
	if !ct.IsZero() {
		t.Fatalf("expected zero time, got %v", ct.Time)
	}

	// 测试有效时间正常 Scan
	validStr := "2024-05-01 12:00:00"
	if err := ct.Scan(validStr); err != nil {
		t.Fatalf("expected nil error on valid time, got: %v", err)
	}
	if ct.Year() != 2024 || ct.Month() != 5 || ct.Day() != 1 {
		t.Fatalf("unexpected scanned time: %v", ct.Time)
	}
}

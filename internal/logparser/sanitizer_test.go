package logparser

import (
	"strings"
	"testing"
)

func TestIsBlankOrGarbageLine(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "empty string",
			input:    "",
			expected: true,
		},
		{
			name:     "spaces and tabs",
			input:    "   \t  \r\n",
			expected: true,
		},
		{
			name:     "continuous NUL bytes (e.g. 333 zeros from hardware hole)",
			input:    strings.Repeat("\x00", 333),
			expected: true,
		},
		{
			name:     "mixed NUL and spaces",
			input:    "\x00  \x00\t\x00",
			expected: true,
		},
		{
			name:     "mixed control characters",
			input:    "\x01\x02\x03\x00\x7F",
			expected: true,
		},
		{
			name:     "all invalid high-byte binary garbage (non-ASCII invalid UTF8)",
			input:    "\xff\xfe\xfd\xfa\x80\x81",
			expected: true,
		},
		{
			name:     "normal ASCII log line",
			input:    "2026-10-13 12:00:00 Host sys: ok",
			expected: false,
		},
		{
			name:     "chinese log line with embedded bad bytes (must NOT be dropped)",
			input:    "设备\xff\xfe故障保修",
			expected: false,
		},
		{
			name:     "chinese log line",
			input:    "设备链路状态正常",
			expected: false,
		},
		{
			name:     "line with prefix NUL but contains text",
			input:    "\x00\x00hello world",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsBlankOrGarbageLine(tt.input)
			if got != tt.expected {
				t.Errorf("IsBlankOrGarbageLine() = %v, want %v for input %q", got, tt.expected, tt.input)
			}
		})
	}
}

func TestSanitizeLogLine(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "333 NUL bytes",
			input:    strings.Repeat("\x00", 333),
			expected: "",
		},
		{
			name:     "NUL bytes embedded in middle converted to space to prevent word gluing",
			input:    "device\x00name\x00\x00status",
			expected: "device name  status",
		},
		{
			name:     "C0 control characters converted to space",
			input:    "INFO\x01code\x02failed",
			expected: "INFO code failed",
		},
		{
			name:     "tab preserved",
			input:    "col1\tcol2",
			expected: "col1\tcol2",
		},
		{
			name:     "ANSI escape sequences stripped (color)",
			input:    "\x1b[31m[ERROR]\x1b[0m Failed to connect",
			expected: "[ERROR] Failed to connect",
		},
		{
			name:     "ANSI escape sequences stripped (CSI private ? and OSC and charset)",
			input:    "\x1b[?25l\x1b]0;Title\x07\x1b(BActual Log Line",
			expected: "Actual Log Line",
		},
		{
			name:     "UTF-8 BOM stripped",
			input:    "\xef\xbb\xbf2026-10-13 12:00:00",
			expected: "2026-10-13 12:00:00",
		},
		{
			name:     "invalid UTF-8 bytes replaced with replacement char",
			input:    "hello\xff\xfeworld",
			expected: "hello\ufffd\ufffdworld",
		},
		{
			name:     "clean normal log stays untouched",
			input:    "2026-10-13 12:00:00 Host VRP %%01INFO/4/UP: Interface up",
			expected: "2026-10-13 12:00:00 Host VRP %%01INFO/4/UP: Interface up",
		},
		{
			name:     "chinese log line with embedded bad bytes (preserved with replacement char)",
			input:    "设备\xff\xfe故障保修",
			expected: "设备\ufffd\ufffd故障保修",
		},
		{
			name:     "clean Chinese log stays untouched",
			input:    "接口 GigabitEthernet0/0/1 状态变为 UP",
			expected: "接口 GigabitEthernet0/0/1 状态变为 UP",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeLogLine(tt.input)
			if got != tt.expected {
				t.Errorf("SanitizeLogLine() = %q, want %q", got, tt.expected)
			}
		})
	}
}

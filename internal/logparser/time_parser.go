package logparser

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

var timePatterns = []struct {
	layout string
	regex  *regexp.Regexp
}{
	// 2026-04-15 14:23:10.123+08:00
	{
		layout: "2006-01-02 15:04:05.999999-07:00",
		regex:  regexp.MustCompile(`^\d{4}-\d{2}-\d{2}\s+\d{2}:\d{2}:\d{2}\.\d+[+-]\d{2}:\d{2}$`),
	},
	// 2026-04-15 14:23:10+08:00
	{
		layout: "2006-01-02 15:04:05-07:00",
		regex:  regexp.MustCompile(`^\d{4}-\d{2}-\d{2}\s+\d{2}:\d{2}:\d{2}[+-]\d{2}:\d{2}$`),
	},
	// 2026-04-15 14:23:10.123
	{
		layout: "2006-01-02 15:04:05.999999",
		regex:  regexp.MustCompile(`^\d{4}-\d{2}-\d{2}\s+\d{2}:\d{2}:\d{2}\.\d+$`),
	},
	// 2026-04-15 14:23:10
	{
		layout: "2006-01-02 15:04:05",
		regex:  regexp.MustCompile(`^\d{4}-\d{2}-\d{2}\s+\d{2}:\d{2}:\d{2}$`),
	},
	// 2026-04-15T14:23:10.123+08:00 / RFC3339Nano
	{
		layout: time.RFC3339Nano,
		regex:  regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d+(?:[+-]\d{2}:\d{2}|Z)$`),
	},
	// 2026-04-15T14:23:10+08:00 / RFC3339
	{
		layout: time.RFC3339,
		regex:  regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:[+-]\d{2}:\d{2}|Z)$`),
	},
	// 2026-04-15T14:23:10.123 (无时区 ISO)
	{
		layout: "2006-01-02T15:04:05.999999",
		regex:  regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d+$`),
	},
	// 2026-04-15T14:23:10 (无时区 ISO)
	{
		layout: "2006-01-02T15:04:05",
		regex:  regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}$`),
	},
	// Apr 15 2026 14:23:10.123
	{
		layout: "Jan 02 2006 15:04:05.999999",
		regex:  regexp.MustCompile(`^[A-Za-z]{3}\s+\d{1,2}\s+\d{4}\s+\d{2}:\d{2}:\d{2}\.\d+$`),
	},
	// Apr 15 2026 14:23:10
	{
		layout: "Jan 02 2006 15:04:05",
		regex:  regexp.MustCompile(`^[A-Za-z]{3}\s+\d{1,2}\s+\d{4}\s+\d{2}:\d{2}:\d{2}$`),
	},
	// Apr 15 14:23:10.123 (默认当年)
	{
		layout: "Jan 02 15:04:05.999999",
		regex:  regexp.MustCompile(`^[A-Za-z]{3}\s+\d{1,2}\s+\d{2}:\d{2}:\d{2}\.\d+$`),
	},
	// Apr 15 14:23:10 (默认当年)
	{
		layout: "Jan 02 15:04:05",
		regex:  regexp.MustCompile(`^[A-Za-z]{3}\s+\d{1,2}\s+\d{2}:\d{2}:\d{2}$`),
	},
}

// ParseHuaweiTimestamp 解析华为日志中多态的时间戳
func ParseHuaweiTimestamp(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, fmt.Errorf("empty timestamp string")
	}

	// 转换前缀时区如 UTC+08:00 2026-04-15 14:23:10 为 2026-04-15 14:23:10+08:00
	if strings.HasPrefix(raw, "UTC") {
		if idx := strings.IndexByte(raw, ' '); idx != -1 {
			offset := raw[3:idx]
			raw = strings.TrimSpace(raw[idx+1:]) + offset
		}
	}

	rawLen := len(raw)

	// Fast-path 1: ISO 格式以数字开头的常见格式 (e.g. 2026-04-15 ...)
	if rawLen >= 19 && raw[0] >= '0' && raw[0] <= '9' && raw[4] == '-' && raw[7] == '-' {
		hasT := (raw[10] == 'T')
		hasSpace := (raw[10] == ' ')

		if hasSpace || hasT {
			if rawLen == 19 {
				// "2006-01-02 15:04:05" 或 "2006-01-02T15:04:05"
				layout := "2006-01-02 15:04:05"
				if hasT {
					layout = "2006-01-02T15:04:05"
				}
				if t, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
					return t, nil
				}
			}

			// 带时区或毫秒
			if hasT {
				if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
					return t, nil
				}
				if t, err := time.Parse(time.RFC3339, raw); err == nil {
					return t, nil
				}
				if t, err := time.ParseInLocation("2006-01-02T15:04:05.999999", raw, time.Local); err == nil {
					return t, nil
				}
			} else {
				if t, err := time.ParseInLocation("2006-01-02 15:04:05.999999-07:00", raw, time.Local); err == nil {
					return t, nil
				}
				if t, err := time.ParseInLocation("2006-01-02 15:04:05-07:00", raw, time.Local); err == nil {
					return t, nil
				}
				if t, err := time.ParseInLocation("2006-01-02 15:04:05.999999", raw, time.Local); err == nil {
					return t, nil
				}
			}
		}
	}

	// Fast-path 2: Syslog BSD 格式 (e.g. Apr 15 2026 14:23:10 或 Apr 15 14:23:10)
	if (raw[0] >= 'A' && raw[0] <= 'Z') || (raw[0] >= 'a' && raw[0] <= 'z') {
		// 尝试带年份
		if t, err := time.ParseInLocation("Jan 02 2006 15:04:05.999999", raw, time.Local); err == nil {
			return t, nil
		}
		if t, err := time.ParseInLocation("Jan 02 2006 15:04:05", raw, time.Local); err == nil {
			return t, nil
		}
		if t, err := time.ParseInLocation("Jan  2 2006 15:04:05", raw, time.Local); err == nil {
			return t, nil
		}

		// 尝试不带年份（默认当年）
		if t, err := time.ParseInLocation("Jan 02 15:04:05.999999", raw, time.Local); err == nil {
			currentYear := time.Now().Year()
			return time.Date(currentYear, t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location()), nil
		}
		if t, err := time.ParseInLocation("Jan 02 15:04:05", raw, time.Local); err == nil {
			currentYear := time.Now().Year()
			return time.Date(currentYear, t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location()), nil
		}
		if t, err := time.ParseInLocation("Jan  2 15:04:05", raw, time.Local); err == nil {
			currentYear := time.Now().Year()
			return time.Date(currentYear, t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location()), nil
		}
	}

	// Fallback: 兜底正则匹配
	for _, p := range timePatterns {
		if p.regex.MatchString(raw) {
			t, err := time.ParseInLocation(p.layout, raw, time.Local)
			if err == nil {
				if t.Year() == 0 {
					// 补全当前年份
					currentYear := time.Now().Year()
					t = time.Date(currentYear, t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
				}
				return t, nil
			}
		}
	}

	return time.Time{}, fmt.Errorf("unable to parse timestamp: %s", raw)
}

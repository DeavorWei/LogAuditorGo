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
		return time.Now(), fmt.Errorf("empty timestamp string")
	}

	// 转换前缀时区如 UTC+08:00 2026-04-15 14:23:10 为 2026-04-15 14:23:10+08:00
	if strings.HasPrefix(raw, "UTC") {
		if idx := strings.IndexByte(raw, ' '); idx != -1 {
			offset := raw[3:idx]
			raw = strings.TrimSpace(raw[idx+1:]) + offset
		}
	}

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

	return time.Now(), fmt.Errorf("unable to parse timestamp: %s", raw)
}

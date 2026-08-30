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
	// Apr 15 2026 14:23:10.123+08:00
	{
		layout: "Jan _2 2006 15:04:05.999999-07:00",
		regex:  regexp.MustCompile(`^[A-Za-z]{3}\s+\d{1,2}\s+\d{4}\s+\d{2}:\d{2}:\d{2}\.\d+[+-]\d{2}:?\d{2}$`),
	},
	// Apr 15 2026 14:23:10+08:00
	{
		layout: "Jan _2 2006 15:04:05-07:00",
		regex:  regexp.MustCompile(`^[A-Za-z]{3}\s+\d{1,2}\s+\d{4}\s+\d{2}:\d{2}:\d{2}[+-]\d{2}:?\d{2}$`),
	},
	// Apr 15 2026 14:23:10.123
	{
		layout: "Jan _2 2006 15:04:05.999999",
		regex:  regexp.MustCompile(`^[A-Za-z]{3}\s+\d{1,2}\s+\d{4}\s+\d{2}:\d{2}:\d{2}\.\d+$`),
	},
	// Apr 15 2026 14:23:10
	{
		layout: "Jan _2 2006 15:04:05",
		regex:  regexp.MustCompile(`^[A-Za-z]{3}\s+\d{1,2}\s+\d{4}\s+\d{2}:\d{2}:\d{2}$`),
	},
	// Apr 15 14:23:10.123+08:00 (默认当年)
	{
		layout: "Jan _2 15:04:05.999999-07:00",
		regex:  regexp.MustCompile(`^[A-Za-z]{3}\s+\d{1,2}\s+\d{2}:\d{2}:\d{2}\.\d+[+-]\d{2}:?\d{2}$`),
	},
	// Apr 15 14:23:10+08:00 (默认当年)
	{
		layout: "Jan _2 15:04:05-07:00",
		regex:  regexp.MustCompile(`^[A-Za-z]{3}\s+\d{1,2}\s+\d{2}:\d{2}:\d{2}[+-]\d{2}:?\d{2}$`),
	},
	// Apr 15 14:23:10.123 (默认当年)
	{
		layout: "Jan _2 15:04:05.999999",
		regex:  regexp.MustCompile(`^[A-Za-z]{3}\s+\d{1,2}\s+\d{2}:\d{2}:\d{2}\.\d+$`),
	},
	// Apr 15 14:23:10 (默认当年)
	{
		layout: "Jan _2 15:04:05",
		regex:  regexp.MustCompile(`^[A-Za-z]{3}\s+\d{1,2}\s+\d{2}:\d{2}:\d{2}$`),
	},
}

// commonTimePattern 可被各解析器正则复用的通用时间戳子式。
//
// PARSE-09: USG 解析器原先只认 `YYYY-MM-DD HH:MM:SS`，
// 变体（BSD / UTC+08:00 前缀）全部落进 VRP 兜底，DeviceType 被错误改写。
// 抽成公共子式后，各解析器共享同一套时间格式认知，不会再出现"某条链路不认某种时间"。
const commonTimePattern = `(?:UTC[+-]\d{1,2}(?::?\d{2})?\s+)?` +
	`(?:[A-Za-z]{3}\s+\d{1,2}\s+(?:\d{4}\s+)?\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:[+-]\d{2}:?\d{2}|Z)?` +
	`|\d{4}-\d{2}-\d{2}[T\s]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:[+-]\d{2}:?\d{2}|Z)?)`

// ParseHuaweiTimestamp 解析华为日志中多态的时间戳
//
// PARSE-11: 同一份日志里混存不同类型时间戳时，原实现会让它们落在不同时区——
// 带 `+08:00` 的走 `time.Parse(RFC3339)` 返回固定 Zone，无时区的走 `ParseInLocation` 返回 Local，
// 两条日志可能相差整小时，时间线排序与 RCA 时序窗口随之错乱。
// 这里统一：解析成功后一律归一到 time.Local，保证同一批日志的时区语义一致。
func ParseHuaweiTimestamp(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, fmt.Errorf("empty timestamp string")
	}

	// 转换前缀时区如 UTC+08:00 2026-04-15 14:23:10 为 2026-04-15 14:23:10+08:00
	//
	// PARSE-12: 原实现直接把原始偏移串拼到末尾，遇到 "UTC+8"（无分钟）会拼出 "...+8"，
	// 而后续所有 layout 都要求 ±07:00 形态，导致整条时间戳解析失败、Timestamp 退化成零值，
	// 时间线直接断裂。这里统一把 +8 / +08 / +0800 规范化为 +08:00。
	if strings.HasPrefix(raw, "UTC") {
		if idx := strings.IndexByte(raw, ' '); idx != -1 {
			offset := raw[3:idx]
			rest := strings.TrimSpace(raw[idx+1:])
			if norm, ok := normalizeUTCOffset(offset); ok {
				raw = rest + norm
			} else if strings.TrimSpace(offset) == "" {
				// 仅 "UTC" 前缀而无偏移量：按无时区处理
				raw = rest
			} else {
				return time.Time{}, fmt.Errorf("invalid UTC offset %q in timestamp %q", offset, raw)
			}
		}
	}

	// PARSE-11: 统一的时区偏移标准化。
	// 形如 `2026-04-15 14:23:10+0800`（偏移量无冒号）在华为设备日志中很常见，
	// 而 Go 的布局要求 `-0700` 与 `-07:00` 是两种不同的占位符，
	// 未标准化时会直接失配、让时间戳退化为零值。
	raw = normalizeNumericOffset(raw)

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
			// PARSE-11: RFC3339 解析得到的是固定 Zone，这里统一归一到 Local
			if hasT {
				if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
					return t.Local(), nil
				}
				if t, err := time.Parse(time.RFC3339, raw); err == nil {
					return t.Local(), nil
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

	// Fast-path 2: Syslog BSD 格式 (e.g. Aug  8 2026 18:07:30+08:00, Apr 15 2026 14:23:10 或 Apr 15 14:23:10)
	if (raw[0] >= 'A' && raw[0] <= 'Z') || (raw[0] >= 'a' && raw[0] <= 'z') {
		// 尝试带年份（优先带时区，支持 Jan _2 与 Jan 02）
		layoutsWithYear := []string{
			"Jan _2 2006 15:04:05.999999-07:00",
			"Jan _2 2006 15:04:05-07:00",
			"Jan 02 2006 15:04:05.999999-07:00",
			"Jan 02 2006 15:04:05-07:00",
			"Jan _2 2006 15:04:05.999999-0700",
			"Jan _2 2006 15:04:05-0700",
			"Jan 02 2006 15:04:05.999999-0700",
			"Jan 02 2006 15:04:05-0700",
			"Jan _2 2006 15:04:05.999999",
			"Jan _2 2006 15:04:05",
			"Jan 02 2006 15:04:05.999999",
			"Jan 02 2006 15:04:05",
			"Jan  2 2006 15:04:05",
		}
		for _, layout := range layoutsWithYear {
			if t, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
				return t, nil
			}
		}

		// 尝试不带年份（默认当年）
		layoutsNoYear := []string{
			"Jan _2 15:04:05.999999-07:00",
			"Jan _2 15:04:05-07:00",
			"Jan 02 15:04:05.999999-07:00",
			"Jan 02 15:04:05-07:00",
			"Jan _2 15:04:05.999999-0700",
			"Jan _2 15:04:05-0700",
			"Jan 02 15:04:05.999999-0700",
			"Jan 02 15:04:05-0700",
			"Jan _2 15:04:05.999999",
			"Jan _2 15:04:05",
			"Jan 02 15:04:05.999999",
			"Jan 02 15:04:05",
			"Jan  2 15:04:05",
		}
		for _, layout := range layoutsNoYear {
			if t, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
				// PARSE-03: 不再无条件取 time.Now().Year()
				return inferYear(t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
			}
		}
	}

	// Fallback: 兜底正则匹配
	for _, p := range timePatterns {
		if p.regex.MatchString(raw) {
			t, err := time.ParseInLocation(p.layout, raw, time.Local)
			if err == nil {
				if t.Year() == 0 {
					// PARSE-03: 缺年份时走窗口推断，不再无条件取当前年
					return inferYear(t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
				}
				return t, nil
			}
		}
	}

	return time.Time{}, fmt.Errorf("unable to parse timestamp: %s", raw)
}

// inferYear 为缺少年份的时间戳推断年份（PARSE-03）。
//
// 原实现无条件使用 time.Now().Year()，存在两个**静默**错误：
//  1. 跨年错位：1 月审计 12 月的日志，会被整体后移约 364 天，时间排序与时间窗筛选全部失真；
//  2. 闰日漂移：平年解析 "Feb 29" 时，Go 的 time.Date 会静默规范化为 3 月 1 日且不返回错误，
//     等于凭空伪造了一个日期，对取证时间线是致命的。
//
// 这里采用 6 个月窗口法：先用当前年份构造，若结果晚于"现在 +24h"则回退到上一年；
// 每次构造后都校验 Day() 是否仍等于原 day，一旦不一致立即换年份，绝不静默挪日。
func inferYear(month time.Month, day, hour, min, sec, nsec int, loc *time.Location) (time.Time, error) {
	if loc == nil {
		loc = time.Local
	}
	now := time.Now().In(loc)
	currentYear := now.Year()
	// 容忍 24h 的时钟偏差与时区差异
	deadline := now.Add(24 * time.Hour)

	for _, y := range [2]int{currentYear, currentYear - 1} {
		t := time.Date(y, month, day, hour, min, sec, nsec, loc)
		// 该年不存在该日期（如平年的 2 月 29 日）：换年份，绝不产出 3 月 1 日这种伪造日期
		if t.Day() != day {
			continue
		}
		// 结果落在未来一天以上，说明这份"缺年份"的日志属于上一年
		if t.After(deadline) {
			continue
		}
		return t, nil
	}

	return time.Time{}, fmt.Errorf("invalid date %02d-%02d: not present in year %d or %d",
		int(month), day, currentYear, currentYear-1)
}

// numericOffsetRegex 匹配以 4 位数字结尾、且没有冒号分隔的时区偏移（如 +0800 / -0530）
var numericOffsetRegex = regexp.MustCompile(`[+-]\d{4}$`)

// normalizeNumericOffset 把尾部形如 `+0800` 的偏移标准化为 `+08:00`（PARSE-11）。
//
// Go 的时间布局里 `-0700` 与 `-07:00` 是互斥的两种占位符，
// 华为设备的 ISO 时间戳两种写法都会出现，不归一化就会漏掉一半。
func normalizeNumericOffset(raw string) string {
	m := numericOffsetRegex.FindStringIndex(raw)
	if m == nil {
		return raw
	}
	tail := raw[m[0]:]
	return raw[:m[0]+len(tail)-2] + ":" + tail[len(tail)-2:]
}

// normalizeUTCOffset 把 +8 / +08 / +0800 / +08:00 规范化为 Go 布局要求的 "+08:00"（PARSE-12）
func normalizeUTCOffset(offset string) (string, bool) {
	off := strings.TrimSpace(offset)
	if off == "" {
		return "", false
	}
	sign := off[0]
	if sign != '+' && sign != '-' {
		return "", false
	}

	body := off[1:]
	hh, mm := body, ""
	if idx := strings.IndexByte(body, ':'); idx >= 0 {
		hh, mm = body[:idx], body[idx+1:]
	} else if len(body) == 4 {
		hh, mm = body[:2], body[2:]
	} else if len(body) == 3 {
		hh, mm = body[:1], body[1:]
	}

	if !isDigits(hh) || len(hh) > 2 {
		return "", false
	}
	if len(hh) == 1 {
		hh = "0" + hh
	}
	if mm == "" {
		mm = "00"
	}
	if len(mm) == 1 {
		mm = "0" + mm
	}
	if !isDigits(mm) || len(mm) != 2 {
		return "", false
	}
	return string(sign) + hh + ":" + mm, true
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

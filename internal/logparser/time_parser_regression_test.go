package logparser_test

import (
	"testing"
	"time"

	"logauditorgo/internal/logparser"
)

// TestTimestampMissingYearCrossYear 回归 PARSE-03：
// 缺年份的时间戳若一律取 time.Now().Year()，1 月审计 12 月的日志会被整体后移约 364 天。
// 这里验证：
//  1. 解析"12 月"且当前处于年初时，年份必须回退到上一年（不得落在未来）；
//  2. 解析"当前月份"时，年份应为当前年（不得误回退）。
func TestTimestampMissingYearCrossYear(t *testing.T) {
	now := time.Now()

	// 构造一个"上个月"的 BSD 时间戳：若今天是 1 月，它会是 12 月，必须推断为上一年；
	// 其他月份则应推断为当前年。两种情况都不允许出现"结果在未来"的错位。
	lastMonth := now.AddDate(0, -1, 0)
	raw := lastMonth.Format("Jan 2") + " 10:20:30"
	// 注意：BSD 格式日不足两位时补空格，与 time.Format("Jan _2") 一致
	raw = lastMonth.Format("Jan _2") + " 10:20:30"

	got, err := logparser.ParseHuaweiTimestamp(raw)
	if err != nil {
		t.Fatalf("ParseHuaweiTimestamp(%q) failed: %v", raw, err)
	}
	if got.Month() != lastMonth.Month() {
		t.Fatalf("month mismatch: got %v, want %v", got.Month(), lastMonth.Month())
	}
	if got.Year() != lastMonth.Year() {
		t.Fatalf("PARSE-03 regression: year mismatch for %q: got %d, want %d",
			raw, got.Year(), lastMonth.Year())
	}
	if got.After(now.Add(24 * time.Hour)) {
		t.Fatalf("PARSE-03 regression: inferred time %v is in the future (now=%v)", got, now)
	}
}

// TestTimestampLeapDayNeverSilentlyShifted 回归 PARSE-03：
// 平年解析 "Feb 29" 时，Go 的 time.Date 会静默规范化为 3 月 1 日且不报错，
// 等于凭空伪造了一个日期。修复后必须显式报错，或返回 2 月内的合法日期。
func TestTimestampLeapDayNeverSilentlyShifted(t *testing.T) {
	now := time.Now()

	// 找到"当前年与上一年都不是闰年"的年份组合，确保 2 月 29 日确实不存在
	leap := func(y int) bool {
		return y%4 == 0 && (y%100 != 0 || y%400 == 0)
	}
	if leap(now.Year()) || leap(now.Year()-1) {
		t.Skipf("skip: %d or %d is a leap year, cannot assert failure", now.Year(), now.Year()-1)
	}

	raw := "Feb 29 23:59:59"
	got, err := logparser.ParseHuaweiTimestamp(raw)
	if err != nil {
		// 显式报错是可接受的结果
		return
	}
	// 若未报错，则绝不能返回 3 月的日期（静默挪日）
	if got.Month() != time.February {
		t.Fatalf("PARSE-03 regression: %q silently shifted to %v (month %v), must stay in February or return error",
			raw, got, got.Month())
	}
}

// TestTimestampUTCOffsetNormalization 回归 PARSE-12：
// "UTC+8"（无分钟）被正则接受，但拼接后所有 layout 都要求 ±07:00 形态，
// 会导致整条时间戳解析失败、Timestamp 退化成零值。
func TestTimestampUTCOffsetNormalization(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"full offset", "UTC+08:00 2026-04-15 14:23:10"},
		{"hour only", "UTC+8 2026-04-15 14:23:10"},
		{"hour two digits", "UTC+08 2026-04-15 14:23:10"},
		{"no colon", "UTC+0800 2026-04-15 14:23:10"},
	}
	for _, c := range cases {
		got, err := logparser.ParseHuaweiTimestamp(c.raw)
		if err != nil {
			t.Fatalf("[%s] ParseHuaweiTimestamp(%q) failed: %v", c.name, c.raw, err)
		}
		if got.IsZero() {
			t.Fatalf("[%s] PARSE-12 regression: %q produced zero timestamp", c.name, c.raw)
		}
		if got.Year() != 2026 || got.Month() != time.April || got.Day() != 15 {
			t.Fatalf("[%s] unexpected parsed date: %v", c.name, got)
		}
		// 各形态偏移都应等价于 +08:00
		if off := got.Format("-07:00"); off != "+08:00" {
			t.Fatalf("[%s] expected offset +08:00, got %s (time=%v)", c.name, off, got)
		}
	}
}

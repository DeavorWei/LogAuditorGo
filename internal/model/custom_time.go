package model

import (
	"database/sql/driver"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// CustomTime 自定义时间类型，兼容多种格式的 JSON 反序列化 (RFC3339, YYYY-MM-DD HH:mm:ss, 时间戳等)
type CustomTime struct {
	time.Time
}

// NewCustomTime 构造 CustomTime 指针
func NewCustomTime(t time.Time) *CustomTime {
	return &CustomTime{Time: t}
}

// timeLayouts 支持的日期时间格式列表
var timeLayouts = []string{
	"2006-01-02 15:04:05",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04",
	time.RFC3339,
	time.RFC3339Nano,
	"2006-01-02T15:04:05",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02",
	"2006/01/02 15:04:05",
	"2006/01/02",
}

// ParseCustomTime 字符串解析为 CustomTime
func ParseCustomTime(str string) (*CustomTime, error) {
	str = strings.TrimSpace(str)
	if str == "" || str == "null" || str == `""` {
		return nil, nil
	}

	// 去除首尾引号
	if len(str) >= 2 && str[0] == '"' && str[len(str)-1] == '"' {
		str = str[1 : len(str)-1]
	}
	str = strings.TrimSpace(str)
	if str == "" {
		return nil, nil
	}

	// 尝试数字时间戳 (秒或毫秒)
	if ts, err := strconv.ParseInt(str, 10, 64); err == nil {
		if ts > 1e11 { // 毫秒级时间戳 (如 1723111200000)
			return &CustomTime{Time: time.UnixMilli(ts)}, nil
		}
		// 秒级时间戳 (如 1723111200)
		return &CustomTime{Time: time.Unix(ts, 0)}, nil
	}

	// 尝试常见日期格式
	for _, layout := range timeLayouts {
		// 若字符串自带时区标识（如 Z 或 +/-08:00），使用标准 Parse
		if strings.HasSuffix(str, "Z") || (len(str) > 19 && (strings.Contains(str[19:], "+") || strings.Contains(str[19:], "-"))) {
			if t, err := time.Parse(layout, str); err == nil {
				return &CustomTime{Time: t}, nil
			}
		} else {
			// 无时区标记的字符串优先使用本地时区解析，避免与本地日志产生 8 小时偏差
			if t, err := time.ParseInLocation(layout, str, time.Local); err == nil {
				return &CustomTime{Time: t}, nil
			}
		}
	}

	return nil, fmt.Errorf("cannot parse %q as valid datetime", str)
}

// UnmarshalJSON 实现 json.Unmarshaler
func (ct *CustomTime) UnmarshalJSON(data []byte) error {
	str := strings.TrimSpace(string(data))
	if str == "" || str == "null" || str == `""` {
		ct.Time = time.Time{}
		return nil
	}

	parsed, err := ParseCustomTime(str)
	if err != nil {
		return err
	}
	if parsed != nil {
		ct.Time = parsed.Time
	} else {
		ct.Time = time.Time{}
	}
	return nil
}

// MarshalJSON 实现 json.Marshaler
func (ct CustomTime) MarshalJSON() ([]byte, error) {
	if ct.IsZero() {
		return []byte("null"), nil
	}
	return []byte(`"` + ct.Format("2006-01-02 15:04:05") + `"`), nil
}

// Value 实现 database/sql/driver.Valuer
func (ct CustomTime) Value() (driver.Value, error) {
	if ct.IsZero() {
		return nil, nil
	}
	return ct.Time, nil
}

// Scan 实现 database/sql.Scanner
func (ct *CustomTime) Scan(value interface{}) error {
	if value == nil {
		ct.Time = time.Time{}
		return nil
	}
	switch v := value.(type) {
	case time.Time:
		ct.Time = v
		return nil
	case string:
		return ct.UnmarshalJSON([]byte(`"` + v + `"`))
	case []byte:
		return ct.UnmarshalJSON([]byte(`"` + string(v) + `"`))
	default:
		return fmt.Errorf("cannot scan type %T into CustomTime", value)
	}
}

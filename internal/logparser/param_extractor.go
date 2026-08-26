package logparser

import (
	"regexp"
	"strings"
)

// 匹配 (Key=Val, Key=[Val], Key="Val", Key='Val')
var paramBlockRegex = regexp.MustCompile(`\((.*?)\)`)
var blockKVRegex = regexp.MustCompile(`([A-Za-z0-9_\-]+)\s*=\s*(?:"([^"]*)"|\[([^\]]*)\]|'([^']*)'|([^,;()]+))`)
var globalKVRegex = regexp.MustCompile(`([A-Za-z0-9_\-]+)\s*=\s*(?:"([^"]*)"|\[([^\]]*)\]|'([^']*)'|([^\s,;)]+))`)

// ExtractParameters 从日志正文中提取动态键值对参数
func ExtractParameters(msg string) map[string]string {
	if strings.IndexByte(msg, '=') == -1 {
		return make(map[string]string)
	}

	params := make(map[string]string, 4)
	ExtractParametersInto(msg, params)
	return params
}

// ExtractParametersInto 从日志正文中提取动态键值对并填充到已有 map 中，减少内存分配
func ExtractParametersInto(msg string, params map[string]string) {
	if strings.IndexByte(msg, '=') == -1 {
		return
	}

	// 1. 提取括号中的 Key=Value (支持逗号/分号分隔的带空格长字符串)
	if strings.IndexByte(msg, '(') != -1 {
		blockMatches := paramBlockRegex.FindAllStringSubmatchIndex(msg, -1)
		for _, bm := range blockMatches {
			if len(bm) >= 4 && bm[2] >= 0 && bm[3] >= 0 {
				subContent := msg[bm[2]:bm[3]]
				if strings.IndexByte(subContent, '=') != -1 {
					extractKV(subContent, blockKVRegex, params, true)
				}
			}
		}
	}

	// 2. 提取整句中散落的 Key="Value" 或 Key=Value
	extractKV(msg, globalKVRegex, params, false)
}

func extractKV(s string, re *regexp.Regexp, params map[string]string, overwrite bool) {
	matches := re.FindAllStringSubmatchIndex(s, -1)
	for _, m := range matches {
		if len(m) < 12 || m[2] < 0 || m[3] < 0 {
			continue
		}
		key := strings.TrimSpace(s[m[2]:m[3]])
		if key == "" {
			continue
		}
		if !overwrite {
			if _, exists := params[key]; exists {
				continue
			}
		}

		var val string
		if m[4] >= 0 && m[5] >= 0 {
			val = s[m[4]:m[5]]
		} else if m[6] >= 0 && m[7] >= 0 {
			val = s[m[6]:m[7]]
		} else if m[8] >= 0 && m[9] >= 0 {
			val = s[m[8]:m[9]]
		} else if m[10] >= 0 && m[11] >= 0 {
			val = s[m[10]:m[11]]
		}
		val = strings.TrimSpace(val)
		if val != "" {
			params[key] = val
		}
	}
}

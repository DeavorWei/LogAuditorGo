package logparser

import (
	"regexp"
	"strings"

	"logauditorgo/pkg/logger"
)

// 匹配 (Key=Val, Key=[Val], Key="Val", Key='Val')
var paramBlockRegex = regexp.MustCompile(`\((.*?)\)`)
var blockKVRegex = regexp.MustCompile(`([A-Za-z0-9_\-]+)\s*=\s*(?:"([^"]*)"|\[([^\]]*)\]|'([^']*)'|([^,;()]+))`)
var globalKVRegex = regexp.MustCompile(`([A-Za-z0-9_\-]+)\s*=\s*(?:"([^"]*)"|\[([^\]]*)\]|'([^']*)'|([^\s,;)]+))`)

// ExtractParameters 从日志正文中提取动态键值对参数
func ExtractParameters(msg string) map[string]string {
	params := make(map[string]string)

	// 1. 提取括号中的 Key=Value (支持逗号/分号分隔的带空格长字符串)
	matches := paramBlockRegex.FindAllStringSubmatch(msg, -1)
	for _, m := range matches {
		if len(m) >= 2 {
			subContent := m[1]
			kvMatches := blockKVRegex.FindAllStringSubmatch(subContent, -1)
			for _, kv := range kvMatches {
				key := strings.TrimSpace(kv[1])
				val := ""
				if kv[2] != "" {
					val = kv[2]
				} else if kv[3] != "" {
					val = kv[3]
				} else if kv[4] != "" {
					val = kv[4]
				} else if kv[5] != "" {
					val = kv[5]
				}
				val = strings.TrimSpace(val)
				if key != "" && val != "" {
					params[key] = val
				}
			}
		}
	}

	// 2. 提取整句中散落的 Key="Value" 或 Key=Value
	kvMatches := globalKVRegex.FindAllStringSubmatch(msg, -1)
	for _, kv := range kvMatches {
		key := strings.TrimSpace(kv[1])
		val := ""
		if kv[2] != "" {
			val = kv[2]
		} else if kv[3] != "" {
			val = kv[3]
		} else if kv[4] != "" {
			val = kv[4]
		} else if kv[5] != "" {
			val = kv[5]
		}
		val = strings.TrimSpace(val)

		// 如果还没被提取过，则补充进去
		if key != "" && val != "" {
			if _, exists := params[key]; !exists {
				params[key] = val
			}
		}
	}

	if len(params) > 0 {
		logger.Log.Debugf("[Param Extractor] Extracted %d dynamic parameters: %v", len(params), params)
	}

	return params
}

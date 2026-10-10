package summary

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode"

	"logauditorgo/pkg/cache"
	"logauditorgo/pkg/logger"
)

// compiledTemplate 存储编译好的模板正则与捕获参数列表
type compiledTemplate struct {
	re         *regexp.Regexp
	paramNames []string
	valid      bool
}

// 模板捕获正则 LRU 缓存，容量 512
var templateCaptureCache = cache.NewLRUCache[string, *compiledTemplate](512)

// 知识库模板头部前缀正则，例如 "FWD/4/SESSCTRLEND: " 或 "%%01FWD/4/hwEntityExtCpuUsageNotfication_clear(l): "
var templateHeaderRegex = regexp.MustCompile(`^(?:%%[0-9a-zA-Z]+\s+)?[\w\-]+/\d+/[\w\-]+(?:\([a-zA-Z]\))?:\s*`)

// 占位符模式：[name], <name>, {name}
var placeholderPattern = regexp.MustCompile(`\[\s*([a-zA-Z0-9_\-]+)\s*\]|<\s*([a-zA-Z0-9_\-]+)\s*>|\{\s*([a-zA-Z0-9_\-]+)\s*\}`)

// 最外层括号块匹配
var parenBlockRegex = regexp.MustCompile(`\(([^()]*(?:\([^()]*\)[^()]*)*)\)`)

// CaptureTemplateParams 用知识库消息模板反向提取正文参数。
// 返回键 = 模板占位符名（即官方参数名），值 = 现场实际值。
// 严格正则失败时自动降级到括号块位置映射。
func CaptureTemplateParams(template, messageBody string) map[string]string {
	params, _ := CaptureTemplateParamsWithProvenance(template, messageBody)
	return params
}

// CaptureTemplateParamsWithProvenance 提取模板参数并返回捕获置信来源（"regex" 或 "positional"）
func CaptureTemplateParamsWithProvenance(template, messageBody string) (map[string]string, string) {
	trimmedTpl := strings.TrimSpace(template)
	trimmedBody := strings.TrimSpace(messageBody)
	if trimmedTpl == "" || trimmedBody == "" {
		return nil, ""
	}

	// 1. 一级策略：严格模板正则提取
	if res := matchTemplateRegex(trimmedTpl, trimmedBody); len(res) > 0 {
		return res, "regex"
	}

	// 2. 二级策略：括号块位置映射（覆盖官方模板拼写错误等降级场景）
	if res := matchParenBlockPositional(trimmedTpl, trimmedBody); len(res) > 0 {
		return res, "positional"
	}

	return nil, ""
}

// matchTemplateRegex 一级策略：使用基于模板构建的正则进行子串匹配与命名捕获提取
func matchTemplateRegex(template, messageBody string) map[string]string {
	ct := getOrCompileTemplate(template)
	if ct == nil || !ct.valid || ct.re == nil || len(ct.paramNames) == 0 {
		return nil
	}

	submatches := ct.re.FindStringSubmatch(messageBody)
	if len(submatches) <= 1 {
		return nil
	}

	// 捕获组数量校验（submatches[0] 是整个匹配串，后续为每个组的值）
	if len(submatches)-1 < len(ct.paramNames) {
		return nil
	}

	results := make(map[string]string, len(ct.paramNames))
	for i, name := range ct.paramNames {
		val := strings.TrimSpace(submatches[i+1])
		if val == "" || isPurePunctuation(val) {
			continue
		}
		// B10: 占位符同名处理（同键异值合并为 JSON 数组，同值去重）
		if existing, ok := results[name]; ok && existing != val {
			var arr []string
			if err := json.Unmarshal([]byte(existing), &arr); err == nil {
				arr = append(arr, val)
				if b, err := json.Marshal(arr); err == nil {
					results[name] = string(b)
				}
			} else {
				arr = []string{existing, val}
				if b, err := json.Marshal(arr); err == nil {
					results[name] = string(b)
				}
			}
		} else {
			results[name] = val
		}
	}

	if len(results) == 0 {
		return nil
	}
	return results
}

// getOrCompileTemplate 从缓存获取或编译模板对应的正则表达式
func getOrCompileTemplate(template string) *compiledTemplate {
	if cached, ok := templateCaptureCache.Get(template); ok && cached != nil {
		return cached
	}

	ct := compileTemplate(template)
	templateCaptureCache.Put(template, ct)
	return ct
}

// compileTemplate 将知识库模板转化为带捕获组的正则表达式
func compileTemplate(template string) *compiledTemplate {
	// 1. 剥离模板头（正文通常不包含 "FWD/4/SESSCTRLEND: " 等头）
	stripped := templateHeaderRegex.ReplaceAllString(template, "")
	stripped = strings.TrimSpace(stripped)
	if stripped == "" {
		return &compiledTemplate{valid: false}
	}

	// 2. 找到所有占位符的位置及原始占位符名称
	matches := placeholderPattern.FindAllStringSubmatchIndex(stripped, -1)
	if len(matches) == 0 {
		return &compiledTemplate{valid: false}
	}

	var paramNames []string
	var regexBuilder strings.Builder
	regexBuilder.WriteString("(?i)")

	lastIdx := 0
	for _, loc := range matches {
		matchStart, matchEnd := loc[0], loc[1]
		// 提取占位符文本中的名称
		var paramName string
		for group := 1; group <= 3; group++ {
			gStart, gEnd := loc[2*group], loc[2*group+1]
			if gStart >= 0 && gEnd >= 0 {
				paramName = strings.TrimSpace(stripped[gStart:gEnd])
				break
			}
		}

		if paramName == "" {
			continue
		}

		// 字面量段（前一个占位符到当前占位符之间）
		literalPart := stripped[lastIdx:matchStart]
		regexBuilder.WriteString(buildLiteralPattern(literalPart))

		// B1 & B12: 判定是否为尾部占位符（后续无有效字面量）
		isTrailing := strings.TrimSpace(stripped[matchEnd:]) == ""

		valuePattern := "(.*?)"
		if isTrailing {
			// B1: 尾部占位符无后随字面量收口，贪婪捕获非定界符字符到行尾/定界符，避免非贪婪匹配吞空串
			valuePattern = `([^,;\r\n]+)`
		} else {
			nextChar := ""
			tail := strings.TrimLeft(stripped[matchEnd:], " \t")
			if len(tail) > 0 {
				nextChar = string([]rune(tail)[0])
			}
			if nextChar == "," || nextChar == ";" || nextChar == ")" {
				// 后邻逗号/分号/右括号，定界匹配，防止空格被吞
				valuePattern = "([^,;)]*?)"
			}
		}

		// 使用序号命名捕获或纯捕获组，避免占位符含 '-'（如 slot-id）导致 Go 正则编译失败
		regexBuilder.WriteString(valuePattern)
		paramNames = append(paramNames, paramName)

		lastIdx = matchEnd
	}

	// 剩余尾部字面量
	if lastIdx < len(stripped) {
		tailPart := stripped[lastIdx:]
		regexBuilder.WriteString(buildLiteralPattern(tailPart))
	}

	pattern := regexBuilder.String()
	re, err := regexp.Compile(pattern)
	if err != nil {
		logger.Log.Warnf("[TemplateCapture] Compile template regex failed for pattern '%s': %v", pattern, err)
		return &compiledTemplate{valid: false}
	}

	return &compiledTemplate{
		re:         re,
		paramNames: paramNames,
		valid:      true,
	}
}

// buildLiteralPattern 将模板字面量文本转义为容差正则
func buildLiteralPattern(literal string) string {
	if literal == "" {
		return ""
	}

	// 1. 先做正则元字符转义
	quoted := regexp.QuoteMeta(literal)

	// 2. B5: 将连续空白转义为 \s+，并智能合并 '=' 前后的空白为 \s*=\s*，杜绝等号前多余 \s+
	var b strings.Builder
	runes := []rune(quoted)
	n := len(runes)

	for i := 0; i < n; {
		r := runes[i]
		if r == ' ' || r == '\t' || r == '\r' || r == '\n' {
			// 向前看：检查这一串空白后面是否紧跟 '='
			j := i + 1
			for j < n && (runes[j] == ' ' || runes[j] == '\t' || runes[j] == '\r' || runes[j] == '\n') {
				j++
			}
			if j < n && runes[j] == '=' {
				// 空白后紧跟 '='，整体合并为 \s*=\s*，并跳过等号及其后续空白
				b.WriteString(`\s*=\s*`)
				i = j + 1
				for i < n && (runes[i] == ' ' || runes[i] == '\t' || runes[i] == '\r' || runes[i] == '\n') {
					i++
				}
				continue
			}
			b.WriteString(`\s+`)
			i = j
		} else if r == '=' {
			b.WriteString(`\s*=\s*`)
			i++
			for i < n && (runes[i] == ' ' || runes[i] == '\t' || runes[i] == '\r' || runes[i] == '\n') {
				i++
			}
		} else {
			b.WriteRune(r)
			i++
		}
	}

	return b.String()
}

// matchParenBlockPositional 二级策略：提取正文与模板中的括号块按位置及词集映射
func matchParenBlockPositional(template, messageBody string) map[string]string {
	// B2: 必须先剥离模板头（如 "FWD/4/hwCpuOver(l):"），否则头部的 "(l)" 会被误当成模板括号块
	strippedTpl := templateHeaderRegex.ReplaceAllString(template, "")

	// 1. 提取模板最外层括号块
	tplParenMatch := parenBlockRegex.FindStringSubmatch(strippedTpl)
	if len(tplParenMatch) < 2 {
		return nil
	}
	tplBlock := strings.TrimSpace(tplParenMatch[1])

	// 2. 提取正文最外层括号块
	bodyParenMatch := parenBlockRegex.FindStringSubmatch(messageBody)
	if len(bodyParenMatch) < 2 {
		return nil
	}
	bodyBlock := strings.TrimSpace(bodyParenMatch[1])

	// 3. 按 ',' 切分成项
	tplItems := splitCommaItems(tplBlock)
	bodyItems := splitCommaItems(bodyBlock)

	if len(tplItems) == 0 || len(tplItems) != len(bodyItems) {
		return nil
	}

	results := make(map[string]string, len(tplItems))

	for i := 0; i < len(tplItems); i++ {
		tplItem := tplItems[i]
		bodyItem := bodyItems[i]

		// 模板项解析：keyPart = [paramName]
		tplEqIdx := strings.Index(tplItem, "=")
		if tplEqIdx == -1 {
			return nil
		}
		tplKeyStr := strings.TrimSpace(tplItem[:tplEqIdx])
		tplValStr := strings.TrimSpace(tplItem[tplEqIdx+1:])

		// 提取占位符名称
		paramMatches := placeholderPattern.FindStringSubmatch(tplValStr)
		if len(paramMatches) < 2 {
			return nil
		}
		var paramName string
		for g := 1; g < len(paramMatches); g++ {
			if paramMatches[g] != "" {
				paramName = strings.TrimSpace(paramMatches[g])
				break
			}
		}
		if paramName == "" {
			return nil
		}

		// 正文项解析：key=value
		bodyEqIdx := strings.Index(bodyItem, "=")
		if bodyEqIdx == -1 {
			return nil
		}
		bodyKeyStr := strings.TrimSpace(bodyItem[:bodyEqIdx])
		bodyValStr := strings.TrimSpace(bodyItem[bodyEqIdx+1:])

		// 弱类型与空值检查
		if bodyValStr == "" || isPurePunctuation(bodyValStr) {
			return nil
		}

		// 词集匹配检查：模板键词集 ⊇ 正文键词集（允许编辑距离 <= 1 容错）
		if !wordsCompatible(tplKeyStr, bodyKeyStr) {
			return nil
		}

		// 数值语义弱类型检查
		if requiresNumericValue(tplKeyStr, bodyKeyStr) && !containsDigit(bodyValStr) {
			return nil
		}

		results[paramName] = bodyValStr
	}

	return results
}

// splitCommaItems 将括号块中的内容按逗号切分，忽略嵌套括号内的逗号
func splitCommaItems(s string) []string {
	var items []string
	start := 0
	depth := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '(' {
			depth++
		} else if c == ')' {
			if depth > 0 {
				depth--
			}
		} else if c == ',' && depth == 0 {
			items = append(items, strings.TrimSpace(s[start:i]))
			start = i + 1
		}
	}
	if start < len(s) {
		items = append(items, strings.TrimSpace(s[start:]))
	}
	return items
}

// extractWords 提取字符串中的单词小写切片
func extractWords(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	res := make([]string, 0, len(fields))
	for _, f := range fields {
		w := strings.ToLower(strings.TrimSpace(f))
		if w != "" {
			res = append(res, w)
		}
	}
	return res
}

// wordsCompatible 校验模板词集是否覆盖正文词集（支持编辑距离 <= 1）
func wordsCompatible(tplKey, bodyKey string) bool {
	tplWords := extractWords(tplKey)
	bodyWords := extractWords(bodyKey)
	if len(bodyWords) == 0 || len(tplWords) == 0 {
		return false
	}

	// 正文中的每个词必须在模板词集中存在等价或相近的词（编辑距离 <= 1）
	for _, bw := range bodyWords {
		matched := false
		for _, tw := range tplWords {
			if bw == tw || levenshteinDistance(bw, tw) <= 1 {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

// requiresNumericValue 检查键名是否暗示数值类型
func requiresNumericValue(tplKey, bodyKey string) bool {
	combined := strings.ToLower(tplKey + " " + bodyKey)
	numericKeywords := []string{"usage", "threshold", "threashold", "count", "num", "percent", "rate", "value", "val"}
	for _, kw := range numericKeywords {
		if strings.Contains(combined, kw) {
			return true
		}
	}
	return false
}

// containsDigit 检查字符串中是否包含数字
func containsDigit(s string) bool {
	for _, r := range s {
		if unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// isPurePunctuation 检查字符串是否由纯标点或空白构成
func isPurePunctuation(s string) bool {
	hasNonPunct := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			hasNonPunct = true
			break
		}
	}
	return !hasNonPunct
}

// levenshteinDistance 计算两个字符串的 Levenshtein 编辑距离
func levenshteinDistance(a, b string) int {
	ra := []rune(a)
	rb := []rune(b)
	la := len(ra)
	lb := len(rb)

	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}

	prev := make([]int, lb+1)
	curr := make([]int, lb+1)

	for j := 0; j <= lb; j++ {
		prev[j] = j
	}

	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 0
			if ra[i-1] != rb[j-1] {
				cost = 1
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost

			min := del
			if ins < min {
				min = ins
			}
			if sub < min {
				min = sub
			}
			curr[j] = min
		}
		copy(prev, curr)
	}

	return prev[lb]
}

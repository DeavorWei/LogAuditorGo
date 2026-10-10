package logparser

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxParamKeyRunes 键名最大长度（rune 计）。
// 放宽键名字符类以支持中文键（PARSE-18）后，必须限制长度，
// 否则整段中文正文都可能被误吞成一个"键名"。
const maxParamKeyRunes = 64

// paramBlockRegex 匹配最外层的括号块并捕获其内部内容。
// 支持一层嵌套（例如 `Location=(1,2)` 这种"值里带括号"的场景），
// 从而避免旧实现把 `(1,2)` 里的逗号误当作键值对分隔符。
var paramBlockRegex = regexp.MustCompile(`\(([^()]*(?:\([^()]*\)[^()]*)*)\)`)

// braceBalancedBlockRegex 仅为保持语义可读性而保留的构造器，返回上述正则
func braceBalancedBlockRegex() *regexp.Regexp {
	return paramBlockRegex
}

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
//
// 处理顺序（与旧实现一致，保证"括号内的结构化参数优先于正文中的散落参数"）：
//  1. 逐个提取括号块内的 Key=Value（块内允许覆盖，后出现的同名键合并为数组）；
//  2. 再对整句做一次全局扫描，已存在的键不再覆盖。
func ExtractParametersInto(msg string, params map[string]string) {
	if strings.IndexByte(msg, '=') == -1 {
		return
	}

	// 1. 提取括号中的 Key=Value (支持逗号/分号分隔的带空格长字符串、嵌套括号)
	if strings.IndexByte(msg, '(') != -1 {
		for _, sub := range paramBlockRegex.FindAllStringSubmatch(msg, -1) {
			if len(sub) < 2 {
				continue
			}
			subContent := sub[1]
			if strings.IndexByte(subContent, '=') != -1 {
				scanKVPairs(subContent, func(k, v string) {
					putParam(params, k, v, true)
				})
			}
		}
	}

	// 2. 提取整句中散落的 Key="Value" 或 Key=Value（不覆盖已解析出的块内参数）
	scanKVPairs(msg, func(k, v string) {
		putParam(params, k, v, false)
	})
}

// putParam 写入一个键值对。
//
// PARSE-18: 原实现"块内后者覆盖、全局先者优先"两套语义并存，
// 同名键的最终取值取决于它出现在括号内还是正文里，行为不可预测。
// 现在统一为：块内允许覆盖（后到的更具体），全局扫描永不覆盖已有键；
// 同名键出现不同取值时合并为 JSON 数组，绝不静默丢弃任何一方。
func putParam(params map[string]string, k, v string, overwrite bool) {
	old, exists := params[k]
	if !exists {
		params[k] = v
		return
	}
	if !overwrite || old == v {
		return
	}

	arr := make([]string, 0, 2)
	if strings.HasPrefix(old, "[") && strings.HasSuffix(old, "]") {
		if err := json.Unmarshal([]byte(old), &arr); err != nil {
			arr = []string{old}
		}
	} else {
		arr = append(arr, old)
	}
	arr = append(arr, v)
	if b, err := json.Marshal(arr); err == nil {
		params[k] = string(b)
		return
	}
	params[k] = v
}

// scanKVPairs 以手写扫描器遍历 s 中的 Key=Value 对。
//
// 之所以放弃单一正则（PARSE-08 / PARSE-18）：
//   - `Key=` 后紧跟空格或下一组键时，正则会把下一组键吞成自己的值；
//   - 括号外的值一遇空格就被截断（Description=Port to core 只剩 Port）；
//   - 中文键名无法用 `[A-Za-z0-9_\-]+` 表达；
//   - 括号包裹的值（如 Location=(1,2)）在全局分支被逗号截成 `(1`。
//
// 手写扫描器对上述每一条都能给出明确、可测的语义。
func scanKVPairs(s string, emit func(key, value string)) {
	n := len(s)
	i := 0

	for i < n {
		// 跳过分隔符（空白、逗号、分号、括号）
		for i < n && isSeparatorByte(s[i]) {
			i++
		}
		if i >= n {
			return
		}

		key, nextI, ok := scanKeyAt(s, i)
		if !ok {
			// 当前字符不可能构成键名，前进一个 rune 继续
			_, size := utf8.DecodeRuneInString(s[i:])
			if size <= 0 {
				size = 1
			}
			i += size
			continue
		}

		val, next := scanValue(s, nextI)
		emit(key, val)
		if next <= nextI {
			// 防御：值扫描未推进时强制前进，杜绝死循环
			next = nextI + 1
		}
		i = next
	}
}

// scanValue 从 s[i:] 扫描一个参数值，返回值与下一次扫描的起始下标
func scanValue(s string, i int) (string, int) {
	n := len(s)
	if i >= n {
		return "", i
	}

	switch s[i] {
	case '"', '\'':
		return scanDelimited(s, i, s[i], s[i])
	case '[':
		return scanDelimited(s, i, '[', ']')
	case '(':
		return scanDelimited(s, i, '(', ')')
	}

	// PARSE-08: 空值判定。
	// `Reason= Code=5` 中的 Reason 必须被记录为 `Reason: ""`，
	// 而不是把下一组键吞成自己的值；"该字段为空"本身也是一条审计事实。
	if s[i] == ' ' || s[i] == '\t' || s[i] == ',' || s[i] == ';' || s[i] == ')' {
		k := i
		for k < n && (s[k] == ' ' || s[k] == '\t') {
			k++
		}
		if k >= n || s[k] == ',' || s[k] == ';' || s[k] == ')' || looksLikeKeyAt(s, k) {
			return "", i
		}
	}

	// 普通值：遇到 , ; ( ) 结束；
	// 遇到空格时，只有"空格之后紧跟 Key= 结构"才截断，否则空格视为值的一部分；
	// 遇到连字符 '-' 时，若其后紧随 Key= 结构（如 -alarmID=...）则切分值。
	j := i
	for j < n {
		c := s[j]
		if c == ',' || c == ';' || c == '(' || c == ')' || c == '\n' || c == '\r' {
			break
		}
		if c == ' ' || c == '\t' {
			k := j
			for k < n && (s[k] == ' ' || s[k] == '\t') {
				k++
			}
			if k >= n || s[k] == ',' || s[k] == ';' || s[k] == ')' || looksLikeKeyAt(s, k) {
				break
			}
		}
		if c == '-' {
			if looksLikeKeyAt(s, j+1) {
				return strings.TrimSpace(s[i:j]), j + 1
			}
		}
		j++
	}
	return strings.TrimSpace(s[i:j]), j
}

// scanDelimited 扫描由 open/close 包裹的值，支持一层嵌套。
// open == close（引号）时退化为"找下一个相同字符"，不做深度计数，
// 否则同一个字符既算开又算闭会导致深度永远无法归零。
func scanDelimited(s string, i int, open, close byte) (string, int) {
	n := len(s)

	if open == close {
		if j := byteIndex(s, open, i+1); j >= 0 {
			return s[i+1 : j], j + 1
		}
		return s[i+1:], n
	}

	depth := 0
	j := i
	for j < n {
		if s[j] == open {
			depth++
		} else if s[j] == close {
			depth--
			if depth == 0 {
				return s[i+1 : j], j + 1
			}
		}
		j++
	}
	// 未闭合：按"到结尾"降级处理，并保留内容而非丢弃
	return s[i+1:], n
}

// byteIndex 从 from 开始查找字节 c 第一次出现的位置
func byteIndex(s string, c byte, from int) int {
	for i := from; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// readWord 从 pos 开始读取一个连续单词（由 isKeyChar 字符组成）
func readWord(s string, pos int) (string, int) {
	n := len(s)
	if pos >= n {
		return "", pos
	}
	i := pos
	runes := 0
	for i < n && runes < maxParamKeyRunes {
		r, size := utf8.DecodeRuneInString(s[i:])
		if !isKeyChar(r) {
			break
		}
		i += size
		runes++
	}
	if i == pos {
		return "", pos
	}
	return s[pos:i], i
}

// isValidKeyWord 判定一个单词是否是合法的复合键组成词（由字母、数字、下划线、短横线或汉字组成）
func isValidKeyWord(w string) bool {
	if w == "" {
		return false
	}
	for _, r := range w {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

// scanKeyAt 从 pos 扫描键名（支持单词键及由空格连接的复合键，如 "forwarding type"、"slot id"）
func scanKeyAt(s string, pos int) (string, int, bool) {
	n := len(s)
	if pos >= n {
		return "", pos, false
	}

	w, next := readWord(s, pos)
	if w == "" {
		return "", pos, false
	}

	// 1. 单词键快速路径：若直接紧随 '='，单词键直接成立（保持原有高兼容性）
	j := next
	for j < n && (s[j] == ' ' || s[j] == '\t') {
		j++
	}
	if j < n && s[j] == '=' {
		key := strings.TrimSpace(w)
		if key != "" && utf8.RuneCountInString(key) <= maxParamKeyRunes {
			return key, j + 1, true
		}
	}

	// 2. 复合键吸收：只有当第一个词为合法词汇且后续有空格时，才尝试吸收后续词
	if !isValidKeyWord(w) {
		return "", pos, false
	}

	words := []string{w}
	curr := next

	// 复合键最多吸收 4 个词（如 "current CPU usage" 为 3 个词）
	for len(words) < 4 {
		j := curr
		for j < n && (s[j] == ' ' || s[j] == '\t') {
			j++
		}
		if j >= n {
			return "", pos, false
		}

		// 复合键词与词之间必须由至少一个空白隔开
		if j == curr {
			return "", pos, false
		}

		nextW, nextPos := readWord(s, j)
		if nextW == "" || !isValidKeyWord(nextW) {
			return "", pos, false
		}
		words = append(words, nextW)
		curr = nextPos

		// 检查当前吸收后的词后面是否紧跟 '='
		k := curr
		for k < n && (s[k] == ' ' || s[k] == '\t') {
			k++
		}
		if k < n && s[k] == '=' {
			key := strings.Join(words, " ")
			key = strings.TrimSpace(key)
			if key != "" && utf8.RuneCountInString(key) <= maxParamKeyRunes {
				return key, k + 1, true
			}
		}
	}

	return "", pos, false
}

// looksLikeKeyAt 判断 s[pos:] 是否为 "Key=" 结构（用于决定空格或连字符是否截断值）
func looksLikeKeyAt(s string, pos int) bool {
	_, _, ok := scanKeyAt(s, pos)
	return ok
}

// isKeyChar 判定字符是否可以作为键名的一部分。
// PARSE-18: 只排除结构性分隔符，其余字符（含中文）均可作为键名。
func isKeyChar(r rune) bool {
	switch r {
	case ' ', '\t', '\r', '\n', '=', ',', ';', '(', ')', '[', ']', '"', '\'':
		return false
	}
	return true
}

// isSeparatorByte 判定字节是否为键值对之间的分隔符
func isSeparatorByte(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == ',' || c == ';' || c == '(' || c == ')'
}

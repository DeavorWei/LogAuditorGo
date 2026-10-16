package logparser

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ansiRegex 匹配 ANSI 终端控制序列：
// 1. CSI 模式（包含带 ? 前缀的私有模式如 \x1b[?25l）: \x1b\[[0-9;?]*[a-zA-Z]
// 2. OSC 模式（如窗口标题设置 \x1b]0;title\x07）: \x1b\][^\x07\x1b]*(?:\x07|\x1b\\)
// 3. 字符集切换（如 \x1b(B）: \x1b[()][0-9A-B]
var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[()][0-9A-B]`)

// IsBlankOrGarbageLine 判断一行是否全由空白字符、\x00、不可见控制字符或纯非法坏字节组成。
// 只要文本中包含任一可成功解码的有效可见字符（可见 ASCII 或可打印 Unicode 字符如中文），即视为有效行。
// 仅当整行完全由不可见字符或完全无法解析的二进制坏字节组成时，才判定为垃圾空洞行予以丢弃。
//
// 设计取舍说明：
// 本函数与 SanitizeLogLine 对“纯不可见格式字符”（如 Unicode Cf 类字符：U+00AD 软连字符、U+200B 零宽空格等）
// 存在有意为之的设计差异：
// - 若整行完全由此类不可见格式字符组成，因缺乏任何可见业务内容，将被本函数判为垃圾行直接丢弃，防止幽灵空记录入库；
// - 但若此类字符混杂在正常日志中，SanitizeLogLine 不会强行将其剔除，以防破坏多语言文本编码或特定格式。
func IsBlankOrGarbageLine(s string) bool {
	for i := 0; i < len(s); {
		b := s[i]
		// 1. 快速检查 ASCII 字符
		if b < 0x80 {
			if b > 0x20 && b != 0x7F {
				// 存在可见 ASCII 字符（如英文、数字、标点符号），直接判定为有效行
				return false
			}
			i++
			continue
		}

		// 2. 非 ASCII 字节：尝试解码 UTF-8 rune
		r, size := utf8.DecodeRuneInString(s[i:])
		if r != utf8.RuneError || size > 1 {
			// 解码出合法的 Unicode rune，检查是否为可打印且非空白字符（如汉字、全角标点等）
			if unicode.IsPrint(r) && !unicode.IsSpace(r) {
				return false
			}
		}
		// 若为非法 UTF-8 字节（RuneError 且 size == 1），跳过该坏字节继续探测后续
		i += size
	}

	// 遍历完毕，未找到任何一个可见/可打印字符，判定为垃圾空洞行
	return true
}

// SanitizeLogLine 对日志行进行前置清洗与标准化。
// 彻底剔除控制字符，剥离 ANSI 颜色码与序列，将 \x00 与控制符替换为空格避免粘词，并规范化非法 UTF-8 字节。
func SanitizeLogLine(s string) string {
	if s == "" {
		return ""
	}

	// 1. 去除 UTF-8 BOM 头
	s = strings.TrimPrefix(s, "\xef\xbb\xbf")

	// 2. 剥离 ANSI 终端控制码
	if strings.IndexByte(s, 0x1b) >= 0 {
		s = ansiRegex.ReplaceAllString(s, "")
	}

	// 3. Fast-Path 快速探查：绝大多数正常日志无需逐 rune 过滤
	needsClean := false
	for i := 0; i < len(s); i++ {
		b := s[i]
		// 检查是否有 \x00、C0 控制字符（排除 \t=0x09）或 DEL(0x7F)
		if (b < 0x20 && b != '\t') || b == 0x7F {
			needsClean = true
			break
		}
	}

	// 检查是否包含非法 UTF-8 序列
	if !needsClean && !utf8.ValidString(s) {
		needsClean = true
	}

	if !needsClean {
		return MaskSensitiveCredentials(strings.TrimSpace(s))
	}

	// 4. Slow-Path 慢路径清洗：使用 strings.Builder 规范化字符
	var b strings.Builder
	b.Grow(len(s))

	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			// 非法字节，使用 Unicode 替代字符替换
			b.WriteRune('\uFFFD')
			i += size
			continue
		}

		if r == 0x7F {
			// 彻底剥离 DEL
		} else if r == 0 || (r < 0x20 && r != '\t') {
			// 将 NUL (\x00) 及其他 C0 控制字符统一转为空格，避免粘词（如 ABC\x00DEF -> ABC DEF）
			b.WriteByte(' ')
		} else {
			b.WriteRune(r)
		}
		i += size
	}

	return MaskSensitiveCredentials(strings.TrimSpace(b.String()))
}

var (
	// 匹配华为特征密文字符串 (前缀 %^%#，后缀 %^# 或 %^%#)
	huaweiCipherRegex = regexp.MustCompile(`%\x5e%#[^\s,;]+?%\x5e%?#`)

	// 匹配命令行及正文中明文口令、密文与 Community (含 irreversible-cipher 与多修饰词组合)
	credCmdRegex = regexp.MustCompile(`(?i)\b(password(?:\s+(?:simple|cipher|irreversible-cipher))?|(?:snmp(?:-agent)?\s+)?community(?:\s+(?:read|write))?(?:\s+(?:simple|cipher))?|shared-key(?:\s+(?:simple|cipher))?|pre-shared-key(?:\s+(?:simple|cipher))?)\s+(\S+)`)

	// 匹配 JSON 报文中的敏感凭据
	credJSONRegex = regexp.MustCompile(`(?i)"(pass(?:word)?|pwd|secret|token|community|shared[_-]?key|pre[_-]?shared[_-]?key|api[_-]?key|auth[_-]?key)"\s*:\s*"([^"]*)"`)

	// 匹配键值对参数中的口令 (避免宽泛裸 key= 误伤正常网络参数如 key=10)
	credKVRegex = regexp.MustCompile(`(?i)\b(pass(?:word)?|pwd|secret|token|shared[_-]?key|pre[_-]?shared[_-]?key|api[_-]?key|auth[_-]?key)\s*=\s*([^,;\s\)]+)`)
)

// MaskSensitiveCredentials 对日志中暴露的密码、密文散列与只读/读写共同体进行脱敏遮盖 (ISSUE-LOG-01 / N1)
func MaskSensitiveCredentials(s string) string {
	lower := strings.ToLower(s)
	if strings.IndexByte(s, '=') == -1 &&
		strings.IndexByte(s, ':') == -1 &&
		!strings.Contains(s, "%^%#") &&
		!strings.Contains(lower, "password") &&
		!strings.Contains(lower, "cipher") &&
		!strings.Contains(lower, "community") &&
		!strings.Contains(lower, "secret") &&
		!strings.Contains(lower, "token") {
		return s
	}

	// 1. JSON 结构脱敏
	if strings.IndexByte(s, '{') != -1 && strings.IndexByte(s, ':') != -1 {
		s = credJSONRegex.ReplaceAllString(s, `"$1":"******"`)
	}

	// 2. 键值对参数脱敏
	if strings.IndexByte(s, '=') != -1 {
		s = credKVRegex.ReplaceAllString(s, "$1=******")
	}

	// 3. 命令行与前缀链命令脱敏
	s = credCmdRegex.ReplaceAllStringFunc(s, func(m string) string {
		parts := strings.Fields(m)
		if len(parts) >= 2 {
			last := strings.ToLower(parts[len(parts)-1])
			// 排除纯描述性状态谓词
			if last == "error" || last == "failed" || last == "mismatch" || last == "invalid" ||
				last == "expired" || last == "changed" || last == "policy" || last == "required" {
				return m
			}
			parts[len(parts)-1] = "******"
			return strings.Join(parts, " ")
		}
		return m
	})

	// 4. 特征密文兜底脱敏 (如独立出现或未被前置规则遮盖的 %^%#...%^#)
	if strings.Contains(s, "%^%#") {
		s = huaweiCipherRegex.ReplaceAllString(s, "******")
	}

	return s
}

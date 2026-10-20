package api

import (
	"fmt"
	"mime"
	"strings"
	"time"
	"unicode/utf8"
)

// maxDownloadNameRunes 文件名主体长度上限（避免超长文件名在 Windows 上失败）。
const maxDownloadNameRunes = 80

// sanitizeDownloadName 过滤 Windows 非法文件名字符与路径分隔符。
func sanitizeDownloadName(name string) string {
	name = strings.TrimSpace(name)
	replacer := strings.NewReplacer(
		"\\", "_", "/", "_", ":", "_", "*", "_", "?", "_",
		"\"", "_", "<", "_", ">", "_", "|", "_",
		"\r", " ", "\n", " ", "\t", " ",
	)
	name = replacer.Replace(name)
	name = strings.Trim(name, " .")
	if utf8.RuneCountInString(name) > maxDownloadNameRunes {
		runes := []rune(name)
		name = string(runes[:maxDownloadNameRunes])
	}
	return name
}

// attachmentHeader 生成 RFC 5987 兼容的下载头："<任务名>_<日期>.<ext>"。
//
// 使用 mime.FormatMediaType 而不是手拼 filename=，让中文任务名通过
// filename* 参数传递，避免浏览器把 UTF-8 文件名解码成乱码。
func attachmentHeader(taskName, suffix, ext string) string {
	base := sanitizeDownloadName(taskName)
	if base == "" {
		base = "report"
	}
	parts := []string{base}
	if suffix != "" {
		parts = append(parts, sanitizeDownloadName(suffix))
	}
	parts = append(parts, time.Now().Format("20060102"))
	filename := strings.Join(parts, "_") + "." + ext

	if value := mime.FormatMediaType("attachment", map[string]string{"filename": filename}); value != "" {
		return value
	}
	return fmt.Sprintf("attachment; filename=\"%s\"", base+"."+ext)
}

package hdx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"logauditorgo/internal/model"
)

var whitespaceRegex = regexp.MustCompile(`\s+`)
var validTitleRegex = regexp.MustCompile(`^[A-Za-z0-9_]+/[1-8]/[A-Za-z0-9_\-]+$`)

// CleanText 去除多余空白与换行
func CleanText(s string) string {
	s = strings.ReplaceAll(s, "\u00a0", " ") // 去除非换行空格
	s = strings.TrimSpace(s)
	return whitespaceRegex.ReplaceAllString(s, " ")
}

// CleanMultilineText 保留换行并清洗多余水平空格
func CleanMultilineText(s string) string {
	s = strings.ReplaceAll(s, "\u00a0", " ")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	var cleanLines []string
	for _, l := range lines {
		trimmed := strings.TrimSpace(whitespaceRegex.ReplaceAllString(l, " "))
		if trimmed != "" {
			cleanLines = append(cleanLines, trimmed)
		}
	}
	return strings.Join(cleanLines, "\n")
}

// ExtractBlockText 提取 HTML 元素内部文本，保留段落、换行与列表。
//
// KB-05: 原实现**就地修改传入的共享 DOM**——
// `sel.Find("br").ReplaceWithHtml("\n")` 与对每个 div/p/li 的 `AppendHtml("\n")`
// 都会直接改写调用方持有的 goquery 文档树。
// 而 parseLogRef 会对重叠子树多次调用它，parseAlarmRef 更是反复作用于 div.section，
// 于是"每调用一次就给全文档 div 再追加一个换行"，
// 解析结果依赖调用顺序，跨版本 HTML 结构差异还会放大文本噪声。
//
// 改为纯函数：先深拷贝再在副本上做替换，调用方的 DOM 保持原样，
// 同一段输入无论调用多少次都得到完全一致的输出。
func ExtractBlockText(sel *goquery.Selection) string {
	if sel == nil || sel.Length() == 0 {
		return ""
	}
	clone := sel.Clone()
	clone.Find("br").ReplaceWithHtml("\n")
	clone.Find("p, li, tr, dt, dd, div").Each(func(_ int, s *goquery.Selection) {
		s.AppendHtml("\n")
	})
	return CleanMultilineText(clone.Text())
}

// maxHTMLFileBytes 单个 HTML 文档的最大读取字节数 (KB-12)。
// 32 并发 × 大 HTML 会造成内存尖峰，这里给出硬上限并明确报错。
const maxHTMLFileBytes = 10 << 20 // 10MB

// resolveHTMLPath 解析并校验 HTML 文件路径，防御目录穿越与编码问题 (KB-12)。
//
// 原实现只做了"去掉 # 锚点"，navi.xml 里的 `../` 或 `%20` 编码 URL
// 会导致越界读取或文件不存在（静默丢条目）。
func resolveHTMLPath(docRootDir string, item LeafNaviItem) (string, error) {
	cleanURL := strings.TrimSpace(item.URL)
	if idx := strings.Index(cleanURL, "#"); idx != -1 {
		cleanURL = cleanURL[:idx]
	}
	if cleanURL == "" {
		return "", fmt.Errorf("empty html URL")
	}

	// 1. URL 解码兜底：HDX 文档中的文件名可能带 %20 之类的百分号编码
	if strings.Contains(cleanURL, "%") {
		if unescaped, err := url.PathUnescape(cleanURL); err == nil && unescaped != "" {
			cleanURL = unescaped
		}
	}
	// Windows 下路径分隔符可能是 '/'
	cleanURL = strings.ReplaceAll(cleanURL, "/", string(filepath.Separator))

	root := filepath.Clean(docRootDir)
	full := filepath.Clean(filepath.Join(root, item.NaviDir, cleanURL))

	// 2. 目录穿越校验：解析后的绝对路径必须仍在文档根目录内
	rel, err := filepath.Rel(root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("html path %q escapes the document root %q", item.URL, root)
	}
	return full, nil
}

// readHTMLFile 读取并校验 HTML 文件内容 (KB-12)
func readHTMLFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("html path %q is a directory", path)
	}
	if info.Size() > maxHTMLFileBytes {
		return nil, fmt.Errorf("html file %q is too large: %d bytes (limit %d)", path, info.Size(), maxHTMLFileBytes)
	}
	return os.ReadFile(path)
}

// ParseHTMLKnowledge 解析单个 HTML 文件提取知识
func ParseHTMLKnowledge(docRootDir string, item LeafNaviItem) (k *model.Knowledge, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in ParseHTMLKnowledge: %v", r)
		}
	}()
	
	// KB-12: 统一走路径解析 + 目录穿越校验 + 大小上限保护
	htmlPath, err := resolveHTMLPath(docRootDir, item)
	if err != nil {
		return nil, err
	}

	rawBytes, err := readHTMLFile(htmlPath)
	if err != nil {
		return nil, fmt.Errorf("read html (%s) failed: %w", htmlPath, err)
	}

	utf8Bytes, err := DecodeGBKBytes(rawBytes)
	if err != nil {
		utf8Bytes = rawBytes // 降级直接使用
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(utf8Bytes))
	if err != nil {
		return nil, fmt.Errorf("parse html dom failed: %w", err)
	}

	k = &model.Knowledge{
		EntryType: item.EntryType,
		Module:    strings.TrimSpace(item.Module),
		Severity:  item.Severity,
		Brief:     strings.TrimSpace(item.Brief),
	}

	// 提取 meta 标签
	dcSubject := doc.Find(`meta[name="DC.subject"]`).AttrOr("content", "")
	dcCreator := doc.Find(`meta[name="DC.Creator"]`).AttrOr("content", "")
	dcTitle := doc.Find(`meta[name="DC.Title"]`).AttrOr("content", "")

	if dcCreator != "" && k.Module == "" {
		k.Module = strings.ToUpper(strings.TrimSpace(dcCreator))
	}
	if dcSubject != "" && k.Brief == "" {
		k.Brief = strings.TrimSpace(dcSubject)
	}

	if item.EntryType == model.EntryTypeLog {
		parseLogRef(doc, k, dcTitle)
	} else {
		parseAlarmRef(doc, k, dcTitle)
	}

	return k, nil
}

func parseLogRef(doc *goquery.Document, k *model.Knowledge, dcTitle string) {
	// 1. 日志信息（Message）
	msg := doc.Find("div.logRefMessage .logRefMessagebody").Text()
	if msg == "" {
		msg = doc.Find("div.logRefMessage").Text()
	}
	k.Message = CleanText(msg)

	// 2. 日志含义（Description）
	desc := doc.Find("div.logRefDesc .logRefDescbody").Text()
	if desc == "" {
		desc = doc.Find("div.logRefDesc").Text()
	}
	k.Description = CleanText(desc)

	// 3. 日志参数（Parameters）- 兼容 div.logRefParams, logRefParamsbody 及 section 表格
	var params []model.ParameterItem
	doc.Find("div.logRefParams table tr, div.logRefParamsbody table tr, div.logRefLvl table tr, div.section:has(h4:contains('参数')) table tr").Each(func(i int, s *goquery.Selection) {
		tds := s.Find("td")
		if tds.Length() >= 2 {
			pName := CleanText(tds.Eq(0).Text())
			// 若有3列以上（如 参数名称, 参数类型, 参数描述），取最后一列作为描述
			pDesc := CleanText(tds.Eq(tds.Length() - 1).Text())
			if pName != "" && !strings.Contains(pName, "参数名称") && !strings.Contains(pName, "参数类型") && !strings.Contains(pName, "VB OID") {
				params = append(params, model.ParameterItem{
					Name:        pName,
					Description: pDesc,
				})
			}
		}
	})
	if len(params) > 0 {
		if pJSON, err := json.Marshal(params); err == nil {
			k.Parameters = string(pJSON)
		}
	}

	// 4. 可能原因（Cause）
	cause := ExtractBlockText(doc.Find("div.logRefCause .logRefCausebody"))
	if cause == "" {
		cause = ExtractBlockText(doc.Find("div.logRefCause"))
	}
	k.Cause = cause

	// 5. 处理步骤（Action）
	action := ""
	doc.Find("div.section").Each(func(i int, s *goquery.Selection) {
		title := s.Find("h4.sectiontitle, h2.sectiontitle").Text()
		if strings.Contains(title, "处理步骤") || strings.Contains(title, "恢复步骤") {
			action = ExtractBlockText(s)
			action = strings.TrimPrefix(action, CleanMultilineText(title))
			action = strings.TrimSpace(action)
		}
	})
	if action == "" {
		action = ExtractBlockText(doc.Find("div.section").Last())
	}
	k.Action = action

	// 仅在缺少 Module 或 Brief 时，且 dcTitle 严格符合 Module/Severity/Brief 规范时才作为兜底
	trimmedTitle := strings.TrimSpace(dcTitle)
	if (k.Module == "" || k.Brief == "") && validTitleRegex.MatchString(trimmedTitle) {
		parts := strings.Split(trimmedTitle, "/")
		if len(parts) >= 3 {
			if k.Module == "" {
				k.Module = strings.TrimSpace(parts[0])
			}
			if k.Severity == 0 {
				if sev, err := strconv.Atoi(strings.TrimSpace(parts[1])); err == nil {
					k.Severity = sev
				}
			}
			if k.Brief == "" {
				k.Brief = strings.TrimSpace(strings.Join(parts[2:], "/"))
			}
		}
	}
}

func parseAlarmRef(doc *goquery.Document, k *model.Knowledge, dcTitle string) {
	// 1. Trap Buffer 信息解释 / 告警信息
	doc.Find("div.section").Each(func(i int, s *goquery.Selection) {
		title := s.Find("h4.sectiontitle, h2.sectiontitle").Text()
		if strings.Contains(title, "Trap Buffer") || strings.Contains(title, "告警信息") || strings.Contains(title, "解释") {
			k.Message = CleanText(s.Find("p").Text())
			k.Description = k.Message
		}
	})
	if k.Message == "" {
		k.Message = CleanText(doc.Find("div.section p").First().Text())
		k.Description = k.Message
	}

	// 2. Trap 属性提取 (OID, MIB, Severity, Alarm ID) - 支持中英文属性名与级别值
	doc.Find("div.section").Each(func(i int, s *goquery.Selection) {
		title := s.Find("h4.sectiontitle, h2.sectiontitle").Text()
		if strings.Contains(title, "属性") {
			s.Find("table tr").Each(func(idx int, row *goquery.Selection) {
				tds := row.Find("td")
				if tds.Length() >= 2 {
					propName := CleanText(tds.Eq(0).Text())
					propVal := CleanText(tds.Eq(1).Text())
					switch {
					case strings.Contains(propName, "OID"):
						k.TrapOID = propVal
					case strings.Contains(propName, "MIB"):
						k.MIBName = propVal
					case strings.Contains(propName, "Alarm ID") || strings.Contains(propName, "告警ID"):
						k.AlarmID = propVal
					case strings.Contains(propName, "Alarm Type") || strings.Contains(propName, "告警类型"):
						k.AlarmType = propVal
					case strings.Contains(propName, "Severity") || strings.Contains(propName, "级别"):
						valLower := strings.ToLower(propVal)
						switch {
						case strings.Contains(valLower, "critical") || strings.Contains(valLower, "紧急"):
							k.Severity = 1
						case strings.Contains(valLower, "major") || strings.Contains(valLower, "重要"):
							k.Severity = 2
						case strings.Contains(valLower, "minor") || strings.Contains(valLower, "次要"):
							k.Severity = 3
						case strings.Contains(valLower, "warning") || strings.Contains(valLower, "警告") || strings.Contains(valLower, "提示"):
							k.Severity = 4
						case strings.Contains(valLower, "info") || strings.Contains(valLower, "informational") || strings.Contains(valLower, "通知") || strings.Contains(valLower, "信息"):
							k.Severity = 6
						default:
							k.Severity = 4
						}
					}
				}
			})
		}
	})

	// 3. 参数信息
	var params []model.ParameterItem
	doc.Find("div.section").Each(func(i int, s *goquery.Selection) {
		title := s.Find("h4.sectiontitle, h2.sectiontitle").Text()
		if strings.Contains(title, "参数") {
			s.Find("table tr").Each(func(idx int, row *goquery.Selection) {
				tds := row.Find("td")
				if tds.Length() >= 2 {
					pName := CleanText(tds.Eq(0).Text())
					pDesc := CleanText(tds.Eq(tds.Length() - 1).Text())
					if pName != "" && !strings.Contains(pName, "参数名称") && !strings.Contains(pName, "VB OID") {
						params = append(params, model.ParameterItem{
							Name:        pName,
							Description: pDesc,
						})
					}
				}
			})
		}
	})
	if len(params) > 0 {
		if pJSON, err := json.Marshal(params); err == nil {
			k.Parameters = string(pJSON)
		}
	}

	// 4. 对系统的影响（Impact）
	k.Impact = ExtractBlockText(doc.Find("div.impactonsystem .impactonsystembody"))

	// 5. 可能原因（Cause）
	cause := ExtractBlockText(doc.Find("div.possiblecauses .alarmpossbody"))
	if cause == "" {
		cause = ExtractBlockText(doc.Find("div.possiblecauses"))
	}
	k.Cause = cause

	// 6. 处理步骤（Action）
	action := ""
	doc.Find("div.section").Each(func(i int, s *goquery.Selection) {
		title := s.Find("h4.sectiontitle, h2.sectiontitle").Text()
		if strings.Contains(title, "处理步骤") || strings.Contains(title, "恢复步骤") {
			action = ExtractBlockText(s)
			action = strings.TrimPrefix(action, CleanMultilineText(title))
			action = strings.TrimSpace(action)
		}
	})
	if action == "" {
		action = ExtractBlockText(doc.Find("div.section").Last())
	}
	k.Action = action
}

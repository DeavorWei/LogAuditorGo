package hdx

import (
	"bytes"
	"encoding/json"
	"fmt"
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

// ParseHTMLKnowledge 解析单个 HTML 文件提取知识
func ParseHTMLKnowledge(docRootDir string, item LeafNaviItem) (k *model.Knowledge, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in ParseHTMLKnowledge: %v", r)
		}
	}()
	
	// 剥离 URL 锚点 (#) 并拼接相对路径
	cleanURL := strings.TrimSpace(item.URL)
	if idx := strings.Index(cleanURL, "#"); idx != -1 {
		cleanURL = cleanURL[:idx]
	}
	if cleanURL == "" {
		return nil, fmt.Errorf("empty html URL")
	}

	htmlPath := filepath.Join(docRootDir, item.NaviDir, cleanURL)
	rawBytes, err := os.ReadFile(htmlPath)
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
	cause := doc.Find("div.logRefCause .logRefCausebody").Text()
	if cause == "" {
		cause = doc.Find("div.logRefCause").Text()
	}
	k.Cause = CleanText(cause)

	// 5. 处理步骤（Action）
	action := ""
	doc.Find("div.section").Each(func(i int, s *goquery.Selection) {
		title := s.Find("h4.sectiontitle, h2.sectiontitle").Text()
		if strings.Contains(title, "处理步骤") || strings.Contains(title, "恢复步骤") {
			action = s.Text()
			action = strings.TrimPrefix(action, title)
		}
	})
	if action == "" {
		action = doc.Find("div.section").Last().Text()
	}
	k.Action = CleanText(action)

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
	k.Impact = CleanText(doc.Find("div.impactonsystem .impactonsystembody").Text())

	// 5. 可能原因（Cause）
	cause := doc.Find("div.possiblecauses .alarmpossbody").Text()
	if cause == "" {
		cause = doc.Find("div.possiblecauses").Text()
	}
	k.Cause = CleanText(cause)

	// 6. 处理步骤（Action）
	action := ""
	doc.Find("div.section").Each(func(i int, s *goquery.Selection) {
		title := s.Find("h4.sectiontitle, h2.sectiontitle").Text()
		if strings.Contains(title, "处理步骤") || strings.Contains(title, "恢复步骤") {
			action = s.Text()
			action = strings.TrimPrefix(action, title)
		}
	})
	k.Action = CleanText(action)
}

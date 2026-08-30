package hdx

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"logauditorgo/internal/model"
	"logauditorgo/pkg/logger"
)

// RawTopic navi.xml 中的 Topic 结构
type RawTopic struct {
	XMLName  xml.Name   `xml:"topic"`
	Txt      string     `xml:"txt,attr"`
	URL      string     `xml:"url,attr"`
	ID       string     `xml:"id,attr"`
	LibID    string     `xml:"libId,attr"`
	Children []RawTopic `xml:"topic"`
}

type RawTopicsRoot struct {
	XMLName xml.Name   `xml:"topics"`
	Topics  []RawTopic `xml:"topic"`
}

// LeafNaviItem 解析出的叶子知识节点
type LeafNaviItem struct {
	EntryType   model.EntryType
	TopicID     string
	Txt         string
	URL         string
	Module      string
	Severity    int
	Brief       string
	ParentChain []string
	NaviDir     string
}

// navi 解析的深度与体积上限 (KB-11)。
//
// 原实现对 navi.xml 无条件递归：畸形/恶意构造的深树会触发栈溢出，
// 而栈溢出是 Go 的 fatal error，ParseNaviXML 里的 recover() **拦截不到**，
// 整个进程会直接崩溃。这里在递归入口显式限深，超限即打断并记录告警。
const (
	maxNaviDepth     = 16
	maxNaviFileBytes = 64 << 20 // 64MB
)

// naviStats 记录一次解析过程中的限深/截断情况，便于在日志中暴露异常文档
type naviStats struct {
	depthTruncated bool
	maxDepthSeen   int
}

// ParseNaviXML 解析 navi.xml 并递归提取叶子日志和告警节点
func ParseNaviXML(docRootDir string, naviRelPath string) (leafItems []LeafNaviItem, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in ParseNaviXML: %v", r)
		}
	}()
	naviFullPath := filepath.Join(docRootDir, naviRelPath)

	// KB-11: 读取前先校验体积，避免超大 navi.xml 一次性载入导致内存尖峰
	if info, statErr := os.Stat(naviFullPath); statErr == nil && info.Size() > maxNaviFileBytes {
		return nil, fmt.Errorf("navi.xml %s is too large: %d bytes (limit %d)", naviFullPath, info.Size(), maxNaviFileBytes)
	}

	data, err := os.ReadFile(naviFullPath)
	if err != nil {
		return nil, fmt.Errorf("read navi.xml (%s) failed: %w", naviFullPath, err)
	}

	var root RawTopicsRoot
	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.CharsetReader = XMLCharsetReader
	if err := decoder.Decode(&root); err != nil {
		// 如果根节点直接是 topic 列表而非 topics
		var singleRoot RawTopic
		dec2 := xml.NewDecoder(bytes.NewReader(data))
		dec2.CharsetReader = XMLCharsetReader
		if err2 := dec2.Decode(&singleRoot); err2 == nil {
			root.Topics = []RawTopic{singleRoot}
		} else {
			return nil, fmt.Errorf("unmarshal navi.xml failed: %w", err)
		}
	}

	naviDir := filepath.Dir(naviRelPath)
	stats := &naviStats{}
	for _, t := range root.Topics {
		traverseTopic(t, []string{}, naviDir, 1, stats, &leafItems)
	}
	if stats.depthTruncated {
		logger.Log.Warnf("[HDX Navigator] navi.xml %s exceeds max depth %d (seen %d), deeper topics were skipped",
			naviFullPath, maxNaviDepth, stats.maxDepthSeen)
	}

	logCount := 0
	alarmCount := 0
	for _, item := range leafItems {
		if item.EntryType == model.EntryTypeLog {
			logCount++
		} else {
			alarmCount++
		}
	}
	logger.Log.Debugf("[HDX Navigator] Extracted %d leaf knowledge items from %s (Logs: %d, Alarms: %d)",
		len(leafItems), naviFullPath, logCount, alarmCount)

	return leafItems, nil
}

// traverseTopic 递归遍历 navi.xml 的 topic 树。
//
// KB-11: depth 参数用于显式限深。Go 的栈溢出属于 fatal error 而非 panic，
// 无法被 ParseNaviXML 的 recover() 捕获，因此必须在递归入口主动拦截。
func traverseTopic(topic RawTopic, parentChain []string, naviDir string, depth int, stats *naviStats, results *[]LeafNaviItem) {
	if depth > stats.maxDepthSeen {
		stats.maxDepthSeen = depth
	}
	if depth > maxNaviDepth {
		stats.depthTruncated = true
		return
	}

	txt := strings.TrimSpace(topic.Txt)
	currentChain := append(parentChain[:len(parentChain):len(parentChain)], txt)

	// 如果有子 topic，则是容器/分类节点，继续向下递归（纠偏 1：跳过非叶子节点）
	if len(topic.Children) > 0 {
		for _, child := range topic.Children {
			traverseTopic(child, currentChain, naviDir, depth+1, stats, results)
		}
		return
	}

	// 如果叶子节点没有有效 URL，跳过
	cleanURL := strings.TrimSpace(topic.URL)
	if cleanURL == "" {
		return
	}

	topicIDUpper := strings.ToUpper(topic.ID)
	// 判别是否属于 LOGREF 或 ALARMREF
	isLog := strings.Contains(topicIDUpper, "LOGREF_")
	isAlarm := strings.Contains(topicIDUpper, "ALARMREF_") || strings.Contains(topicIDUpper, "ALARM_")

	if !isLog && !isAlarm {
		// 不是日志或告警叶子条目，跳过
		return
	}

	item := LeafNaviItem{
		TopicID:     topic.ID,
		Txt:         txt,
		URL:         cleanURL,
		ParentChain: currentChain,
		NaviDir:     naviDir,
	}

	if isLog {
		// 解析 Module/Severity/Brief 格式，如 AAA/4/hwRadiusAuthServerDown_active
		parts := strings.Split(txt, "/")
		if len(parts) < 3 {
			// 纠偏1：日志的 txt 必须符合 Module/Severity/Brief 格式，否则认为是无效叶子节点
			return
		}
		module := strings.TrimSpace(parts[0])
		if module == "" {
			return
		}
		item.EntryType = model.EntryTypeLog
		item.Module = module

		sev, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil || sev < 1 || sev > 8 {
			// 非法日志级别，直接跳过非标准叶子节点
			return
		}
		item.Severity = sev

		brief := strings.TrimSpace(strings.Join(parts[2:], "/"))
		if brief == "" {
			return
		}
		item.Brief = brief
	} else if isAlarm {
		item.EntryType = model.EntryTypeAlarm
		item.Module = inferModuleFromChain(parentChain)
		item.Severity = 4
		item.Brief = txt
	}

	*results = append(*results, item)
}

func inferModuleFromChain(chain []string) string {
	for i := len(chain) - 1; i >= 0; i-- {
		txt := chain[i]
		if strings.HasSuffix(txt, "日志") {
			return strings.TrimSpace(strings.TrimSuffix(txt, "日志"))
		}
		if strings.HasSuffix(txt, "告警") {
			return strings.TrimSpace(strings.TrimSuffix(txt, "告警"))
		}
	}
	if len(chain) > 0 {
		return strings.TrimSpace(chain[len(chain)-1])
	}
	return "SYSTEM"
}

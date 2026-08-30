package logparser

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"logauditorgo/internal/model"
	"logauditorgo/pkg/logger"
)

// vrpRegex 标准 VRP 日志格式。
//
// PARSE-10: 两处变体兼容修正——
//  1. severity 由 `[1-8]` 放宽为 `[0-8]`：华为部分产品的 Emergency 级别日志 severity 为 0，
//     旧正则会让它整行解析失败并退化成 UNPARSED 噪声；
//  2. 类型组 `(?i:\((?P<type>[a-z])\))` 由必选改为可选 `(?:...)?`：
//     部分变体日志省略了 (l)/(s)/(p)/(d)/(c) 标识，旧正则同样会整行失配。
// PARSE-09: 时间组复用 commonTimePattern，与 USG 解析器共享同一套时间格式认知，
// 避免"某条解析链路只认部分时间格式"的历史问题再次出现。
var vrpRegex = regexp.MustCompile(`^(?:<(?P<pri>\d+)>)?\s*(?P<time>` + commonTimePattern + `)\s+(?P<host>\S+)\s+%%(?P<version>\d{2})?(?P<module>[A-Za-z0-9_\-]+)/(?P<severity>[0-8])/(?P<brief>[A-Za-z0-9_\-]+)(?:(?i:\((?P<type>[a-z])\)))?(?:\[(?P<seq>\d+)\])?(?:\[(?P<slot>[^\]]+)\])?:\s*(?P<msg>.*)$`)

var (
	vrpPriIdx     = vrpRegex.SubexpIndex("pri")
	vrpTimeIdx    = vrpRegex.SubexpIndex("time")
	vrpHostIdx    = vrpRegex.SubexpIndex("host")
	vrpVersionIdx = vrpRegex.SubexpIndex("version")
	vrpModIdx     = vrpRegex.SubexpIndex("module")
	vrpSevIdx     = vrpRegex.SubexpIndex("severity")
	vrpBriefIdx   = vrpRegex.SubexpIndex("brief")
	vrpTypeIdx    = vrpRegex.SubexpIndex("type")
	vrpSeqIdx     = vrpRegex.SubexpIndex("seq")
	vrpSlotIdx    = vrpRegex.SubexpIndex("slot")
	vrpMsgIdx     = vrpRegex.SubexpIndex("msg")
)

// 简化的 VRP 格式正则 (某些 syslog 转发器可能丢弃了部分 header)
// PARSE-10: 同主正则，severity 放宽为 [0-8]、类型组改为可选。
var vrpSimpleRegex = regexp.MustCompile(`%%(?P<version>\d{2})?(?P<module>[A-Za-z0-9_\-]+)/(?P<severity>[0-8])/(?P<brief>[A-Za-z0-9_\-]+)(?:(?i:\((?P<type>[a-z])\)))?(?:\[(?P<seq>\d+)\])?(?:\[(?P<slot>[^\]]+)\])?:\s*(?P<msg>.*)$`)

var (
	vrpSimpleVersionIdx = vrpSimpleRegex.SubexpIndex("version")
	vrpSimpleModIdx     = vrpSimpleRegex.SubexpIndex("module")
	vrpSimpleSevIdx     = vrpSimpleRegex.SubexpIndex("severity")
	vrpSimpleBriefIdx   = vrpSimpleRegex.SubexpIndex("brief")
	vrpSimpleTypeIdx    = vrpSimpleRegex.SubexpIndex("type")
	vrpSimpleSeqIdx     = vrpSimpleRegex.SubexpIndex("seq")
	vrpSimpleSlotIdx    = vrpSimpleRegex.SubexpIndex("slot")
	vrpSimpleMsgIdx     = vrpSimpleRegex.SubexpIndex("msg")
)

type VRPParser struct{}

func (p *VRPParser) Name() string {
	return "Huawei-VRP-Standard-Parser"
}

// vrpSupportRegex 用于判定一行是否属于 VRP 体系。
//
// PARSE-10: 原实现靠 `strings.Contains(line, "(s)")` 这类子串猜测，
// 报文体里只要出现 (s) 就会被误判为受支持的华为日志；
// 同时省略了类型标识的合法变体（如 `%%01IFNET/4/IF_DOWN:`）反而不被认领。
// 改为对 header 段（`%%` + 模块/级别/助记符）做一次预匹配，既准又覆盖变体。
var vrpSupportRegex = regexp.MustCompile(`%%\d{0,2}[A-Za-z0-9_\-]+/[0-8]/[A-Za-z0-9_\-]+`)

func (p *VRPParser) Support(line string) bool {
	return vrpSupportRegex.MatchString(line)
}

func (p *VRPParser) Parse(line string) (*model.NormalizedLog, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, fmt.Errorf("empty log line")
	}

	norm := &model.NormalizedLog{
		RawLog:     line,
		DeviceType: "Huawei-VRP",
	}

	if match := vrpRegex.FindStringSubmatch(line); match != nil {
		if vrpTimeIdx >= 0 && vrpTimeIdx < len(match) && match[vrpTimeIdx] != "" {
			if t, err := ParseHuaweiTimestamp(match[vrpTimeIdx]); err == nil {
				norm.Timestamp = t
			} else {
				logger.Log.Warnf("[LogParser VRP] Parse timestamp '%s' failed: %v", match[vrpTimeIdx], err)
			}
		}
		if vrpHostIdx >= 0 && vrpHostIdx < len(match) {
			norm.Hostname = match[vrpHostIdx]
		}
		if vrpModIdx >= 0 && vrpModIdx < len(match) {
			norm.Module = strings.ToUpper(match[vrpModIdx])
		}
		if vrpSevIdx >= 0 && vrpSevIdx < len(match) && match[vrpSevIdx] != "" {
			sev, _ := strconv.Atoi(match[vrpSevIdx])
			norm.Severity = sev
		}
		if vrpBriefIdx >= 0 && vrpBriefIdx < len(match) {
			norm.Brief = match[vrpBriefIdx]
		}
		if vrpTypeIdx >= 0 && vrpTypeIdx < len(match) {
			norm.LogType = strings.ToLower(match[vrpTypeIdx])
		}
		if vrpSeqIdx >= 0 && vrpSeqIdx < len(match) && match[vrpSeqIdx] != "" {
			seq, _ := strconv.ParseUint(match[vrpSeqIdx], 10, 64)
			norm.Sequence = seq
		}
		if vrpSlotIdx >= 0 && vrpSlotIdx < len(match) {
			norm.SlotInfo = match[vrpSlotIdx]
		}
		if vrpMsgIdx >= 0 && vrpMsgIdx < len(match) {
			norm.MessageBody = strings.TrimSpace(match[vrpMsgIdx])
		}
	} else if match := vrpSimpleRegex.FindStringSubmatch(line); match != nil {
		// 截取前面可能的时间与主机
		prefix := line[:strings.Index(line, "%%")]
		prefixParts := strings.Fields(prefix)
		if len(prefixParts) >= 2 {
			norm.Hostname = prefixParts[len(prefixParts)-1]
			timeStr := strings.Join(prefixParts[:len(prefixParts)-1], " ")
			if t, err := ParseHuaweiTimestamp(timeStr); err == nil {
				norm.Timestamp = t
			}
		} else if len(prefixParts) == 1 {
			norm.Hostname = prefixParts[0]
		}

		if vrpSimpleModIdx >= 0 && vrpSimpleModIdx < len(match) {
			norm.Module = strings.ToUpper(match[vrpSimpleModIdx])
		}
		if vrpSimpleSevIdx >= 0 && vrpSimpleSevIdx < len(match) && match[vrpSimpleSevIdx] != "" {
			sev, _ := strconv.Atoi(match[vrpSimpleSevIdx])
			norm.Severity = sev
		}
		if vrpSimpleBriefIdx >= 0 && vrpSimpleBriefIdx < len(match) {
			norm.Brief = match[vrpSimpleBriefIdx]
		}
		if vrpSimpleTypeIdx >= 0 && vrpSimpleTypeIdx < len(match) {
			norm.LogType = strings.ToLower(match[vrpSimpleTypeIdx])
		}
		if vrpSimpleSeqIdx >= 0 && vrpSimpleSeqIdx < len(match) && match[vrpSimpleSeqIdx] != "" {
			seq, _ := strconv.ParseUint(match[vrpSimpleSeqIdx], 10, 64)
			norm.Sequence = seq
		}
		if vrpSimpleSlotIdx >= 0 && vrpSimpleSlotIdx < len(match) {
			norm.SlotInfo = match[vrpSimpleSlotIdx]
		}
		if vrpSimpleMsgIdx >= 0 && vrpSimpleMsgIdx < len(match) {
			norm.MessageBody = strings.TrimSpace(match[vrpSimpleMsgIdx])
		}
	} else {
		return nil, fmt.Errorf("line does not match VRP format: %s", line)
	}

	// 提取动态参数
	norm.Parameters = ExtractParameters(norm.MessageBody)

	logger.Log.Debugf("[LogParser VRP] Parsed log: Host=%s, Time=%s, Mod=%s, Sev=%d, Brief=%s, Slot=%s, Params=%v",
		norm.Hostname, norm.Timestamp.Format("2006-01-02 15:04:05"), norm.Module, norm.Severity, norm.Brief, norm.SlotInfo, norm.Parameters)

	return norm, nil
}

package logparser

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"logauditorgo/internal/model"
	"logauditorgo/pkg/logger"
)

// VRP 标准 Syslog 正则表达式
// 匹配: [PRI] Time Hostname %%[Version]Module/Severity/Brief(Type)[Seq][Slot]: Message
var vrpRegex = regexp.MustCompile(`^(?:<(?P<pri>\d+)>)?\s*(?P<time>(?:[A-Za-z]{3}\s+\d+\s+(?:\d{4}\s+)?\d{2}:\d{2}:\d{2}(?:\.\d+)?|\d{4}-\d{2}-\d{2}[T\s]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:[+-]\d{2}:?\d{2}|Z)?|UTC[+-]\d{1,2}(?::?\d{2})?\s+\d{4}-\d{2}-\d{2}\s+\d{2}:\d{2}:\d{2}(?:\.\d+)?))\s+(?P<host>\S+)\s+%%(?P<version>\d{2})?(?P<module>[A-Za-z0-9_]+)/(?P<severity>[1-8])/(?P<brief>[A-Za-z0-9_\-]+)(?i:\((?P<type>[a-z])\))(?:\[(?P<seq>\d+)\])?(?:\[(?P<slot>[^\]]+)\])?:\s*(?P<msg>.*)$`)
var vrpGroupNames = vrpRegex.SubexpNames()

// 简化的 VRP 格式正则 (某些 syslog 转发器可能丢弃了部分 header)
var vrpSimpleRegex = regexp.MustCompile(`%%(?P<version>\d{2})?(?P<module>[A-Za-z0-9_]+)/(?P<severity>[1-8])/(?P<brief>[A-Za-z0-9_\-]+)(?i:\((?P<type>[a-z])\))(?:\[(?P<seq>\d+)\])?(?:\[(?P<slot>[^\]]+)\])?:\s*(?P<msg>.*)$`)
var vrpSimpleGroupNames = vrpSimpleRegex.SubexpNames()

type VRPParser struct{}

func (p *VRPParser) Name() string {
	return "Huawei-VRP-Standard-Parser"
}

func (p *VRPParser) Support(line string) bool {
	lineLower := strings.ToLower(line)
	return strings.Contains(line, "%%") && (strings.Contains(lineLower, "(l)") || strings.Contains(lineLower, "(s)") || strings.Contains(lineLower, "(p)") || strings.Contains(lineLower, "(d)") || strings.Contains(lineLower, "(c)"))
}

func (p *VRPParser) Parse(line string) (*model.NormalizedLog, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, fmt.Errorf("empty log line")
	}

	norm := &model.NormalizedLog{
		RawLog:     line,
		DeviceType: "Huawei-VRP",
		Timestamp:  time.Now(),
	}

	if match := vrpRegex.FindStringSubmatch(line); match != nil {
		for i, name := range vrpGroupNames {
			if i == 0 || name == "" {
				continue
			}
			val := match[i]
			switch name {
			case "time":
				if t, err := ParseHuaweiTimestamp(val); err == nil {
					norm.Timestamp = t
				} else {
					logger.Log.Warnf("[LogParser VRP] Parse timestamp '%s' failed: %v", val, err)
				}
			case "host":
				norm.Hostname = val
			case "module":
				norm.Module = strings.ToUpper(val)
			case "severity":
				sev, _ := strconv.Atoi(val)
				norm.Severity = sev
			case "brief":
				norm.Brief = val
			case "type":
				norm.LogType = strings.ToLower(val)
			case "seq":
				seq, _ := strconv.ParseUint(val, 10, 64)
				norm.Sequence = seq
			case "slot":
				norm.SlotInfo = val
			case "msg":
				norm.MessageBody = strings.TrimSpace(val)
			}
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

		for i, name := range vrpSimpleGroupNames {
			if i == 0 || name == "" {
				continue
			}
			val := match[i]
			switch name {
			case "module":
				norm.Module = strings.ToUpper(val)
			case "severity":
				sev, _ := strconv.Atoi(val)
				norm.Severity = sev
			case "brief":
				norm.Brief = val
			case "type":
				norm.LogType = strings.ToLower(val)
			case "seq":
				seq, _ := strconv.ParseUint(val, 10, 64)
				norm.Sequence = seq
			case "slot":
				norm.SlotInfo = val
			case "msg":
				norm.MessageBody = strings.TrimSpace(val)
			}
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

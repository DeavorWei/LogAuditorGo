package logparser

import (
	"regexp"
	"strconv"
	"strings"

	"logauditorgo/internal/model"
)

// USG 安全日志格式正则
// 匹配 USG 防火墙 UTM 会话/策略/攻击日志:
// e.g. 2026-04-15 14:00:00 USG6000F-FW %%01SEC/4/SESSION_CLOSE(l): Protocol=TCP, SrcIP=10.1.1.1, DstIP=192.168.1.1, Policy=default
var usgSecRegex = regexp.MustCompile(`^(?:<(?P<pri>\d+)>)?(?P<time>\d{4}-\d{2}-\d{2}\s+\d{2}:\d{2}:\d{2})\s+(?P<host>\S+)\s+%%(?P<version>\d{2})?(?P<module>[A-Za-z0-9_]+)/(?P<severity>[1-8])/(?P<brief>[A-Za-z0-9_]+)\(s\):\s*(?P<msg>.*)$`)
var usgSecGroupNames = usgSecRegex.SubexpNames()

type USGSecurityParser struct{}

func (p *USGSecurityParser) Name() string {
	return "Huawei-USG-Security-Parser"
}

func (p *USGSecurityParser) Support(line string) bool {
	return strings.Contains(line, "%%") && strings.Contains(line, "(s)")
}

func (p *USGSecurityParser) Parse(line string) (*model.NormalizedLog, error) {
	line = strings.TrimSpace(line)
	
	match := usgSecRegex.FindStringSubmatch(line)
	if match == nil {
		// 降级使用 VRP 标准解析器
		vrp := &VRPParser{}
		return vrp.Parse(line)
	}

	norm := &model.NormalizedLog{
		RawLog:     line,
		DeviceType: "Huawei-USG-Firewall",
		LogType:    "s",
	}

	for i, name := range usgSecGroupNames {
		if i == 0 || name == "" {
			continue
		}
		val := match[i]
		switch name {
		case "time":
			t, _ := ParseHuaweiTimestamp(val)
			norm.Timestamp = t
		case "host":
			norm.Hostname = val
		case "module":
			norm.Module = strings.ToUpper(val)
		case "severity":
			sev, _ := strconv.Atoi(val)
			norm.Severity = sev
		case "brief":
			norm.Brief = val
		case "msg":
			norm.MessageBody = strings.TrimSpace(val)
		}
	}

	norm.Parameters = ExtractParameters(norm.MessageBody)
	return norm, nil
}

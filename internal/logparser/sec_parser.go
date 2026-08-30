package logparser

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"logauditorgo/internal/model"
	"logauditorgo/pkg/logger"
)

// USG 安全日志格式正则
// 匹配 USG 防火墙 UTM 会话/策略/攻击日志:
// e.g. 2026-04-15 14:00:00 USG6000F-FW %%01SEC/4/SESSION_CLOSE(l): Protocol=TCP, SrcIP=10.1.1.1, DstIP=192.168.1.1, Policy=default
//
// PARSE-09: 三处修正——
//  1. `(?:<(?P<pri>\d+)>)?` 后补 `\s*`：`<134> 2026-...`（PRI 后带空格）的日志
//     旧正则直接失配，全部落进 VRP 兜底分支，DeviceType 被改写成 Huawei-VRP；
//  2. 时间组扩展为可复用的通用时间子式，支持 BSD（`Apr 15 2026 ...`）与
//     `UTC+08:00 2026-04-15 ...` 前缀这两种真实存在的变体；
//  3. 补上 slot / seq 捕获组，Slot 与 Sequence 不再永久丢失。
var usgSecRegex = regexp.MustCompile(`^(?:<(?P<pri>\d+)>)?\s*(?P<time>` + commonTimePattern + `)\s+(?P<host>\S+)\s+%%(?P<version>\d{2})?(?P<module>[A-Za-z0-9_\-]+)/(?P<severity>[0-8])/(?P<brief>[A-Za-z0-9_\-]+)(?i:\((?P<type>[a-z])\))?(?:\[(?P<seq>\d+)\])?(?:\[(?P<slot>[^\]]+)\])?:\s*(?P<msg>.*)$`)

// usgSecSupportRegex 认领 USG 安全日志：模块/级别/助记符 header + 安全类模块。
// PARSE-09: 原实现用大小写敏感的 `strings.Contains(line, "(s)")`，
// `(S)` 大写的变体不被认领；且任意含 "(s)" 子串的非华为日志都会被误判为受支持。
var usgSecSupportRegex = regexp.MustCompile(`%%\d{0,2}(SEC|UTM|IPS|AV|FW|SSLVPN|ATTACK|POLICY)/[0-8]/[A-Za-z0-9_\-]+`)

var (
	usgSecPriIdx     = usgSecRegex.SubexpIndex("pri")
	usgSecTimeIdx    = usgSecRegex.SubexpIndex("time")
	usgSecHostIdx    = usgSecRegex.SubexpIndex("host")
	usgSecVersionIdx = usgSecRegex.SubexpIndex("version")
	usgSecModIdx     = usgSecRegex.SubexpIndex("module")
	usgSecSevIdx     = usgSecRegex.SubexpIndex("severity")
	usgSecBriefIdx   = usgSecRegex.SubexpIndex("brief")
	usgSecTypeIdx    = usgSecRegex.SubexpIndex("type")
	usgSecSeqIdx     = usgSecRegex.SubexpIndex("seq")
	usgSecSlotIdx    = usgSecRegex.SubexpIndex("slot")
	usgSecMsgIdx     = usgSecRegex.SubexpIndex("msg")
)

type USGSecurityParser struct{}

func (p *USGSecurityParser) Name() string {
	return "Huawei-USG-Security-Parser"
}

func (p *USGSecurityParser) Support(line string) bool {
	return usgSecSupportRegex.MatchString(line)
}

// Parse 解析 USG 安全日志。
//
// PARSE-09: 降级分支原先是静默的——时间解析失败既不写日志也不置错误标记，
// 运维看到"时间戳为空"的记录无从判断原因。现在降级时输出结构化告警。
func (p *USGSecurityParser) Parse(line string) (*model.NormalizedLog, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, fmt.Errorf("empty log line")
	}

	match := usgSecRegex.FindStringSubmatch(line)
	if match == nil {
		// 降级使用 VRP 标准解析器
		logger.Log.Debugf("[LogParser USG] line does not match USG security pattern, falling back to VRP parser: %s", truncateForLog(line, 160))
		vrp := &VRPParser{}
		return vrp.Parse(line)
	}

	norm := &model.NormalizedLog{
		RawLog:     line,
		DeviceType: "Huawei-USG-Firewall",
		LogType:    "s",
	}
	if usgSecTypeIdx >= 0 && usgSecTypeIdx < len(match) && match[usgSecTypeIdx] != "" {
		norm.LogType = strings.ToLower(match[usgSecTypeIdx])
	}

	if usgSecTimeIdx >= 0 && usgSecTimeIdx < len(match) && match[usgSecTimeIdx] != "" {
		if t, err := ParseHuaweiTimestamp(match[usgSecTimeIdx]); err == nil {
			norm.Timestamp = t
		} else {
			// USG 安全日志的时间戳是后续 RCA 时序聚类的唯一依据，
			// 解析失败必须留痕，否则整条日志会被当成"零时间戳"在 RCA 阶段被过滤掉。
			logger.Log.Warnf("[LogParser USG] Parse timestamp '%s' failed: %v", match[usgSecTimeIdx], err)
		}
	}
	if usgSecHostIdx >= 0 && usgSecHostIdx < len(match) {
		norm.Hostname = match[usgSecHostIdx]
	}
	if usgSecModIdx >= 0 && usgSecModIdx < len(match) {
		norm.Module = strings.ToUpper(match[usgSecModIdx])
	}
	if usgSecSevIdx >= 0 && usgSecSevIdx < len(match) && match[usgSecSevIdx] != "" {
		sev, _ := strconv.Atoi(match[usgSecSevIdx])
		norm.Severity = sev
	}
	if usgSecBriefIdx >= 0 && usgSecBriefIdx < len(match) {
		norm.Brief = match[usgSecBriefIdx]
	}
	if usgSecSeqIdx >= 0 && usgSecSeqIdx < len(match) && match[usgSecSeqIdx] != "" {
		if seq, err := strconv.ParseUint(match[usgSecSeqIdx], 10, 64); err == nil {
			norm.Sequence = seq
		}
	}
	if usgSecSlotIdx >= 0 && usgSecSlotIdx < len(match) {
		norm.SlotInfo = match[usgSecSlotIdx]
	}
	if usgSecMsgIdx >= 0 && usgSecMsgIdx < len(match) {
		norm.MessageBody = strings.TrimSpace(match[usgSecMsgIdx])
	}

	_ = usgSecPriIdx
	_ = usgSecVersionIdx

	norm.Parameters = ExtractParameters(norm.MessageBody)
	return norm, nil
}

// truncateForLog 截断过长文本，避免单行超长日志把日志系统打爆
func truncateForLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}

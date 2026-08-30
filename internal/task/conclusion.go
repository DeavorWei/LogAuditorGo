package task

import (
	"fmt"
	"sort"
	"strings"

	"logauditorgo/internal/model"
)

// conclusionRule 结论规则表 (REANA-04)。
//
// 原实现用 `strings.Contains(m, "IFNET") || strings.Contains(m, "PORT") || strings.Contains(m, "ETH")`
// 这类宽泛子串判断模块是否属于"物理链路"，
// 于是 ETHERNET_OAM、SUPPORT 等任意含这些字符的模块都会得到"检查物理光纤"的建议，
// 结论严重误导。改为显式的模块白名单集合，命中才给对应建议。
var conclusionRules = []struct {
	// ID 用于去重与排序
	ID string
	// Modules 精确匹配的模块名白名单（大写比较）
	Modules map[string]bool
	// Headline 建议标题
	Headline string
	// Detail 建议正文
	Detail string
}{
	{
		ID:       "OSPF",
		Modules:  map[string]bool{"OSPF": true, "OSPFV3": true},
		Headline: "OSPF 邻居震荡排查",
		Detail:   "请重点检查对端路由器接口 MTU 一致性、Hello/Dead Timer 配置、链路丢包以及 BFD 联动保活状态。",
	},
	{
		ID:       "BGP",
		Modules:  map[string]bool{"BGP": true, "BGP4": true},
		Headline: "BGP 状态排查",
		Detail:   "请检查 TCP 179 端口可达性、Hold Timer 超时原因、以及对等体 Keepalive 报文交互是否被 ACL 或 CPU 防攻击策略丢弃。",
	},
	{
		ID: "LINK",
		Modules: map[string]bool{
			"IFNET": true, "ETHBASE": true, "PORT": true, "ETHERNET": true,
			"LACP": true, "TRUNK": true, "PHY": true,
		},
		Headline: "物理链路排查",
		Detail:   "检查对端光模块收发光功率（optical-power）、接口 CRC 错包统计及物理光纤链路质量。",
	},
	{
		ID:       "OPTICAL",
		Modules:  map[string]bool{"OPTICAL": true, "TRANSCEIVER": true, "DEVM": true},
		Headline: "光模块与硬件排查",
		Detail:   "执行 display transceiver interface 查看收发光功率，必要时清洗光纤接头或更换故障光模块。",
	},
	{
		ID:       "AUTH",
		Modules:  map[string]bool{"AAA": true, "RADIUS": true, "PORTAL": true, "DOT1X": true},
		Headline: "认证系统排查",
		Detail:   "测试设备与 RADIUS 服务器的网络连通性，检查认证服务进程状态与共享密钥配置。",
	},
	{
		ID:       "MLAG",
		Modules:  map[string]bool{"MLAG": true, "DFS": true, "PEERLINK": true, "M-LAG": true},
		Headline: "M-LAG 双主排查",
		Detail:   "检查 Peer-Link 成员口物理状态，严禁重启处于 Error-Down 状态的备机，避免双主流量黑洞。",
	},
	{
		ID:       "RESOURCE",
		Modules:  map[string]bool{"SYSTEM": true, "RESOURCE": true, "CPU": true},
		Headline: "设备资源排查",
		Detail:   "执行 display cpu-usage 定位占用最高的进程，排查路由震荡、广播风暴或未命中硬件转发表的大流量。",
	},
}

// maxClusterDisplay 结论中最多展示的时序关联簇数量 (REANA-17)
const maxClusterDisplay = 3

// generateMultiDeviceConclusion 生成多设备协同与时间线分析结论。
//
// 三处修正：
//   - REANA-05：时间跨度与总条数改用 SQL 聚合结果，不再基于被截断的时间线切片；
//   - REANA-10：区分"单设备突发"与"跨设备传播"两类簇，文案按涉及设备数分支；
//   - REANA-16/17：结论文案用 strings.Builder 分段拼装，建议编号动态生成
//     （旧实现硬编码 1./2./3.，只命中 OSPF+IFNET 时会出现 1. 后直接 3.）。
func generateMultiDeviceConclusion(
	devices []model.DeviceStats,
	timeline []model.DeviceTimelineEvent,
	clusters []model.CorrelatedTimelineCluster,
	commonEvents []string,
	span timelineSpan,
) string {
	if len(devices) == 0 {
		return "当前任务尚未配置设备，建议添加设备或执行按 Hostname 自动识别以进行多设备协同分析。"
	}

	var sb strings.Builder

	totalCrit := 0
	for _, d := range devices {
		for sev, count := range d.SeverityDist {
			if sev <= 3 {
				totalCrit += count
			}
		}
	}

	// 1. 综述：时间跨度与规模一律来自 SQL 聚合 (REANA-05)
	sb.WriteString(fmt.Sprintf("【多设备协同审计综述】本次分析覆盖 %d 台网络设备，", len(devices)))
	if span.HasEvents {
		sb.WriteString(fmt.Sprintf("时间跨度为 %s 至 %s，共汇聚分析 %d 条时序日志，其中严重告警（级别≤3）共 %d 条。\n\n",
			span.MinTime.Format("2006-01-02 15:04:05"),
			span.MaxTime.Format("2006-01-02 15:04:05"),
			span.Total, totalCrit))
	} else {
		sb.WriteString("暂无时间线日志记录。\n\n")
	}

	// 时间线被截断时明确提示，避免用户以为看到的是全量 (UI-04)
	if span.Total > int64(len(timeline)) {
		sb.WriteString(fmt.Sprintf("注：下方时间线明细展示其中 %d 条（按时间升序），结论中的统计口径为全量 %d 条。\n\n",
			len(timeline), span.Total))
	}

	if len(commonEvents) > 0 {
		sb.WriteString("【跨设备共性事件】检测到以下在多台设备间协同或相继发生的事件：\n")
		for _, ce := range commonEvents {
			sb.WriteString(fmt.Sprintf(" • %s\n", ce))
		}
		sb.WriteString("\n")
	}

	// 2. 故障传播簇：按"单设备突发"与"跨设备传播"分别描述 (REANA-10)
	if len(clusters) > 0 {
		sb.WriteString("【故障传播与时间窗口推断】\n")
		for i, cl := range clusters {
			if i >= maxClusterDisplay {
				// REANA-17: 剩余数量按真实下标计算，旧实现写死减 3 在只展示部分时算错
				sb.WriteString(fmt.Sprintf(" • 另有 %d 个时序关联事件簇...\n", len(clusters)-i))
				break
			}
			if len(cl.Events) == 0 {
				continue
			}
			firstEv := cl.Events[0]
			if len(cl.Devices) > 1 {
				sb.WriteString(fmt.Sprintf(" • [%s] 由设备「%s」率先上报 %s/%s 事件，随后在时间窗口内协同影响设备 (%s)。\n",
					firstEv.Timestamp.Format("15:04:05"), firstEv.DeviceName, firstEv.Module, firstEv.Brief,
					strings.Join(cl.Devices, ", ")))
			} else {
				sb.WriteString(fmt.Sprintf(" • [%s] 设备「%s」在时间窗口内集中上报 %d 条 %s/%s 相关事件，属于单设备突发。\n",
					firstEv.Timestamp.Format("15:04:05"), firstEv.DeviceName, len(cl.Events), firstEv.Module, firstEv.Brief))
			}
		}
		sb.WriteString("\n")
	}

	// 3. 专家排查建议：模块白名单匹配 + 动态编号 (REANA-04 / REANA-16)
	sb.WriteString("【专家排查建议】\n")
	hits := matchConclusionRules(collectModules(devices, timeline))
	if len(hits) == 0 {
		sb.WriteString(" 1. 请依据时间线率先产生告警的设备与模块，结合华为官方知识库排查指引依次进行处置。\n")
		return sb.String()
	}
	for i, rule := range hits {
		sb.WriteString(fmt.Sprintf(" %d. %s：%s\n", i+1, rule.Headline, rule.Detail))
	}

	return sb.String()
}

// collectModules 汇总设备主要模块与时间线中出现过的模块
func collectModules(devices []model.DeviceStats, timeline []model.DeviceTimelineEvent) map[string]bool {
	seen := make(map[string]bool)
	for _, d := range devices {
		for _, mc := range d.TopModules {
			if m := strings.ToUpper(strings.TrimSpace(mc.Module)); m != "" {
				seen[m] = true
			}
		}
	}
	for _, ev := range timeline {
		if m := strings.ToUpper(strings.TrimSpace(ev.Module)); m != "" {
			seen[m] = true
		}
	}
	return seen
}

// matchConclusionRules 按模块白名单匹配结论规则，输出顺序稳定
func matchConclusionRules(modules map[string]bool) []struct {
	ID       string
	Headline string
	Detail   string
} {
	var matched []int
	for i, rule := range conclusionRules {
		for m := range modules {
			if rule.Modules[m] {
				matched = append(matched, i)
				break
			}
		}
	}
	sort.Ints(matched)

	out := make([]struct {
		ID       string
		Headline string
		Detail   string
	}, 0, len(matched))
	for _, i := range matched {
		out = append(out, struct {
			ID       string
			Headline string
			Detail   string
		}{ID: conclusionRules[i].ID, Headline: conclusionRules[i].Headline, Detail: conclusionRules[i].Detail})
	}
	return out
}

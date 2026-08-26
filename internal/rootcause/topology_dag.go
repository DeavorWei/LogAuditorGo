package rootcause

import (
	"strings"
	"sync"
)

type Pattern struct {
	keywords []string
	isModule bool
}

func compilePattern(pattern string, isModule bool) Pattern {
	var p Pattern
	p.isModule = isModule
	for _, kw := range strings.Split(pattern, ",") {
		kw = strings.TrimSpace(kw)
		if kw != "" {
			p.keywords = append(p.keywords, strings.ToUpper(kw))
		}
	}
	return p
}

func (p *Pattern) Match(val string) bool {
	valUpper := strings.ToUpper(strings.TrimSpace(val))
	if valUpper == "" {
		return false
	}
	for _, kw := range p.keywords {
		if p.isModule {
			if valUpper == kw {
				return true
			}
		} else {
			if strings.Contains(valUpper, kw) {
				return true
			}
		}
	}
	return false
}

// DAGEdge 表示故障传播的单条边
type DAGEdge struct {
	FromModulePattern string
	FromBriefPattern  string
	ToModulePattern   string
	ToBriefPattern    string

	fromMod  Pattern
	fromBrf  Pattern
	toMod    Pattern
	toBrf    Pattern
	compiled bool
	once     sync.Once
}

// ProtocolFaultRule 定义基于 DAG 的故障传播规则
type ProtocolFaultRule struct {
	ID              string
	Category        string
	DAGEdges        []DAGEdge
	SummaryTemplate string
	ActionTemplate  string
}

var DefaultRules = []ProtocolFaultRule{
	// 规则 1: 物理链路中断引发 BFD / BGP / OSPF / 路由撤销
	{
		ID:       "LINK_ROUTING_CASCADE",
		Category: "LINK_ROUTING",
		DAGEdges: []DAGEdge{
			{FromModulePattern: "IFNET,PORT,ETHBASE", FromBriefPattern: "IF_DOWN,LINK_DOWN,PORT_DOWN,PORT_MAC_DOWN,PHY_DOWN", ToModulePattern: "BFD", ToBriefPattern: "BFD_SESS_DOWN,SESS_DOWN"},
			{FromModulePattern: "BFD", FromBriefPattern: "BFD_SESS_DOWN,SESS_DOWN", ToModulePattern: "BGP,OSPF,OSPFV3,ISIS", ToBriefPattern: "PEER_BACKWARD,NBR_CHG,PEER_DOWN,STATE_CHANGED"},
			{FromModulePattern: "IFNET,PORT,ETHBASE", FromBriefPattern: "IF_DOWN,LINK_DOWN,PORT_DOWN,PORT_MAC_DOWN,PHY_DOWN", ToModulePattern: "BGP,OSPF,OSPFV3,ISIS", ToBriefPattern: "PEER_BACKWARD,NBR_CHG,PEER_DOWN,STATE_CHANGED"}, // 直接触发
			{FromModulePattern: "BGP,OSPF,OSPFV3,ISIS", FromBriefPattern: "PEER_BACKWARD,NBR_CHG,PEER_DOWN,STATE_CHANGED", ToModulePattern: "RM,STATIC_ROUTE,ROUTING", ToBriefPattern: "ROUTE_DELETE,ROUTE_DEL,UNREACHABLE"},
		},
		SummaryTemplate: "物理接口链路中断，引发上层BFD会话及动态路由协议邻居相继断开",
		ActionTemplate:  "1. 优先检查故障接口物理光纤连接、光模块收发光功率及对端端口状态；2. 物理链路恢复后上层协议将自动重新协商，无需手动修改路由配置。",
	},
	// 规则 2: 光模块异常引发 CRC 错包及端口震荡
	{
		ID:       "OPTICAL_CRC_DOWN",
		Category: "OPTICAL",
		DAGEdges: []DAGEdge{
			{FromModulePattern: "OPTICAL,TRANSCEIVER,DEVM", FromBriefPattern: "OPTICAL_POWER,TRANSCEIVER_FAIL,POWER_ABNORMAL,MODULE_EXCEPTION", ToModulePattern: "ETHBASE,IFNET,PORT", ToBriefPattern: "CRC_ERR,INPUT_ERR,CRC_ERROR"},
			{FromModulePattern: "ETHBASE,IFNET,PORT", FromBriefPattern: "CRC_ERR,INPUT_ERR,CRC_ERROR", ToModulePattern: "ETHBASE,IFNET,PORT", ToBriefPattern: "IF_DOWN,PORT_DOWN,LINK_DOWN"},
		},
		SummaryTemplate: "光模块收发光功率异常或硬件故障，导致接口产生大量CRC错包并最终Down",
		ActionTemplate:  "1. 执行 display transceiver interface 查看光功率；2. 建议清洗光纤接头或更换故障光模块。",
	},
	// 规则 3: RADIUS 认证服务器不可达引发大量用户认证失败及下线
	{
		ID:       "RADIUS_AUTH_CASCADE",
		Category: "AUTH",
		DAGEdges: []DAGEdge{
			{FromModulePattern: "AAA,RADIUS", FromBriefPattern: "hwRadiusAuthServerDown,hwRadiusAcctServerDown,SERVER_DOWN,RADIUS_DOWN", ToModulePattern: "AAA", ToBriefPattern: "USER_AUTH_FAIL,AUTHEN_FAIL,AUTH_FAIL,hwUserAuthenFailure"},
			{FromModulePattern: "AAA", FromBriefPattern: "USER_AUTH_FAIL,AUTHEN_FAIL,AUTH_FAIL,hwUserAuthenFailure", ToModulePattern: "PORTAL,DOT1X,BRAS", ToBriefPattern: "USER_OFFLINE,CUT_OFF"},
			{FromModulePattern: "PORTAL,DOT1X,BRAS", FromBriefPattern: "USER_OFFLINE,CUT_OFF", ToModulePattern: "IPSEC,SSLVPN", ToBriefPattern: "DISCONNECT"},
		},
		SummaryTemplate: "RADIUS认证/计费服务器不可达，导致终端用户鉴权失败并批量下线",
		ActionTemplate:  "1. 使用 ping / trace 测试设备与 RADIUS 服务器 IP 的网络连通性；2. 检查 RADIUS 服务器上的认证服务进程运行状态及共享密钥配置。",
	},
	// 规则 4: M-LAG Peer-Link 中断引发双主检测及从机端口隔离
	{
		ID:       "MLAG_PEERLINK_DAD",
		Category: "MLAG",
		DAGEdges: []DAGEdge{
			{FromModulePattern: "MLAG,DFS,PEERLINK", FromBriefPattern: "PEERLINK_DOWN,DFS_DOWN,HEARTBEAT_LOST", ToModulePattern: "MLAG", ToBriefPattern: "DUAL_ACTIVE"},
			{FromModulePattern: "MLAG", FromBriefPattern: "DUAL_ACTIVE", ToModulePattern: "IFNET,PORT,LACP,STP", ToBriefPattern: "PORT_ERRORDOWN,PORT_DISABLED,STANDBY_DISABLE,ERROR_DOWN"},
		},
		SummaryTemplate: "M-LAG Peer-Link 聚合链路中断，触发双主冲突检测机制(DAD)并将从机业务口隔离",
		ActionTemplate:  "1. 立即检查 Peer-Link 链路成员物理口与光纤；2. 严禁重启处于 Error-Down 状态的备机，避免发生双主流量黑洞。",
	},
	// 规则 5: CPU/内存过载引发协议心跳超时
	{
		ID:       "RESOURCE_OVERLOAD_TIMEOUT",
		Category: "HARDWARE",
		DAGEdges: []DAGEdge{
			{FromModulePattern: "DEVM,SYSTEM,RESOURCE", FromBriefPattern: "CPU_OVERLOAD,CPU_HIGH,MEM_OVERLOAD,MEM_HIGH,RESOURCE_EXHAUST,CPU_USAGE_HIGH,CPURISING,MEM_USAGE_HIGH", ToModulePattern: "BGP,OSPF,BFD,LACP,VRRP", ToBriefPattern: "HOLD_TIME_EXPIRED,KEEPALIVE_TIMEOUT,TIMER_EXPIRED,STATE_CHANGED"},
		},
		SummaryTemplate: "主控板CPU/内存负载过高，导致协议保活报文处理延迟并发生心跳超时",
		ActionTemplate:  "1. 执行 display cpu-usage 查看占用率最高的前3个进程；2. 排查是否存在路由震荡、广播风暴或大流量未命中硬件转发表。",
	},
}

func init() {
	for r := range DefaultRules {
		for e := range DefaultRules[r].DAGEdges {
			DefaultRules[r].DAGEdges[e].compile()
		}
	}
}

func (e *DAGEdge) compile() {
	e.once.Do(func() {
		e.fromMod = compilePattern(e.FromModulePattern, true)
		e.fromBrf = compilePattern(e.FromBriefPattern, false)
		e.toMod = compilePattern(e.ToModulePattern, true)
		e.toBrf = compilePattern(e.ToBriefPattern, false)
		e.compiled = true
	})
}

func (e *DAGEdge) MatchesNode(module, brief string, isFrom bool) bool {
	e.compile()
	if isFrom {
		return e.fromMod.Match(module) && e.fromBrf.Match(brief)
	}
	return e.toMod.Match(module) && e.toBrf.Match(brief)
}

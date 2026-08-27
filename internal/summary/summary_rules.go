package summary

import (
	"fmt"
	"strings"
)

// EventContext 事件上下文，封装了生成摘要所需的信息
type EventContext struct {
	Module     string
	Brief      string
	Severity   int
	RawMsg     string
	RawParams  map[string]string
	NormParams map[string]string
}

// P 快速从上下文提取语义角色对应的值
func (c *EventContext) P(semanticRole string, extraKeys ...string) string {
	return ResolveParam(c.NormParams, semanticRole, extraKeys...)
}

// HasP 检查某个语义角色的值是否存在
func (c *EventContext) HasP(semanticRole string, extraKeys ...string) bool {
	return c.P(semanticRole, extraKeys...) != ""
}

// Rule 定义单个协议的摘要规则
type Rule struct {
	Name    string
	Modules []string
	Handler func(ctx *EventContext) string
}

// DefaultRules 系统内置的协议摘要规则表
var DefaultRules = []Rule{
	// 1. BGP 协议
	{
		Name:    "BGP",
		Modules: []string{"BGP"},
		Handler: func(ctx *EventContext) string {
			briefUpper := strings.ToUpper(ctx.Brief)
			peer := ctx.P("peer")
			if peer == "" {
				peer = "—"
			}
			local := ctx.P("local")
			localText := ""
			if local != "" {
				localText = fmt.Sprintf(" 本端[%s]", local)
			}
			reason := ctx.P("reason")

			if strings.Contains(briefUpper, "DOWN") || strings.Contains(briefUpper, "BACKWARD") ||
				strings.Contains(briefUpper, "RESET") || strings.Contains(briefUpper, "HOLD_TIME") {
				if reason == "" {
					reason = "HoldTimer超时或对端重置会话"
				}
				return fmt.Sprintf("BGP邻居中断: 对端[%s]%s，中断原因: %s", peer, localText, reason)
			}
			if strings.Contains(briefUpper, "ESTABLISHED") || strings.Contains(briefUpper, "FORWARD") ||
				strings.Contains(briefUpper, "UP") {
				return fmt.Sprintf("BGP邻居建立: 对端[%s]%s，状态已转换为 ESTABLISHED", peer, localText)
			}
			if strings.Contains(briefUpper, "FLAP") || strings.Contains(briefUpper, "DAMP") {
				return fmt.Sprintf("BGP路由震荡: 对端[%s] 路由频繁抖动", peer)
			}
			res := fmt.Sprintf("BGP事件 [%s]: 对端[%s]", ctx.Brief, peer)
			if reason != "" {
				res += fmt.Sprintf("，原因: %s", reason)
			}
			return res
		},
	},

	// 2. OSPF 协议
	{
		Name:    "OSPF",
		Modules: []string{"OSPF", "OSPFV3"},
		Handler: func(ctx *EventContext) string {
			briefUpper := strings.ToUpper(ctx.Brief)
			nbr := ctx.P("routerid")
			if nbr == "" {
				nbr = "—"
			}
			iface := ctx.P("interface")
			if iface == "" {
				iface = "—"
			}
			reason := ctx.P("reason")

			if strings.Contains(briefUpper, "DOWN") || strings.Contains(briefUpper, "ADJCHANGE") ||
				strings.Contains(briefUpper, "RESET") {
				if reason == "" {
					reason = "邻居失效超时或接口Down"
				}
				return fmt.Sprintf("OSPF邻居中断: 接口[%s] 邻居Router-ID[%s]，原因: %s", iface, nbr, reason)
			}
			if strings.Contains(briefUpper, "FULL") || strings.Contains(briefUpper, "UP") ||
				strings.Contains(briefUpper, "ESTABLISHED") {
				return fmt.Sprintf("OSPF邻居建立: 接口[%s] 与邻居Router-ID[%s] 达到 Full 状态", iface, nbr)
			}
			return fmt.Sprintf("OSPF事件 [%s]: 接口[%s] 邻居Router-ID[%s]", ctx.Brief, iface, nbr)
		},
	},

	// 3. BFD 快速链路检测
	{
		Name:    "BFD",
		Modules: []string{"BFD"},
		Handler: func(ctx *EventContext) string {
			briefUpper := strings.ToUpper(ctx.Brief)
			peer := ctx.P("peer")
			if peer == "" {
				peer = "—"
			}
			iface := ctx.P("interface")
			ifaceText := ""
			if iface != "" {
				ifaceText = fmt.Sprintf(" 接口[%s]", iface)
			}
			diag := ctx.P("reason", "diag", "diagnostic", "diagcode")

			if strings.Contains(briefUpper, "DOWN") || strings.Contains(briefUpper, "FAIL") ||
				strings.Contains(briefUpper, "TIMEOUT") {
				if diag == "" {
					diag = "链路回显超时或对端Down"
				}
				return fmt.Sprintf("BFD会话中断: 对端[%s]%s，检测原因: %s", peer, ifaceText, diag)
			}
			if strings.Contains(briefUpper, "UP") || strings.Contains(briefUpper, "ESTABLISHED") {
				return fmt.Sprintf("BFD会话建立: 对端[%s] 双向连通状态恢复正常", peer)
			}
			res := fmt.Sprintf("BFD状态变更 [%s]: 对端[%s]", ctx.Brief, peer)
			if diag != "" {
				res += fmt.Sprintf("，诊断: %s", diag)
			}
			return res
		},
	},

	// 4. IFNET / PORT / ETHBASE 接口链路
	{
		Name:    "IFNET",
		Modules: []string{"IFNET", "PORT", "ETHBASE", "INTERFACE"},
		Handler: func(ctx *EventContext) string {
			briefUpper := strings.ToUpper(ctx.Brief)
			iface := ctx.P("interface")
			if iface == "" {
				iface = "—"
			}
			reason := ctx.P("reason")

			if strings.Contains(briefUpper, "DOWN") || strings.Contains(briefUpper, "ERRORDOWN") ||
				strings.Contains(briefUpper, "FAIL") {
				if reason == "" {
					reason = "物理光电信号丢失或人为关闭"
				}
				return fmt.Sprintf("接口链路中断: 接口[%s] 状态变更为 DOWN，原因: %s", iface, reason)
			}
			if strings.Contains(briefUpper, "UP") {
				return fmt.Sprintf("接口链路恢复: 接口[%s] 物理与协议状态已转换为 UP", iface)
			}
			res := fmt.Sprintf("接口事件 [%s]: 接口[%s]", ctx.Brief, iface)
			if reason != "" {
				res += fmt.Sprintf("，原因: %s", reason)
			}
			return res
		},
	},

	// 5. ISIS 协议
	{
		Name:    "ISIS",
		Modules: []string{"ISIS"},
		Handler: func(ctx *EventContext) string {
			briefUpper := strings.ToUpper(ctx.Brief)
			nbr := ctx.P("routerid", "neighborsystemid", "systemid", "nbrid")
			if nbr == "" {
				nbr = "—"
			}
			iface := ctx.P("interface")
			if iface == "" {
				iface = "—"
			}
			reason := ctx.P("reason")

			if strings.Contains(briefUpper, "DOWN") || strings.Contains(briefUpper, "RESET") {
				if reason == "" {
					reason = "HoldTime超时"
				}
				return fmt.Sprintf("ISIS邻居中断: 接口[%s] 邻居System-ID[%s]，原因: %s", iface, nbr, reason)
			}
			if strings.Contains(briefUpper, "UP") || strings.Contains(briefUpper, "ESTABLISHED") {
				return fmt.Sprintf("ISIS邻居建立: 接口[%s] 与邻居[%s] 邻接关系正常", iface, nbr)
			}
			res := fmt.Sprintf("ISIS事件 [%s]: 邻居[%s]", ctx.Brief, nbr)
			if reason != "" {
				res += fmt.Sprintf("，原因: %s", reason)
			}
			return res
		},
	},

	// 6. LAG / TRUNK / ETH-TRUNK 链路聚合
	{
		Name:    "LAG",
		Modules: []string{"LAG", "TRUNK", "ETHTRUNK", "ETH-TRUNK", "LACP"},
		Handler: func(ctx *EventContext) string {
			briefUpper := strings.ToUpper(ctx.Brief)
			trunk := ctx.P("trunk")
			if trunk == "" {
				trunk = "Eth-Trunk"
			}
			port := ctx.P("interface", "portname", "port", "memberport")
			if port == "" {
				port = "—"
			}
			reason := ctx.P("reason")

			if strings.Contains(briefUpper, "DOWN") || strings.Contains(briefUpper, "DEL") ||
				strings.Contains(briefUpper, "REMOVE") {
				if reason == "" {
					reason = "物理状态Down"
				}
				return fmt.Sprintf("聚合链路告警: 聚合组[%s] 成员端口[%s] 异常退出，原因: %s", trunk, port, reason)
			}
			if strings.Contains(briefUpper, "UP") || strings.Contains(briefUpper, "ADD") {
				return fmt.Sprintf("聚合链路变动: 聚合组[%s] 成员端口[%s] 成功加入聚合", trunk, port)
			}
			res := fmt.Sprintf("链路聚合事件 [%s]: 聚合组[%s]", ctx.Brief, trunk)
			if port != "—" {
				res += fmt.Sprintf(" 端口[%s]", port)
			}
			return res
		},
	},

	// 7. AAA / RADIUS / HWTACACS 安全认证
	{
		Name:    "AAA",
		Modules: []string{"AAA", "RADIUS", "HWTACACS"},
		Handler: func(ctx *EventContext) string {
			briefUpper := strings.ToUpper(ctx.Brief)
			server := ctx.P("server")
			user := ctx.P("user")
			reason := ctx.P("reason")

			if strings.Contains(briefUpper, "DOWN") || strings.Contains(briefUpper, "TIMEOUT") ||
				strings.Contains(briefUpper, "UNREACHABLE") {
				if server == "" {
					server = "—"
				}
				if reason == "" {
					reason = "网络中断或服务停止"
				}
				return fmt.Sprintf("AAA服务器异常: 认证服务器[%s] 状态不可达/无响应，原因: %s", server, reason)
			}
			if strings.Contains(briefUpper, "FAIL") || strings.Contains(briefUpper, "DENY") ||
				strings.Contains(briefUpper, "REJECT") {
				if user == "" {
					user = "—"
				}
				if reason == "" {
					reason = "密码错误或策略限制"
				}
				serverText := ""
				if server != "" {
					serverText = fmt.Sprintf(" (服务器: %s)", server)
				}
				return fmt.Sprintf("AAA认证失败: 用户[%s] 认证被拒绝%s，原因: %s", user, serverText, reason)
			}
			res := fmt.Sprintf("AAA/RADIUS事件 [%s]:", ctx.Brief)
			if user != "" {
				res += fmt.Sprintf(" 用户[%s]", user)
			}
			if server != "" {
				res += fmt.Sprintf(" 服务器[%s]", server)
			}
			return res
		},
	},

	// 8. DEVM / FAN / POWER 硬件环境
	{
		Name:    "DEVM",
		Modules: []string{"DEVM", "FAN", "POWER", "ENVIRONMENT"},
		Handler: func(ctx *EventContext) string {
			component := ctx.P("component")
			if component == "" {
				component = "硬件部件"
			}
			reason := ctx.P("reason", "currentstate", "threshold")
			if reason == "" {
				reason = "硬件指标异常"
			}
			return fmt.Sprintf("设备硬件告警 [%s]: 部件[%s]，状态/原因: %s", ctx.Brief, component, reason)
		},
	},

	// 9. EVPN 协议（支持序列化带编号参数）
	{
		Name:    "EVPN",
		Modules: []string{"EVPN"},
		Handler: func(ctx *EventContext) string {
			briefUpper := strings.ToUpper(ctx.Brief)
			instance := ctx.P("evpninstance")
			if instance == "" {
				instance = "—"
			}
			mac := ctx.P("mac")
			iface := ctx.P("interface")
			ip := ctx.P("ip")

			if strings.Contains(briefUpper, "MACDUP") || strings.Contains(briefUpper, "ACTIVE") {
				res := fmt.Sprintf("EVPN MAC重复告警: 实例[%s]", instance)
				if mac != "" {
					res += fmt.Sprintf(" MAC[%s]", mac)
				}
				if iface != "" {
					res += fmt.Sprintf(" 接口[%s]", iface)
				}
				if ip != "" {
					res += fmt.Sprintf(" IP[%s]", ip)
				}
				return res
			}
			if strings.Contains(briefUpper, "CLEAR") {
				res := fmt.Sprintf("EVPN告警恢复: 实例[%s]", instance)
				if mac != "" {
					res += fmt.Sprintf(" MAC[%s]", mac)
				}
				return res
			}
			res := fmt.Sprintf("EVPN事件 [%s]: 实例[%s]", ctx.Brief, instance)
			if mac != "" {
				res += fmt.Sprintf(" MAC[%s]", mac)
			}
			return res
		},
	},

	// 10. CLI 操作日志
	{
		Name:    "CLI",
		Modules: []string{"CLI"},
		Handler: func(ctx *EventContext) string {
			user := ctx.P("user")
			if user == "" {
				user = "admin"
			}
			ip := ctx.P("ip", "remoteip")
			ipText := ""
			if ip != "" {
				ipText = fmt.Sprintf(" 从[%s]", ip)
			}
			cmd := ctx.P("command")
			if cmd != "" {
				return fmt.Sprintf("CLI操作记录: 用户[%s]%s 执行 [%s]", user, ipText, cmd)
			}
			task := ctx.P("task", "taskname")
			taskText := ""
			if task != "" {
				taskText = fmt.Sprintf(" 终端[%s]", task)
			}
			return fmt.Sprintf("CLI记录: 用户[%s]%s%s", user, ipText, taskText)
		},
	},

	// 11. MLAG / DFS / PEERLINK
	{
		Name:    "MLAG",
		Modules: []string{"MLAG", "DFS", "PEERLINK"},
		Handler: func(ctx *EventContext) string {
			briefUpper := strings.ToUpper(ctx.Brief)
			if strings.Contains(briefUpper, "DOWN") {
				return "M-LAG告警: Peer-Link中断，可能触发双主冲突"
			}
			if strings.Contains(briefUpper, "UP") {
				return "M-LAG恢复: Peer-Link双向正常"
			}
			return fmt.Sprintf("M-LAG事件 [%s]", ctx.Brief)
		},
	},

	// 12. OPTICAL / TRANSCEIVER 光模块
	{
		Name:    "OPTICAL",
		Modules: []string{"OPTICAL", "TRANSCEIVER"},
		Handler: func(ctx *EventContext) string {
			comp := ctx.P("component", "interface")
			if comp == "" {
				comp = "光模块"
			}
			reason := ctx.P("reason")
			res := fmt.Sprintf("光模块告警 [%s]: 部件[%s]", ctx.Brief, comp)
			if reason != "" {
				res += fmt.Sprintf("，原因: %s", reason)
			}
			return res
		},
	},

	// 13. VRRP 协议
	{
		Name:    "VRRP",
		Modules: []string{"VRRP"},
		Handler: func(ctx *EventContext) string {
			iface := ctx.P("interface")
			if iface == "" {
				iface = "—"
			}
			state := ctx.P("state")
			res := fmt.Sprintf("VRRP状态变更 [%s]: 接口[%s]", ctx.Brief, iface)
			if state != "" {
				res += fmt.Sprintf(" 状态: %s", state)
			}
			return res
		},
	},
}

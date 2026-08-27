package summary

import (
	"fmt"
	"sort"
	"strings"
)

// NormalizeKey 规范化参数键名（小写 + 去除下划线、短横线、空格）
func NormalizeKey(k string) string {
	s := strings.ToLower(strings.TrimSpace(k))
	s = strings.ReplaceAll(s, "_", "")
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, " ", "")
	return s
}

// AliasGroups 华为多协议同义词映射池
// 键为语义角色，值为规范化后的所有候选字段名（按优先级排序）
var AliasGroups = map[string][]string{
	// 对端 / 邻居 IP 或地址
	"peer": {
		"bgppeerremoteaddr", "peerid", "peerremoteaddr", "neighbor",
		"peeraddr", "remoteaddr", "peerip", "peeraddress", "peer",
		"destination", "dstip", "sessid", "remotename",
	},
	// 本端 IP 或地址
	"local": {
		"bgppeerlocaladdr", "localaddr", "localaddress", "localip",
		"local", "srcip", "sourceip",
	},
	// 原因 / 错误信息 / 诊断码
	"reason": {
		"notifyreason", "reason", "errorcode", "errorsubcode",
		"bgppeerlasterror", "lasterror", "failreason", "eventreason",
		"cause", "errorreason", "diagnostic", "diag", "diagcode",
		"downreason", "disconnectreason",
	},
	// 状态
	"state": {
		"bgppeerstate", "state", "laststate", "currentstate",
		"peerstate", "nbrstate", "lineprotocolstatus", "sessionstate",
	},
	// 接口 / 端口
	"interface": {
		"interfacename", "interface", "ifname", "port", "portname",
		"ifnet", "memberport", "sourceinterface", "ininterface",
		"outinterface", "circuitid",
		// 华为日志中常见的序列化带编号接口名
		"interfacename1", "interfacename2", "interfacename3", "interfacename4",
		"interface1", "interface2",
	},
	// 路由 ID / 邻居 System ID
	"routerid": {
		"routerid", "nbrrouterid", "neighborrouterid", "neighbor",
		"nbrip", "nbr", "nbrid", "neighborsystemid", "systemid",
	},
	// 聚合链路 / Trunk
	"trunk": {
		"trunkid", "lagname", "ethtrunk", "trunkname", "lag",
	},
	// 认证 / 远端服务器
	"server": {
		"serverip", "serveraddr", "server", "radiusserver", "tacacsserver",
	},
	// 用户 / 账号
	"user": {
		"username", "user", "account", "authoruser",
	},
	// 硬件部件
	"component": {
		"entityname", "slot", "subslot", "fanid", "powerid", "cpuid",
		"boardname", "cardname", "chassisid",
	},
	// MAC 地址
	"mac": {
		"mac", "macaddress", "srcmac", "dstmac", "macaddr",
	},
	// EVPN 实例
	"evpninstance": {
		"evpninstancename", "vpninstance", "vpnname", "vpninstancename", "vcid",
	},
	// IP 地址（通用或序列化带编号 IP）
	"ip": {
		"ipaddress", "ipaddress1", "ipaddress2", "ipaddress3", "ipaddress4",
		"ip", "address", "remoteip", "clientip",
	},
	// 命令 / 操作
	"command": {
		"command", "cmd", "cmdrecord",
	},
	// 阈值与当前值
	"threshold": {
		"threshold", "upperlimit", "lowerlimit", "currentvalue", "val",
	},
}

// BuildNormalizedMap 构建规范化键名到原始键值的映射，方便快速模糊匹配
func BuildNormalizedMap(params map[string]string) map[string]string {
	res := make(map[string]string, len(params))
	for k, v := range params {
		val := strings.TrimSpace(v)
		if val == "" || val == "-" {
			continue
		}
		res[NormalizeKey(k)] = val
	}
	return res
}

// ResolveParam 根据语义角色或候选键名提取参数值
// 优先级: 1. extraKeys (精确或规范化) -> 2. AliasGroups[semanticRole]
func ResolveParam(normMap map[string]string, semanticRole string, extraKeys ...string) string {
	// 1. 尝试显式指定的候选键
	for _, k := range extraKeys {
		normK := NormalizeKey(k)
		if val, ok := normMap[normK]; ok && val != "" && val != "-" {
			return val
		}
	}

	// 2. 尝试别名组中的同义词
	if candidates, ok := AliasGroups[semanticRole]; ok {
		for _, cand := range candidates {
			if val, ok := normMap[cand]; ok && val != "" && val != "-" {
				return val
			}
		}
	}

	return ""
}

// ExtractTopParams 从参数字典中提取前 N 个键值对拼接展示（用于通用兜底）
func ExtractTopParams(params map[string]string, limit int) string {
	if len(params) == 0 {
		return ""
	}

	// 排序保证输出确定性
	keys := make([]string, 0, len(params))
	for k, v := range params {
		val := strings.TrimSpace(v)
		if val != "" && val != "-" {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)

	var parts []string
	count := 0
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s: %s", k, params[k]))
		count++
		if count >= limit {
			break
		}
	}

	return strings.Join(parts, " | ")
}

/**
 * 筛选预设与系统内置专家场景常量库 (Phase 2)。
 * 严格遵从《前端样式规范.md》铁律 5，杜绝 Emoji，使用 Element Plus 矢量图标与设计系统色彩令牌。
 */

export const SYSTEM_EXPERT_PRESETS = [
  {
    id: 'sys_link_flap',
    name: '核心链路震荡排查',
    description: '快速定位网口抖动、生成树拓扑计算与端口批量变更事件',
    icon: 'WarningFilled',
    colorToken: 'var(--warning-strong)',
    filters: {
      severity: 4,
      advanced: {
        logic: 'AND',
        conditions: [
          { field: 'module', op: 'in', value: 'IFNET,MSTP,ETH-TRUNK', disabled: false },
          { field: 'brief', op: 'regex', value: '(?i)(down|flap|change|topology)', disabled: false }
        ]
      }
    }
  },
  {
    id: 'sys_routing_anomaly',
    name: '动态路由邻居异常',
    description: '快速排查 BGP/OSPF/ISIS 邻居状态失效、保持定时器超时与路由震荡',
    icon: 'Connection',
    colorToken: 'var(--brand-600)',
    filters: {
      severity: 4,
      advanced: {
        logic: 'AND',
        conditions: [
          { field: 'module', op: 'in', value: 'BGP,OSPF,ISIS', disabled: false },
          { field: 'raw_log', op: 'regex', value: '(?i)(down|fail|reset|expire|backward)', disabled: false }
        ]
      }
    }
  },
  {
    id: 'sys_security_attack',
    name: '安全攻防与 ACL 拦截',
    description: '聚焦防火墙安全策略黑名单封禁、攻击防御与安全策略丢包',
    icon: 'Lock',
    colorToken: 'var(--danger-strong)',
    filters: {
      severity: 3,
      advanced: {
        logic: 'AND',
        conditions: [
          { field: 'module', op: 'in', value: 'SEC,ACL,FW,FILTER,BLACKLIST', disabled: false }
        ]
      }
    }
  },
  {
    id: 'sys_unmatched_kb',
    name: '未收录知识库日志定位',
    description: '精确定位官方知识库字典未覆盖或匹配置信度偏低的边缘告警日志',
    icon: 'QuestionFilled',
    colorToken: 'var(--info-strong)',
    filters: {
      matched: false,
      severity: 4,
      advanced: {
        logic: 'AND',
        conditions: []
      }
    }
  },
  {
    id: 'sys_hardware_env',
    name: '设备硬件与环境隐患',
    description: '排查板卡槽位、电源模块、风扇转速异常及光模块光衰告警',
    icon: 'Cpu',
    colorToken: 'var(--warning-text)',
    filters: {
      severity: 3,
      advanced: {
        logic: 'AND',
        conditions: [
          { field: 'module', op: 'in', value: 'DEVM,POWER,FAN,ENVMON,SFP,CARD', disabled: false }
        ]
      }
    }
  },
  {
    id: 'sys_untagged_audit',
    name: '未打标遗留日志审计',
    description: '过滤尚未被人工打标复核的原始日志，排查潜在漏网之鱼',
    icon: 'CollectionTag',
    colorToken: 'var(--brand-600)',
    filters: {
      untaggedOnly: true,
      advanced: {
        logic: 'AND',
        conditions: []
      }
    }
  }
]

export default {
  SYSTEM_EXPERT_PRESETS
}

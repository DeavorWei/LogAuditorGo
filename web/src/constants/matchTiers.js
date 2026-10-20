/**
 * 知识匹配层级的中文展示与解释。
 *
 * 背景（规划 F3）：v1.0 只把 EXACT/BLEVE 等英文枚举直接透出，
 * 用户不知道"匹配层级"意味着什么，也不知道该不该相信这条知识。
 * 这里为每个层级给出中文名、可信度说明与颜色语义。
 */

export const MATCH_TIER = Object.freeze({
  EXACT: 'EXACT',
  MNEMONIC: 'MNEMONIC',
  TEMPLATE: 'TEMPLATE',
  BLEVE: 'BLEVE',
  FAMILY_SUSPECT: 'FAMILY_SUSPECT',
  UNMATCHED: 'UNMATCHED'
})

const TIER_META = Object.freeze({
  [MATCH_TIER.EXACT]: {
    label: '精确匹配',
    tagType: 'success',
    description: '模块与助记符完全一致，可信度最高'
  },
  [MATCH_TIER.MNEMONIC]: {
    label: '助记符匹配',
    tagType: 'success',
    description: '助记符别名或状态后缀归一后命中'
  },
  [MATCH_TIER.TEMPLATE]: {
    label: '模板匹配',
    tagType: 'warning',
    description: '日志正文与官方消息模板匹配，现场参数已注入'
  },
  [MATCH_TIER.BLEVE]: {
    label: '语义检索',
    tagType: 'warning',
    description: '基于全文检索召回，相关度有限，需人工确认'
  },
  [MATCH_TIER.FAMILY_SUSPECT]: {
    label: '产品族存疑',
    tagType: 'danger',
    description: '候选产品族与日志特征不一致，仅作参考'
  },
  [MATCH_TIER.UNMATCHED]: {
    label: '暂无官方知识',
    tagType: 'info',
    description: '知识库中没有可匹配条目，不代表日志无意义'
  }
})

export function matchTierMeta(tier) {
  return TIER_META[tier] || {
    label: tier || '未知',
    tagType: 'info',
    description: '未知匹配层级'
  }
}

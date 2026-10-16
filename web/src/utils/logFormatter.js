/**
 * 日志报文与知识库富文本格式化工具 (纯函数 + WeakMap 缓存)
 * 
 * 背景：
 * 原实现在 AuditWorkbench.vue 内部维护了 300+ 行计算属性与正则替换逻辑。
 * 在重构为多组件架构后，若在各组件内重复实例化计算属性，会导致切换日志时对同一条日志重复执行 2~3 遍复杂的正则匹配与拼接。
 * 
 * 优化设计：
 * 1. 纯函数下沉：剥离所有 Vue 响应式依赖，便于轻量测试与无副作用复用；
 * 2. WeakMap 双层缓存：WeakMap<logObject, Map<contextualizeMode, Result>>
 *    - 解决过期数据：重新分析或更新拉取时，后端返回全新对象引用，WeakMap 自动失配并触发全新解析，杜绝陈旧数据；
 *    - 自动内存回收：日志对象被 GC 时缓存自动清除，零内存泄漏；
 * 3. 导出统一的 Symbol 注入标识：LOG_DETAIL_CONTEXT_KEY
 */

export const LOG_DETAIL_CONTEXT_KEY = Symbol('LOG_DETAIL_CONTEXT')

/**
 * HTML 转义防 XSS
 */
export const escapeHtml = (text) => {
  if (!text) return ''
  return String(text)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#039;')
}

/**
 * 规范化辅助函数：忽略大小写、空格与下划线
 */
export const normalizeParamKey = (k) => {
  return (k || '').toString().toLowerCase().replace(/[-_\s]/g, '')
}

/**
 * 换行符归一化（处理字面量 \n 与 \r\n），并对"步骤/原因序号"智能补换行
 */
export const normalizeLineBreaks = (raw) => {
  if (!raw) return ''
  let normalized = String(raw)
    .replace(/\\r\\n/g, '\n')
    .replace(/\\n/g, '\n')
    .replace(/\r\n/g, '\n')
    .replace(/\r/g, '\n')

  if (!normalized.includes('\n')) {
    normalized = normalized
      .replace(/(\s+)(\d+[\.、]\s*)/g, '\n$2')
      .replace(/(\s+)(步骤\s*\d+[\.、:：]?\s*)/g, '\n$2')
      .replace(/(\s+)(原因\s*\d+[\.、:：]?\s*)/g, '\n$2')
      .trim()
  }
  return normalized
}

/**
 * 格式化 tooltip 内容，支持换行并限制最大显示范围
 */
export const formatTooltipHtml = (text) => {
  if (!text) return ''
  const escaped = escapeHtml(text).replace(/\r?\n/g, '<br/>')
  return `<div style="max-width: 420px; max-height: 280px; overflow-y: auto; white-space: normal; line-height: 1.6; font-size: 12px; padding: 2px 4px;"><strong>官方文档定义:</strong><br/>${escaped}</div>`
}

/**
 * 解析原始日志中的 parameters_json
 */
export const parseParameters = (log) => {
  if (!log || !log.parameters_json) return {}
  try {
    return JSON.parse(log.parameters_json)
  } catch (e) {
    return {}
  }
}

/**
 * 融合动态变量与官方文档参数定义
 */
export const buildEnrichedParameters = (log, rawParams = {}) => {
  if (!log) return []

  // 如果后端已经返回 enriched_parameters，优先直接使用
  if (Array.isArray(log.enriched_parameters) && log.enriched_parameters.length > 0) {
    return log.enriched_parameters.map((p) => ({
      name: p.name || p.Name,
      value: p.value || p.Value,
      description: p.description || p.Description || '',
      matched: !!(p.description || p.Description)
    }))
  }

  const kb = log.knowledge
  let defs = []
  if (kb && kb.parameters) {
    try {
      const parsed = typeof kb.parameters === 'string' ? JSON.parse(kb.parameters) : kb.parameters
      if (Array.isArray(parsed)) {
        defs = parsed
      }
    } catch (e) {}
  }

  // 构建精确查找字典与规范化模糊查找字典
  const exactMap = new Map()
  const normalizedMap = new Map()
  for (const d of defs) {
    const name = d.name || d.Name || ''
    const desc = d.description || d.Description || ''
    if (name) {
      exactMap.set(name, desc)
      normalizedMap.set(normalizeParamKey(name), desc)
    }
  }

  const result = []
  for (const [key, val] of Object.entries(rawParams)) {
    let desc = exactMap.get(key)
    if (!desc) {
      desc = normalizedMap.get(normalizeParamKey(key)) || ''
    }
    result.push({
      name: key,
      value: val,
      description: desc,
      matched: !!desc
    })
  }

  return result
}

/**
 * 官方日志消息模板实例化（将占位符替换为提取的真实值并高亮）
 */
export const renderTemplateHtml = (log, enrichedParams = [], rawParams = {}) => {
  if (!log || !log.knowledge || !log.knowledge.message) return ''

  const rawTemplate = log.knowledge.message

  // 构建所有可用参数字典
  const params = { ...(rawParams || {}) }
  if (Array.isArray(enrichedParams)) {
    for (const ep of enrichedParams) {
      if (ep.name && ep.value !== undefined && ep.value !== '') {
        params[ep.name] = ep.value
      }
    }
  }

  const normParams = new Map()
  for (const [k, v] of Object.entries(params)) {
    normParams.set(normalizeParamKey(k), v)
  }

  const findActualVal = (keyName) => {
    if (!keyName) return undefined
    if (params[keyName] !== undefined) return params[keyName]
    const normK = normalizeParamKey(keyName)
    if (normParams.has(normK)) return normParams.get(normK)
    return undefined
  }

  const placeholderRegex = /(\[\s*([a-zA-Z0-9_\-\s]+?)\s*\]|<\s*([a-zA-Z0-9_\-\s]+?)\s*>|\{\s*([a-zA-Z0-9_\-\s]+?)\s*\}|%\s*([a-zA-Z0-9_\-\s]+?)\s*%|\$\s*([a-zA-Z0-9_\-]+))/g

  let lastIdx = 0
  let html = ''
  let match

  while ((match = placeholderRegex.exec(rawTemplate)) !== null) {
    const start = match.index
    const end = placeholderRegex.lastIndex
    const fullMatch = match[0]
    const rawKey = match[2] || match[3] || match[4] || match[5] || match[6]
    const keyName = rawKey ? rawKey.trim() : ''

    html += escapeHtml(rawTemplate.substring(lastIdx, start))

    const actualVal = findActualVal(keyName)
    if (actualVal !== undefined) {
      html += `<span class="inst-param-injected" title="参数: ${escapeHtml(keyName)} = ${escapeHtml(actualVal)}">${escapeHtml(actualVal)}</span>`
    } else {
      html += `<span class="inst-param-placeholder">${escapeHtml(fullMatch)}</span>`
    }

    lastIdx = end
  }

  html += escapeHtml(rawTemplate.substring(lastIdx))
  return html
}

/**
 * 现场排查上下文参数注入与文本高亮
 */
export const renderContextualizedHtml = (text, contextualizeMode, enrichedParams = [], rawParams = {}) => {
  if (!text) return ''
  if (!contextualizeMode) {
    return escapeHtml(normalizeLineBreaks(String(text))).replace(/\n/g, '<br/>')
  }

  const normalized = normalizeLineBreaks(text)

  const params = { ...(rawParams || {}) }
  if (Array.isArray(enrichedParams)) {
    for (const ep of enrichedParams) {
      if (ep.name && ep.value !== undefined && ep.value !== '') {
        params[ep.name] = ep.value
      }
    }
  }

  const normParams = new Map()
  for (const [k, v] of Object.entries(params)) {
    normParams.set(normalizeParamKey(k), v)
  }

  const placeholderRegex = /(\[([a-zA-Z0-9_\-]+)\]|<([a-zA-Z0-9_\-]+)>|\{([a-zA-Z0-9_\-]+)\}|%([a-zA-Z0-9_\-]+)%|\$([a-zA-Z0-9_\-]+))/g

  let lastIdx = 0
  let html = ''
  let match

  while ((match = placeholderRegex.exec(normalized)) !== null) {
    const start = match.index
    const end = placeholderRegex.lastIndex
    const fullMatch = match[0]
    const keyName = match[2] || match[3] || match[4] || match[5] || match[6]

    html += escapeHtml(normalized.substring(lastIdx, start)).replace(/\n/g, '<br/>')

    let actualVal = params[keyName]
    if (actualVal === undefined) {
      actualVal = normParams.get(normalizeParamKey(keyName))
    }

    if (actualVal !== undefined) {
      html += `<span class="inst-param-injected" title="现场变量: ${escapeHtml(keyName)} = ${escapeHtml(actualVal)}">${escapeHtml(actualVal)}</span>`
    } else {
      html += `<span class="inst-param-placeholder">${escapeHtml(fullMatch)}</span>`
    }

    lastIdx = end
  }

  html += escapeHtml(normalized.substring(lastIdx)).replace(/\n/g, '<br/>')
  return html
}

/**
 * 提取官方知识库参数字典与现场实际值对照列表
 */
export const extractKbParamDefs = (log, enrichedParams = [], rawParams = {}) => {
  const kb = log?.knowledge
  if (!kb || !kb.parameters) return []

  let defs = []
  try {
    const parsed = typeof kb.parameters === 'string' ? JSON.parse(kb.parameters) : kb.parameters
    if (Array.isArray(parsed)) {
      defs = parsed
    }
  } catch (e) {
    return []
  }

  const params = { ...(rawParams || {}) }
  if (Array.isArray(enrichedParams)) {
    for (const ep of enrichedParams) {
      if (ep.name && ep.value !== undefined && ep.value !== '') {
        params[ep.name] = ep.value
      }
    }
  }

  const normParams = new Map()
  for (const [k, v] of Object.entries(params)) {
    normParams.set(normalizeParamKey(k), v)
  }

  return defs.map((d) => {
    const name = d.name || d.Name || ''
    const desc = d.description || d.Description || ''
    let actualVal = params[name]
    if (actualVal === undefined) {
      actualVal = normParams.get(normalizeParamKey(name))
    }

    return {
      name,
      description: desc,
      actualValue: actualVal
    }
  })
}

/**
 * 全局 WeakMap 缓存实例
 * WeakMap<logObject, Map<contextualizeMode, Result>>
 */
const detailCache = new WeakMap()

/**
 * 格式化日志详情总入口（带 WeakMap 双层缓存）
 * @param {Object|null} log 原始日志对象
 * @param {boolean} contextualizeMode 是否开启现场参数注入
 */
export function formatLogDetail(log, contextualizeMode = true) {
  if (!log || typeof log !== 'object') {
    return {
      parsedParameters: {},
      enrichedParameters: [],
      renderedTemplateHtml: '',
      renderedKnowledgeHtml: { description: '', cause: '', action: '', impact: '' },
      kbParamDefs: [],
      matchedParamCount: 0
    }
  }

  const modeKey = !!contextualizeMode

  let modeMap = detailCache.get(log)
  if (!modeMap) {
    modeMap = new Map()
    detailCache.set(log, modeMap)
  }

  if (modeMap.has(modeKey)) {
    return modeMap.get(modeKey)
  }

  const parsedParameters = parseParameters(log)
  const enrichedParameters = buildEnrichedParameters(log, parsedParameters)
  const renderedTemplateHtml = renderTemplateHtml(log, enrichedParameters, parsedParameters)

  const kb = log.knowledge
  const renderedKnowledgeHtml = kb
    ? {
        description: renderContextualizedHtml(kb.description || kb.message, modeKey, enrichedParameters, parsedParameters),
        cause: renderContextualizedHtml(kb.cause || '官方文档未提供特定原因', modeKey, enrichedParameters, parsedParameters),
        action: renderContextualizedHtml(kb.action || '按标准网络排错规范处理', modeKey, enrichedParameters, parsedParameters),
        impact: renderContextualizedHtml(kb.impact, modeKey, enrichedParameters, parsedParameters)
      }
    : { description: '', cause: '', action: '', impact: '' }

  const kbParamDefs = extractKbParamDefs(log, enrichedParameters, parsedParameters)
  const matchedParamCount = enrichedParameters.filter((p) => p.matched).length
  const matchedDocName = getMatchedDocName(log)

  const result = {
    parsedParameters,
    enrichedParameters,
    renderedTemplateHtml,
    renderedKnowledgeHtml,
    kbParamDefs,
    matchedParamCount,
    matchedDocName
  }

  modeMap.set(modeKey, result)
  return result
}

/**
 * 提取日志命中的官方产品文档全称
 * 优先取后端富化的 matched_doc_name，其次遍历 versions 中的 doc_name，若无全称则回退为 product_type
 * 未匹配返回 '-'
 */
export const getMatchedDocName = (log) => {
  if (!log) return '-'
  if (log.matched_doc_name && typeof log.matched_doc_name === 'string' && log.matched_doc_name.trim()) {
    return log.matched_doc_name.trim()
  }
  const versions = log.knowledge?.versions
  if (Array.isArray(versions) && versions.length > 0) {
    for (const v of versions) {
      if (v?.doc_name && String(v.doc_name).trim()) {
        return String(v.doc_name).trim()
      }
    }
    for (const v of versions) {
      if (v?.product_type && String(v.product_type).trim()) {
        return String(v.product_type).trim()
      }
    }
  }
  return '-'
}

export default formatLogDetail


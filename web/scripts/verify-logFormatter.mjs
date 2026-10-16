/**
 * verify-logFormatter.mjs
 * 纯函数与 WeakMap 缓存核心单元断言测试脚本
 * 运行方式: node scripts/verify-logFormatter.mjs
 */
import assert from 'node:assert/strict'
import {
  escapeHtml,
  formatLogDetail,
  LOG_DETAIL_CONTEXT_KEY
} from '../src/utils/logFormatter.js'

console.log('🧪 开始执行 logFormatter 纯函数与 WeakMap 缓存单元测试...\n')

// 1. 测试 Symbol Key 导出
assert.equal(typeof LOG_DETAIL_CONTEXT_KEY, 'symbol', 'LOG_DETAIL_CONTEXT_KEY 应为 Symbol 类型')
assert.equal(LOG_DETAIL_CONTEXT_KEY.description, 'LOG_DETAIL_CONTEXT', 'Symbol 描述需匹配')
console.log('✓ 1. LOG_DETAIL_CONTEXT_KEY 符号导出验证通过')

// 2. 测试 HTML 安全转义
assert.equal(escapeHtml('<script>alert("xss")</script>'), '&lt;script&gt;alert(&quot;xss&quot;)&lt;/script&gt;')
assert.equal(escapeHtml('a & b > c < d \'e\''), 'a &amp; b &gt; c &lt; d &#039;e&#039;')
assert.equal(escapeHtml(''), '')
assert.equal(escapeHtml(null), '')
console.log('✓ 2. escapeHtml XSS 转义防御测试通过')

// 3. 测试空对象与无效输入降级为安全空结构
const emptyRes = formatLogDetail(null)
assert.ok(emptyRes && typeof emptyRes === 'object', '空值应返回安全空结构')
assert.deepEqual(emptyRes.enrichedParameters, [])
assert.deepEqual(emptyRes.parsedParameters, {})
assert.equal(emptyRes.matchedParamCount, 0)
assert.equal(formatLogDetail('invalid-string').matchedParamCount, 0)
console.log('✓ 3. 空值与异常输入安全默认值降级测试通过')

// 4. 测试基础参数解析与富文本高亮
const mockLog1 = {
  id: 1001,
  device_ip: '192.168.1.1',
  timestamp: '2026-10-12 10:00:00',
  message: 'User admin logged in from 10.0.0.1 via SSH status SUCCESS',
  enriched_parameters: [
    { name: 'user', value: 'admin' },
    { name: 'src_ip', value: '10.0.0.1' },
    { name: 'status', value: 'SUCCESS' }
  ],
  knowledge: {
    message: 'User <user> logged in from <src_ip> via SSH status <status>',
    description: 'SSH 登录审计日志: 用户 <user> 来源 <src_ip>'
  }
}

const res1 = formatLogDetail(mockLog1, false)
assert.ok(res1, '解析结果不应为空')
assert.ok(Array.isArray(res1.enrichedParameters), 'enrichedParameters 应为数组')
assert.equal(res1.enrichedParameters.length, 3, '应提取 3 个富化参数')
assert.ok(res1.renderedTemplateHtml.includes('inst-param-injected'), '报文模板应包含参数注入高亮样式类')
assert.ok(res1.renderedKnowledgeHtml.description.includes('&lt;user&gt;'), '未开启现场注入时知识库描述应保留转义占位符')
console.log('✓ 4. 日志参数解析与模板 HTML 渲染测试通过')

// 5. 测试 WeakMap 缓存命中
const res1_again = formatLogDetail(mockLog1, false)
assert.equal(res1, res1_again, '相同对象与相同 mode 应命中 WeakMap 缓存返回相同引用')
console.log('✓ 5. WeakMap 缓存命中一致性验证通过')

// 6. 测试 contextualizeMode 双层模式缓存区分
const res1_contextualized = formatLogDetail(mockLog1, true)
assert.notEqual(res1, res1_contextualized, '不同 contextualizeMode 应生成独立计算结果')
assert.ok(res1_contextualized.renderedKnowledgeHtml.description.includes('admin'), '现场注入模式下知识库描述应注入真实参数值 admin')
assert.ok(res1_contextualized.renderedKnowledgeHtml.description.includes('inst-param-injected'), '现场注入模式下应包含高亮标签')
console.log('✓ 6. contextualizeMode 双层缓存模式切换测试通过')

// 7. 测试对象引用替换（重新分析场景）导致缓存自动失效
const mockLog1_reanalyzed = {
  ...mockLog1,
  knowledge: {
    message: 'User <user> logged in from <src_ip> via SSH status <status> (UPDATED_KB)',
    description: '知识库重新匹配后的新解释 (UPDATED_KB)'
  }
}

const res_reanalyzed = formatLogDetail(mockLog1_reanalyzed, false)
assert.notEqual(res1, res_reanalyzed, '新对象引用必须自动失效旧缓存，杜绝脏数据')
assert.ok(res_reanalyzed.renderedKnowledgeHtml.description.includes('UPDATED_KB'), '应解析出重新分析后的最新知识库内容')
console.log('✓ 7. 重新分析对象引用失效与缓存自动隔离验证通过')

// 8. 测试匹配产品文档名称提取 (getMatchedDocName)
import { getMatchedDocName } from '../src/utils/logFormatter.js'
assert.equal(getMatchedDocName(null), '-', '空日志对象应返回 -')
assert.equal(getMatchedDocName({}), '-', '无文档信息的空日志应返回 -')
assert.equal(getMatchedDocName({ matched_doc_name: 'CloudEngine 16800 产品文档' }), 'CloudEngine 16800 产品文档', '优先提取 matched_doc_name')
assert.equal(
  getMatchedDocName({
    knowledge: {
      versions: [
        { doc_name: 'USG6000E 产品文档', product_type: 'USG6000E' }
      ]
    }
  }),
  'USG6000E 产品文档',
  '若无 matched_doc_name，应从 versions[0].doc_name 获取'
)
assert.equal(
  getMatchedDocName({
    knowledge: {
      versions: [
        { product_type: 'AirEngine 9700-M' }
      ]
    }
  }),
  'AirEngine 9700-M',
  '若 doc_name 为空，应回退到 product_type'
)
console.log('✓ 8. 匹配产品文档名称 (getMatchedDocName) 优先级与回退策略测试通过')

console.log('\n🎉 logFormatter 全部 8 项单元断言测试 100% 通过！\n')

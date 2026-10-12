import { computed, toValue } from 'vue'

/**
 * severity → 等级 class 的全局唯一映射（支持 Ref、getter 函数与普通值）
 * 级别标准：
 *   - Lv 0~2: crit (严重/致命)
 *   - Lv 3~4: err (错误/警告)
 *   - Lv 5~6: warn (通知/提示)
 *   - Lv 7+: info (调试/信息)
 *   - module === 'COMMENT': comment (注释型日志)
 */
export function useSeverity(severitySource, moduleSource) {
  const level = computed(() => {
    const mod = toValue(moduleSource)
    if (mod === 'COMMENT') return 'comment'
    const sev = Number(toValue(severitySource))
    if (isNaN(sev)) return 'info'
    if (sev <= 2) return 'crit'
    if (sev <= 4) return 'err'
    if (sev <= 6) return 'warn'
    return 'info'
  })
  const tagClass = computed(() => `is-${level.value}`)
  const stripClass = computed(() => `sev-strip is-${level.value}`)
  return { level, tagClass, stripClass }
}

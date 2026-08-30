import { onScopeDispose } from 'vue'

/**
 * useRequest —— 请求竞态守卫 (WEB-08)。
 *
 * 背景：api 层原本没有任何取消机制。筛选条件快速切换、任务切换时
 * `fetchLogs` 之类的请求会并发发出，慢请求后返回会覆盖新结果，
 * 用户看到的是"筛选 A 却显示 B 的数据"。
 *
 * 这里用 AbortController + 请求序号双重保护：
 *  1. 每次 run() 都会先取消上一次仍在途的请求；
 *  2. 组件卸载（scope dispose）时自动取消，杜绝"给已销毁组件赋值"。
 *
 * 关于错误提示：
 *   api/index.js 的响应拦截器已经对所有失败请求统一弹出 ElMessage，
 *   因此本守卫**默认不再重复弹窗**，避免"一次失败弹两次"。
 *   调用方若需要把错误反映到组件状态（例如渲染可重试的错误态），
 *   通过 onError 回调自行处理即可。
 *
 * 用法：
 *   const { run } = useRequest(api.queryTaskLogs, {
 *     onError: (e) => { errorMessage.value = e.message }
 *   })
 *   const res = await run(taskId, params)   // 取消或失败时返回 undefined
 */
export function useRequest(apiFn, options = {}) {
  const { onError } = options

  let controller = null
  let seq = 0

  const abort = () => {
    if (controller) {
      try {
        controller.abort()
      } catch (e) {
        // 已完成的 controller 再次 abort 不会抛错，这里仅为防御
      }
      controller = null
    }
  }

  const run = async (...args) => {
    abort()
    const current = ++seq
    controller = new AbortController()

    try {
      // api 层的方法最后一个参数支持 options，这里把 signal 注入进去
      const result = await apiFn(...args, { signal: controller.signal })
      // 竞态守卫：只有最新一次请求的结果才被采用
      if (current !== seq) {
        return undefined
      }
      return result
    } catch (err) {
      if (isCancelError(err)) {
        return undefined
      }
      if (current !== seq) {
        return undefined
      }
      if (onError) {
        onError(err)
      }
      return undefined
    } finally {
      if (current === seq) {
        controller = null
      }
    }
  }

  // 组件作用域销毁时自动取消在途请求
  onScopeDispose(abort)

  return { run, abort, get running() { return controller !== null } }
}

/**
 * isCancelError 判断是否为主动取消产生的错误
 */
export function isCancelError(err) {
  if (!err) return false
  return (
    err.name === 'CanceledError' ||
    err.name === 'AbortError' ||
    err.code === 'ERR_CANCELED' ||
    err.message === 'canceled'
  )
}

export default useRequest

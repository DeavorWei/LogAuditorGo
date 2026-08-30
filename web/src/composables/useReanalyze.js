import { ElMessage, ElMessageBox } from 'element-plus'
import api from '@/api'

/**
 * useReanalyze —— "重新分析"流程的共享编排 (WEB-16)。
 *
 * 背景：Tasks.vue 的 `handleReanalyze` 与 AuditWorkbench.vue 的
 * `handleReanalyzeTask` 原先几乎逐行重复（确认弹窗 → 调异步接口 →
 * 拿 job_id 挂进度弹窗 → 失败提示），两处各自演化已经出现行为分叉：
 * Tasks 版拿到 job_id 就结束，Workbench 版还会再触发一次完成回调。
 *
 * 这里收敛为单一实现，两个视图通过回调注入各自的"完成后收尾"逻辑。
 *
 * @param {Object} handlers
 * @param {(jobId: string) => void} handlers.onJobStarted 拿到 job_id，用于拉起进度弹窗
 * @param {(data: any) => void} [handlers.onSettled] 同步完成时（无 job_id）的收尾
 */
export function useReanalyze({ onJobStarted, onSettled } = {}) {
  const reanalyze = (task) => {
    if (!task || !task.task_id) return

    ElMessageBox.confirm(
      `确认对任务【${task.task_name}】的全部 ${task.log_count || 0} 行已导入日志重新执行知识库匹配与 RCA 根因拓扑分析吗？`,
      '重新分析确认',
      {
        confirmButtonText: '开始重新分析',
        cancelButtonText: '取消',
        type: 'warning'
      }
    )
      .then(async () => {
        try {
          const res = await api.reanalyzeTask(task.task_id, true)
          if (res.code === 0 && res.data?.job_id) {
            // 异步模式：交给进度弹窗跟踪，完成时由其 completed 事件驱动刷新
            onJobStarted?.(res.data.job_id)
            return
          }
          // 同步模式：接口已直接返回结果，走调用方的收尾逻辑
          ElMessage.success('重新分析请求已提交')
          await onSettled?.(res.data)
        } catch (e) {
          ElMessage.error('触发重新分析失败: ' + (e?.message || '网络异常'))
        }
      })
      .catch(() => {
        // 用户取消确认，静默返回
      })
  }

  return { reanalyze }
}

export default useReanalyze

import { onMounted, onUnmounted } from 'vue'

/**
 * 工作台全局按键守卫分发引擎与无障碍快捷键调度
 * 
 * 职责：
 * 1. 焦点守卫：在 input / textarea / contenteditable 编辑时静默；
 * 2. 状态守卫：仅在主工作台且存在有效任务时激活；
 * 3. 弹窗层级守卫：高优先级弹窗打开时不劫持快捷键；
 * 4. 连发拦截：阻止 e.repeat 连击；
 * 5. 统一分发 Space (快速打标), Shift+F (浏览器全屏), F (沉浸Zen模式), Escape (分层退出)。
 */
export function useWorkbenchHotkeys(options = {}) {
  const {
    isWorkbenchView = () => true,
    hasActiveTask = () => true,
    hasActiveOverlay = () => false,
    onSpace = () => {},
    onPrevLog = () => false,
    onNextLog = () => false,
    onToggleZenMode = () => {},
    onToggleBrowserFullscreen = () => {},
    onEscape = () => {}
  } = options

  const onGlobalKeydown = (e) => {
    // 1. 视图与空任务守卫
    if (!isWorkbenchView() || !hasActiveTask()) return

    // 2. 输入框控件焦点守卫
    const target = e.target
    const isInput =
      target &&
      (target.tagName === 'INPUT' ||
        target.tagName === 'TEXTAREA' ||
        target.isContentEditable)
    if (isInput) return

    // 3. 长按连发拦截
    if (e.repeat) return

    // 4. Esc 键分层退出 (允许在弹窗开启时关闭弹窗，故在 overlay 拦截之前执行)
    if (e.key === 'Escape') {
      e.preventDefault()
      onEscape()
      return
    }

    // 5. 浮层/抽屉层级守卫 (所有后续业务快捷键在此处统一拦截，杜绝弹窗穿透)
    if (hasActiveOverlay()) return

    // 6. 空格键：快速打标 (严格排除所有修饰键，仅在成功命中有效目标时 preventDefault)
    if (!e.shiftKey && !e.ctrlKey && !e.altKey && !e.metaKey && e.code === 'Space') {
      if (onSpace() !== false) {
        e.preventDefault()
        return
      }
    }

    // 7. j / k / ↑ / ↓ 键：日志快速上下穿梭
    if (!e.shiftKey && !e.ctrlKey && !e.altKey && !e.metaKey) {
      if (e.key === 'j' || e.key === 'J' || e.key === 'ArrowDown') {
        if (onNextLog() !== false) {
          e.preventDefault()
          return
        }
      } else if (e.key === 'k' || e.key === 'K' || e.key === 'ArrowUp') {
        if (onPrevLog() !== false) {
          e.preventDefault()
          return
        }
      }
    }

    // 8. Shift + F: 浏览器物理全屏 (排除 Ctrl/Alt/Meta)
    if (e.shiftKey && !e.ctrlKey && !e.altKey && !e.metaKey && (e.key === 'F' || e.key === 'f')) {
      e.preventDefault()
      onToggleBrowserFullscreen()
      return
    }

    // 9. f 键: 应用内 Zen Mode 沉浸全屏切换 (严格排除 Shift/Ctrl/Alt/Meta，杜绝劫持 Ctrl+F 页面查找)
    if (!e.shiftKey && !e.ctrlKey && !e.altKey && !e.metaKey && (e.key === 'f' || e.key === 'F')) {
      e.preventDefault()
      onToggleZenMode()
      return
    }
  }

  onMounted(() => {
    window.addEventListener('keydown', onGlobalKeydown)
  })

  onUnmounted(() => {
    window.removeEventListener('keydown', onGlobalKeydown)
  })

  return {
    onGlobalKeydown
  }
}

export default useWorkbenchHotkeys

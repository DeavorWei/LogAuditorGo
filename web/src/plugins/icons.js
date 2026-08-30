/**
 * 全局图标按需注册表 (WEB-15 / UI-14)。
 *
 * 背景：main.js 原先遍历注册 @element-plus/icons-vue 的全部图标组件，
 * 首屏需要解析上千个组件定义，是构建产物体积与启动耗时的主要来源之一。
 *
 * 绝大多数视图已经改用"具名导入 + 局部注册"，这里只保留
 * **通过字符串名动态引用**的图标（例如 `<el-button icon="Check">`
 * 或 Element Plus 内部按名查找的场景）。
 *
 * 新增图标时在此补一行即可 —— 既保持按需引入的收益，
 * 又不会像"全量注册"那样把整个图标库打进首屏 chunk。
 */
import {
  Check,
  Close,
  CloseBold,
  Loading,
  Refresh,
  Search,
  Delete,
  Edit,
  Download,
  Upload,
  UploadFilled,
  Folder,
  FolderOpened,
  FolderAdd,
  Document,
  DocumentCopy,
  Files,
  Monitor,
  Odometer,
  DataAnalysis,
  Histogram,
  Aim,
  ArrowRight,
  Opportunity,
  Connection,
  Setting,
  Platform,
  List,
  Reading,
  Position,
  Warning,
  WarningFilled,
  SuccessFilled,
  CircleClose,
  InfoFilled,
  Clock
} from '@element-plus/icons-vue'

// 字符串名 → 组件。键名必须与模板中使用的 icon 属性完全一致。
const globalIcons = {
  Check,
  Close,
  CloseBold,
  Loading,
  Refresh,
  Search,
  Delete,
  Edit,
  Download,
  Upload,
  UploadFilled,
  Folder,
  FolderOpened,
  FolderAdd,
  Document,
  DocumentCopy,
  Files,
  Monitor,
  Odometer,
  DataAnalysis,
  Histogram,
  Aim,
  ArrowRight,
  Opportunity,
  Connection,
  Setting,
  Platform,
  List,
  Reading,
  Position,
  Warning,
  WarningFilled,
  SuccessFilled,
  CircleClose,
  InfoFilled,
  Clock
}

export function registerGlobalIcons(app) {
  for (const [name, component] of Object.entries(globalIcons)) {
    if (component) {
      app.component(name, component)
    }
  }
}

export default globalIcons

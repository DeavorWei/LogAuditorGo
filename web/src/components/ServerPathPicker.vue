<template>
  <el-dialog
    v-model="dialogVisible"
    :title="title || '选择路径'"
    width="1000px"
    top="5vh"
    append-to-body
    destroy-on-close
    class="path-picker-dialog"
    @open="handleOpen"
  >
    <div class="path-picker">
      <!--
        顶部工具栏：导航 + 智能路径条。
        原实现为 el-breadcrumb + overflow-x:auto，长路径会出现横向滚动条并被拉伸。
        现改为"段式芯片路径条"：超过阈值时自动折叠中间层级为 …，
        点击 … 弹出完整层级列表；路径条右侧固定复制/编辑操作，
        编辑态才展开输入框，避免常驻输入框挤占路径展示空间。
      -->
      <div class="picker-toolbar">
        <el-button-group>
          <el-button size="small" :disabled="!parentPath" title="上级目录" @click="goParent">
            <el-icon><ArrowUp /></el-icon>
          </el-button>
          <el-button size="small" title="刷新" @click="refresh">
            <el-icon><Refresh /></el-icon>
          </el-button>
        </el-button-group>

        <div v-if="!pathEditing" class="pathbar-track">
          <span
            class="pathbar-seg is-root"
            :class="{ 'is-current': rootSegment && rootSegment.path === currentPath }"
            :title="currentPath"
            @click="rootSegment && navigate(rootSegment.path)"
          >{{ rootSegment ? rootSegment.name : '...' }}</span>
          <el-popover
            v-if="collapsedCount > 0"
            placement="bottom"
            trigger="click"
            :width="280"
            :show-arrow="false"
          >
            <template #reference>
              <span class="pathbar-seg is-ellipsis" :title="`展开已折叠的 ${collapsedCount} 个层级`">…</span>
            </template>
            <div class="pathbar-pop">
              <div
                v-for="c in collapsedSegments"
                :key="c.path"
                class="pathbar-pop-item"
                :title="c.path"
                @click="navigate(c.path)"
              >
                <el-icon><Folder /></el-icon>
                <span>{{ c.name }}</span>
              </div>
            </div>
          </el-popover>
          <span
            v-for="c in tailSegments"
            :key="c.path"
            class="pathbar-seg"
            :class="{ 'is-current': c.path === currentPath }"
            :title="c.path"
            @click="navigate(c.path)"
          >{{ c.name }}</span>
          <span v-if="collapsedCount > 0" class="pathbar-fold-tip">已折叠 {{ collapsedCount }} 层</span>
        </div>

        <el-input
          v-else
          ref="pathEditRef"
          v-model="pathInput"
          size="small"
          placeholder="粘贴绝对路径后回车跳转，Esc 取消"
          class="picker-path-input"
          @keyup.enter="jumpToInput"
          @keyup.esc="pathEditing = false"
        />

        <div class="pathbar-actions">
          <el-button size="small" :title="pathEditing ? '完成编辑' : '编辑路径'" @click="togglePathEdit">
            <el-icon><Check v-if="pathEditing" /><EditPen v-else /></el-icon>
          </el-button>
          <el-button size="small" title="复制完整路径" @click="copyPath">
            <el-icon><CopyDocument /></el-icon>
          </el-button>
        </div>
      </div>

      <div class="picker-body">
        <!-- 左侧：常用位置 + 目录树 -->
        <div class="picker-side">
          <div class="side-title">
            <span>常用位置</span>
            <el-button link size="small" type="primary" :disabled="!currentPath" @click="addFavorite">
              收藏当前
            </el-button>
          </div>
          <div class="fav-list">
            <div v-for="f in favorites" :key="f.path" class="fav-item" @click="navigate(f.path)">
              <el-icon class="fav-icon"><Star /></el-icon>
              <span class="fav-name" :title="f.path">{{ f.name }}</span>
              <el-icon class="fav-del" @click.stop="removeFavorite(f)"><Close /></el-icon>
            </div>
            <div v-if="!favorites.length" class="side-empty">暂无收藏</div>
          </div>

          <!-- 最近访问：自动记录最近 navigate 过的 5 个目录，减少重复层级点击 -->
          <div class="side-title">最近访问</div>
          <div class="fav-list is-recent">
            <div v-for="r in recents" :key="r.path" class="fav-item" @click="navigate(r.path)">
              <el-icon class="recent-icon"><Clock /></el-icon>
              <span class="fav-name" :title="r.path">{{ r.name }}</span>
              <el-icon class="fav-del" @click.stop="removeRecent(r)"><Close /></el-icon>
            </div>
            <div v-if="!recents.length" class="side-empty">暂无记录</div>
          </div>

          <div class="side-title">目录树</div>
          <div class="tree-box">
            <el-tree
              :props="treeProps"
              :load="loadTreeNode"
              lazy
              highlight-current
              @node-click="onTreeNodeClick"
            />
          </div>
        </div>

        <!-- 右侧：条目列表 -->
        <div class="picker-main">
          <!-- 当前目录统计条：进入目录后立即告知符合要求的文件/文件夹数量 -->
          <div class="main-stats">
            <span class="main-stats-item is-dir" title="当前目录下的子文件夹数量">
              <el-icon><Folder /></el-icon>{{ dirCount }} 个文件夹
            </span>
            <span v-if="mode !== 'dir'" class="main-stats-item is-file" title="当前目录下符合扩展名要求的文件数量">
              <el-icon><Document /></el-icon>{{ fileCount }} 个符合条件的文件
            </span>
            <span v-if="extsLabel.length" class="imp-ext-chips" title="程序可接受的文件扩展名">
              <span class="imp-ext-chip" v-for="e in extsLabel" :key="e">{{ e }}</span>
            </span>
            <span v-if="!loading && dirCount === 0 && fileCount === 0" class="main-stats-hint">
              该目录为空，可返回上级或切换其他位置
            </span>
            <span v-else-if="!loading && mode !== 'dir' && fileCount === 0" class="main-stats-hint">
              当前目录没有符合条件的文件，可进入子文件夹继续浏览
            </span>
          </div>

          <div class="main-toolbar">
            <el-input
              v-model="keyword"
              size="small"
              placeholder="筛选当前目录内的名称"
              clearable
              class="filter-input"
              @input="onKeywordInput"
              @clear="onKeywordInput"
            />
            <el-button
              v-if="mode !== 'dir' && multiple"
              size="small"
              :disabled="!selectableEntries.length"
              @click="selectCurrentPage"
            >
              {{ mode === 'both' ? '全选本页文件' : '全选本页' }}
            </el-button>
            <el-button
              v-if="mode === 'dir' || mode === 'both'"
              size="small"
              type="primary"
              plain
              :disabled="!currentPath"
              @click="selectCurrentDir"
            >
              选择当前目录
            </el-button>
          </div>

          <!--
            UI-08: 引入窗口化（windowing）渲染。
            原实现对 entries 全量 v-for，滚动加载数十页后 DOM 节点破万，
            而本工具的典型场景正是"数十万文件的日志目录"，滚动与筛选会直接卡死浏览器。
            这里改为固定高度的占位容器 + 只渲染可视区域内的行，
            DOM 节点数恒定在 (可视行数 + 缓冲) 以内。
          -->
          <div ref="listRef" class="entry-list" @scroll="onListScroll">
            <div class="virtual-spacer" :style="{ height: totalListHeight + 'px' }">
              <div
                v-for="item in visibleRows"
                :key="item.entry.path"
                class="entry-row"
                :class="{
                  'is-disabled': !item.entry.readable,
                  'is-selected': isSelected(item.entry.path)
                }"
                :style="{ transform: `translateY(${item.offset}px)` }"
                @click="onRowClick(item.entry)"
                @dblclick="onRowDblClick(item.entry)"
              >
                <el-checkbox
                  v-if="canCheck(item.entry)"
                  :model-value="isSelected(item.entry.path)"
                  @click.stop
                  @change="toggleSelect(item.entry)"
                />
                <span v-else class="check-placeholder" />
                <el-icon class="entry-icon" :class="{ 'is-dir': item.entry.is_dir }">
                  <Folder v-if="item.entry.is_dir" /><Document v-else />
                </el-icon>
                <span class="entry-name" :title="item.entry.path">{{ item.entry.name }}</span>
                <span class="entry-size">{{ item.entry.is_dir ? '' : formatSize(item.entry.size) }}</span>
                <span class="entry-time">{{ item.entry.mod_time || '' }}</span>
              </div>
            </div>

            <div v-if="loading" class="list-tip">加载中…</div>
            <div v-else-if="hasMore" class="list-tip">
              向下滚动加载更多（{{ statsText }}，已载入 {{ entries.length }} / {{ total }} 项）
            </div>
            <div v-else-if="entries.length" class="list-tip">
              已全部加载（{{ statsText }}）
            </div>
            <div v-else class="list-tip">该目录为空或没有匹配项</div>
          </div>
        </div>
      </div>

      <!-- 底部：已选清单与统计 -->
      <div class="picker-summary">
        <div class="summary-header">
          <span>已选 <strong>{{ selected.length }}</strong> 项</span>
          <el-button link type="danger" size="small" :disabled="!selected.length" @click="clearSelected">
            清空
          </el-button>
        </div>
        <div class="summary-chips">
          <el-tag
            v-for="p in visibleSelected"
            :key="p"
            closable
            size="small"
            :type="selectedMeta[p] ? (selectedMeta[p].is_dir ? 'warning' : 'primary') : 'info'"
            @close="removeSelected(p)"
          >
            <el-icon class="chip-icon"><Folder v-if="selectedMeta[p] && selectedMeta[p].is_dir" /><Document v-else /></el-icon>
            {{ shortName(p) }}
          </el-tag>
          <span v-if="selected.length > 8" class="more-tip">…等共 {{ selected.length }} 项</span>
          <span v-if="!selected.length" class="more-tip">未选择任何路径</span>
        </div>
      </div>
    </div>

    <template #footer>
      <el-button @click="dialogVisible = false">取消</el-button>
      <el-button
        type="primary"
        :disabled="!selected.length && !(mode === 'dir' && currentPath)"
        @click="confirm"
      >
        确定 ({{ selected.length || (mode === 'dir' && currentPath ? 1 : 0) }})
      </el-button>
    </template>
  </el-dialog>
</template>

<script setup>
import { ref, computed, watch, nextTick, onMounted, onUnmounted } from 'vue'
import { ElMessage } from 'element-plus'
import api from '@/api'
import { useRequest } from '@/composables/useRequest'
import { formatSize as sharedFormatSize } from '@/utils/format'

const props = defineProps({
  visible: { type: Boolean, default: false },
  // 选中的绝对路径数组
  modelValue: { type: Array, default: () => [] },
  // dir: 只能选目录；file: 只能选文件；both: 均可
  mode: { type: String, default: 'dir' },
  multiple: { type: Boolean, default: true },
  // 文件模式下的扩展名白名单，为空表示不过滤
  exts: { type: Array, default: () => [] },
  title: { type: String, default: '' },
  // 收藏与最近访问路径的本地存储命名空间
  favoriteKey: { type: String, default: 'default' }
})

const emit = defineEmits(['update:visible', 'update:modelValue', 'confirm'])

const PAGE_SIZE = 500

const dialogVisible = computed({
  get: () => props.visible,
  set: v => emit('update:visible', v)
})

const currentPath = ref('')
const pathInput = ref('')
const keyword = ref('')
const entries = ref([])
const total = ref(0)
const offset = ref(0)
const loading = ref(false)
const hasMore = ref(false)
// 当前目录的全量统计（分页前）：子文件夹数 / 符合条件的文件数
const dirCount = ref(0)
const fileCount = ref(0)
const selected = ref([])
// 已选路径的类型元数据（path → { is_dir }），用于 chips 与已选清单展示文件/目录图标
const selectedMeta = ref({})
const favorites = ref([])
const recents = ref([])
const roots = ref([])
const listRef = ref(null)
// 智能路径条：编辑态与输入框引用
const pathEditing = ref(false)
const pathEditRef = ref(null)

const treeProps = { label: 'name', children: 'children', isLeaf: 'leaf' }

const favStorageKey = () => `fsx_favorites_${props.favoriteKey}`
const lastPathKey = () => `fsx_last_path_${props.favoriteKey}`
const recentStorageKey = () => `fsx_recent_${props.favoriteKey}`

// ---------- 路径与智能路径条 ----------

const isWindowsPath = p => /^[A-Za-z]:[\\/]/.test(p)

const crumbs = computed(() => {
  const p = currentPath.value
  if (!p) return []
  const parts = []
  if (isWindowsPath(p)) {
    const segs = p.split(/[\\/]/).filter(Boolean)
    let cur = ''
    segs.forEach((s, i) => {
      cur = i === 0 ? s + '\\' : (cur.endsWith('\\') ? cur : cur + '\\') + s
      parts.push({ name: s, path: cur })
    })
  } else {
    const segs = p.split('/').filter(Boolean)
    let cur = ''
    segs.forEach(s => {
      cur = cur + '/' + s
      parts.push({ name: s, path: cur })
    })
  }
  return parts
})

const parentPath = computed(() => {
  const c = crumbs.value
  return c.length > 1 ? c[c.length - 2].path : ''
})

// 路径条最多展示的层级段数（含根段）；超出时中间层级折叠为 …
const MAX_VISIBLE_SEGMENTS = 4

const rootSegment = computed(() => crumbs.value[0] || null)

const collapsedCount = computed(() => Math.max(0, crumbs.value.length - MAX_VISIBLE_SEGMENTS))

const collapsedSegments = computed(() => {
  const list = crumbs.value
  if (collapsedCount.value === 0) return []
  return list.slice(1, 1 + collapsedCount.value)
})

const tailSegments = computed(() => {
  const list = crumbs.value
  if (collapsedCount.value === 0) return list.slice(1)
  return list.slice(1 + collapsedCount.value)
})

const togglePathEdit = () => {
  if (pathEditing.value) {
    jumpToInput()
  } else {
    pathEditing.value = true
    pathInput.value = currentPath.value
    nextTick(() => pathEditRef.value?.focus())
  }
}

const copyPath = async () => {
  const p = currentPath.value
  if (!p) return
  try {
    await navigator.clipboard.writeText(p)
    ElMessage.success('已复制完整路径')
  } catch (e) {
    // 剪贴板 API 在非安全上下文下不可用，回退为选中输入框内容供手动复制
    pathInput.value = p
    pathEditing.value = true
    nextTick(() => {
      pathEditRef.value?.focus()
      pathEditRef.value?.select?.()
    })
    ElMessage.info('浏览器不支持自动复制，已选中路径请按 Ctrl+C')
  }
}

// 扩展名提示 chips（统一补上前导点）
const extsLabel = computed(() =>
  (props.exts || [])
    .map(e => String(e).trim())
    .filter(Boolean)
    .map(e => (e.startsWith('.') ? e : '.' + e))
)

// 当前目录统计文案：目录模式（dirsOnly）不统计文件，避免展示无意义的 0
const statsText = computed(() => {
  if (props.mode === 'dir') return `${dirCount.value} 个文件夹`
  return `${dirCount.value} 个文件夹 · ${fileCount.value} 个符合条件的文件`
})

const shortName = p => {
  if (!p) return ''
  const segs = p.split(/[\\/]/).filter(Boolean)
  return segs.length ? segs[segs.length - 1] : p
}

// ---------- 目录浏览 ----------

/**
 * UI-08: 虚拟滚动参数。
 * ROW_HEIGHT 必须与 CSS 中 .entry-row 的实际行高保持一致，
 * 否则窗口计算会出现偏移/留白。
 */
const ROW_HEIGHT = 34
// 可视区之外额外渲染的缓冲行数，避免快速滚动时出现白屏
const ROW_BUFFER = 6
// 内存内最多保留的条目数（超出后丢弃最早的部分，避免节点数与内存无界增长）
const MAX_KEPT_ENTRIES = 5000

const scrollTop = ref(0)
const viewportHeight = ref(400)

const totalListHeight = computed(() => entries.value.length * ROW_HEIGHT)

/** 当前需要真实渲染的行（窗口化） */
const visibleRows = computed(() => {
  const list = entries.value
  if (!list.length) return []
  const start = Math.max(0, Math.floor(scrollTop.value / ROW_HEIGHT) - ROW_BUFFER)
  const visibleCount = Math.ceil(viewportHeight.value / ROW_HEIGHT) + ROW_BUFFER * 2
  const end = Math.min(list.length, start + visibleCount)
  const rows = []
  for (let i = start; i < end; i++) {
    rows.push({ entry: list[i], offset: i * ROW_HEIGHT })
  }
  return rows
})

const syncViewportHeight = () => {
  if (listRef.value) {
    viewportHeight.value = listRef.value.clientHeight || viewportHeight.value
  }
}

/**
 * UI-07: 目录浏览的请求竞态治理。
 *
 * 原实现用 `if (loading) return` 拦截：上一个目录请求未回时再跳转新目录，
 * 请求会被**直接丢弃**，界面显示新路径却保留着旧目录的内容。
 *
 * 改为统一复用 useRequest 的"AbortController 取消旧请求 + 请求序号守卫"：
 *   - 切换目录时取消在途请求，新请求立即发出；
 *   - 只有最新一次请求的结果才被写入，杜绝旧响应覆盖新结果。
 */
// 失败提示由 api 拦截器统一弹出，这里不再重复 toast
const { run: runBrowse } = useRequest(api.fsBrowse)

const loadDir = async (reset = true) => {
  if (!currentPath.value) return

  if (reset) {
    offset.value = 0
    entries.value = []
    // 重置虚拟窗口的滚动位置
    scrollTop.value = 0
    if (listRef.value) listRef.value.scrollTop = 0
  }

  loading.value = true
  try {
    const res = await runBrowse({
      path: currentPath.value,
      exts: props.exts,
      keyword: keyword.value,
      dirsOnly: props.mode === 'dir',
      offset: offset.value,
      limit: PAGE_SIZE
    })
    // 被 newer 请求取消时返回 undefined，直接丢弃
    if (!res) return

    const d = res.data || {}
    total.value = d.total || 0
    dirCount.value = d.dir_count || 0
    fileCount.value = d.file_count || 0
    hasMore.value = !!d.truncated
    entries.value = reset ? (d.entries || []) : entries.value.concat(d.entries || [])
    // 回填已选路径的类型元数据，使已选 chips 能正确展示文件/目录图标
    backfillSelectedMeta(d.entries || [])
    // UI-08: 限制内存内保留的条目数，避免"滚动数十页后 DOM 节点破万"导致页面卡死
    if (entries.value.length > MAX_KEPT_ENTRIES) {
      entries.value = entries.value.slice(entries.value.length - MAX_KEPT_ENTRIES)
    }
    offset.value = (d.offset || 0) + (d.entries || []).length
    currentPath.value = d.path || currentPath.value
    pathInput.value = currentPath.value
    if (reset && localStorage) {
      try { localStorage.setItem(lastPathKey(), currentPath.value) } catch (e) {}
      // 记录最近访问，便于快速回到高频目录
      pushRecent(currentPath.value)
    }
  } finally {
    loading.value = false
  }
}

// 将目录页中出现的已选路径回填类型元数据
const backfillSelectedMeta = list => {
  const meta = { ...selectedMeta.value }
  let changed = false
  for (const e of list) {
    if (selected.value.includes(e.path) && !meta[e.path]) {
      meta[e.path] = { is_dir: !!e.is_dir }
      changed = true
    }
  }
  if (changed) selectedMeta.value = meta
}

const loadMore = () => {
  if (loading.value || !hasMore.value) return
  loadDir(false)
}

// UI-08: 滚动事件节流，避免大目录滚动时每秒触发上百次回调
let scrollRaf = null
const onListScroll = e => {
  if (scrollRaf) return
  scrollRaf = requestAnimationFrame(() => {
    scrollRaf = null
    const el = listRef.value
    if (!el) return
    scrollTop.value = el.scrollTop
    if (el.scrollTop + el.clientHeight >= el.scrollHeight - 200) {
      loadMore()
    }
  })
}

const navigate = async path => {
  if (!path) return
  keyword.value = ''
  currentPath.value = path
  await loadDir(true)
  if (listRef.value) listRef.value.scrollTop = 0
}

const goParent = () => { if (parentPath.value) navigate(parentPath.value) }
const refresh = () => loadDir(true)

const jumpToInput = async () => {
  const p = (pathInput.value || '').trim()
  if (!p) return
  pathEditing.value = false
  currentPath.value = p
  keyword.value = ''
  await loadDir(true)
  if (listRef.value) listRef.value.scrollTop = 0
}

// UI-11: keywordTimer 原先从未被清理，组件销毁后仍可能触发一次请求。
// 这里统一在 onUnmounted 中清理，并把变量名改为 const（避免误用 var 语义）。
let keywordTimer = null
const onKeywordInput = () => {
  if (keywordTimer) clearTimeout(keywordTimer)
  keywordTimer = setTimeout(() => loadDir(true), 300)
}

const initRoots = async () => {
  if (roots.value.length) return
  try {
    const res = await api.fsRoots()
    const d = res.data || {}
    const list = []
    ;(d.shortcuts || []).forEach(s => list.push(s))
    ;(d.roots || []).forEach(r => list.push(r))
    roots.value = list
  } catch (e) {
    roots.value = []
  }
}

const handleOpen = async () => {
  selected.value = [...props.modelValue]
  selectedMeta.value = {}
  keyword.value = ''
  pathEditing.value = false
  loadFavorites()
  loadRecents()
  // UI-08: 弹窗打开后测量可视区高度，供窗口化渲染使用
  nextTick(syncViewportHeight)

  const last = localStorage ? localStorage.getItem(lastPathKey()) : ''
  if (last) {
    currentPath.value = last
    await loadDir(true)
    if (entries.value.length || total.value > 0) {
      return
    }
  }
  await initRoots()
  if (roots.value.length) {
    await navigate(roots.value[0].path)
  }
}

// ---------- 目录树 ----------

const loadTreeNode = async (node, resolve) => {
  if (node.level === 0) {
    await initRoots()
    resolve(roots.value.map(r => ({ name: r.name, path: r.path, leaf: false })))
    return
  }
  try {
    const res = await api.fsBrowse({ path: node.data.path, dirsOnly: true, limit: PAGE_SIZE })
    // UI-11: 原实现把所有节点都设为 leaf: false，
    // 于是任何文件也会显示成"可展开目录"，点开后得到空列表。
    // 这里依据服务端返回的 is_dir 正确设置 leaf。
    resolve(
      (res.data?.entries || []).map(e => ({
        name: e.name,
        path: e.path,
        leaf: !e.is_dir
      }))
    )
  } catch (e) {
    resolve([])
  }
}

const onTreeNodeClick = data => { if (data?.path) navigate(data.path) }

// ---------- 收藏 ----------

const loadFavorites = () => {
  try {
    favorites.value = JSON.parse(localStorage.getItem(favStorageKey()) || '[]')
  } catch (e) {
    favorites.value = []
  }
}
const addFavorite = () => {
  if (!currentPath.value) return
  if (favorites.value.some(f => f.path === currentPath.value)) {
    ElMessage.info('该位置已在收藏中')
    return
  }
  favorites.value.push({ name: shortName(currentPath.value), path: currentPath.value })
  try { localStorage.setItem(favStorageKey(), JSON.stringify(favorites.value)) } catch (e) {}
}

const removeFavorite = f => {
  favorites.value = favorites.value.filter(x => x.path !== f.path)
  try { localStorage.setItem(favStorageKey(), JSON.stringify(favorites.value)) } catch (e) {}
}

// ---------- 最近访问 ----------

const MAX_RECENTS = 5

const loadRecents = () => {
  try {
    recents.value = JSON.parse(localStorage.getItem(recentStorageKey()) || '[]')
  } catch (e) {
    recents.value = []
  }
}

const pushRecent = path => {
  if (!path || !localStorage) return
  const next = [
    { name: shortName(path), path },
    ...recents.value.filter(r => r.path !== path)
  ].slice(0, MAX_RECENTS)
  recents.value = next
  try { localStorage.setItem(recentStorageKey(), JSON.stringify(next)) } catch (e) {}
}

const removeRecent = r => {
  recents.value = recents.value.filter(x => x.path !== r.path)
  try { localStorage.setItem(recentStorageKey(), JSON.stringify(recents.value)) } catch (e) {}
}

// ---------- 选择 ----------

const canCheck = e => {
  if (!e.readable) return false
  if (props.mode === 'dir') return !!e.is_dir
  if (props.mode === 'file') return !e.is_dir
  return true
}

const selectableEntries = computed(() => entries.value.filter(canCheck))

const isSelected = p => selected.value.includes(p)

const recordMeta = (path, isDir) => {
  if (selectedMeta.value[path]) return
  selectedMeta.value = { ...selectedMeta.value, [path]: { is_dir: !!isDir } }
}

const toggleSelect = e => {
  if (props.multiple) {
    if (isSelected(e.path)) {
      selected.value = selected.value.filter(x => x !== e.path)
    } else {
      selected.value = selected.value.concat(e.path)
      recordMeta(e.path, e.is_dir)
    }
  } else {
    selected.value = isSelected(e.path) ? [] : [e.path]
    if (selected.value.length) recordMeta(e.path, e.is_dir)
  }
}

const onRowClick = e => {
  if (canCheck(e)) toggleSelect(e)
}

const onRowDblClick = e => {
  if (e.is_dir && e.readable) navigate(e.path)
}

const selectCurrentPage = () => {
  // both 模式下只全选本页文件：目录的选择交给"选择当前目录"，
  // 避免同时勾选目录与其子文件造成重复收集。
  const pool = props.mode === 'both'
    ? entries.value.filter(e => !e.is_dir && canCheck(e))
    : selectableEntries.value
  const targets = pool.map(e => e.path)
  const merged = new Set(selected.value.concat(targets))
  selected.value = Array.from(merged)
  pool.forEach(e => recordMeta(e.path, e.is_dir))
}

const removeSelected = p => {
  selected.value = selected.value.filter(x => x !== p)
  if (selectedMeta.value[p]) {
    const meta = { ...selectedMeta.value }
    delete meta[p]
    selectedMeta.value = meta
  }
}

const clearSelected = () => {
  selected.value = []
  selectedMeta.value = {}
}

const visibleSelected = computed(() => selected.value.slice(0, 8))

const selectCurrentDir = () => {
  if (!currentPath.value) return
  if (!selected.value.includes(currentPath.value)) {
    if (props.multiple) {
      selected.value = selected.value.concat(currentPath.value)
    } else {
      selected.value = [currentPath.value]
    }
    recordMeta(currentPath.value, true)
  }
}

// ---------- 提交 ----------

const confirm = () => {
  let picked = [...selected.value]
  // 仅目录模式允许"未勾选但确认当前目录"；both 模式必须显式勾选，
  // 避免用户只是浏览到某目录就被动导入了整个目录。
  if (picked.length === 0 && props.mode === 'dir' && currentPath.value) {
    picked = [currentPath.value]
  }
  emit('update:modelValue', picked)
  emit('confirm', picked)
  dialogVisible.value = false
}

// WEB-16: 复用统一实现。目录项传空值占位（目录不展示大小），文件走标准单位换算。
const formatSize = bytes => sharedFormatSize(bytes, '')

// UI-11: 组件销毁时统一清理定时器、在途请求与 RAF 回调，
// 避免"组件已销毁却仍在发起请求 / 触发回调"的泄漏。
onUnmounted(() => {
  if (keywordTimer) {
    clearTimeout(keywordTimer)
    keywordTimer = null
  }
  // 在途的 fsBrowse 请求由 useRequest 的 onScopeDispose 统一取消
  if (scrollRaf) {
    cancelAnimationFrame(scrollRaf)
    scrollRaf = null
  }
  window.removeEventListener('resize', syncViewportHeight)
})

// UI-08: 窗口尺寸变化时重新测量可视区高度
onMounted(() => {
  window.addEventListener('resize', syncViewportHeight)
})

watch(() => props.visible, v => {
  if (v) {
    loadFavorites()
    loadRecents()
    nextTick(syncViewportHeight)
  }
})
</script>

<style scoped>
.path-picker {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.picker-toolbar {
  display: flex;
  align-items: center;
  gap: 10px;
}

.picker-path-input {
  flex: 1;
  min-width: 0;
}

/* 智能路径条：段式芯片 + 中间折叠 + 右侧操作 */
.pathbar-track {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 2px;
  height: 32px;
  padding: 0 8px;
  background: #fff;
  border: 1px solid #e2e8f0;
  border-radius: 6px;
  overflow: hidden;
  white-space: nowrap;
}

.pathbar-seg {
  display: inline-flex;
  align-items: center;
  max-width: 180px;
  min-width: 0;
  padding: 2px 6px;
  border-radius: 4px;
  font-size: 12px;
  color: #334155;
  cursor: pointer;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  flex-shrink: 1;
}

.pathbar-seg.is-root,
.pathbar-seg.is-ellipsis {
  flex-shrink: 0;
}

.pathbar-seg:hover {
  background: #f1f5f9;
  color: #0284c7;
}

.pathbar-seg.is-root {
  font-weight: 600;
  color: #0f172a;
}

.pathbar-seg.is-current {
  background: #e0f2fe;
  color: #0369a1;
  font-weight: 600;
}

.pathbar-seg.is-ellipsis {
  color: #64748b;
  letter-spacing: 1px;
  padding: 2px 8px;
}

.pathbar-fold-tip {
  margin-left: auto;
  font-size: 11px;
  color: #94a3b8;
  flex-shrink: 0;
  padding-left: 8px;
}

.pathbar-actions {
  display: flex;
  gap: 4px;
  flex-shrink: 0;
}

.pathbar-pop {
  display: flex;
  flex-direction: column;
  max-height: 260px;
  overflow-y: auto;
}

.pathbar-pop-item {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 8px;
  font-size: 12px;
  color: #334155;
  border-radius: 4px;
  cursor: pointer;
}

.pathbar-pop-item:hover {
  background: #f1f5f9;
  color: #0284c7;
}

.pathbar-pop-item span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.picker-body {
  display: flex;
  gap: 12px;
  height: 420px;
}

.picker-side {
  width: 240px;
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  overflow: hidden;
}

.side-title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 6px 10px;
  background: #f8fafc;
  border-bottom: 1px solid #e2e8f0;
  font-size: 12px;
  font-weight: 600;
  color: #475569;
}

.fav-list {
  max-height: 108px;
  overflow-y: auto;
  padding: 4px 0;
}

.fav-list.is-recent {
  border-top: 1px solid #e2e8f0;
}

.recent-icon {
  color: #0284c7;
  font-size: 13px;
}

.fav-item {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 4px 10px;
  cursor: pointer;
  font-size: 12px;
  color: #334155;
}

.fav-item:hover {
  background: #f1f5f9;
}

.fav-icon {
  color: #f59e0b;
  font-size: 13px;
}

.fav-name {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.fav-del {
  color: #94a3b8;
}

.fav-del:hover {
  color: #ef4444;
}

.side-empty {
  padding: 10px;
  font-size: 12px;
  color: #94a3b8;
}

.tree-box {
  flex: 1;
  overflow: auto;
  padding: 4px 0;
  border-top: 1px solid #e2e8f0;
}

.picker-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.main-toolbar {
  display: flex;
  gap: 8px;
}

.filter-input {
  flex: 1;
}

/* 当前目录统计条 */
.main-stats {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
  padding: 6px 10px;
  background: #fff;
  border: 1px solid #e2e8f0;
  border-radius: 6px;
  font-size: 12px;
  color: #475569;
}

.main-stats-item {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-weight: 600;
  white-space: nowrap;
}

.main-stats-item.is-dir {
  color: #92400e;
}

.main-stats-item.is-file {
  color: #0369a1;
}

.main-stats-hint {
  color: #94a3b8;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.chip-icon {
  margin-right: 2px;
  vertical-align: -0.15em;
}

.entry-list {
  flex: 1;
  overflow-y: auto;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
}

/*
 * UI-08: 虚拟滚动（windowing）所需样式。
 * .virtual-spacer 撑出整份列表的滚动高度，.entry-row 通过 translateY 定位到各自的位置。
 * 行高必须固定为 ROW_HEIGHT（34px）：padding 5px + 字号行高 + 边框，
 * 否则窗口计算与实际渲染会错位。
 */
.virtual-spacer {
  position: relative;
  width: 100%;
}

.entry-row {
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0 10px;
  height: 34px;
  line-height: 34px;
  font-size: 13px;
  cursor: pointer;
  user-select: none;
  box-sizing: border-box;
}

.entry-row:hover {
  background: #f1f5f9;
}

.entry-row.is-selected {
  background: #e0f2fe;
}

.entry-row.is-disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

.check-placeholder {
  width: 14px;
  flex-shrink: 0;
}

.entry-icon {
  color: #94a3b8;
  flex-shrink: 0;
}

.entry-icon.is-dir {
  color: #f59e0b;
}

.entry-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: #1e293b;
}

.entry-size {
  width: 90px;
  text-align: right;
  color: #64748b;
  font-size: 12px;
  flex-shrink: 0;
}

.entry-time {
  width: 130px;
  text-align: right;
  color: #94a3b8;
  font-size: 12px;
  flex-shrink: 0;
}

.list-tip {
  padding: 10px;
  text-align: center;
  font-size: 12px;
  color: #94a3b8;
}

.picker-summary {
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  padding: 8px 10px;
  background: #f8fafc;
}

.summary-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: 13px;
  color: #334155;
}

.summary-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 6px;
  max-height: 76px;
  overflow-y: auto;
}

.more-tip {
  font-size: 12px;
  color: #94a3b8;
  align-self: center;
}
</style>

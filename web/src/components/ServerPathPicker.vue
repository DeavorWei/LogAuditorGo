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
      <!-- 顶部工具栏：导航 + 路径输入 -->
      <div class="picker-toolbar">
        <el-button-group>
          <el-button size="small" :disabled="!parentPath" title="上级目录" @click="goParent">
            <el-icon><ArrowUp /></el-icon>
          </el-button>
          <el-button size="small" title="刷新" @click="refresh">
            <el-icon><Refresh /></el-icon>
          </el-button>
        </el-button-group>

        <el-breadcrumb separator="/" class="picker-crumbs">
          <el-breadcrumb-item v-for="c in crumbs" :key="c.path">
            <a href="javascript:void(0)" @click="navigate(c.path)">{{ c.name }}</a>
          </el-breadcrumb-item>
        </el-breadcrumb>

        <el-input
          v-model="pathInput"
          size="small"
          placeholder="粘贴绝对路径后回车跳转"
          class="picker-path-input"
          clearable
          @keyup.enter="jumpToInput"
        >
          <template #append>
            <el-button size="small" @click="jumpToInput">跳转</el-button>
          </template>
        </el-input>
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
              全选本页
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
              向下滚动加载更多（共 {{ total }} 项，已载入 {{ entries.length }} 项）
            </div>
            <div v-else-if="entries.length" class="list-tip">已全部加载（共 {{ total }} 项）</div>
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
            @close="removeSelected(p)"
          >
            {{ shortName(p) }}
          </el-tag>
          <span v-if="selected.length > 8" class="more-tip">…等共 {{ selected.length }} 项</span>
          <span v-if="!selected.length" class="more-tip">未选择任何路径</span>
        </div>
      </div>
    </div>

    <template #footer>
      <el-button @click="dialogVisible = false">取消</el-button>
      <el-button type="primary" :disabled="!selected.length" @click="confirm">
        确定 ({{ selected.length }})
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
const selected = ref([])
const favorites = ref([])
const roots = ref([])
const listRef = ref(null)

const treeProps = { label: 'name', children: 'children', isLeaf: 'leaf' }

const favStorageKey = () => `fsx_favorites_${props.favoriteKey}`
const lastPathKey = () => `fsx_last_path_${props.favoriteKey}`

// ---------- 路径与面包屑 ----------

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
    hasMore.value = !!d.truncated
    entries.value = reset ? (d.entries || []) : entries.value.concat(d.entries || [])
    // UI-08: 限制内存内保留的条目数，避免"滚动数十页后 DOM 节点破万"导致页面卡死
    if (entries.value.length > MAX_KEPT_ENTRIES) {
      entries.value = entries.value.slice(entries.value.length - MAX_KEPT_ENTRIES)
    }
    offset.value = (d.offset || 0) + (d.entries || []).length
    currentPath.value = d.path || currentPath.value
    pathInput.value = currentPath.value
    if (reset && localStorage) {
      try { localStorage.setItem(lastPathKey(), currentPath.value) } catch (e) {}
    }
  } finally {
    loading.value = false
  }
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
  keyword.value = ''
  loadFavorites()
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

// ---------- 选择 ----------

const canCheck = e => {
  if (!e.readable) return false
  if (props.mode === 'dir') return !!e.is_dir
  if (props.mode === 'file') return !e.is_dir
  return true
}

const selectableEntries = computed(() => entries.value.filter(canCheck))

const isSelected = p => selected.value.includes(p)

const toggleSelect = e => {
  if (props.multiple) {
    selected.value = isSelected(e.path)
      ? selected.value.filter(x => x !== e.path)
      : selected.value.concat(e.path)
  } else {
    selected.value = isSelected(e.path) ? [] : [e.path]
  }
}

const onRowClick = e => {
  if (canCheck(e)) toggleSelect(e)
}

const onRowDblClick = e => {
  if (e.is_dir && e.readable) navigate(e.path)
}

const selectCurrentPage = () => {
  const targets = selectableEntries.value.map(e => e.path)
  const merged = new Set(selected.value.concat(targets))
  selected.value = Array.from(merged)
}

const removeSelected = p => {
  selected.value = selected.value.filter(x => x !== p)
}

const clearSelected = () => {
  selected.value = []
}

const visibleSelected = computed(() => selected.value.slice(0, 8))

// ---------- 提交 ----------

const confirm = () => {
  const picked = [...selected.value]
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
  width: 320px;
  margin-left: auto;
}

.picker-crumbs {
  flex: 1;
  min-width: 0;
  overflow-x: auto;
  white-space: nowrap;
  font-size: 13px;
  padding: 2px 0;
}

.picker-crumbs a {
  color: #0284c7;
  text-decoration: none;
}

.picker-crumbs a:hover {
  text-decoration: underline;
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
  max-height: 150px;
  overflow-y: auto;
  padding: 4px 0;
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

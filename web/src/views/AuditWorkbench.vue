<template>
  <div class="workbench-container">
    <!-- 顶部工作台状态栏 -->
    <div class="workbench-header">
      <div class="header-left">
        <el-select
          v-model="currentTaskId"
          placeholder="选择审计任务"
          style="width: 280px;"
          @change="handleTaskChange"
        >
          <el-option
            v-for="t in taskList"
            :key="t.task_id"
            :label="`${t.task_name} (${t.task_id.substring(0, 8)})`"
            :value="t.task_id"
          />
        </el-select>
        <template v-if="currentTask">
          <span class="task-badge">设备: {{ currentTask.device_type }}</span>
          <el-tag v-if="currentTask.status === 'PENDING'" type="warning" size="small" effect="dark">
            待导入日志
          </el-tag>
          <span v-else class="task-badge">
            匹配率: {{ ((currentTask.matched_count / (currentTask.log_count || 1)) * 100).toFixed(1) }}%
          </span>
          <el-button
            size="small"
            type="info"
            plain
            icon="Files"
            style="margin-left: 4px;"
            @click="showFilesDrawer = true"
          >
            已导入 {{ taskFiles.length }} 个文件 ({{ currentTask.log_count }} 行)
          </el-button>
        </template>
      </div>
      <div class="header-right">
        <el-button-group>
          <el-button icon="Refresh" :disabled="!currentTaskId" @click="fetchLogs">刷新日志</el-button>
          <el-button type="primary" icon="Upload" :disabled="!currentTaskId" @click="openImportDialog">
            {{ currentTask?.status === 'PENDING' || currentTask?.log_count === 0 ? '导入日志' : '补充导入' }}
          </el-button>
          <el-button type="success" icon="Download" :disabled="!currentTaskId || currentTask?.status === 'PENDING'" @click="handleExportHTML">
            导出报告
          </el-button>
          <el-button type="primary" plain icon="Plus" @click="openNewTaskDialog">新建任务</el-button>
        </el-button-group>
      </div>
    </div>

    <!-- 空任务（PENDING 状态）引导卡片 -->
    <div v-if="currentTask && (currentTask.status === 'PENDING' || (totalLogs === 0 && !loadingLogs))" class="empty-task-guide">
      <el-card shadow="never" class="guide-card">
        <div class="guide-header">
          <el-icon size="48" color="#0284c7"><FolderOpened /></el-icon>
          <h3>任务「{{ currentTask.task_name }}」尚未导入日志数据</h3>
          <p>请选择以下方式之一，直接将本地日志导入到本任务中开始智能审计与 RCA 根因分析：</p>
        </div>

        <div class="guide-actions">
          <div class="action-tile" @click="triggerGuideFilesSelect">
            <el-icon size="32" color="#0284c7"><Files /></el-icon>
            <h4>多选日志文件上传</h4>
            <p>支持多选 .log, .txt, .syslog 等多个日志文件同时上传</p>
            <el-button type="primary" size="small">选择多个文件</el-button>
          </div>

          <div class="action-tile" @click="triggerGuideDirSelect">
            <el-icon size="32" color="#16a34a"><FolderAdd /></el-icon>
            <h4>选择本地日志文件夹</h4>
            <p>浏览器直接选择文件夹，自动检索并批量上传全部日志文件</p>
            <el-button type="success" size="small">选择日志目录</el-button>
          </div>

          <div class="action-tile" @click="openImportTextTab">
            <el-icon size="32" color="#ea580c"><DocumentCopy /></el-icon>
            <h4>粘贴 Syslog 日志文本</h4>
            <p>直接在网页中粘贴 syslog 报文文本开始分析</p>
            <el-button type="warning" size="small">粘贴日志文本</el-button>
          </div>
        </div>
      </el-card>
    </div>

    <!-- 核心三栏交互工作台 (任务就绪时展示) -->
    <div v-else class="workbench-body">
      <!-- 左栏：日志流与动态筛选过滤 (28%) -->
      <div class="col-left">
        <div class="filter-panel">
          <div class="filter-row">
            <el-input
              v-model="filter.keyword"
              placeholder="搜索报文/简名..."
              prefix-icon="Search"
              clearable
              size="small"
              @change="onFilterChange"
            />
          </div>
          <div class="filter-row">
            <el-select v-model="filter.severity" placeholder="级别过滤" clearable size="small" style="width: 48%;" @change="onFilterChange">
              <el-option label="全部级别" :value="null" />
              <el-option label="<=2 (紧急/告警)" :value="2" />
              <el-option label="<=4 (错误及以上)" :value="4" />
              <el-option label="<=6 (通知及以上)" :value="6" />
            </el-select>
            <el-select v-model="filter.matched" placeholder="匹配状态" clearable size="small" style="width: 48%;" @change="onFilterChange">
              <el-option label="全部状态" :value="null" />
              <el-option label="已匹配知识库" :value="true" />
              <el-option label="未匹配" :value="false" />
            </el-select>
          </div>
          <div v-if="taskFiles.length > 1" class="filter-row">
            <el-select v-model="filter.sourceFile" placeholder="按来源文件筛选" clearable size="small" style="width: 100%;" @change="onFilterChange">
              <el-option label="全部文件来源" value="" />
              <el-option
                v-for="f in taskFiles"
                :key="f.id"
                :label="`${f.file_name} (${f.line_count}行)`"
                :value="f.file_name"
              />
            </el-select>
          </div>
        </div>

        <div class="log-stream-list" v-loading="loadingLogs">
          <div
            v-for="rec in logRecords"
            :key="rec.id"
            :class="['log-card', { active: selectedLog && selectedLog.id === rec.id }]"
            @click="selectLog(rec)"
          >
            <div class="log-card-header">
              <span :class="['sev-tag', getSevClass(rec.severity)]">Lv.{{ rec.severity }}</span>
              <span class="log-mod">{{ rec.module }}/{{ rec.brief }}</span>
              <span v-if="rec.knowledge_id > 0" class="match-tag">{{ rec.match_tier }}</span>
            </div>
            <div class="log-card-msg">{{ rec.raw_log }}</div>
            <div class="log-card-footer">
              <span>{{ formatTime(rec.timestamp) }}</span>
              <span v-if="rec.source_file" class="file-tag" :title="`来源文件: ${rec.source_file}`">
                📄 {{ rec.source_file }}
              </span>
              <span v-if="rec.slot_info" class="slot-tag">{{ rec.slot_info }}</span>
            </div>
          </div>
          <el-empty v-if="!loadingLogs && logRecords.length === 0" description="暂无匹配日志" />
        </div>

        <div class="pagination-bar">
          <el-pagination
            v-model:current-page="filter.page"
            :page-size="filter.pageSize"
            :total="totalLogs"
            layout="prev, pager, next"
            small
            @current-change="fetchLogs"
          />
        </div>
      </div>

      <!-- 中栏：结构化报文与动态参数解析 (36%) -->
      <div class="col-middle">
        <div v-if="selectedLog" class="detail-container">
          <div class="panel-title">
            <span>📄 日志报文结构化解析 (#{{ selectedLog.id }})</span>
            <el-tag size="small" :type="selectedLog.knowledge_id ? 'success' : 'info'">
              {{ selectedLog.knowledge_id ? `知识库已匹配 (${(selectedLog.match_confidence * 100).toFixed(0)}%)` : '未匹配' }}
            </el-tag>
          </div>

          <!-- 原始日志 -->
          <div class="section-box">
            <div class="box-title">原始 Syslog 报文</div>
            <div class="raw-code">{{ selectedLog.raw_log }}</div>
          </div>

          <!-- 结构化字段表格 -->
          <div class="section-box">
            <div class="box-title">核心结构化字段</div>
            <el-descriptions :column="2" border size="small">
              <el-descriptions-item label="设备主机名">{{ selectedLog.hostname || '-' }}</el-descriptions-item>
              <el-descriptions-item label="时间戳">{{ formatTime(selectedLog.timestamp) }}</el-descriptions-item>
              <el-descriptions-item label="所属模块">{{ selectedLog.module }}</el-descriptions-item>
              <el-descriptions-item label="事件简名">{{ selectedLog.brief }}</el-descriptions-item>
              <el-descriptions-item label="日志级别">
                <span :class="['sev-tag', getSevClass(selectedLog.severity)]">Level {{ selectedLog.severity }}</span>
              </el-descriptions-item>
              <el-descriptions-item label="来源文件">{{ selectedLog.source_file || '-' }}</el-descriptions-item>
              <el-descriptions-item label="槽位/序列号">{{ selectedLog.slot_info || '-' }}</el-descriptions-item>
            </el-descriptions>
          </div>

          <!-- 动态提取参数 -->
          <div class="section-box">
            <div class="box-title">动态提取变量参数 (Parameters)</div>
            <div v-if="parsedParameters && Object.keys(parsedParameters).length > 0" class="param-grid">
              <div v-for="(val, key) in parsedParameters" :key="key" class="param-chip">
                <span class="p-key">{{ key }}</span>
                <span class="p-val">{{ val }}</span>
              </div>
            </div>
            <div v-else class="empty-hint">该日志未解析出结构化动态键值变量</div>
          </div>

          <!-- 关联根因提示 -->
          <div v-if="matchedRCA" class="rca-alert">
            <div class="rca-alert-title">🚨 关联根因事件预警</div>
            <div>{{ matchedRCA.root_cause_summary }}</div>
          </div>
        </div>
        <div v-else class="empty-state">
          <el-empty description="请从左侧日志列表中选择一条日志查看结构化解析" />
        </div>
      </div>

      <!-- 右栏：华为官方知识库排查指导与 RCA 拓扑 (36%) -->
      <div class="col-right">
        <div v-if="selectedLog" class="knowledge-container">
          <el-tabs v-model="activeTab" class="custom-tabs">
            <el-tab-pane label="官方知识与处理步骤" name="knowledge">
              <div v-if="selectedLog.knowledge" class="kb-content">
                <div class="kb-header-card">
                  <div class="kb-title">{{ selectedLog.knowledge.module }}/{{ selectedLog.knowledge.brief }}</div>
                  <div class="kb-meta">
                    <span class="badge-tier">匹配层级: {{ selectedLog.match_tier }}</span>
                    <span class="badge-conf">置信度: {{ (selectedLog.match_confidence * 100).toFixed(0) }}%</span>
                  </div>
                </div>

                <!-- 含义 -->
                <div class="kb-block">
                  <div class="kb-subtitle">📖 日志/告警含义解释</div>
                  <div class="kb-text">{{ selectedLog.knowledge.description || selectedLog.knowledge.message }}</div>
                </div>

                <!-- 官方可能原因 -->
                <div class="kb-block">
                  <div class="kb-subtitle">🔍 官方可能原因</div>
                  <div class="kb-text cause-text">{{ selectedLog.knowledge.cause || '官方文档未提供特定原因' }}</div>
                </div>

                <!-- 官方建议处理步骤 -->
                <div class="kb-block">
                  <div class="kb-subtitle">🛠️ 官方处理排错步骤</div>
                  <div class="kb-text action-box">{{ selectedLog.knowledge.action || '按标准网络排错规范处理' }}</div>
                </div>

                <!-- 系统影响 -->
                <div v-if="selectedLog.knowledge.impact" class="kb-block">
                  <div class="kb-subtitle">⚠️ 对系统的影响</div>
                  <div class="kb-text">{{ selectedLog.knowledge.impact }}</div>
                </div>
              </div>
              <div v-else class="empty-kb">
                <el-empty description="该日志未命中官方知识库，可在知识库中心使用语义搜索进行检索" />
              </div>
            </el-tab-pane>

            <el-tab-pane label="根因传播拓扑 (RCA)" name="rca">
              <div v-if="matchedRCA" class="rca-tab-content">
                <RcaGraph :rcaEvent="matchedRCA" />
                <div class="rca-guide">
                  <div class="guide-title">💡 根因处置指南</div>
                  <div>{{ matchedRCA.recommended_action }}</div>
                </div>
              </div>
              <div v-else class="empty-kb">
                <el-empty description="当前选中日志未触发协议级连环故障传播链路" />
              </div>
            </el-tab-pane>
          </el-tabs>
        </div>
        <div v-else class="empty-state">
          <el-empty description="请选择日志查看官方故障知识库与排查建议" />
        </div>
      </div>
    </div>

    <!-- 已导入日志文件抽屉 -->
    <el-drawer v-model="showFilesDrawer" title="已导入日志文件清单" size="480px">
      <div class="files-drawer-content">
        <div class="drawer-summary">
          <span>共 <strong>{{ taskFiles.length }}</strong> 个日志文件</span>
          <el-button type="primary" size="small" icon="Upload" @click="openImportDialog">补充导入日志</el-button>
        </div>

        <el-table :data="taskFiles" border size="small" style="width: 100%;">
          <el-table-column prop="file_name" label="文件名" min-width="160" show-overflow-tooltip>
            <template #default="{ row }">
              <span style="font-weight: 600; color: #0284c7;">📄 {{ row.file_name }}</span>
            </template>
          </el-table-column>
          <el-table-column prop="line_count" label="日志行数" width="90" align="center" />
          <el-table-column prop="file_size" label="大小" width="80" align="center">
            <template #default="{ row }">{{ formatSize(row.file_size) }}</template>
          </el-table-column>
          <el-table-column prop="created_at" label="导入时间" width="130">
            <template #default="{ row }">{{ formatTime(row.created_at) }}</template>
          </el-table-column>
        </el-table>
      </div>
    </el-drawer>

    <!-- 新建任务弹窗 -->
    <el-dialog v-model="showNewTaskDialog" title="新建日志审计任务" width="620px">
      <el-form label-position="top">
        <el-form-item label="任务名称">
          <el-input v-model="newTaskForm.taskName" placeholder="例如: Core-SW-01排查-20260415" />
        </el-form-item>
        <el-form-item label="设备类型">
          <el-select v-model="newTaskForm.deviceType" style="width: 100%;">
            <el-option label="CloudEngine 数据中心交换机" value="CloudEngine" />
            <el-option label="HiSecEngine 防火墙 (USG)" value="HiSecEngine-USG" />
            <el-option label="通用华为 VRP 设备" value="Huawei-VRP" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showNewTaskDialog = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="handleCreateEmptyTask">仅创建任务 (稍后导入)</el-button>
        <el-button type="success" :loading="submitting" @click="handleCreateAndOpenImport">创建并立即导入日志</el-button>
      </template>
    </el-dialog>

    <!-- 导入 / 补充导入日志弹窗 -->
    <el-dialog v-model="showImportDialog" :title="`导入日志 - ${currentTask?.task_name || ''}`" width="620px">
      <el-tabs v-model="importTab" type="border-card">
        <!-- 标签页 1: 多选日志文件 -->
        <el-tab-pane label="多选日志文件" name="files">
          <div class="upload-dropzone" @dragover.prevent @drop.prevent="handleDropFiles">
            <el-icon size="40" color="#94a3b8"><UploadFilled /></el-icon>
            <div class="dropzone-text">将日志文件拖到此处，或</div>
            <el-button type="primary" size="small" @click="triggerFilesInput">选择多个文件 (支持 .log, .txt, .syslog)</el-button>
            <input
              ref="filesInputRef"
              type="file"
              multiple
              style="display: none;"
              @change="handleFilesSelected"
            />
          </div>
        </el-tab-pane>

        <!-- 标签页 2: 选择日志目录 -->
        <el-tab-pane label="选择日志目录" name="dir">
          <div class="upload-dropzone" @dragover.prevent @drop.prevent="handleDropFiles">
            <el-icon size="40" color="#16a34a"><FolderAdd /></el-icon>
            <div class="dropzone-text">直接选择本地日志归档目录</div>
            <p style="font-size: 12px; color: #64748b; margin: 4px 0 10px 0;">浏览器将自动遍历提取文件夹下的所有日志文件并批量上传</p>
            <el-button type="success" size="small" @click="triggerDirInput">选择本地日志文件夹</el-button>
            <input
              ref="dirInputRef"
              type="file"
              webkitdirectory
              directory
              multiple
              style="display: none;"
              @change="handleDirSelected"
            />
          </div>
        </el-tab-pane>

        <!-- 标签页 3: 粘贴 Syslog 文本 -->
        <el-tab-pane label="输入日志文本" name="text">
          <el-form label-position="top">
            <el-form-item label="自定义日志文件名 (可选)">
              <el-input v-model="manualFileName" placeholder="例如: manual_syslog.txt" />
            </el-form-item>
            <el-form-item label="Syslog 报文内容">
              <el-input
                v-model="manualLogText"
                type="textarea"
                :rows="7"
                placeholder="粘贴 Syslog 原始日志报文文本..."
              />
            </el-form-item>
          </el-form>
        </el-tab-pane>
      </el-tabs>

      <!-- 待上传文件清单预览 -->
      <div v-if="selectedPendingFiles.length > 0 && importTab !== 'text'" class="pending-files-box">
        <div class="pending-files-header">
          <span>待导入文件列表 (共 <strong>{{ selectedPendingFiles.length }}</strong> 个)</span>
          <el-button type="danger" link size="small" @click="selectedPendingFiles = []">清空</el-button>
        </div>
        <div class="pending-files-list">
          <div v-for="(f, idx) in selectedPendingFiles" :key="idx" class="pending-file-item">
            <span class="file-name">📄 {{ f.name }}</span>
            <span class="file-size">{{ formatSize(f.size) }}</span>
            <el-tag v-if="isConflictFile(f.name)" type="danger" size="small">已存在同名文件</el-tag>
            <el-icon class="del-btn" @click="removePendingFile(idx)"><Close /></el-icon>
          </div>
        </div>
      </div>

      <template #footer>
        <el-button @click="showImportDialog = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="handleCheckAndStartImport">开始导入并分析</el-button>
      </template>
    </el-dialog>

    <!-- 同名文件冲突处理弹窗 -->
    <el-dialog v-model="showConflictDialog" title="⚠️ 检测到同名日志文件" width="520px">
      <div class="conflict-dialog-body">
        <p style="margin-bottom: 12px; line-height: 1.6; color: #334155;">
          当前任务中已存在以下 <strong>{{ conflictingFileNames.length }}</strong> 个同名日志文件：
        </p>
        <div class="conflict-file-list">
          <div v-for="name in conflictingFileNames" :key="name" class="conflict-item">
            ⚠️ <strong>{{ name }}</strong>
          </div>
        </div>
        <p style="margin-top: 14px; font-size: 13px; color: #64748b;">
          请选择冲突文件的处理方式：
        </p>
      </div>
      <template #footer>
        <el-button @click="showConflictDialog = false">取消</el-button>
        <el-button type="warning" :loading="submitting" @click="executeImportWithConflict('skip')">
          跳过同名文件 (仅导入新文件)
        </el-button>
        <el-button type="danger" :loading="submitting" @click="executeImportWithConflict('overwrite')">
          覆盖已有同名文件
        </el-button>
      </template>
    </el-dialog>

    <!-- 隐藏的全局文件输入框（用于引导区域快捷触发） -->
    <input ref="guideFilesInputRef" type="file" multiple style="display: none;" @change="handleGuideFilesSelected" />
    <input ref="guideDirInputRef" type="file" webkitdirectory directory multiple style="display: none;" @change="handleGuideDirSelected" />
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { FolderOpened, Files, FolderAdd, DocumentCopy, UploadFilled, Close } from '@element-plus/icons-vue'
import api from '@/api'
import RcaGraph from '@/components/RcaGraph.vue'

const route = useRoute()
const router = useRouter()

const taskList = ref([])
const currentTaskId = ref('')
const currentTask = ref(null)
const taskFiles = ref([])
const showFilesDrawer = ref(false)

const logRecords = ref([])
const totalLogs = ref(0)
const loadingLogs = ref(false)
const selectedLog = ref(null)
const rcaEvents = ref([])
const activeTab = ref('knowledge')

const filter = ref({
  page: 1,
  pageSize: 50,
  keyword: '',
  severity: null,
  matched: null,
  sourceFile: ''
})

// 新建任务相关
const showNewTaskDialog = ref(false)
const submitting = ref(false)
const newTaskForm = ref({
  taskName: '',
  deviceType: 'CloudEngine'
})

// 导入相关
const showImportDialog = ref(false)
const importTab = ref('files')
const manualFileName = ref('')
const manualLogText = ref('')
const selectedPendingFiles = ref([])

const filesInputRef = ref(null)
const dirInputRef = ref(null)
const guideFilesInputRef = ref(null)
const guideDirInputRef = ref(null)

// 冲突弹窗
const showConflictDialog = ref(false)
const conflictingFileNames = ref([])

const parsedParameters = computed(() => {
  if (!selectedLog.value || !selectedLog.value.parameters_json) return {}
  try {
    return JSON.parse(selectedLog.value.parameters_json)
  } catch (e) {
    return {}
  }
})

const matchedRCA = computed(() => {
  if (!selectedLog.value || !rcaEvents.value.length) return null
  return rcaEvents.value.find(ev => {
    if (ev.root_log_id === selectedLog.value.id) return true
    try {
      const corr = JSON.parse(ev.correlated_log_ids)
      return corr.includes(selectedLog.value.id)
    } catch (e) {
      return false
    }
  })
})

const fetchTasks = async () => {
  try {
    const res = await api.getTasks()
    if (res.code === 0) {
      taskList.value = res.data
      if (route.params.id) {
        currentTaskId.value = route.params.id
      } else if (taskList.value.length > 0 && !currentTaskId.value) {
        currentTaskId.value = taskList.value[0].task_id
      }
      if (currentTaskId.value) {
        handleTaskChange(currentTaskId.value)
      }
    }
  } catch (e) {}
}

const handleTaskChange = async (taskId) => {
  currentTaskId.value = taskId
  currentTask.value = taskList.value.find(t => t.task_id === taskId)
  filter.value.page = 1
  filter.value.sourceFile = ''
  selectedLog.value = null
  await fetchTaskFiles()
  await fetchLogs()
  await fetchRCA()
}

const fetchTaskFiles = async () => {
  if (!currentTaskId.value) return
  try {
    const res = await api.getTaskFiles(currentTaskId.value)
    if (res.code === 0) {
      taskFiles.value = res.data || []
    }
  } catch (e) {}
}

const onFilterChange = async () => {
  filter.value.page = 1
  await fetchLogs()
}

const fetchLogs = async () => {
  if (!currentTaskId.value) return
  loadingLogs.value = true
  try {
    const res = await api.queryTaskLogs(currentTaskId.value, {
      page: filter.value.page,
      page_size: filter.value.pageSize,
      keyword: filter.value.keyword,
      severity: filter.value.severity,
      matched: filter.value.matched,
      source_file: filter.value.sourceFile
    })
    if (res.code === 0) {
      logRecords.value = res.data.records
      totalLogs.value = res.data.total
      if (logRecords.value.length > 0) {
        const found = selectedLog.value && logRecords.value.some(r => r.id === selectedLog.value.id)
        if (!found) {
          selectLog(logRecords.value[0])
        }
      } else {
        selectedLog.value = null
      }
    }
  } finally {
    loadingLogs.value = false
  }
}

const fetchRCA = async () => {
  if (!currentTaskId.value) return
  try {
    const res = await api.getTaskRCA(currentTaskId.value)
    if (res.code === 0) {
      rcaEvents.value = res.data
    }
  } catch (e) {}
}

const selectLog = (log) => {
  selectedLog.value = log
}

const handleExportHTML = () => {
  if (!currentTaskId.value) return
  window.open(`/api/v1/tasks/${currentTaskId.value}/export?format=html`, '_blank')
}

// 新建任务
const openNewTaskDialog = () => {
  const nowStr = new Date().toISOString().replace(/[-:T.]/g, '').substring(0, 14)
  newTaskForm.value.taskName = `Audit-${nowStr}`
  newTaskForm.value.deviceType = 'CloudEngine'
  showNewTaskDialog.value = true
}

const handleCreateEmptyTask = async () => {
  submitting.value = true
  try {
    const res = await api.createTask({
      task_name: newTaskForm.value.taskName,
      device_type: newTaskForm.value.deviceType
    })
    if (res.code === 0) {
      ElMessage.success('空任务创建成功，可随时导入日志')
      showNewTaskDialog.value = false
      await fetchTasks()
      handleTaskChange(res.data.task_id)
    }
  } finally {
    submitting.value = false
  }
}

const handleCreateAndOpenImport = async () => {
  submitting.value = true
  try {
    const res = await api.createTask({
      task_name: newTaskForm.value.taskName,
      device_type: newTaskForm.value.deviceType
    })
    if (res.code === 0) {
      showNewTaskDialog.value = false
      await fetchTasks()
      handleTaskChange(res.data.task_id)
      openImportDialog()
    }
  } finally {
    submitting.value = false
  }
}

// 导入弹窗交互
const openImportDialog = () => {
  selectedPendingFiles.value = []
  manualLogText.value = ''
  manualFileName.value = ''
  importTab.value = 'files'
  showImportDialog.value = true
}

const openImportTextTab = () => {
  openImportDialog()
  importTab.value = 'text'
}

const triggerFilesInput = () => {
  filesInputRef.value?.click()
}
const triggerDirInput = () => {
  dirInputRef.value?.click()
}
const triggerGuideFilesSelect = () => {
  guideFilesInputRef.value?.click()
}
const triggerGuideDirSelect = () => {
  guideDirInputRef.value?.click()
}

const handleFilesSelected = (e) => {
  if (e.target.files) {
    addPendingFiles(Array.from(e.target.files))
  }
  e.target.value = ''
}

const handleDirSelected = (e) => {
  if (e.target.files) {
    addPendingFiles(Array.from(e.target.files))
  }
  e.target.value = ''
}

const handleGuideFilesSelected = (e) => {
  if (e.target.files) {
    openImportDialog()
    addPendingFiles(Array.from(e.target.files))
  }
  e.target.value = ''
}

const handleGuideDirSelected = (e) => {
  if (e.target.files) {
    openImportDialog()
    addPendingFiles(Array.from(e.target.files))
  }
  e.target.value = ''
}

const handleDropFiles = (e) => {
  if (e.dataTransfer.files) {
    addPendingFiles(Array.from(e.dataTransfer.files))
  }
}

const addPendingFiles = (files) => {
  for (const f of files) {
    const exists = selectedPendingFiles.value.some(p => p.name === f.name)
    if (!exists) {
      selectedPendingFiles.value.push(f)
    }
  }
}

const removePendingFile = (index) => {
  selectedPendingFiles.value.splice(index, 1)
}

const isConflictFile = (fileName) => {
  return taskFiles.value.some(tf => tf.file_name === fileName)
}

// 提交导入前检查冲突
const handleCheckAndStartImport = () => {
  if (importTab.value === 'text') {
    if (!manualLogText.value.trim()) {
      ElMessage.warning('请输入 Syslog 报文文本')
      return
    }
    const fname = manualFileName.value.trim() || 'manual_input.txt'
    if (isConflictFile(fname)) {
      conflictingFileNames.value = [fname]
      showConflictDialog.value = true
      return
    }
    executeImportWithConflict('overwrite')
    return
  }

  if (selectedPendingFiles.value.length === 0) {
    ElMessage.warning('请选择至少一个日志文件')
    return
  }

  // 检查是否有同名文件
  const conflicts = []
  for (const f of selectedPendingFiles.value) {
    if (isConflictFile(f.name)) {
      conflicts.push(f.name)
    }
  }

  if (conflicts.length > 0) {
    conflictingFileNames.value = conflicts
    showConflictDialog.value = true
  } else {
    executeImportWithConflict('overwrite')
  }
}

// 执行实际导入
const executeImportWithConflict = async (conflictMode) => {
  if (!currentTaskId.value) return
  submitting.value = true
  showConflictDialog.value = false

  try {
    const formData = new FormData()
    formData.append('conflict_mode', conflictMode)

    if (importTab.value === 'text') {
      formData.append('content', manualLogText.value)
      formData.append('file_name', manualFileName.value.trim() || 'manual_input.txt')
    } else {
      for (const f of selectedPendingFiles.value) {
        formData.append('files', f)
      }
    }

    const res = await api.importTaskLogs(currentTaskId.value, formData)
    if (res.code === 0) {
      ElMessage.success(`导入成功！共解析日志 ${res.data.log_count} 条，匹配知识 ${res.data.matched_count} 条，关联 RCA ${res.data.rca_count} 条`)
      showImportDialog.value = false
      selectedPendingFiles.value = []
      manualLogText.value = ''
      await fetchTasks()
      handleTaskChange(currentTaskId.value)
    }
  } finally {
    submitting.value = false
  }
}

const formatTime = (ts) => {
  if (!ts) return '-'
  return ts.replace('T', ' ').substring(0, 19)
}

const formatSize = (bytes) => {
  if (!bytes) return '0 B'
  if (bytes < 1024) return bytes + ' B'
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB'
  return (bytes / (1024 * 1024)).toFixed(2) + ' MB'
}

const getSevClass = (sev) => {
  if (sev <= 2) return 'sev-crit'
  if (sev <= 4) return 'sev-err'
  if (sev <= 5) return 'sev-warn'
  return 'sev-info'
}

onMounted(() => {
  fetchTasks()
})
</script>

<style scoped>
.workbench-container {
  height: calc(100vh - 92px);
  display: flex;
  flex-direction: column;
  background: #fff;
  border-radius: 8px;
  box-shadow: 0 1px 3px rgba(0,0,0,0.1);
  overflow: hidden;
}

.workbench-header {
  height: 52px;
  background: #f8fafc;
  border-bottom: 1px solid #e2e8f0;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 16px;
}
.header-left {
  display: flex;
  align-items: center;
  gap: 10px;
}
.task-badge {
  font-size: 12px;
  color: #64748b;
  background: #e2e8f0;
  padding: 3px 8px;
  border-radius: 4px;
}

/* 空任务引导区 */
.empty-task-guide {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  background: #f8fafc;
  padding: 24px;
}
.guide-card {
  width: 720px;
  border-radius: 12px;
  text-align: center;
  padding: 24px 16px;
}
.guide-header h3 {
  margin: 12px 0 6px 0;
  color: #0f172a;
}
.guide-header p {
  font-size: 13px;
  color: #64748b;
  margin-bottom: 24px;
}
.guide-actions {
  display: flex;
  gap: 16px;
  justify-content: center;
}
.action-tile {
  flex: 1;
  border: 1px solid #e2e8f0;
  background: #fff;
  border-radius: 8px;
  padding: 20px 14px;
  cursor: pointer;
  transition: all 0.2s ease;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
}
.action-tile:hover {
  border-color: #38bdf8;
  transform: translateY(-2px);
  box-shadow: 0 4px 12px rgba(0,0,0,0.05);
}
.action-tile h4 {
  margin: 4px 0 0 0;
  font-size: 14px;
  color: #1e293b;
}
.action-tile p {
  font-size: 12px;
  color: #64748b;
  margin: 0 0 10px 0;
  min-height: 32px;
  line-height: 1.4;
}

.workbench-body {
  flex: 1;
  display: flex;
  overflow: hidden;
}

/* 左栏 28% */
.col-left {
  width: 28%;
  border-right: 1px solid #e2e8f0;
  display: flex;
  flex-direction: column;
  background: #f8fafc;
}
.filter-panel {
  padding: 10px;
  border-bottom: 1px solid #e2e8f0;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.filter-row {
  display: flex;
  justify-content: space-between;
}
.log-stream-list {
  flex: 1;
  overflow-y: auto;
  padding: 8px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.log-card {
  background: #fff;
  border: 1px solid #e2e8f0;
  border-radius: 6px;
  padding: 8px 10px;
  cursor: pointer;
  transition: all 0.15s ease;
}
.log-card:hover {
  border-color: #94a3b8;
  background: #f1f5f9;
}
.log-card.active {
  border-color: #38bdf8;
  background: #f0f9ff;
  box-shadow: 0 0 0 1px #38bdf8;
}
.log-card-header {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
}
.log-mod {
  font-weight: 600;
  color: #0f172a;
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.match-tag {
  background: #dcfce7;
  color: #166534;
  font-size: 10px;
  padding: 1px 4px;
  border-radius: 3px;
}
.log-card-msg {
  font-size: 11px;
  color: #475569;
  margin: 4px 0;
  overflow: hidden;
  text-overflow: ellipsis;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  line-height: 1.4;
}
.log-card-footer {
  font-size: 10px;
  color: #94a3b8;
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 4px;
}
.file-tag {
  background: #f1f5f9;
  color: #475569;
  padding: 0 4px;
  border-radius: 2px;
  max-width: 110px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.slot-tag {
  background: #e2e8f0;
  padding: 0 4px;
  border-radius: 2px;
}
.pagination-bar {
  padding: 6px;
  display: flex;
  justify-content: center;
  border-top: 1px solid #e2e8f0;
  background: #fff;
}

/* 中栏 36% */
.col-middle {
  width: 36%;
  border-right: 1px solid #e2e8f0;
  overflow-y: auto;
  padding: 16px;
  background: #ffffff;
}
.panel-title {
  font-size: 15px;
  font-weight: 700;
  color: #0f172a;
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 14px;
}
.section-box {
  background: #f8fafc;
  border: 1px solid #e2e8f0;
  border-radius: 6px;
  padding: 12px;
  margin-bottom: 14px;
}
.box-title {
  font-size: 12px;
  font-weight: 600;
  color: #475569;
  margin-bottom: 8px;
}
.raw-code {
  font-family: monospace;
  font-size: 12px;
  background: #1e293b;
  color: #f8fafc;
  padding: 10px;
  border-radius: 4px;
  word-break: break-all;
  line-height: 1.5;
}
.param-grid {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.param-chip {
  background: #fff;
  border: 1px solid #cbd5e1;
  border-radius: 4px;
  padding: 4px 8px;
  font-size: 12px;
}
.p-key {
  color: #0284c7;
  font-weight: 600;
  margin-right: 4px;
}
.p-val {
  color: #334155;
}
.rca-alert {
  background: #fff7ed;
  border: 1px solid #fdba74;
  border-radius: 6px;
  padding: 12px;
  font-size: 12px;
  color: #9a3412;
}
.rca-alert-title {
  font-weight: bold;
  margin-bottom: 4px;
}

/* 右栏 36% */
.col-right {
  width: 36%;
  overflow-y: auto;
  padding: 16px;
  background: #ffffff;
}
.kb-header-card {
  background: #f0f9ff;
  border: 1px solid #bae6fd;
  border-radius: 6px;
  padding: 12px;
  margin-bottom: 14px;
}
.kb-title {
  font-size: 16px;
  font-weight: bold;
  color: #0369a1;
}
.kb-meta {
  margin-top: 6px;
  display: flex;
  gap: 8px;
  font-size: 11px;
}
.badge-tier { background: #e0f2fe; color: #0284c7; padding: 2px 6px; border-radius: 3px; }
.badge-conf { background: #dcfce7; color: #15803d; padding: 2px 6px; border-radius: 3px; font-weight: bold; }
.kb-block {
  margin-bottom: 14px;
}
.kb-subtitle {
  font-size: 13px;
  font-weight: 600;
  color: #334155;
  margin-bottom: 6px;
}
.kb-text {
  font-size: 13px;
  color: #475569;
  line-height: 1.6;
}
.action-box {
  background: #f8fafc;
  border-left: 3px solid #10b981;
  padding: 10px 12px;
  border-radius: 0 4px 4px 0;
}
.cause-text {
  background: #fffbeb;
  border-left: 3px solid #f59e0b;
  padding: 8px 12px;
  border-radius: 0 4px 4px 0;
}

.empty-state {
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
}

.sev-tag {
  font-size: 10px;
  font-weight: bold;
  padding: 1px 5px;
  border-radius: 3px;
}
.sev-crit { background: #fee2e2; color: #b91c1c; }
.sev-err { background: #ffedd5; color: #c2410c; }
.sev-warn { background: #fef9c3; color: #a16207; }
.sev-info { background: #f1f5f9; color: #475569; }

.rca-guide {
  margin-top: 14px;
  background: #fff7ed;
  border: 1px solid #fed7aa;
  border-radius: 6px;
  padding: 12px;
  font-size: 13px;
  color: #9a3412;
}
.guide-title {
  font-weight: bold;
  margin-bottom: 6px;
}

/* 抽屉与弹窗样式 */
.drawer-summary {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 12px;
  font-size: 13px;
  color: #475569;
}
.upload-dropzone {
  border: 2px dashed #cbd5e1;
  border-radius: 8px;
  padding: 30px 16px;
  text-align: center;
  background: #f8fafc;
  transition: all 0.2s ease;
}
.upload-dropzone:hover {
  border-color: #0284c7;
  background: #f0f9ff;
}
.dropzone-text {
  margin: 10px 0 12px 0;
  font-size: 14px;
  color: #475569;
}
.pending-files-box {
  margin-top: 14px;
  border: 1px solid #e2e8f0;
  border-radius: 6px;
  background: #fff;
  padding: 10px;
}
.pending-files-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 8px;
  font-size: 12px;
  color: #64748b;
}
.pending-files-list {
  max-height: 140px;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.pending-file-item {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  padding: 4px 6px;
  background: #f8fafc;
  border-radius: 4px;
}
.pending-file-item .file-name {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.pending-file-item .file-size {
  color: #94a3b8;
  font-size: 11px;
}
.pending-file-item .del-btn {
  cursor: pointer;
  color: #ef4444;
}

.conflict-dialog-body .conflict-file-list {
  background: #fff7ed;
  border: 1px solid #fed7aa;
  border-radius: 6px;
  padding: 10px 12px;
  max-height: 120px;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.conflict-item {
  font-size: 12px;
  color: #9a3412;
}
</style>

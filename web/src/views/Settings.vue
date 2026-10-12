<template>
  <div class="settings-page">
    <!-- 顶部状态统计卡片 -->
    <el-row :gutter="16" class="stats-row">
      <el-col :span="6">
        <StatCard
          :icon="Document"
          :value="formatSize(logStats.current_size || 0)"
          title="当前活跃日志 (log.log)"
          tone="brand"
        />
      </el-col>
      <el-col :span="6">
        <StatCard
          :icon="Files"
          :value="logStats.file_count || 0"
          unit="个"
          title="日志文件总数 (含转储归档)"
          tone="success"
        />
      </el-col>
      <el-col :span="6">
        <StatCard
          :icon="PieChart"
          :value="formatSize(logStats.total_size || 0)"
          :title="`总占用空间 / 配额 ${(logStats.max_size_mb || 1024) >= 1024 ? ((logStats.max_size_mb || 1024) / 1024).toFixed(1) + ' GB' : (logStats.max_size_mb || 1024) + ' MB'}`"
          tone="warning"
        />
      </el-col>
      <el-col :span="6">
        <StatCard
          :icon="Timer"
          :value="logStats.max_days || 180"
          unit="天"
          title="最大保留天数 (超期自动清理)"
          tone="purple"
        />
      </el-col>
    </el-row>

    <el-row :gutter="16" class="main-row">
      <!-- 左侧：日志策略与系统配置 -->
      <el-col :span="10">
        <el-card shadow="hover" class="config-card">
          <template #header>
            <div class="card-header">
              <span class="card-header-title"><el-icon><Setting /></el-icon> 日志存放与清理策略配置</span>
            </div>
          </template>

          <el-alert
            title="日志存放与转储规则说明"
            type="info"
            :closable="false"
            show-icon
            class="alert-desc-box"
          >
            <template #default>
              <div class="rule-tip">
                <div>1. 每次程序启动生成全新空白 <code>log.log</code>，并将上次运行产生的日志按其修改时间重命名转储（例如 <code>log_20260822_13000213.log</code>）。</div>
                <div>2. 单个 <code>log.log</code> 超过 10MB 时自动改名转储并新建空白日志文件。</div>
                <div>3. 达到最大保留大小或保留天数时，先达到哪个条件就按日期从旧到新自动清理。</div>
              </div>
            </template>
          </el-alert>

          <el-form label-position="top" :model="logForm">
            <el-form-item label="日志存放目录 (默认数据目录 log 文件夹)">
              <el-input v-model="logForm.dir" disabled>
                <template #prepend>
                  <el-icon><Folder /></el-icon>
                </template>
              </el-input>
            </el-form-item>

            <el-row :gutter="16">
              <el-col :span="12">
                <el-form-item label="最大保留总大小 (MB)">
                  <el-input-number
                    v-model="logForm.max_size_mb"
                    :min="10"
                    :max="102400"
                    :step="128"
                    class="w-full"
                  />
                  <div class="form-hint">
                    约等于 {{ (logForm.max_size_mb / 1024).toFixed(2) }} GB
                  </div>
                </el-form-item>
              </el-col>
              <el-col :span="12">
                <el-form-item label="最大保留天数 (天)">
                  <el-input-number
                    v-model="logForm.max_days"
                    :min="1"
                    :max="3650"
                    :step="30"
                    class="w-full"
                  />
                  <div class="form-hint">
                    超过 {{ logForm.max_days }} 天的旧日志将被自动清理
                  </div>
                </el-form-item>
              </el-col>
            </el-row>

            <el-row :gutter="16">
              <el-col :span="12">
                <el-form-item label="日志输出级别">
                  <el-select v-model="logForm.level" class="w-full">
                    <el-option label="DEBUG (详细调试)" value="debug" />
                    <el-option label="INFO (常规信息)" value="info" />
                    <el-option label="WARN (警告级别)" value="warn" />
                    <el-option label="ERROR (仅错误)" value="error" />
                  </el-select>
                </el-form-item>
              </el-col>
              <el-col :span="12">
                <el-form-item label="日志格式">
                  <el-select v-model="logForm.format" class="w-full">
                    <el-option label="Console (可读文本)" value="console" />
                    <el-option label="JSON (结构化日志)" value="json" />
                  </el-select>
                </el-form-item>
              </el-col>
            </el-row>

            <div class="form-actions">
              <el-button type="primary" :loading="saving" @click="handleSaveConfig">
                <el-icon><Check /></el-icon>
                <span>保存配置并应用</span>
              </el-button>
              <!--
                UI-18: 原按钮文案是"重置"，实际行为却是"重新载入配置"，
                用户会以为点了就会丢弃未保存的修改（结果什么都没发生），
                或以为点了没生效。这里把文案改为与行为一致的"重新载入"。
              -->
              <el-button @click="fetchConfig" title="放弃未保存的修改，重新从服务端载入配置">
                <el-icon><Refresh /></el-icon>
                <span>重新载入</span>
              </el-button>
            </div>
          </el-form>
        </el-card>
      </el-col>

      <!-- 右侧：日志文件列表与即时维护 -->
      <el-col :span="14">
        <el-card shadow="hover" class="files-card">
          <template #header>
            <div class="card-header-flex">
              <span class="card-header-title"><el-icon><Tickets /></el-icon> 日志文件清单与状态</span>
              <div class="header-btns">
                <el-button
                  type="danger"
                  plain
                  size="small"
                  :loading="cleaning"
                  @click="handleCleanLogs"
                >
                  <el-icon><Delete /></el-icon>
                  <span>立即执行清理</span>
                </el-button>
                <el-button size="small" :loading="loadingLogs" @click="fetchLogs">
                  <el-icon><Refresh /></el-icon>
                  <span>刷新</span>
                </el-button>
              </div>
            </div>
          </template>

          <el-table
            :data="logStats.files || []"
            v-loading="loadingLogs"
            stripe
            class="w-full"
            max-height="520"
          >
            <el-table-column prop="name" label="日志文件名" min-width="240">
              <template #default="{ row }">
                <div class="file-name-cell">
                  <el-icon v-if="row.is_active" class="icon-active"><DocumentChecked /></el-icon>
                  <el-icon v-else class="icon-archive"><Document /></el-icon>
                  <span :class="{ 'active-file-text': row.is_active }">{{ row.name }}</span>
                </div>
              </template>
            </el-table-column>

            <el-table-column prop="is_active" label="状态" width="110">
              <template #default="{ row }">
                <el-tag v-if="row.is_active" type="success" size="small" effect="dark">当前写入</el-tag>
                <el-tag v-else type="info" size="small">历史转储</el-tag>
              </template>
            </el-table-column>

            <el-table-column prop="size" label="文件大小" width="120">
              <template #default="{ row }">
                <span class="file-size-tag">{{ formatSize(row.size) }}</span>
              </template>
            </el-table-column>

            <el-table-column prop="mod_time" label="最后修改时间" width="190">
              <template #default="{ row }">
                <span class="time-text">{{ formatTime(row.mod_time) }}</span>
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-col>
    </el-row>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Document, Files, PieChart, Timer, Setting, Tickets } from '@element-plus/icons-vue'
import api from '@/api'
import StatCard from '@/components/StatCard.vue'
import { formatTime, formatSize } from '@/utils/format'

const logStats = ref({
  dir: '',
  current_size: 0,
  total_size: 0,
  file_count: 0,
  max_size_mb: 1024,
  max_days: 180,
  files: []
})

const logForm = ref({
  dir: 'LogAuditorGoData/log',
  max_size_mb: 1024,
  max_days: 180,
  level: 'debug',
  format: 'console'
})

const saving = ref(false)
const cleaning = ref(false)
const loadingLogs = ref(false)

const fetchConfig = async () => {
  try {
    const res = await api.getSystemConfig()
    if (res.code === 0 && res.data) {
      if (res.data.config && res.data.config.log) {
        logForm.value = {
          dir: res.data.config.log.dir || 'LogAuditorGoData/log',
          max_size_mb: res.data.config.log.max_size_mb || 1024,
          max_days: res.data.config.log.max_days || 180,
          level: res.data.config.log.level || 'debug',
          format: res.data.config.log.format || 'console'
        }
      }
      if (res.data.log_stats) {
        logStats.value = res.data.log_stats
      }
    }
  } catch (e) {
    // handled in interceptor
  }
}

const fetchLogs = async () => {
  loadingLogs.value = true
  try {
    const res = await api.getSystemLogs()
    if (res.code === 0 && res.data) {
      logStats.value = res.data
    }
  } finally {
    loadingLogs.value = false
  }
}

const handleSaveConfig = async () => {
  saving.value = true
  try {
    const res = await api.updateLogConfig({
      max_size_mb: logForm.value.max_size_mb,
      max_days: logForm.value.max_days,
      level: logForm.value.level,
      format: logForm.value.format
    })
    if (res.code === 0) {
      ElMessage.success('日志策略配置已更新并实时生效！')
      if (res.data && res.data.log_stats) {
        logStats.value = res.data.log_stats
      } else {
        fetchLogs()
      }
    }
  } finally {
    saving.value = false
  }
}

const handleCleanLogs = () => {
  ElMessageBox.confirm(
    `确定要立即根据当前配置规则（保留 ${logForm.value.max_days} 天 / 最多 ${logForm.value.max_size_mb} MB）执行过期日志清理吗？`,
    '确认清理日志',
    {
      confirmButtonText: '立即清理',
      cancelButtonText: '取消',
      type: 'warning'
    }
  ).then(async () => {
    cleaning.value = true
    try {
      const res = await api.cleanSystemLogs()
      if (res.code === 0) {
        ElMessage.success('日志清理完成！')
        if (res.data) {
          logStats.value = res.data
        }
      }
    } finally {
      cleaning.value = false
    }
  }).catch(() => {})
}

onMounted(() => {
  fetchConfig()
})
</script>

<style scoped>
.settings-page {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.config-card, .files-card {
  border-radius: var(--radius-lg);
}

.card-header {
  font-weight: 600;
  font-size: 15px;
}

.card-header-flex {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  font-weight: 600;
  font-size: 15px;
}

.header-btns {
  display: flex;
  gap: 8px;
}

.rule-tip {
  font-size: var(--fs-sm);
  line-height: 1.6;
  color: var(--gray-600);
}
.rule-tip code {
  background-color: var(--border-base);
  padding: 2px 4px;
  border-radius: var(--radius-sm);
  color: var(--gray-900);
}

.form-hint {
  font-size: var(--fs-sm);
  color: var(--gray-400);
  margin-top: 4px;
}

.form-actions {
  display: flex;
  gap: 12px;
  margin-top: 24px;
  padding-top: 16px;
  border-top: 1px solid var(--gray-100);
}

.file-name-cell {
  display: flex;
  align-items: center;
  gap: 8px;
  font-family: monospace;
  font-size: var(--fs-md);
}

.icon-active {
  color: var(--success-strong);
  font-size: var(--fs-xl);
}

.icon-archive {
  color: var(--gray-500);
  font-size: var(--fs-xl);
}

.active-file-text {
  font-weight: 600;
  color: var(--gray-900);
}

.file-size-tag {
  font-family: monospace;
  font-weight: 500;
  color: var(--gray-700);
}

.time-text {
  font-size: var(--fs-sm);
  color: var(--gray-500);
}

.alert-desc-box {
  margin-bottom: 20px;
}

.w-full {
  width: 100%;
}
</style>

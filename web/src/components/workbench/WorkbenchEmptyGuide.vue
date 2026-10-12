<template>
  <div class="empty-task-guide">
    <el-card shadow="never" class="guide-card">
      <div class="guide-header">
        <el-icon size="48" color="#0284c7"><FolderOpened /></el-icon>
        <h3>任务「{{ taskName }}」尚未导入日志数据</h3>
        <p>请选择以下方式之一，直接将本地日志导入到本任务中开始智能审计与 RCA 根因分析：</p>
      </div>

      <div class="guide-actions">
        <div class="action-tile" @click="$emit('open-picker', 'files')">
          <el-icon size="32" color="#0284c7"><Files /></el-icon>
          <h4>选择日志文件</h4>
          <p>直接选择服务端本机上的 .log / .txt / .syslog 日志文件，支持多选</p>
          <el-button type="primary" size="small">选择多个文件</el-button>
        </div>

        <div class="action-tile" @click="$emit('open-picker', 'dir')">
          <el-icon size="32" color="#16a34a"><FolderAdd /></el-icon>
          <h4>选择日志目录</h4>
          <p>选择日志归档目录，由服务端递归收集其中全部日志文件</p>
          <el-button type="success" size="small">选择日志目录</el-button>
        </div>

        <div class="action-tile" @click="$emit('open-text')">
          <el-icon size="32" color="#ea580c"><DocumentCopy /></el-icon>
          <h4>粘贴 Syslog 日志文本</h4>
          <p>直接在网页中粘贴 syslog 报文文本开始分析</p>
          <el-button type="warning" size="small">粘贴日志文本</el-button>
        </div>
      </div>
    </el-card>
  </div>
</template>

<script setup>
import { FolderOpened, Files, FolderAdd, DocumentCopy } from '@element-plus/icons-vue'

defineProps({
  taskName: {
    type: String,
    default: ''
  }
})

defineEmits(['open-picker', 'open-text'])
</script>

<style scoped>
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
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.05);
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
</style>

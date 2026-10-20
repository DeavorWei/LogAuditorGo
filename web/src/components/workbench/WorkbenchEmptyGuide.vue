<template>
  <div class="empty-task-guide">
    <el-card shadow="never" class="guide-card">
      <div class="guide-header">
        <el-icon size="48" color="var(--brand-600)"><FolderOpened /></el-icon>
        <h3>任务「{{ taskName }}」尚未导入日志数据</h3>
        <p>请选择以下方式之一，将本地日志导入本任务，系统会自动解析并匹配官方知识：</p>
      </div>

      <div class="guide-actions">
        <button type="button" class="action-tile reset" @click="$emit('open-picker', 'both')">
          <el-icon size="32" color="var(--brand-600)"><FolderOpened /></el-icon>
          <h4>选择文件或目录</h4>
          <p>在服务端本机选择 .log / .txt / .syslog 日志文件或归档目录，支持文件与目录混合多选，目录将递归收集其中全部日志</p>
          <span class="el-button el-button--primary el-button--small">选择文件 / 目录</span>
        </button>

        <button type="button" class="action-tile reset" @click="$emit('open-text')">
          <el-icon size="32" color="var(--warning-strong)"><DocumentCopy /></el-icon>
          <h4>粘贴 Syslog 日志文本</h4>
          <p>直接在网页中粘贴 syslog 报文文本开始分析</p>
          <span class="el-button el-button--warning el-button--small">粘贴日志文本</span>
        </button>
      </div>
    </el-card>
  </div>
</template>

<script setup>
import { FolderOpened, DocumentCopy } from '@element-plus/icons-vue'

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
  background: var(--bg-subtle);
  padding: 24px;
}

.guide-card {
  width: 720px;
  border-radius: var(--radius-xl);
  text-align: center;
  padding: 24px 16px;
}

.guide-header h3 {
  margin: 12px 0 6px 0;
  color: var(--gray-900);
}

.guide-header p {
  font-size: var(--fs-md);
  color: var(--text-secondary);
  margin-bottom: 24px;
}

.guide-actions {
  display: flex;
  gap: 16px;
  justify-content: center;
}

.action-tile {
  flex: 1;
  border: 1px solid var(--border-base);
  background: var(--gray-0);
  border-radius: var(--radius-lg);
  padding: 20px 14px;
  cursor: pointer;
  transition: all 0.2s ease;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
}

.action-tile:hover {
  border-color: var(--brand-400);
  transform: translateY(-2px);
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.05);
}

.action-tile h4 {
  margin: 4px 0 0 0;
  font-size: var(--fs-lg);
  color: var(--gray-800);
}

.action-tile p {
  font-size: var(--fs-sm);
  color: var(--text-secondary);
  margin: 0 0 10px 0;
  min-height: 32px;
  line-height: 1.4;
}
</style>

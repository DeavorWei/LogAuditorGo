<template>
  <div class="hdx-help-card" :class="{ compact }">
    <div class="hdx-title">
      <el-icon color="var(--brand-600)"><InfoFilled /></el-icon>
      <span>如何获取华为 HDX 产品文档</span>
    </div>
    <ol class="hdx-steps">
      <li>访问 <a href="https://support.huawei.com/enterprise" target="_blank" rel="noopener">华为企业技术支持官网</a>，使用企业账号登录。</li>
      <li>搜索设备型号或产品系列（如 S12700、USG6000F），进入“产品文档 / Hedex”。</li>
      <li>下载 hdx 文件或解压包，回到本产品选择文件/文件夹导入。</li>
    </ol>
    <div class="hdx-actions">
      <el-button type="primary" plain size="small" :icon="FolderAdd" @click="$emit('import-doc')">
        我已下载，去导入
      </el-button>
      <span v-if="!compact" class="hdx-note">没有企业账号/下载权限时，请联系企业管理员或华为客户经理。</span>
    </div>
  </div>
</template>

<script setup>
import { FolderAdd, InfoFilled } from '@element-plus/icons-vue'

defineProps({
  compact: { type: Boolean, default: false }
})

defineEmits(['import-doc'])
</script>

<style scoped>
.hdx-help-card {
  border: 1px solid var(--brand-200, #bfdbfe);
  background: var(--brand-50, #eff6ff);
  border-radius: var(--radius-lg);
  padding: 14px 16px;
}

.hdx-help-card.compact {
  padding: 10px 12px;
}

.hdx-title {
  display: flex;
  align-items: center;
  gap: 6px;
  font-weight: 700;
  color: var(--text-primary);
  margin-bottom: 8px;
}

.hdx-steps {
  margin: 0;
  padding-left: 20px;
  font-size: var(--fs-sm);
  color: var(--text-secondary);
  line-height: 1.9;
}

.hdx-steps a {
  color: var(--brand-600);
}

.hdx-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  margin-top: 10px;
}

.hdx-note {
  font-size: var(--fs-xs);
  color: var(--text-secondary);
}
</style>

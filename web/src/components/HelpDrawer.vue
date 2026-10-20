<template>
  <el-drawer
    :model-value="modelValue"
    title="帮助"
    size="520px"
    @update:model-value="(val) => $emit('update:modelValue', val)"
  >
    <el-tabs v-model="activeTab" class="help-tabs">
      <el-tab-pane label="使用说明" name="guide">
        <div class="guide-block">
          <h3>第一次使用（4 步）</h3>
          <ol class="guide-list">
            <li><strong>导入 HDX 文档</strong>：产品文档页 → 导入 HDX 压缩包或解压目录。</li>
            <li><strong>创建排障任务</strong>：概览/工作台/案例库的“新建排障任务”都进入同一向导。</li>
            <li><strong>导入日志</strong>：优先用 Windows 原生对话框选文件/文件夹；也可粘贴文本。</li>
            <li><strong>查看诊断摘要</strong>：导入完成自动进入，先看高频模块、未匹配热点与建议。</li>
          </ol>
        </div>
        <div class="guide-block">
          <h3>日常排查路径</h3>
          <ol class="guide-list">
            <li>工作台左栏筛日志 → 中栏看结构化详情 → 右栏看官方知识与匹配依据。</li>
            <li>未匹配日志可在右栏“补充本地知识”或“绑定到已有知识”。</li>
            <li>排查结束写“结论”，导出报告或证据包完成交接。</li>
          </ol>
        </div>
        <el-button type="primary" plain :icon="Guide" @click="$emit('open-onboarding')">
          重新打开首次引导
        </el-button>
      </el-tab-pane>

      <el-tab-pane label="HDX 文档" name="hdx">
        <HdxHelpCard @import-doc="$emit('import-doc')" />
        <div class="guide-block">
          <h3>知识库为空时会发生什么？</h3>
          <p class="muted">
            日志仍会被解析，但“知识覆盖率”为 0，右栏只显示未匹配提示与下一步建议。
            这不代表日志无意义，只表示还没有对应产品的官方条目。
          </p>
        </div>
      </el-tab-pane>

      <el-tab-pane label="快捷键" name="shortcuts">
        <div class="shortcut-list">
          <div v-for="item in shortcuts" :key="item.desc" class="shortcut-row">
            <span>{{ item.desc }}</span>
            <span class="keys">
              <kbd v-for="key in item.keys" :key="key">{{ key }}</kbd>
            </span>
          </div>
        </div>
        <p class="muted">快捷键在工作台“日志分析”Tab 生效；输入框聚焦时自动禁用。</p>
      </el-tab-pane>
    </el-tabs>
  </el-drawer>
</template>

<script setup>
import { ref } from 'vue'
import { Guide } from '@element-plus/icons-vue'
import HdxHelpCard from '@/components/HdxHelpCard.vue'

defineProps({
  modelValue: { type: Boolean, default: false }
})

defineEmits(['update:modelValue', 'open-onboarding', 'import-doc'])

const activeTab = ref('guide')

const shortcuts = [
  { desc: '快速打标签', keys: ['Space'] },
  { desc: '上一条 / 下一条日志', keys: ['↑', '↓', 'k', 'j'] },
  { desc: '沉浸全屏模式 (Zen)', keys: ['F'] },
  { desc: '浏览器物理全屏', keys: ['Shift', 'F'] },
  { desc: '打开专家筛选', keys: ['Alt', 'F'] },
  { desc: '关闭弹窗 / 退出沉浸', keys: ['Esc'] }
]
</script>

<style scoped>
.help-tabs {
  height: 100%;
}

.guide-block {
  margin-bottom: 18px;
}

.guide-block h3 {
  margin: 0 0 8px;
  font-size: var(--fs-lg);
  color: var(--text-primary);
}

.guide-list {
  margin: 0;
  padding-left: 20px;
  color: var(--text-secondary);
  line-height: 1.9;
  font-size: var(--fs-sm);
}

.shortcut-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.shortcut-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-size: var(--fs-sm);
  color: var(--text-secondary);
  padding: 6px 0;
  border-bottom: 1px dashed var(--border-base);
}

.keys {
  display: flex;
  gap: 4px;
}

kbd {
  display: inline-block;
  padding: 1px 6px;
  font-size: 11px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  color: var(--text-primary);
  background: var(--bg-subtle);
  border: 1px solid var(--border-base);
  border-radius: var(--radius-xs);
}

.muted {
  color: var(--text-secondary);
  font-size: var(--fs-sm);
  line-height: 1.8;
  margin-top: 12px;
}
</style>

<template>
  <el-dialog
    v-model="visible"
    title="标签管理"
    width="680px"
    destroy-on-close
    append-to-body
    class="tag-manager-modal"
  >
    <div class="tag-manager-content">
      <!-- 头部创建表单 -->
      <div class="create-card">
        <h4 class="card-title">{{ isEditing ? '编辑标签' : '新建标签' }}</h4>
        <el-form :model="form" inline class="tag-form">
          <el-form-item label="名称" required>
            <el-input
              v-model="form.name"
              placeholder="标签名称 (≤32字)"
              maxlength="32"
              style="width: 160px"
            />
          </el-form-item>
          <el-form-item label="颜色">
            <el-color-picker v-model="form.color" :predefine="PREDEFINE_TAG_COLORS" />
          </el-form-item>
          <el-form-item label="备注">
            <el-input
              v-model="form.remark"
              placeholder="说明或备注"
              style="width: 160px"
              maxlength="64"
            />
          </el-form-item>
          <el-form-item>
            <el-button
              type="primary"
              :loading="submitting"
              @click="submitTag"
            >
              {{ isEditing ? '保存修改' : '创建' }}
            </el-button>
            <el-button v-if="isEditing" @click="cancelEdit">取消</el-button>
          </el-form-item>
        </el-form>
      </div>

      <!-- 标签列表表格 -->
      <el-table
        v-loading="tagStore.loading"
        :data="tagStore.tags"
        stripe
        style="width: 100%; margin-top: 16px"
        max-height="380"
      >
        <el-table-column label="标签样式" width="160">
          <template #default="{ row }">
            <el-tag
              :color="row.color"
              effect="dark"
              style="border: none; color: #fff; font-weight: 500"
            >
              {{ row.name }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="remark" label="备注" min-width="160" show-overflow-tooltip />
        <el-table-column label="关联日志数" width="120" align="center">
          <template #default="{ row }">
            <el-link
              type="primary"
              :underline="false"
              @click="filterByTag(row)"
            >
              <strong>{{ row.log_count || 0 }}</strong> 条
            </el-link>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="140" align="center">
          <template #default="{ row }">
            <el-button type="primary" link size="small" @click="startEdit(row)">
              编辑
            </el-button>
            <el-popconfirm
              title="确定删除此标签？将同步清空所有日志的打标关联"
              confirm-button-text="删除"
              cancel-button-text="取消"
              confirm-button-type="danger"
              width="240"
              @confirm="handleDelete(row)"
            >
              <template #reference>
                <el-button type="danger" link size="small">删除</el-button>
              </template>
            </el-popconfirm>
          </template>
        </el-table-column>
      </el-table>
    </div>
    <template #footer>
      <el-button @click="visible = false">关闭</el-button>
    </template>
  </el-dialog>
</template>

<script setup>
import { ref, reactive } from 'vue'
import { ElMessage } from 'element-plus'
import useTagStore from '@/stores/tag'
import useFilterStore from '@/stores/filter'
import { PREDEFINE_TAG_COLORS, DEFAULT_TAG_COLOR } from '@/constants/tagColors'

const props = defineProps({
  taskId: {
    type: String,
    required: true
  }
})

const emit = defineEmits(['filter-applied'])

const tagStore = useTagStore()
const filterStore = useFilterStore()

const visible = ref(false)
const submitting = ref(false)
const isEditing = ref(false)
const editingTagId = ref(null)

const form = reactive({
  name: '',
  color: DEFAULT_TAG_COLOR,
  remark: ''
})

const open = () => {
  visible.value = true
  cancelEdit()
  tagStore.fetchTags(props.taskId)
}

defineExpose({ open, visible })

const startEdit = (tag) => {
  isEditing.value = true
  editingTagId.value = tag.id
  form.name = tag.name
  form.color = tag.color || '#409EFF'
  form.remark = tag.remark || ''
}

const cancelEdit = () => {
  isEditing.value = false
  editingTagId.value = null
  form.name = ''
  form.color = '#409EFF'
  form.remark = ''
}

const submitTag = async () => {
  if (!form.name.trim()) {
    ElMessage.warning('请输入标签名称')
    return
  }

  submitting.value = true
  try {
    if (isEditing.value) {
      const res = await tagStore.updateTag(props.taskId, editingTagId.value, {
        name: form.name.trim(),
        color: form.color,
        remark: form.remark.trim()
      })
      if (res) {
        cancelEdit()
      }
    } else {
      const res = await tagStore.createTag(props.taskId, {
        name: form.name.trim(),
        color: form.color,
        remark: form.remark.trim()
      })
      if (res) {
        cancelEdit()
      }
    }
  } finally {
    submitting.value = false
  }
}

const handleDelete = async (tag) => {
  await tagStore.deleteTag(props.taskId, tag.id)
  // 若当前筛选中包含了已删除的标签，同步移除
  if (filterStore.filters.tagIds.includes(tag.id)) {
    filterStore.filters.tagIds = filterStore.filters.tagIds.filter(id => id !== tag.id)
    emit('filter-applied')
  }
}

// 快速按该标签筛选
const filterByTag = (tag) => {
  filterStore.filters.tagIds = [tag.id]
  filterStore.resetPagination()
  visible.value = false
  emit('filter-applied')
}
</script>

<style scoped>
.create-card {
  background: var(--el-fill-color-light, #f5f7fa);
  padding: 12px 16px;
  border-radius: 6px;
  border: 1px solid var(--el-border-color-lighter, #ebeef5);
}
.card-title {
  font-size: 13px;
  font-weight: 600;
  margin: 0 0 10px 0;
  color: var(--el-text-color-primary, #303133);
}
.tag-form {
  margin-bottom: -18px;
}
</style>

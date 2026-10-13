<template>
  <el-drawer
    v-model="visible"
    title="高级筛选与规则圈选"
    size="720px"
    destroy-on-close
    class="advanced-filter-drawer"
    :before-close="handleBeforeClose"
  >
    <div class="drawer-container">
      <!-- 0. 顶栏预设方案选择器 (Preset Selector Bar) -->
      <div class="preset-bar-wrapper">
        <div class="preset-label-group">
          <el-icon class="preset-icon"><CollectionTag /></el-icon>
          <span class="preset-label">场景预设：</span>
        </div>
        <el-select
          v-model="selectedPresetId"
          placeholder="选择系统排查场景或自定义预设..."
          size="default"
          clearable
          class="preset-select"
          @change="onPresetSelected"
        >
          <el-option-group label="系统内置专家场景">
            <el-option
              v-for="p in SYSTEM_EXPERT_PRESETS"
              :key="p.id"
              :label="p.name"
              :value="p.id"
            >
              <div class="preset-option-item">
                <span class="preset-name">{{ p.name }}</span>
                <span class="preset-desc">{{ p.description }}</span>
              </div>
            </el-option>
          </el-option-group>

          <el-option-group v-if="customPresets.length > 0" label="任务自定义预设">
            <el-option
              v-for="p in customPresets"
              :key="p.id"
              :label="p.name"
              :value="String(p.id)"
            >
              <div class="preset-option-item">
                <span class="preset-name">{{ p.name }}</span>
                <el-button
                  type="danger"
                  link
                  size="small"
                  class="preset-del-btn"
                  @click.stop="confirmDeletePreset(p)"
                >
                  <el-icon><Delete /></el-icon>
                </el-button>
              </div>
            </el-option>
          </el-option-group>
        </el-select>

        <el-button
          type="primary"
          plain
          size="default"
          class="save-preset-btn"
          @click="openSavePresetDialog"
        >
          另存为预设
        </el-button>
      </div>

      <!-- 1. 基础检索维度控制矩阵 (Base Filter Matrix) -->
      <div class="filter-section-card base-matrix-card">
        <div class="section-header" @click="isBaseSectionCollapsed = !isBaseSectionCollapsed">
          <div class="section-title">
            <el-icon class="section-icon"><Search /></el-icon>
            <span>基础检索维度</span>
            <el-tag size="small" type="info" class="section-badge">
              {{ activeBaseCount }} 项生效
            </el-tag>
          </div>
          <el-button link size="small" class="collapse-toggle-btn">
            <el-icon><ArrowUp v-if="!isBaseSectionCollapsed" /><ArrowDown v-else /></el-icon>
          </el-button>
        </div>

        <div v-show="!isBaseSectionCollapsed" class="section-body base-matrix-body">
          <!-- 关键词输入行 -->
          <div class="matrix-row">
            <el-input
              v-model="draftBase.keyword"
              placeholder="搜索报文内容、模块名或事件助记符..."
              prefix-icon="Search"
              clearable
              size="default"
              class="base-keyword-input"
              @input="debouncedPreview"
              @clear="debouncedPreview"
            />
          </div>

          <!-- 时间区间与快捷药丸 -->
          <div class="matrix-row time-row">
            <div class="time-picker-group">
              <el-date-picker
                v-model="draftBase.timeStart"
                type="datetime"
                placeholder="起始时间"
                size="default"
                format="YYYY-MM-DD HH:mm:ss"
                value-format="YYYY-MM-DDTHH:mm:ssZ"
                clearable
                class="matrix-time-picker"
                @change="debouncedPreview"
              />
              <span class="time-sep">~</span>
              <el-date-picker
                v-model="draftBase.timeEnd"
                type="datetime"
                placeholder="截止时间"
                size="default"
                format="YYYY-MM-DD HH:mm:ss"
                value-format="YYYY-MM-DDTHH:mm:ssZ"
                clearable
                class="matrix-time-picker"
                @change="debouncedPreview"
              />
            </div>
            <div class="quick-time-pills">
              <el-button size="small" plain @click="applyQuickTime('all')">全部时间</el-button>
              <el-button size="small" plain @click="applyQuickTime('1h')">近 1 小时</el-button>
              <el-button size="small" plain @click="applyQuickTime('24h')">近 24 小时</el-button>
            </div>
          </div>

          <!-- 设备、级别、状态与排序网格 -->
          <div class="matrix-grid">
            <!-- 设备选择 -->
            <div class="grid-item">
              <span class="grid-label">设备范围：</span>
              <el-select
                v-model="draftBase.deviceId"
                placeholder="全部设备"
                clearable
                size="default"
                class="grid-control"
                @change="debouncedPreview"
              >
                <el-option label="全部设备" :value="null" />
                <el-option
                  v-for="d in taskDevices"
                  :key="d.id"
                  :label="`${d.device_name} (${d.log_count}条)`"
                  :value="d.id"
                />
              </el-select>
            </div>

            <!-- 严重级别 -->
            <div class="grid-item">
              <span class="grid-label">严重级别：</span>
              <el-select
                v-model="draftBase.severity"
                placeholder="全部级别"
                clearable
                size="default"
                class="grid-control"
                @change="debouncedPreview"
              >
                <el-option label="全部级别" :value="null" />
                <el-option label="≤ 2 (紧急/告警)" :value="2" />
                <el-option label="≤ 4 (错误及以上)" :value="4" />
                <el-option label="≤ 6 (通知及以上)" :value="6" />
              </el-select>
            </div>

            <!-- 知识库匹配态 -->
            <div class="grid-item">
              <span class="grid-label">匹配状态：</span>
              <el-select
                v-model="draftBase.matched"
                placeholder="全部状态"
                clearable
                size="default"
                class="grid-control"
                @change="debouncedPreview"
              >
                <el-option label="全部状态" :value="null" />
                <el-option label="已收录知识库" :value="true" />
                <el-option label="未匹配知识库" :value="false" />
              </el-select>
            </div>

            <!-- 排序方式 -->
            <div class="grid-item">
              <span class="grid-label">排序方式：</span>
              <el-select
                v-model="currentSortOption"
                placeholder="排序方式"
                size="default"
                class="grid-control"
                @change="debouncedPreview"
              >
                <el-option label="时间正序 (最早在前)" value="time_asc" />
                <el-option label="时间倒序 (最新在前)" value="time_desc" />
                <el-option label="原始入库顺序" value="id_asc" />
              </el-select>
            </div>
          </div>
        </div>
      </div>

      <!-- 2. 标签多模态筛选面板 (Tag Filter Ecosystem) -->
      <div class="filter-section-card tag-matrix-card">
        <div class="section-header">
          <div class="section-title">
            <el-icon class="section-icon"><PriceTag /></el-icon>
            <span>标签多模态约束</span>
          </div>
          <el-checkbox
            v-model="draftBase.untaggedOnly"
            class="untagged-checkbox"
            @change="handleUntaggedChange"
          >
            仅看未打标日志
          </el-checkbox>
        </div>

        <div class="section-body tag-matrix-body" :class="{ 'is-disabled-body': draftBase.untaggedOnly }">
          <!-- 包含标签行 -->
          <div class="tag-control-row">
            <span class="tag-control-label">包含标签：</span>
            <el-select
              v-model="draftBase.tagIds"
              multiple
              collapse-tags
              collapse-tags-tooltip
              clearable
              placeholder="选择必须包含的标签..."
              size="default"
              :disabled="draftBase.untaggedOnly"
              class="tag-select-box"
              @change="debouncedPreview"
            >
              <el-option
                v-for="t in tagStore.tags"
                :key="t.id"
                :label="`${t.name} (${t.log_count || 0})`"
                :value="t.id"
              >
                <div class="tag-option-item">
                  <span>{{ t.name }} ({{ t.log_count || 0 }})</span>
                  <span
                    class="tag-dot-indicator"
                    :style="{ backgroundColor: t.color || DEFAULT_TAG_COLOR }"
                  />
                </div>
              </el-option>
            </el-select>

            <el-button-group class="tag-logic-toggle">
              <el-button
                size="default"
                :type="draftBase.tagLogic === 'any' ? 'primary' : 'default'"
                :disabled="draftBase.untaggedOnly || draftBase.tagIds.length <= 1"
                @click="setTagLogic('any')"
              >
                任一 (OR)
              </el-button>
              <el-button
                size="default"
                :type="draftBase.tagLogic === 'all' ? 'primary' : 'default'"
                :disabled="draftBase.untaggedOnly || draftBase.tagIds.length <= 1"
                @click="setTagLogic('all')"
              >
                全部 (AND)
              </el-button>
            </el-button-group>
          </div>

          <!-- 排除标签行 -->
          <div class="tag-control-row exclude-row">
            <span class="tag-control-label exclude-label">排除标签：</span>
            <el-select
              v-model="draftBase.excludeTagIds"
              multiple
              collapse-tags
              collapse-tags-tooltip
              clearable
              placeholder="选择严格排除的标签 (如: 已知误报)..."
              size="default"
              :disabled="draftBase.untaggedOnly"
              class="tag-select-box exclude-select-box"
              @change="debouncedPreview"
            >
              <el-option
                v-for="t in tagStore.tags"
                :key="t.id"
                :label="`${t.name} (${t.log_count || 0})`"
                :value="t.id"
              >
                <div class="tag-option-item">
                  <span class="danger-text">{{ t.name }} ({{ t.log_count || 0 }})</span>
                  <span
                    class="tag-dot-indicator"
                    :style="{ backgroundColor: t.color || DEFAULT_TAG_COLOR }"
                  />
                </div>
              </el-option>
            </el-select>
          </div>
        </div>
      </div>

      <!-- 3. 高级规则构造器 (Advanced Rule Builder) -->
      <div class="filter-section-card rule-builder-card">
        <div class="section-header">
          <div class="section-title">
            <el-icon class="section-icon"><Opportunity /></el-icon>
            <span>高级规则构造器</span>
            <span class="rule-limit-hint">({{ localAdvanced.conditions.length }}/15)</span>
          </div>

          <!-- 顶层组合逻辑切换 -->
          <div class="logic-switch-wrapper">
            <span class="logic-label">规则间逻辑：</span>
            <el-radio-group v-model="localAdvanced.logic" size="small" @change="debouncedPreview">
              <el-radio-button label="AND">全部满足 (AND)</el-radio-button>
              <el-radio-button label="OR">满足任一 (OR)</el-radio-button>
            </el-radio-group>
          </div>
        </div>

        <div class="section-body rule-builder-body">
          <!-- 条件行列表 -->
          <div class="conditions-list">
            <div
              v-for="(cond, index) in localAdvanced.conditions"
              :key="index"
              class="condition-row"
              :class="{ 'is-disabled-row': cond.disabled }"
            >
              <!-- 行级启用/禁用复选框 -->
              <el-checkbox
                v-model="cond.disabled"
                :true-label="false"
                :false-label="true"
                class="cond-active-checkbox"
                title="勾选启用该规则，取消勾选临时停用"
                @change="debouncedPreview"
              />

              <div class="row-index">#{{ index + 1 }}</div>

              <!-- 字段选择 -->
              <el-select
                v-model="cond.field"
                placeholder="字段"
                size="default"
                class="field-select"
                :disabled="cond.disabled"
                @change="onFieldChange(cond)"
              >
                <el-option-group label="核心报文">
                  <el-option label="原始报文 (raw_log)" value="raw_log" />
                  <el-option label="消息正文 (message_body)" value="message_body" />
                  <el-option label="事件简名 (brief)" value="brief" />
                  <el-option label="模块名 (module)" value="module" />
                  <el-option label="主机名 (hostname)" value="hostname" />
                  <el-option label="插槽槽位 (slot_info)" value="slot_info" />
                </el-option-group>
                <el-option-group label="动态参数 (KV)">
                  <el-option label="参数提取 (parameters)" value="parameters" />
                </el-option-group>
                <el-option-group label="标签维度">
                  <el-option label="标签关联 (tag)" value="tag" />
                </el-option-group>
                <el-option-group label="审计元数据">
                  <el-option label="来源文件 (source_file)" value="source_file" />
                  <el-option label="严重级别 (severity)" value="severity" />
                  <el-option label="匹配分层 (match_tier)" value="match_tier" />
                  <el-option label="置信度 (match_confidence)" value="match_confidence" />
                  <el-option label="知识库ID (knowledge_id)" value="knowledge_id" />
                </el-option-group>
              </el-select>

              <!-- 操作符选择 + 一键取反按钮 -->
              <div class="op-wrapper">
                <el-select
                  v-model="cond.op"
                  placeholder="操作符"
                  size="default"
                  class="op-select"
                  :disabled="cond.disabled"
                  @change="onOpChange(cond)"
                >
                  <el-option
                    v-for="op in getAvailableOps(cond.field)"
                    :key="op.value"
                    :label="op.label"
                    :value="op.value"
                  />
                </el-select>

                <el-tooltip content="一键取反 (Invert/NOT)" placement="top">
                  <el-button
                    link
                    size="small"
                    class="invert-op-btn"
                    :disabled="cond.disabled"
                    @click="invertConditionOp(cond)"
                  >
                    <el-icon><RefreshRight /></el-icon>
                  </el-button>
                </el-tooltip>
              </div>

              <!-- 值输入区 (针对 tag、facets 联想与常规输入) -->
              <div class="value-input-wrapper">
                <!-- 场景 A: 字段为 tag 且属于无值操作符 (is_empty / is_not_empty) -->
                <el-input
                  v-if="cond.field === 'tag' && (cond.op === 'is_empty' || cond.op === 'is_not_empty')"
                  model-value="（无须指定值）"
                  disabled
                  size="default"
                />

                <!-- 场景 B: 字段为 tag 且需选择标签 (has_any / has_all / not_has) -->
                <el-select
                  v-else-if="cond.field === 'tag'"
                  :model-value="getTagSelectedIDs(cond)"
                  multiple
                  collapse-tags
                  collapse-tags-tooltip
                  placeholder="选择关联标签..."
                  size="default"
                  :disabled="cond.disabled"
                  class="tag-value-select"
                  @update:model-value="(val) => setTagSelectedIDs(cond, val)"
                >
                  <el-option
                    v-for="t in tagStore.tags"
                    :key="t.id"
                    :label="`${t.name} (${t.log_count || 0})`"
                    :value="t.id"
                  >
                    <div class="tag-option-item">
                      <span>{{ t.name }} ({{ t.log_count || 0 }})</span>
                      <span
                        class="tag-dot-indicator"
                        :style="{ backgroundColor: t.color || DEFAULT_TAG_COLOR }"
                      />
                    </div>
                  </el-option>
                </el-select>

                <!-- 场景 C: 针对 module、brief、hostname、source_file 提供带频次的输入联想 -->
                <el-autocomplete
                  v-else-if="['module', 'brief', 'hostname', 'source_file'].includes(cond.field)"
                  v-model="cond.value"
                  :fetch-suggestions="(queryString, cb) => queryFacetSuggestions(cond.field, queryString, cb)"
                  :placeholder="getValuePlaceholder(cond)"
                  clearable
                  size="default"
                  :disabled="cond.disabled"
                  @input="onValueInput"
                  @select="onValueInput"
                >
                  <template #default="{ item }">
                    <div class="facet-suggest-item">
                      <span class="facet-suggest-val">{{ item.value }}</span>
                      <span class="facet-suggest-cnt">{{ item.count }} 次</span>
                    </div>
                  </template>
                </el-autocomplete>

                <!-- 场景 D: 常规文本/参数/正则输入框 -->
                <template v-else>
                  <el-input
                    v-model="cond.value"
                    :placeholder="getValuePlaceholder(cond)"
                    clearable
                    size="default"
                    :disabled="cond.disabled"
                    @input="onValueInput"
                  />
                  <!-- 正则语法检验提示 -->
                  <div v-if="getRegexError(cond)" class="regex-error-tip">
                    {{ getRegexError(cond) }}
                  </div>
                </template>
              </div>

              <!-- 删除单行按钮 -->
              <el-button
                type="danger"
                link
                class="delete-btn"
                title="删除此规则"
                @click="removeCondition(index)"
              >
                <el-icon><Delete /></el-icon>
              </el-button>
            </div>

            <!-- 空条件引导 -->
            <div v-if="localAdvanced.conditions.length === 0" class="empty-conditions">
              <p class="empty-tip">暂无高级条件，点击下方按钮添加规则</p>
            </div>
          </div>

          <!-- 添加条件按钮 -->
          <div class="add-bar">
            <el-button
              type="primary"
              plain
              size="small"
              :disabled="localAdvanced.conditions.length >= 15"
              @click="addCondition"
            >
              + 添加筛选条件 ({{ localAdvanced.conditions.length }}/15)
            </el-button>
          </div>
        </div>
      </div>

      <!-- 4. 实时匹配预览 -->
      <div class="preview-box">
        <div class="preview-info">
          <el-icon class="preview-icon"><Aim /></el-icon>
          <span class="preview-title">当前全量复合条件匹配预览：</span>
          <span v-if="previewLoading" class="preview-loading">计算中...</span>
          <span v-else class="preview-count">
            <strong>{{ previewTotal }}</strong> 条日志
          </span>
        </div>
        <div class="preview-hint">
          * 预览结果已实时叠加基础检索维度、标签多模态约束与高级启用的规则条件
        </div>
      </div>
    </div>

    <!-- 底部操作条 -->
    <template #footer>
      <div class="drawer-footer">
        <div class="footer-left-actions">
          <el-button @click="resetToDefaults">清空全部条件</el-button>
          <el-button @click="clearAdvancedOnly">仅清空高级条件</el-button>
        </div>

        <div class="footer-right-actions">
          <el-button
            type="warning"
            plain
            :disabled="previewTotal <= 0"
            @click="openBatchTagDialog"
          >
            <el-icon><PriceTag /></el-icon>
            <span>批量打标…</span>
          </el-button>
          <el-button
            type="danger"
            plain
            :disabled="previewTotal <= 0"
            @click="openBatchUntagDialog"
          >
            <el-icon><PriceTag /></el-icon>
            <span>批量摘标…</span>
          </el-button>
          <el-button type="primary" @click="applyFilters">
            应用筛选并查看
          </el-button>
        </div>
      </div>
    </template>

    <!-- 另存为预设弹窗 (Phase 2) -->
    <el-dialog
      v-model="savePresetDialogVisible"
      title="将当前筛选条件另存为任务预设"
      width="440px"
      append-to-body
      destroy-on-close
    >
      <el-form label-position="top">
        <el-form-item label="预设名称" required>
          <el-input
            v-model="newPresetName"
            placeholder="例如: 核心BGP震荡排查 / 未打标告警清单"
            maxlength="48"
          />
        </el-form-item>
        <el-form-item label="场景说明 (可选)">
          <el-input
            v-model="newPresetDescription"
            type="textarea"
            rows="2"
            placeholder="简要描述适用场景与排查意图..."
            maxlength="200"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="savePresetDialogVisible = false">取消</el-button>
        <el-button
          type="primary"
          :loading="savingPresetLoading"
          @click="confirmSavePreset"
        >
          保存预设
        </el-button>
      </template>
    </el-dialog>

    <!-- 批量打标确认弹窗 -->
    <el-dialog
      v-model="batchTagDialogVisible"
      title="按当前筛选结果批量打标签"
      width="440px"
      append-to-body
      destroy-on-close
    >
      <div class="batch-tag-body">
        <p class="batch-tag-tip">
          将为当前筛选命中的 <strong>{{ previewTotal }}</strong> 条日志添加指定标签：
        </p>

        <el-alert
          v-if="previewTotal > 50000"
          type="warning"
          :closable="false"
          class="batch-alert"
        >
          命中数量较大 ({{ previewTotal }} 条)，批量打标预计耗时数秒，请谨慎操作。
        </el-alert>

        <el-form label-position="top">
          <el-form-item label="选择已有标签">
            <el-select
              v-model="selectedTagId"
              placeholder="选择已有标签"
              style="width: 100%"
              filterable
            >
              <el-option
                v-for="t in tagStore.tags"
                :key="t.id"
                :label="t.name"
                :value="t.id"
              >
                <div class="tag-option-item">
                  <span>{{ t.name }}</span>
                  <span
                    class="tag-dot-indicator"
                    :style="{ backgroundColor: t.color || DEFAULT_TAG_COLOR }"
                  />
                </div>
              </el-option>
            </el-select>
          </el-form-item>

          <el-divider>或快速新建标签</el-divider>

          <el-form-item label="新标签名称">
            <el-input
              v-model="newTagName"
              placeholder="如: BGP振荡 / 待复核 / 核心接口"
              maxlength="32"
            />
          </el-form-item>
          <el-form-item label="标签颜色">
            <el-color-picker v-model="newTagColor" :predefine="PREDEFINE_TAG_COLORS" />
          </el-form-item>
        </el-form>
      </div>
      <template #footer>
        <el-button @click="batchTagDialogVisible = false">取消</el-button>
        <el-button
          type="primary"
          :loading="taggingLoading"
          @click="confirmBatchTag"
        >
          确认打标
        </el-button>
      </template>
    </el-dialog>

    <!-- 批量摘标确认弹窗 -->
    <el-dialog
      v-model="batchUntagDialogVisible"
      title="按当前筛选结果批量摘除标签"
      width="440px"
      append-to-body
      destroy-on-close
    >
      <div class="batch-tag-body">
        <p class="batch-tag-tip">
          将为当前筛选命中的 <strong>{{ previewTotal }}</strong> 条日志移除指定标签关联：
        </p>

        <el-alert
          v-if="previewTotal > 50000"
          type="warning"
          :closable="false"
          class="batch-alert"
        >
          命中数量较大 ({{ previewTotal }} 条)，批量摘标预计耗时数秒，请谨慎操作。
        </el-alert>

        <el-form label-position="top">
          <el-form-item label="选择要摘除的标签" required>
            <el-select
              v-model="selectedUntagId"
              placeholder="选择已有标签"
              style="width: 100%"
              filterable
            >
              <el-option
                v-for="t in tagStore.tags"
                :key="t.id"
                :label="`${t.name} (${t.log_count || 0})`"
                :value="t.id"
              >
                <div class="tag-option-item">
                  <span>{{ t.name }}</span>
                  <span
                    class="tag-dot-indicator"
                    :style="{ backgroundColor: t.color || DEFAULT_TAG_COLOR }"
                  />
                </div>
              </el-option>
            </el-select>
          </el-form-item>
        </el-form>
      </div>
      <template #footer>
        <el-button @click="batchUntagDialogVisible = false">取消</el-button>
        <el-button
          type="danger"
          :loading="untaggingLoading"
          @click="confirmBatchUntag"
        >
          确认摘标
        </el-button>
      </template>
    </el-dialog>

    <!-- 批量处理异步进度弹窗 -->
    <ImportProgressModal
      v-model="showProgressModal"
      :job-id="progressJobId"
      title="批量标签处理流水线"
      @completed="handleBatchCompleted"
    />
  </el-drawer>
</template>

<script setup>
import { ref, reactive, computed, onUnmounted } from 'vue'
import {
  Delete,
  PriceTag,
  Aim,
  Opportunity,
  Search,
  ArrowDown,
  ArrowUp,
  RefreshRight,
  CollectionTag
} from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import api from '@/api'
import useFilterStore from '@/stores/filter'
import useTagStore from '@/stores/tag'
import useTaskStore from '@/stores/task'
import { PREDEFINE_TAG_COLORS, DEFAULT_TAG_COLOR } from '@/constants/tagColors'
import { SYSTEM_EXPERT_PRESETS } from '@/constants/filterPresets'
import ImportProgressModal from '@/components/ImportProgressModal.vue'

const props = defineProps({
  taskId: {
    type: String,
    default: ''
  },
  taskDevices: {
    type: Array,
    default: () => []
  }
})

const emit = defineEmits(['apply', 'reset'])

const filterStore = useFilterStore()
const tagStore = useTagStore()
const taskStore = useTaskStore()

const visible = ref(false)
const previewLoading = ref(false)
const previewTotal = ref(0)
const isBaseSectionCollapsed = ref(false)

// 有效任务 ID 计算 (优先 props，兜底 store)
const effectiveTaskId = computed(() => props.taskId || taskStore.currentTaskId || '')

// 预设列表与选中状态 (Phase 2)
const selectedPresetId = ref('')
const customPresets = ref([])
const savePresetDialogVisible = ref(false)
const newPresetName = ref('')
const newPresetDescription = ref('')
const savingPresetLoading = ref(false)

// 基础检索草稿状态
const draftBase = reactive({
  keyword: '',
  timeStart: null,
  timeEnd: null,
  deviceId: null,
  severity: null,
  matched: null,
  sortBy: 'time',
  order: 'asc',
  tagIds: [],
  tagLogic: 'any',
  excludeTagIds: [],
  untaggedOnly: false
})

// 高级规则草稿状态
const localAdvanced = reactive({
  logic: 'AND',
  conditions: []
})

// 快照用于脏检查 (isDirty)
let snapshotJson = ''

const currentSortOption = computed({
  get() {
    const { sortBy, order } = draftBase
    if (sortBy === 'time' && order === 'asc') return 'time_asc'
    if (sortBy === 'time' && order === 'desc') return 'time_desc'
    if (sortBy === 'id' && order === 'asc') return 'id_asc'
    return 'time_asc'
  },
  set(val) {
    if (val === 'time_asc') {
      draftBase.sortBy = 'time'
      draftBase.order = 'asc'
    } else if (val === 'time_desc') {
      draftBase.sortBy = 'time'
      draftBase.order = 'desc'
    } else if (val === 'id_asc') {
      draftBase.sortBy = 'id'
      draftBase.order = 'asc'
    }
  }
})

// 活跃基础筛选数量计算
const activeBaseCount = computed(() => {
  let count = 0
  if (draftBase.keyword) count++
  if (draftBase.timeStart || draftBase.timeEnd) count++
  if (draftBase.deviceId) count++
  if (draftBase.severity !== null && draftBase.severity !== undefined) count++
  if (draftBase.matched !== null && draftBase.matched !== undefined) count++
  if (draftBase.untaggedOnly) {
    count++
  } else {
    if (draftBase.tagIds.length > 0) count++
    if (draftBase.excludeTagIds.length > 0) count++
  }
  return count
})

// 加载预设列表
const loadPresets = async () => {
  if (!effectiveTaskId.value) return
  try {
    const res = await api.listFilterPresets(effectiveTaskId.value)
    if (res.code === 0 && Array.isArray(res.data)) {
      customPresets.value = res.data
    }
  } catch (e) {
    console.warn('Load filter presets failed:', e)
  }
}

// 打开 Drawer
const open = () => {
  const f = filterStore.filters

  // 1. 同步基础检索条件到草稿
  draftBase.keyword = f.keyword || ''
  draftBase.timeStart = f.timeStart || null
  draftBase.timeEnd = f.timeEnd || null
  draftBase.deviceId = f.deviceId || null
  draftBase.severity = f.severity ?? null
  draftBase.matched = f.matched ?? null
  draftBase.sortBy = f.sortBy || 'time'
  draftBase.order = f.order || 'asc'
  draftBase.tagIds = Array.isArray(f.tagIds) ? [...f.tagIds] : []
  draftBase.tagLogic = f.tagLogic || 'any'
  draftBase.excludeTagIds = Array.isArray(f.excludeTagIds) ? [...f.excludeTagIds] : []
  draftBase.untaggedOnly = !!f.untaggedOnly

  // 2. 同步高级条件到草稿
  const currentAdv = f.advanced || { logic: 'AND', conditions: [] }
  localAdvanced.logic = currentAdv.logic || 'AND'
  localAdvanced.conditions = JSON.parse(JSON.stringify(currentAdv.conditions || []))
  if (localAdvanced.conditions.length === 0) {
    addCondition()
  }

  // 3. 记录打开快照
  snapshotJson = serializeCurrentDraft()
  selectedPresetId.value = ''

  visible.value = true
  if (effectiveTaskId.value) {
    tagStore.fetchTags(effectiveTaskId.value)
    loadPresets()
  }
  fetchPreviewCount()
}

// 外部上下文一键钻取支持 (One-Click Pivot)
const openWithCondition = (cond, autoApply = false) => {
  open()
  if (
    localAdvanced.conditions.length === 1 &&
    !localAdvanced.conditions[0].value &&
    localAdvanced.conditions[0].field === 'raw_log'
  ) {
    localAdvanced.conditions = []
  }
  localAdvanced.conditions.push({
    disabled: false,
    field: cond.field || 'raw_log',
    op: cond.op || 'contains',
    value: cond.value || ''
  })
  if (autoApply) {
    applyFilters()
  } else {
    debouncedPreview()
  }
}

const openWithTimePivot = (timeISO, windowMinutes = 5, autoApply = false) => {
  open()
  const target = new Date(timeISO)
  if (!isNaN(target.getTime())) {
    const ms = windowMinutes * 60 * 1000
    draftBase.timeStart = new Date(target.getTime() - ms).toISOString()
    draftBase.timeEnd = new Date(target.getTime() + ms).toISOString()
    if (autoApply) {
      applyFilters()
    } else {
      debouncedPreview()
    }
  }
}

const serializeCurrentDraft = () => {
  return JSON.stringify({
    base: draftBase,
    adv: localAdvanced
  })
}

// 检查是否有未保存的变更
const isDirty = computed(() => {
  if (!visible.value) return false
  return serializeCurrentDraft() !== snapshotJson
})

// 抽屉退出拦截
const handleBeforeClose = (done) => {
  if (isDirty.value) {
    ElMessageBox.confirm('当前筛选条件已修改但未应用，确定退出并放弃修改吗？', '放弃修改提示', {
      confirmButtonText: '放弃修改',
      cancelButtonText: '继续编辑',
      type: 'warning'
    })
      .then(() => {
        done()
      })
      .catch(() => {})
  } else {
    done()
  }
}

defineExpose({
  open,
  openWithCondition,
  openWithTimePivot,
  visible
})

// 预设选中加载
const onPresetSelected = (val) => {
  if (!val) return

  // 1. 查找系统专家预设
  const sys = SYSTEM_EXPERT_PRESETS.find(p => p.id === val)
  if (sys) {
    applyPresetConfig(sys.filters)
    ElMessage.success(`已载入专家场景: ${sys.name}`)
    return
  }

  // 2. 查找自定义任务预设
  const custom = customPresets.value.find(p => String(p.id) === String(val))
  if (custom && custom.filters) {
    applyPresetConfig(custom.filters)
    ElMessage.success(`已载入自定义预设: ${custom.name}`)
  }
}

const applyPresetConfig = (cfg) => {
  if (!cfg) return
  if (cfg.keyword !== undefined) draftBase.keyword = cfg.keyword || ''
  if (cfg.severity !== undefined) draftBase.severity = cfg.severity
  if (cfg.matched !== undefined) draftBase.matched = cfg.matched
  if (cfg.deviceId !== undefined) draftBase.deviceId = cfg.deviceId
  if (cfg.timeStart !== undefined) draftBase.timeStart = cfg.timeStart
  if (cfg.timeEnd !== undefined) draftBase.timeEnd = cfg.timeEnd
  if (cfg.untaggedOnly !== undefined) draftBase.untaggedOnly = !!cfg.untaggedOnly
  if (cfg.tagIds !== undefined) draftBase.tagIds = [...cfg.tagIds]
  if (cfg.tagLogic !== undefined) draftBase.tagLogic = cfg.tagLogic
  if (cfg.excludeTagIds !== undefined) draftBase.excludeTagIds = [...cfg.excludeTagIds]

  if (cfg.advanced) {
    localAdvanced.logic = cfg.advanced.logic || 'AND'
    localAdvanced.conditions = JSON.parse(JSON.stringify(cfg.advanced.conditions || []))
  }
  debouncedPreview()
}

// 另存为预设
const openSavePresetDialog = () => {
  newPresetName.value = ''
  newPresetDescription.value = ''
  savePresetDialogVisible.value = true
}

const confirmSavePreset = async () => {
  if (!newPresetName.value.trim()) {
    ElMessage.warning('请输入预设名称')
    return
  }

  savingPresetLoading.value = true
  try {
    const validConds = localAdvanced.conditions.filter(
      c => c.field && c.op && (
        (c.field === 'tag' && (c.op === 'is_empty' || c.op === 'is_not_empty')) ||
        (c.value !== undefined && c.value !== '' && !getRegexError(c))
      )
    )

    const payload = {
      name: newPresetName.value.trim(),
      description: newPresetDescription.value.trim(),
      filters: {
        keyword: draftBase.keyword || undefined,
        severity: draftBase.severity ?? undefined,
        matched: draftBase.matched ?? undefined,
        device_id: draftBase.deviceId || undefined,
        time_start: draftBase.timeStart || undefined,
        time_end: draftBase.timeEnd || undefined,
        tag_ids: draftBase.untaggedOnly ? undefined : (draftBase.tagIds.length > 0 ? draftBase.tagIds : undefined),
        tag_logic: draftBase.tagLogic,
        exclude_tag_ids: draftBase.untaggedOnly ? undefined : (draftBase.excludeTagIds.length > 0 ? draftBase.excludeTagIds : undefined),
        untagged_only: draftBase.untaggedOnly || undefined,
        advanced: validConds.length > 0 ? {
          logic: localAdvanced.logic || 'AND',
          conditions: validConds
        } : undefined
      }
    }

    const res = await api.createFilterPreset(effectiveTaskId.value, payload)
    if (res.code === 0) {
      ElMessage.success('预设保存成功')
      savePresetDialogVisible.value = false
      await loadPresets()
      selectedPresetId.value = String(res.data.id)
    }
  } catch (e) {
    ElMessage.error(e.message || '保存预设失败')
  } finally {
    savingPresetLoading.value = false
  }
}

// 删除自定义预设
const confirmDeletePreset = (preset) => {
  ElMessageBox.confirm(`确定删除自定义预设「${preset.name}」吗？`, '删除确认', {
    confirmButtonText: '删除',
    cancelButtonText: '取消',
    type: 'warning'
  })
    .then(async () => {
      try {
        const res = await api.deleteFilterPreset(effectiveTaskId.value, preset.id)
        if (res.code === 0) {
          ElMessage.success('预设已删除')
          if (selectedPresetId.value === String(preset.id)) {
            selectedPresetId.value = ''
          }
          await loadPresets()
        }
      } catch (e) {
        ElMessage.error(e.message || '删除预设失败')
      }
    })
    .catch(() => {})
}

// 快捷时间设置
const applyQuickTime = (type) => {
  if (type === 'all') {
    draftBase.timeStart = null
    draftBase.timeEnd = null
  } else if (type === '1h') {
    const end = new Date()
    const start = new Date(end.getTime() - 60 * 60 * 1000)
    draftBase.timeEnd = end.toISOString()
    draftBase.timeStart = start.toISOString()
  } else if (type === '24h') {
    const end = new Date()
    const start = new Date(end.getTime() - 24 * 60 * 60 * 1000)
    draftBase.timeEnd = end.toISOString()
    draftBase.timeStart = start.toISOString()
  }
  debouncedPreview()
}

// 标签与未打标互斥
const handleUntaggedChange = (val) => {
  if (val) {
    draftBase.tagIds = []
    draftBase.excludeTagIds = []
  }
  debouncedPreview()
}

const setTagLogic = (logic) => {
  draftBase.tagLogic = logic
  debouncedPreview()
}

// 添加条件 (上限 15 条)
const addCondition = () => {
  if (localAdvanced.conditions.length >= 15) return
  localAdvanced.conditions.push({
    disabled: false,
    field: 'raw_log',
    op: 'contains',
    value: ''
  })
  debouncedPreview()
}

// 移除条件
const removeCondition = (index) => {
  localAdvanced.conditions.splice(index, 1)
  debouncedPreview()
}

// 字段与操作符白名单联动配置
const operatorMap = {
  raw_log: [
    { label: '包含', value: 'contains' },
    { label: '不包含', value: 'not_contains' },
    { label: '正则匹配', value: 'regex' },
    { label: '正则不匹配', value: 'not_regex' },
    { label: '精确等于', value: 'eq' },
    { label: '前缀匹配', value: 'prefix' },
    { label: '后缀匹配', value: 'suffix' }
  ],
  message_body: [
    { label: '包含', value: 'contains' },
    { label: '不包含', value: 'not_contains' },
    { label: '正则匹配', value: 'regex' },
    { label: '正则不匹配', value: 'not_regex' },
    { label: '精确等于', value: 'eq' },
    { label: '前缀匹配', value: 'prefix' },
    { label: '后缀匹配', value: 'suffix' }
  ],
  brief: [
    { label: '包含', value: 'contains' },
    { label: '不包含', value: 'not_contains' },
    { label: '正则匹配', value: 'regex' },
    { label: '正则不匹配', value: 'not_regex' },
    { label: '前缀匹配', value: 'prefix' },
    { label: '后缀匹配', value: 'suffix' },
    { label: '多值包含 (逗号分隔)', value: 'in' },
    { label: '多值排除 (逗号分隔)', value: 'not_in' },
    { label: '精确等于', value: 'eq' }
  ],
  module: [
    { label: '包含', value: 'contains' },
    { label: '不包含', value: 'not_contains' },
    { label: '正则匹配', value: 'regex' },
    { label: '正则不匹配', value: 'not_regex' },
    { label: '前缀匹配', value: 'prefix' },
    { label: '后缀匹配', value: 'suffix' },
    { label: '多值包含 (逗号分隔)', value: 'in' },
    { label: '多值排除 (逗号分隔)', value: 'not_in' },
    { label: '精确等于', value: 'eq' }
  ],
  hostname: [
    { label: '包含', value: 'contains' },
    { label: '不包含', value: 'not_contains' },
    { label: '正则匹配', value: 'regex' },
    { label: '正则不匹配', value: 'not_regex' },
    { label: '前缀匹配', value: 'prefix' },
    { label: '后缀匹配', value: 'suffix' },
    { label: '精确等于', value: 'eq' }
  ],
  slot_info: [
    { label: '包含', value: 'contains' },
    { label: '不包含', value: 'not_contains' },
    { label: '正则匹配', value: 'regex' },
    { label: '正则不匹配', value: 'not_regex' },
    { label: '前缀匹配', value: 'prefix' },
    { label: '后缀匹配', value: 'suffix' },
    { label: '精确等于', value: 'eq' }
  ],
  source_file: [
    { label: '精确等于', value: 'eq' },
    { label: '包含', value: 'contains' },
    { label: '前缀匹配', value: 'prefix' }
  ],
  parameters: [
    { label: 'KV 包含 (如 PeerIP=10.)', value: 'kv_contains' },
    { label: 'KV 不包含', value: 'kv_not_contains' },
    { label: 'KV 精确等于 (如 PeerIP=10.1.1.1)', value: 'kv_eq' },
    { label: 'KV 正则 (如 PeerIP=^10\\.)', value: 'kv_regex' },
    { label: 'KV 正则不匹配', value: 'kv_not_regex' }
  ],
  tag: [
    { label: '包含任一 (has_any)', value: 'has_any' },
    { label: '同时具备 (has_all)', value: 'has_all' },
    { label: '排除包含 (not_has)', value: 'not_has' },
    { label: '未打标 (is_empty)', value: 'is_empty' },
    { label: '已有标签 (is_not_empty)', value: 'is_not_empty' }
  ],
  severity: [
    { label: '级别 <=', value: 'lte' },
    { label: '级别 >=', value: 'gte' },
    { label: '级别 <', value: 'lt' },
    { label: '级别 >', value: 'gt' },
    { label: '级别 =', value: 'eq' },
    { label: '多级别 (1,2,3)', value: 'in' }
  ],
  match_tier: [
    { label: '等于 (EXACT/MNEMONIC等)', value: 'eq' },
    { label: '包含于', value: 'in' },
    { label: '不属于', value: 'not_in' }
  ],
  match_confidence: [
    { label: '置信度 >=', value: 'gte' },
    { label: '置信度 <=', value: 'lte' },
    { label: '置信度 >', value: 'gt' },
    { label: '置信度 <', value: 'lt' },
    { label: '置信度 =', value: 'eq' }
  ],
  knowledge_id: [
    { label: '知识ID >=', value: 'gte' },
    { label: '知识ID <=', value: 'lte' },
    { label: '知识ID >', value: 'gt' },
    { label: '知识ID <', value: 'lt' },
    { label: '知识ID =', value: 'eq' }
  ]
}

const getAvailableOps = (field) => {
  return operatorMap[field] || operatorMap.raw_log
}

// 一键取反映射 (Phase 2 Invert/NOT)
const invertOpMap = {
  contains: 'not_contains',
  not_contains: 'contains',
  regex: 'not_regex',
  not_regex: 'regex',
  kv_contains: 'kv_not_contains',
  kv_not_contains: 'kv_contains',
  kv_regex: 'kv_not_regex',
  kv_not_regex: 'kv_regex',
  in: 'not_in',
  not_in: 'in',
  has_any: 'not_has',
  not_has: 'has_any',
  is_empty: 'is_not_empty',
  is_not_empty: 'is_empty',
  eq: 'not_contains'
}

const invertConditionOp = (cond) => {
  if (invertOpMap[cond.op]) {
    cond.op = invertOpMap[cond.op]
    debouncedPreview()
  }
}

const onFieldChange = (cond) => {
  const ops = getAvailableOps(cond.field)
  if (ops.length > 0) {
    cond.op = ops[0].value
  }
  cond.value = ''
  debouncedPreview()
}

const onOpChange = (cond) => {
  if (cond.field === 'tag' && (cond.op === 'is_empty' || cond.op === 'is_not_empty')) {
    cond.value = ''
  }
  debouncedPreview()
}

// 标签字段与 ID 列表序列化双向转换
const getTagSelectedIDs = (cond) => {
  if (!cond.value) return []
  return cond.value
    .split(',')
    .map(s => parseInt(s.trim(), 10))
    .filter(n => !isNaN(n) && n > 0)
}

const setTagSelectedIDs = (cond, ids) => {
  cond.value = Array.isArray(ids) ? ids.join(',') : ''
  debouncedPreview()
}

// 元数据候选词联想缓存 (30s TTL 避免高频请求)
const facetCache = new Map()

const queryFacetSuggestions = async (field, queryString, cb) => {
  if (!effectiveTaskId.value) {
    cb([])
    return
  }

  const cacheKey = `${effectiveTaskId.value}:${field}`
  const cached = facetCache.get(cacheKey)
  const now = Date.now()

  let list = []
  if (cached && now - cached.timestamp < 30000) {
    list = cached.data
  } else {
    try {
      const res = await api.getTaskFacets(effectiveTaskId.value, field)
      if (res.code === 0 && Array.isArray(res.data)) {
        list = res.data
        facetCache.set(cacheKey, { timestamp: now, data: list })
      }
    } catch (e) {
      console.warn('Query facets failed:', e)
    }
  }

  const q = (queryString || '').trim().toLowerCase()
  const results = q
    ? list.filter(item => String(item.value).toLowerCase().includes(q))
    : list

  cb(results)
}

const getValuePlaceholder = (cond) => {
  if (cond.field === 'parameters') {
    return 'Key=Value，例如: PeerIP=10.1.1.1'
  }
  if (cond.op === 'regex' || cond.op === 'not_regex' || cond.op === 'kv_regex' || cond.op === 'kv_not_regex') {
    return '输入标准 RE2 正则表达式'
  }
  if (cond.op === 'in' || cond.op === 'not_in') {
    return '多个值以英文逗号分隔'
  }
  return '输入筛选值'
}

// 正则校验提示
const getRegexError = (cond) => {
  if (!cond.value) return ''
  if (cond.op === 'regex' || cond.op === 'not_regex' || cond.op === 'kv_regex' || cond.op === 'kv_not_regex') {
    let pat = cond.value
    if (cond.field === 'parameters') {
      const parts = pat.split('=')
      if (parts.length < 2) {
        return '参数格式必须为 Key=Regex'
      }
      pat = parts.slice(1).join('=')
    }
    // 检测 RE2 不支持的语法
    if (pat.includes('(?<=') || pat.includes('(?<!') || pat.includes('(?=')) {
      return '提示: SQLite RE2 引擎不支持零宽断言/环视'
    }
    try {
      new RegExp(pat)
    } catch (e) {
      return `语法错误: ${e.message}`
    }
  }
  return ''
}

const onValueInput = () => {
  debouncedPreview()
}

// 防抖实时预览与取消控制
let timer = null
let previewAbortController = null

const debouncedPreview = () => {
  if (timer) clearTimeout(timer)
  timer = setTimeout(() => {
    fetchPreviewCount()
  }, 350)
}

onUnmounted(() => {
  if (timer) {
    clearTimeout(timer)
    timer = null
  }
  if (previewAbortController) {
    previewAbortController.abort()
  }
})

const fetchPreviewCount = async () => {
  if (!effectiveTaskId.value) return
  if (previewAbortController) {
    previewAbortController.abort()
  }
  previewAbortController = new AbortController()

  previewLoading.value = true
  try {
    // 过滤未禁用且有效的条件
    const validConds = localAdvanced.conditions.filter(
      c => !c.disabled && c.field && c.op && (
        (c.field === 'tag' && (c.op === 'is_empty' || c.op === 'is_not_empty')) ||
        (c.value !== undefined && c.value !== '' && !getRegexError(c))
      )
    )

    // 组装全量草稿请求体
    const queryBody = {
      page: 1,
      page_size: 1,
      sort_by: draftBase.sortBy,
      order: draftBase.order
    }
    if (draftBase.keyword) queryBody.keyword = draftBase.keyword
    if (draftBase.severity !== null && draftBase.severity !== undefined) queryBody.severity = draftBase.severity
    if (draftBase.matched !== null && draftBase.matched !== undefined) queryBody.matched = !!draftBase.matched
    if (draftBase.deviceId) queryBody.device_id = draftBase.deviceId
    if (draftBase.timeStart) queryBody.time_start = draftBase.timeStart
    if (draftBase.timeEnd) queryBody.time_end = draftBase.timeEnd

    if (draftBase.untaggedOnly) {
      queryBody.untagged_only = true
    } else {
      if (draftBase.tagIds.length > 0) {
        queryBody.tag_ids = draftBase.tagIds
        queryBody.tag_logic = draftBase.tagLogic
      }
      if (draftBase.excludeTagIds.length > 0) {
        queryBody.exclude_tag_ids = draftBase.excludeTagIds
      }
    }

    if (validConds.length > 0) {
      queryBody.advanced = {
        logic: localAdvanced.logic || 'AND',
        conditions: validConds
      }
    }

    const res = await api.queryTaskLogsUnified(effectiveTaskId.value, queryBody, {
      signal: previewAbortController.signal
    })
    if (res.code === 0) {
      previewTotal.value = res.data.total || 0
    }
  } catch (e) {
    if (e.name !== 'CanceledError' && e.name !== 'AbortError') {
      console.error('Fetch preview count failed:', e)
    }
  } finally {
    previewLoading.value = false
  }
}

// 仅清空高级筛选
const clearAdvancedOnly = () => {
  localAdvanced.conditions = []
  debouncedPreview()
  ElMessage.info('已清空高级规则条件')
}

// 清空全部条件 (基础 + 高级)
const resetToDefaults = () => {
  draftBase.keyword = ''
  draftBase.timeStart = null
  draftBase.timeEnd = null
  draftBase.deviceId = null
  draftBase.severity = null
  draftBase.matched = null
  draftBase.sortBy = 'time'
  draftBase.order = 'asc'
  draftBase.tagIds = []
  draftBase.tagLogic = 'any'
  draftBase.excludeTagIds = []
  draftBase.untaggedOnly = false
  localAdvanced.conditions = []
  selectedPresetId.value = ''
  debouncedPreview()
  ElMessage.info('已重置所有筛选草稿')
}

// 应用筛选 (原子化写入 store)
const applyFilters = () => {
  const f = filterStore.filters

  // 1. 同步基础条件
  f.keyword = draftBase.keyword
  f.timeStart = draftBase.timeStart
  f.timeEnd = draftBase.timeEnd
  f.deviceId = draftBase.deviceId
  f.severity = draftBase.severity
  f.matched = draftBase.matched
  f.sortBy = draftBase.sortBy
  f.order = draftBase.order
  f.tagIds = [...draftBase.tagIds]
  f.tagLogic = draftBase.tagLogic
  f.excludeTagIds = [...draftBase.excludeTagIds]
  f.untaggedOnly = draftBase.untaggedOnly

  // 2. 同步高级条件
  const validConds = localAdvanced.conditions.filter(
    c => c.field && c.op && (
      (c.field === 'tag' && (c.op === 'is_empty' || c.op === 'is_not_empty')) ||
      (c.value !== undefined && c.value !== '' && !getRegexError(c))
    )
  )

  f.advanced = {
    logic: localAdvanced.logic || 'AND',
    conditions: validConds
  }

  filterStore.resetPagination()
  snapshotJson = serializeCurrentDraft()
  visible.value = false
  emit('apply')
}

// 批量处理异步进度追踪
const showProgressModal = ref(false)
const progressJobId = ref('')
const pendingTagId = ref(null)

const handleBatchCompleted = async () => {
  showProgressModal.value = false
  ElMessage.success('批量标签处理完成')
  if (pendingTagId.value && !draftBase.tagIds.includes(pendingTagId.value)) {
    draftBase.tagIds.push(pendingTagId.value)
  }
  pendingTagId.value = null
  if (effectiveTaskId.value) {
    await tagStore.fetchTags(effectiveTaskId.value)
  }
  applyFilters()
}

// 批量打标
const batchTagDialogVisible = ref(false)
const selectedTagId = ref(null)
const newTagName = ref('')
const newTagColor = ref(PREDEFINE_TAG_COLORS[0])
const taggingLoading = ref(false)

const openBatchTagDialog = () => {
  if (previewTotal.value > 500000) {
    ElMessage.warning('单次批量打标不能超过 50 万条，请增加筛选条件细化范围')
    return
  }
  selectedTagId.value = tagStore.tags.length > 0 ? tagStore.tags[0].id : null
  newTagName.value = ''
  batchTagDialogVisible.value = true
}

const confirmBatchTag = async () => {
  let targetTagId = selectedTagId.value
  taggingLoading.value = true
  try {
    if (newTagName.value.trim()) {
      const created = await tagStore.createTag(effectiveTaskId.value, {
        name: newTagName.value.trim(),
        color: newTagColor.value
      })
      if (!created) return
      targetTagId = created.id
    }

    if (!targetTagId) {
      ElMessage.warning('请选择或输入要打的标签')
      return
    }

    const validConds = localAdvanced.conditions.filter(
      c => !c.disabled && c.field && c.op && (
        (c.field === 'tag' && (c.op === 'is_empty' || c.op === 'is_not_empty')) ||
        (c.value !== undefined && c.value !== '' && !getRegexError(c))
      )
    )

    const baseBody = {
      page: 1,
      page_size: 50,
      keyword: draftBase.keyword || undefined,
      severity: draftBase.severity ?? undefined,
      matched: draftBase.matched ?? undefined,
      device_id: draftBase.deviceId || undefined,
      time_start: draftBase.timeStart || undefined,
      time_end: draftBase.timeEnd || undefined,
      tag_ids: draftBase.untaggedOnly ? undefined : (draftBase.tagIds.length > 0 ? draftBase.tagIds : undefined),
      tag_logic: draftBase.tagLogic,
      exclude_tag_ids: draftBase.untaggedOnly ? undefined : (draftBase.excludeTagIds.length > 0 ? draftBase.excludeTagIds : undefined),
      untagged_only: draftBase.untaggedOnly || undefined,
      advanced: validConds.length > 0 ? {
        logic: localAdvanced.logic || 'AND',
        conditions: validConds
      } : undefined
    }

    const res = await api.batchTagLogs(effectiveTaskId.value, targetTagId, baseBody)
    if (res.code === 0) {
      if (res.data?.job_id) {
        batchTagDialogVisible.value = false
        progressJobId.value = res.data.job_id
        pendingTagId.value = targetTagId
        showProgressModal.value = true
      } else {
        ElMessage.success(`成功为 ${res.data.tagged_count} 条日志添加标签`)
        batchTagDialogVisible.value = false
        if (!draftBase.tagIds.includes(targetTagId)) {
          draftBase.tagIds.push(targetTagId)
        }
        await tagStore.fetchTags(effectiveTaskId.value)
        applyFilters()
      }
    }
  } catch (e) {
    ElMessage.error(e.message || '批量打标失败')
  } finally {
    taggingLoading.value = false
  }
}

// 批量摘标
const batchUntagDialogVisible = ref(false)
const selectedUntagId = ref(null)
const untaggingLoading = ref(false)

const openBatchUntagDialog = () => {
  if (previewTotal.value > 500000) {
    ElMessage.warning('单次批量摘标不能超过 50 万条，请增加筛选条件细化范围')
    return
  }
  selectedUntagId.value = tagStore.tags.length > 0 ? tagStore.tags[0].id : null
  batchUntagDialogVisible.value = true
}

const confirmBatchUntag = async () => {
  if (!selectedUntagId.value) {
    ElMessage.warning('请选择要摘除的标签')
    return
  }
  untaggingLoading.value = true
  try {
    const validConds = localAdvanced.conditions.filter(
      c => !c.disabled && c.field && c.op && (
        (c.field === 'tag' && (c.op === 'is_empty' || c.op === 'is_not_empty')) ||
        (c.value !== undefined && c.value !== '' && !getRegexError(c))
      )
    )

    const baseBody = {
      page: 1,
      page_size: 50,
      keyword: draftBase.keyword || undefined,
      severity: draftBase.severity ?? undefined,
      matched: draftBase.matched ?? undefined,
      device_id: draftBase.deviceId || undefined,
      time_start: draftBase.timeStart || undefined,
      time_end: draftBase.timeEnd || undefined,
      tag_ids: draftBase.untaggedOnly ? undefined : (draftBase.tagIds.length > 0 ? draftBase.tagIds : undefined),
      tag_logic: draftBase.tagLogic,
      exclude_tag_ids: draftBase.untaggedOnly ? undefined : (draftBase.excludeTagIds.length > 0 ? draftBase.excludeTagIds : undefined),
      untagged_only: draftBase.untaggedOnly || undefined,
      advanced: validConds.length > 0 ? {
        logic: localAdvanced.logic || 'AND',
        conditions: validConds
      } : undefined
    }

    const res = await api.batchUntagLogs(effectiveTaskId.value, selectedUntagId.value, baseBody)
    if (res.code === 0) {
      if (res.data?.job_id) {
        batchUntagDialogVisible.value = false
        progressJobId.value = res.data.job_id
        pendingTagId.value = null
        showProgressModal.value = true
      } else {
        ElMessage.success(`成功从 ${res.data.untagged_count} 条日志中移除标签`)
        batchUntagDialogVisible.value = false
        await tagStore.fetchTags(effectiveTaskId.value)
        applyFilters()
      }
    }
  } catch (e) {
    ElMessage.error(e.message || '批量摘标失败')
  } finally {
    untaggingLoading.value = false
  }
}
</script>

<style scoped>
.drawer-container {
  display: flex;
  flex-direction: column;
  gap: 16px;
  padding-bottom: 8px;
}

/* 预设工具栏样式 (Phase 2) */
.preset-bar-wrapper {
  display: flex;
  align-items: center;
  gap: 10px;
  background: var(--bg-subtle, var(--gray-50));
  padding: 10px 14px;
  border-radius: var(--radius-lg, 8px);
  border: 1px solid var(--border-base, var(--gray-200));
}

.preset-label-group {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
}

.preset-icon {
  font-size: 16px;
  color: var(--brand-600, #0284c7);
}

.preset-label {
  font-size: var(--fs-sm, 12px);
  font-weight: 600;
  color: var(--text-primary, var(--gray-900));
}

.preset-select {
  flex: 1;
}

.save-preset-btn {
  flex-shrink: 0;
}

.preset-option-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  gap: 10px;
}

.preset-name {
  font-weight: 500;
  color: var(--text-primary, var(--gray-900));
}

.preset-desc {
  font-size: var(--fs-xs, 11px);
  color: var(--text-secondary, var(--gray-500));
  max-width: 280px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.preset-del-btn {
  padding: 0 4px;
}

/* 分区通用卡片设计 (符合前端样式规范) */
.filter-section-card {
  background: var(--bg-surface, #ffffff);
  border: 1px solid var(--border-base, var(--gray-200));
  border-radius: var(--radius-lg, 8px);
  overflow: hidden;
  transition: border-color 0.2s, box-shadow 0.2s;
}

.filter-section-card:hover {
  border-color: var(--brand-300, #7dd3fc);
}

.section-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 10px 14px;
  background: var(--bg-subtle, var(--gray-50));
  border-bottom: 1px solid var(--border-base, var(--gray-200));
  cursor: pointer;
  user-select: none;
}

.section-title {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: var(--fs-md, 14px);
  font-weight: 600;
  color: var(--text-primary, var(--gray-900));
}

.section-icon {
  font-size: 16px;
  color: var(--brand-600, #0284c7);
}

.section-badge {
  font-size: var(--fs-xs, 11px);
}

.section-body {
  padding: 14px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}

/* 基础检索矩阵样式 */
.matrix-row {
  display: flex;
  align-items: center;
  gap: 12px;
}

.base-keyword-input {
  width: 100%;
}

.time-row {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
}

.time-picker-group {
  display: flex;
  align-items: center;
  gap: 6px;
  flex: 1;
}

.matrix-time-picker {
  flex: 1;
}

.time-sep {
  color: var(--text-secondary, var(--gray-500));
  font-weight: 600;
}

.quick-time-pills {
  display: flex;
  gap: 6px;
}

.matrix-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 12px;
}

.grid-item {
  display: flex;
  align-items: center;
  gap: 8px;
}

.grid-label {
  font-size: var(--fs-sm, 12px);
  color: var(--text-secondary, var(--gray-500));
  white-space: nowrap;
  width: 70px;
  flex-shrink: 0;
}

.grid-control {
  flex: 1;
}

/* 标签面板样式 */
.tag-control-row {
  display: flex;
  align-items: center;
  gap: 10px;
}

.tag-control-label {
  font-size: var(--fs-sm, 12px);
  color: var(--text-secondary, var(--gray-500));
  width: 70px;
  flex-shrink: 0;
}

.tag-select-box {
  flex: 1;
}

.tag-logic-toggle {
  flex-shrink: 0;
}

.exclude-label {
  color: var(--danger-text, #991b1b);
}

.danger-text {
  color: var(--danger-text, #991b1b);
}

.is-disabled-body {
  opacity: 0.5;
  pointer-events: none;
}

/* 高级规则构造器样式 */
.rule-limit-hint {
  font-size: var(--fs-xs, 11px);
  color: var(--text-secondary, var(--gray-500));
  font-weight: normal;
}

.logic-switch-wrapper {
  display: flex;
  align-items: center;
  gap: 8px;
}

.logic-label {
  font-size: var(--fs-sm, 12px);
  color: var(--text-secondary, var(--gray-500));
}

.conditions-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.condition-row {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  background: var(--bg-surface, #ffffff);
  border: 1px solid var(--border-light, var(--gray-100));
  padding: 8px 10px;
  border-radius: var(--radius-md, 6px);
  transition: all 0.2s;
}

.condition-row:hover {
  border-color: var(--brand-300, #7dd3fc);
}

.is-disabled-row {
  opacity: 0.45;
  background: var(--bg-subtle, var(--gray-50));
}

.cond-active-checkbox {
  margin-top: 5px;
  margin-right: 2px;
}

.row-index {
  font-size: var(--fs-sm, 12px);
  font-weight: 600;
  color: var(--text-secondary, var(--gray-500));
  line-height: 32px;
  width: 24px;
  flex-shrink: 0;
}

.field-select {
  width: 170px;
  flex-shrink: 0;
}

.op-wrapper {
  display: flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
}

.op-select {
  width: 145px;
}

.invert-op-btn {
  line-height: 32px;
  color: var(--text-secondary, var(--gray-500));
}

.invert-op-btn:hover {
  color: var(--brand-600, #0284c7);
}

.value-input-wrapper {
  flex: 1;
  display: flex;
  flex-direction: column;
}

.tag-value-select {
  width: 100%;
}

.facet-suggest-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
}

.facet-suggest-val {
  font-weight: 500;
  color: var(--text-primary, var(--gray-900));
}

.facet-suggest-cnt {
  font-size: var(--fs-xs, 11px);
  color: var(--text-secondary, var(--gray-500));
}

.regex-error-tip {
  font-size: var(--fs-xs, 11px);
  color: var(--danger-strong, #dc2626);
  margin-top: 3px;
  line-height: 1.2;
}

.delete-btn {
  line-height: 32px;
  padding: 0 4px;
}

.empty-conditions {
  text-align: center;
  padding: 24px 0;
  color: var(--text-secondary, var(--gray-500));
  font-size: var(--fs-md, 14px);
  border: 1px dashed var(--border-base, var(--gray-300));
  border-radius: var(--radius-md, 6px);
}

.add-bar {
  display: flex;
  justify-content: flex-start;
  margin-top: 4px;
}

/* 实时预览盒 */
.preview-box {
  background: var(--warning-bg, #fffbeb);
  border: 1px solid var(--warning-bg, #fffbeb);
  padding: 12px 16px;
  border-radius: var(--radius-md, 6px);
}

.preview-info {
  font-size: var(--fs-md, 14px);
  color: var(--warning-text, #92400e);
  display: flex;
  align-items: center;
  gap: 6px;
}

.preview-icon {
  font-size: 16px;
  color: var(--warning-strong, #d97706);
}

.preview-title {
  font-weight: 600;
}

.preview-count {
  font-size: var(--fs-xl, 18px);
  color: var(--danger-strong, #dc2626);
  margin-left: 2px;
}

.preview-loading {
  font-size: var(--fs-sm, 12px);
  color: var(--text-secondary, var(--gray-500));
}

.preview-hint {
  font-size: var(--fs-xs, 11px);
  color: var(--text-secondary, var(--gray-500));
  margin-top: 4px;
}

/* 底部操作区 */
.drawer-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
  width: 100%;
}

.footer-left-actions,
.footer-right-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.batch-tag-tip {
  font-size: var(--fs-md, 14px);
  color: var(--text-regular, var(--gray-700));
  margin-bottom: 12px;
}

.batch-alert {
  margin-bottom: 12px;
}

.tag-option-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
}

.tag-dot-indicator {
  display: inline-block;
  width: 10px;
  height: 10px;
  border-radius: 50%;
  flex-shrink: 0;
}
</style>

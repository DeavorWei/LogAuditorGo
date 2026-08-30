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
          <el-button type="warning" icon="Opportunity" :disabled="!currentTaskId || currentTask?.status === 'PENDING' || currentTask?.log_count === 0" @click="handleReanalyzeTask">
            重新分析
          </el-button>
          <el-button type="primary" icon="Upload" :disabled="!currentTaskId" @click="openImportDialog">
            {{ currentTask?.status === 'PENDING' || currentTask?.log_count === 0 ? '导入日志' : '补充导入' }}
          </el-button>
          <el-button type="success" icon="Download" :loading="exportingHTML" :disabled="!currentTaskId || currentTask?.status === 'PENDING'" @click="handleExportHTML">
            导出报告
          </el-button>
          <el-button type="primary" plain icon="Plus" @click="openNewTaskDialog">新建任务</el-button>
        </el-button-group>
      </div>
    </div>

    <!-- 功能视图切换导航 -->
    <div v-if="currentTaskId" class="workbench-nav-bar">
      <!--
        WEB-16: 视图模式改为由常量驱动。
        原先 label 是 5 个裸字符串，与下面 v-if 的判断条件各写各的，
        改一处漏一处就会渲染成空白视图。现在统一取自 VIEW_MODE_OPTIONS。
      -->
      <el-radio-group v-model="currentViewMode" size="default">
        <el-radio-button :label="VIEW_MODE.WORKBENCH">
          <el-icon style="margin-right: 4px; vertical-align: middle;"><Document /></el-icon>
          <span>日志审计工作台</span>
        </el-radio-button>
        <el-radio-button :label="VIEW_MODE.DEVICES">
          <el-icon style="margin-right: 4px; vertical-align: middle;"><Monitor /></el-icon>
          <span>设备管理</span>
          <el-badge v-if="currentTask && currentTask.device_count" :value="currentTask.device_count" type="primary" style="margin-left: 6px;" />
        </el-radio-button>
        <el-radio-button :label="VIEW_MODE.MULTI_TIMELINE">
          <el-icon style="margin-right: 4px; vertical-align: middle;"><Histogram /></el-icon>
          <span>多设备协同时间线</span>
        </el-radio-button>
        <el-radio-button :label="VIEW_MODE.RCA">
          <el-icon style="margin-right: 4px; vertical-align: middle;"><Aim /></el-icon>
          <span>RCA 故障联动</span>
          <el-badge v-if="currentTask && currentTask.rca_count" :value="currentTask.rca_count" type="danger" style="margin-left: 6px;" />
        </el-radio-button>
        <el-radio-button :label="VIEW_MODE.MULTI_REPORT">
          <el-icon style="margin-right: 4px; vertical-align: middle;"><DataAnalysis /></el-icon>
          <span>多设备对比诊断报告</span>
        </el-radio-button>
      </el-radio-group>
    </div>

    <!--
      视图切换说明（WEB-02）：
      原实现外层用 v-show 控制显隐、内层 v-if 只判断 currentTaskId，
      导致选中任务后 4 个子组件被**同时挂载**（各自 onMounted 立即发请求），
      首屏并发 10+ 请求（含 500 条时间线数据），且 4 个重型组件常驻内存。
      现将显隐条件收敛到 v-if 上，未激活的视图不创建实例、不发请求；
      同时去掉 refreshTrigger 拼 key 的强制重挂载，改由子组件的 refresh() 方法刷新。
    -->

    <!-- 视图 1：设备管理视图 -->
    <div v-if="currentTaskId && currentViewMode === VIEW_MODE.DEVICES" class="workbench-sub-view">
      <DeviceManager
        ref="deviceManagerRef"
        :task-id="currentTaskId"
        @device-updated="handleDeviceUpdated"
        @open-progress="openProgressModalWithId"
      />
    </div>

    <!-- 视图 2：多设备时间线视图 -->
    <div v-if="currentTaskId && currentViewMode === VIEW_MODE.MULTI_TIMELINE" class="workbench-sub-view">
      <MultiDeviceTimeline
        ref="timelineRef"
        :task-id="currentTaskId"
      />
    </div>

    <!-- 视图 3：独立 RCA 故障联动分析中心 -->
    <div v-if="currentTaskId && currentViewMode === VIEW_MODE.RCA" class="workbench-sub-view">
      <RcaCenter
        ref="rcaCenterRef"
        :task-id="currentTaskId"
        @jump-to-log="handleJumpToLog"
      />
    </div>

    <!-- 视图 4：多设备对比诊断报告视图 -->
    <div v-if="currentTaskId && currentViewMode === VIEW_MODE.MULTI_REPORT" class="workbench-sub-view">
      <MultiDeviceReport
        ref="multiReportRef"
        :task-id="currentTaskId"
      />
    </div>

    <!-- 视图 5：经典日志审计工作台视图 -->
    <div v-show="currentTaskId && (currentViewMode === VIEW_MODE.WORKBENCH || !currentViewMode)" class="workbench-main-view">
      <!-- 空任务（PENDING 状态）引导卡片 -->
      <div v-if="currentTask && (currentTask.status === 'PENDING' || (totalLogs === 0 && !loadingLogs))" class="empty-task-guide">
        <el-card shadow="never" class="guide-card">
          <div class="guide-header">
            <el-icon size="48" color="#0284c7"><FolderOpened /></el-icon>
            <h3>任务「{{ currentTask.task_name }}」尚未导入日志数据</h3>
            <p>请选择以下方式之一，直接将本地日志导入到本任务中开始智能审计与 RCA 根因分析：</p>
          </div>

          <div class="guide-actions">
            <div class="action-tile" @click="openGuidePicker('files')">
              <el-icon size="32" color="#0284c7"><Files /></el-icon>
              <h4>选择日志文件</h4>
              <p>直接选择服务端本机上的 .log / .txt / .syslog 日志文件，支持多选</p>
              <el-button type="primary" size="small">选择多个文件</el-button>
            </div>

            <div class="action-tile" @click="openGuidePicker('dir')">
              <el-icon size="32" color="#16a34a"><FolderAdd /></el-icon>
              <h4>选择日志目录</h4>
              <p>选择日志归档目录，由服务端递归收集其中全部日志文件</p>
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
          <div v-if="taskDevices.length > 0" class="filter-row">
            <el-select
              v-model="filter.deviceId"
              placeholder="按设备筛选"
              clearable
              size="small"
              style="width: 100%;"
              @change="onFilterChange"
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
            <div v-if="rec.event_summary" class="log-card-msg" :title="rec.event_summary">
              {{ rec.event_summary }}
            </div>
            <div class="log-card-footer">
              <span class="log-time">{{ formatTime(rec.timestamp) }}</span>
              <span v-if="rec.hostname" class="host-tag">{{ rec.hostname }}</span>
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
        <!-- RCA 联动告警全局提示条 -->
        <div v-if="rcaEvents && rcaEvents.length > 0" class="rca-banner-alert">
          <div class="banner-left">
            <el-icon color="#ea580c" size="18"><Aim /></el-icon>
            <span class="banner-text">
              <strong>系统已识别 {{ rcaEvents.length }} 个协议联动事件</strong>
            </span>
          </div>
          <el-button type="warning" link size="small" icon="ArrowRight" @click="currentViewMode = VIEW_MODE.RCA">
            查看 RCA 全景分析
          </el-button>
        </div>

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

          <!-- 事件语义解析摘要 -->
          <div v-if="selectedLog.event_summary" class="section-box event-summary-box-wb">
            <div class="box-title">事件语义解析摘要</div>
            <div class="event-summary-highlight-wb">
              <el-icon color="#0284c7" size="18" style="margin-right: 8px; flex-shrink: 0;"><InfoFilled /></el-icon>
              <span class="summary-text-wb">{{ selectedLog.event_summary }}</span>
            </div>
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

          <!-- 动态提取参数与文档说明融合 -->
          <div class="section-box">
            <div class="box-title flex-between">
              <span>动态提取变量与文档说明 (Parameters & Documentation)</span>
              <span v-if="enrichedParameters.length > 0" class="param-count-badge">
                已提取 {{ enrichedParameters.length }} 个变量
                <template v-if="matchedParamCount > 0">（已匹配 {{ matchedParamCount }} 条文档说明）</template>
              </span>
            </div>
            <div v-if="enrichedParameters && enrichedParameters.length > 0" class="param-grid-enhanced">
              <div
                v-for="p in enrichedParameters"
                :key="p.name"
                :class="['param-card', { 'has-desc': !!p.description }]"
              >
                <div class="param-card-top">
                  <span class="p-key">{{ p.name }}</span>
                  <el-tooltip
                    v-if="p.description"
                    placement="top"
                    raw-content
                    :content="formatTooltipHtml(p.description)"
                  >
                    <span class="p-desc-badge">📖 {{ p.description }}</span>
                  </el-tooltip>
                </div>
                <div class="p-val-box">
                  <span class="p-val">{{ p.value }}</span>
                </div>
              </div>
            </div>
            <div v-else class="empty-hint">该日志未解析出结构化动态键值变量</div>
          </div>

          <!-- 消息模板动态实例化对照 -->
          <div v-if="selectedLog.knowledge && selectedLog.knowledge.message" class="section-box">
            <div class="box-title flex-between">
              <span>📋 官方日志消息模板实例化 (Template Instantiation)</span>
              <el-tag size="small" type="success" effect="plain">变量已注入</el-tag>
            </div>
            <div class="template-box">
              <div class="template-rendered" v-html="renderedTemplateHtml"></div>
              <div class="template-raw-sub">
                <span class="sub-label">官方原始模板:</span>
                <code>{{ selectedLog.knowledge.message }}</code>
              </div>
            </div>
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
                  <div class="kb-header-top">
                    <div class="kb-title">{{ selectedLog.knowledge.module }}/{{ selectedLog.knowledge.brief }}</div>
                    <el-switch
                      v-model="contextualizeMode"
                      size="small"
                      active-text="现场参数注入"
                      inactive-text="原始文档"
                      style="--el-switch-on-color: #10b981;"
                    />
                  </div>
                  <div class="kb-meta">
                    <span class="badge-tier">匹配层级: {{ selectedLog.match_tier }}</span>
                    <span class="badge-conf">置信度: {{ (selectedLog.match_confidence * 100).toFixed(0) }}%</span>
                    <span v-if="contextualizeMode && matchedParamCount > 0" class="badge-ctx">
                      ✨ 已将现场 {{ matchedParamCount }} 个参数动态注入至排查步骤
                    </span>
                  </div>
                </div>

                <!-- 含义 -->
                <div class="kb-block">
                  <div class="kb-subtitle">📖 日志/告警含义解释</div>
                  <div
                    class="kb-text"
                    v-html="renderedKnowledgeHtml.description"
                  ></div>
                </div>

                <!-- 官方可能原因 -->
                <div class="kb-block">
                  <div class="kb-subtitle">🔍 官方可能原因</div>
                  <div
                    class="kb-text cause-text"
                    v-html="renderedKnowledgeHtml.cause"
                  ></div>
                </div>

                <!-- 官方建议处理步骤 -->
                <div class="kb-block">
                  <div class="kb-subtitle">🛠️ 官方处理排错步骤</div>
                  <div
                    class="kb-text action-box"
                    v-html="renderedKnowledgeHtml.action"
                  ></div>
                </div>

                <!-- 系统影响 -->
                <div v-if="selectedLog.knowledge.impact" class="kb-block">
                  <div class="kb-subtitle">⚠️ 对系统的影响</div>
                  <div
                    class="kb-text"
                    v-html="renderedKnowledgeHtml.impact"
                  ></div>
                </div>

                <!-- 官方参数定义字典与现场对照表 -->
                <div v-if="kbParamDefs.length > 0" class="kb-block">
                  <div class="kb-subtitle flex-between">
                    <span>📚 官方参数字典与现场实际值对照</span>
                    <span class="dict-count-tag">共 {{ kbParamDefs.length }} 项参数定义</span>
                  </div>
                  <el-table :data="kbParamDefs" size="small" border style="width: 100%; margin-top: 6px;">
                    <el-table-column prop="name" label="参数名称" width="130">
                      <template #default="{ row }">
                        <span class="dict-pname">{{ row.name }}</span>
                      </template>
                    </el-table-column>
                    <el-table-column prop="description" label="官方含义说明" min-width="140" />
                    <el-table-column label="现场实际值" width="130">
                      <template #default="{ row }">
                        <span v-if="row.actualValue !== undefined" class="dict-pval">{{ row.actualValue }}</span>
                        <span v-else class="dict-pnone">未捕获</span>
                      </template>
                    </el-table-column>
                  </el-table>
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
          <!-- WEB-16: 选项统一取自常量，与 Dashboard 保持同一口径（原先此处只有 3 项） -->
          <el-select v-model="newTaskForm.deviceType" style="width: 100%;">
            <el-option
              v-for="opt in DEVICE_TYPE_OPTIONS"
              :key="opt.value"
              :label="opt.label"
              :value="opt.value"
            />
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
        <!-- 标签页 1: 从本机目录导入 -->
        <el-tab-pane label="从本机目录导入" name="dir">
          <div class="path-import-pane">
            <el-icon size="40" color="#16a34a"><FolderAdd /></el-icon>
            <div class="pane-title">选择存放日志文件的目录</div>
            <p class="pane-desc">
              目录由服务端进程直接读取，不经过浏览器上传。可一次选择多个目录，
              并按扩展名递归收集其中所有日志文件。
            </p>
            <el-button type="success" size="small" @click="openPicker('dir')">选择日志目录</el-button>
          </div>
        </el-tab-pane>

        <!-- 标签页 2: 从本机文件导入 -->
        <el-tab-pane label="从本机文件导入" name="files">
          <div class="path-import-pane">
            <el-icon size="40" color="#0284c7"><Files /></el-icon>
            <div class="pane-title">选择一个或多个日志文件</div>
            <p class="pane-desc">
              直接选择服务端本机上的日志文件，支持 .log / .txt / .syslog 等常见格式。
            </p>
            <el-button type="primary" size="small" @click="openPicker('file')">选择日志文件</el-button>
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

      <!-- 已选路径清单 -->
      <div v-if="selectedPaths.length > 0 && importTab !== 'text'" class="pending-paths-box">
        <div class="pending-paths-header">
          <span>已选择 <strong>{{ selectedPaths.length }}</strong> 个路径</span>
          <el-button type="danger" link size="small" @click="selectedPaths = []">清空</el-button>
        </div>
        <div class="pending-paths-list">
          <div v-for="(p, idx) in selectedPaths" :key="p" class="pending-path-item">
            <span class="path-value" :title="p">📁 {{ p }}</span>
            <el-icon class="del-btn" @click="removePath(idx)"><Close /></el-icon>
          </div>
        </div>
      </div>

      <template #footer>
        <el-button @click="showImportDialog = false">取消</el-button>
        <el-button type="primary" :loading="submitting" :disabled="!canStartImport" @click="handleCheckAndStartImport">
          开始导入并分析
        </el-button>
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
        <el-button type="primary" :loading="submitting" @click="executeImportWithConflict('rename')">
          自动重命名共存 (推荐)
        </el-button>
        <el-button type="warning" :loading="submitting" @click="executeImportWithConflict('skip')">
          跳过同名文件
        </el-button>
        <el-button type="danger" :loading="submitting" @click="executeImportWithConflict('overwrite')">
          覆盖已有文件
        </el-button>
      </template>
    </el-dialog>

    <!-- 全流程阶段进度实时追踪弹窗 -->
    <ImportProgressModal
      v-model="showProgressModal"
      :job-id="currentJobId"
      title="日志智能审计与 RCA 根因分析流水线"
      @completed="handleLogImportCompleted"
    />

    <!-- 服务端本地路径选择器（日志导入专用） -->
    <ServerPathPicker
      v-model="selectedPaths"
      v-model:visible="showPathPicker"
      :mode="pickerMode"
      :exts="logExts"
      :multiple="true"
      favorite-key="task-logs"
      :title="pickerMode === 'dir' ? '选择日志目录' : '选择日志文件'"
    />
  </div>
</template>

<script setup>
import { ref, computed, onMounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { FolderOpened, Files, FolderAdd, DocumentCopy, Close, Document, Monitor, Histogram, DataAnalysis, Aim, ArrowRight, Opportunity } from '@element-plus/icons-vue'
import api from '@/api'
import RcaGraph from '@/components/RcaGraph.vue'
import ImportProgressModal from '@/components/ImportProgressModal.vue'
import DeviceManager from '@/components/DeviceManager.vue'
import ServerPathPicker from '@/components/ServerPathPicker.vue'
import MultiDeviceTimeline from '@/components/MultiDeviceTimeline.vue'
import MultiDeviceReport from '@/components/MultiDeviceReport.vue'
import RcaCenter from '@/components/RcaCenter.vue'
import { useFilterStore } from '@/stores/filter'
import { useTaskStore } from '@/stores/task'
import { VIEW_MODE, DEFAULT_VIEW_MODE, isValidViewMode } from '@/constants/viewModes'
import { TASK_DEVICE_TYPE_OPTIONS as DEVICE_TYPE_OPTIONS, DEFAULT_TASK_DEVICE_TYPE as DEFAULT_DEVICE_TYPE } from '@/constants/deviceTypes'
import { formatTime as sharedFormatTime, formatSize as sharedFormatSize } from '@/utils/format'
import { useReanalyze } from '@/composables/useReanalyze'

const route = useRoute()
// WEB-14: 原文件声明了 `const router = useRouter()` 却从未使用（全文件无 router. 调用），
// 属于死代码，已移除。需要跳转时请重新引入 useRouter。

const taskList = ref([])
const currentTaskId = ref('')
const currentTask = ref(null)
/**
 * 视图模式：优先恢复上次使用的视图（持久化在 filter store），
 * 并用 isValidViewMode 校验外部输入——localStorage 可能被手工改坏，
 * 脏值回退到默认视图，避免出现"什么都渲染不出来"的空白页面。
 */
const currentViewMode = ref(
  isValidViewMode(filterStore.filters.viewMode)
    ? filterStore.filters.viewMode
    : DEFAULT_VIEW_MODE
)

// 视图切换即落盘，下次进入工作台直接回到上次的分析视图
watch(currentViewMode, (mode) => {
  filterStore.filters.viewMode = mode
})
const taskFiles = ref([])
const showFilesDrawer = ref(false)
const refreshTrigger = ref(0)
const deviceManagerRef = ref(null)
const timelineRef = ref(null)
const rcaCenterRef = ref(null)
const multiReportRef = ref(null)

// 主动刷新当前激活的子视图。
// 原实现靠 refreshTrigger++ 触发 :key 变化来"销毁重建"子组件，代价是丢失滚动位置与内部筛选状态；
// 现改为调用子组件暴露的 refresh()，仅在子视图已挂载时生效（未激活的视图本就不该拉数据）。
const refreshActiveSubView = async () => {
  const target = {
    [VIEW_MODE.DEVICES]: deviceManagerRef,
    [VIEW_MODE.MULTI_TIMELINE]: timelineRef,
    [VIEW_MODE.RCA]: rcaCenterRef,
    [VIEW_MODE.MULTI_REPORT]: multiReportRef
  }[currentViewMode.value]
  try {
    await target?.value?.refresh?.()
  } catch (e) {
    // 子组件尚未实现 refresh() 时静默降级，不影响主流程
  }
}

const handleDeviceUpdated = async (devices) => {
  if (currentTask.value) {
    currentTask.value.device_count = devices ? devices.length : 0
  }
  taskDevices.value = devices || []
  await refreshActiveSubView()
}

const openProgressModalWithId = (jobId) => {
  currentJobId.value = jobId
  showProgressModal.value = true
}

const handleJumpToLog = async (logId) => {
  currentViewMode.value = DEFAULT_VIEW_MODE
  try {
    const res = await api.queryTaskLogs(currentTaskId.value, {
      page: 1,
      page_size: 50,
      keyword: `#${logId}`
    })
    if (res.code === 0 && res.data?.records?.length > 0) {
      selectLog(res.data.records[0])
    }
  } catch (e) {
    console.error('Jump to log failed:', e)
  }
}

const logRecords = ref([])
const totalLogs = ref(0)
const loadingLogs = ref(false)
const selectedLog = ref(null)
const activeTab = ref('knowledge')

const taskDevices = ref([])

/**
 * WEB-05: 筛选条件上收至 Pinia store。
 * 原先是组件内的局部 ref，切换视图即丢失，刷新页面也要重新勾选。
 * 这里直接复用 store 中的响应式对象（读写都走同一份状态，不做二次拷贝）。
 */
const filterStore = useFilterStore()
const filter = computed(() => filterStore.filters)

/**
 * WEB-05 / WEB-13: RCA 事件与其倒排索引上收至 task store。
 *
 * 原先 `rcaEvents` 是本地 ref，且 `matchedRCA` 在模板里对每条日志
 * 都要遍历全部 RCA 事件并 `JSON.parse(correlated_log_ids)`——
 * 日志列表一翻页就成百上千次重复解析。
 * 改为由 store 在拉取时预建 `Map<logId, rcaEvent>`，查询降为 O(1)。
 */
const taskStore = useTaskStore()
const rcaEvents = computed(() => taskStore.rcaEvents)

// 新建任务相关
const showNewTaskDialog = ref(false)
const submitting = ref(false)
const newTaskForm = ref({
  taskName: '',
  deviceType: DEFAULT_DEVICE_TYPE
})

// 导入相关
const showImportDialog = ref(false)
const importTab = ref('dir')
const manualFileName = ref('')
const manualLogText = ref('')
const selectedPaths = ref([])
const showPathPicker = ref(false)
const pickerMode = ref('dir')
const logExts = ['.log', '.txt', '.syslog']

// 进度追踪相关
const showProgressModal = ref(false)
const currentJobId = ref('')

// WEB-14: filesInputRef / dirInputRef / guideFilesInputRef / guideDirInputRef
// 四个 ref 原先只出现在定义处，模板与脚本均无引用（历史遗留的 input[type=file] 方案残留），
// 已一并移除。当前的文件选择统一走 ServerPathPicker 服务端直读模式。

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

// 规范化辅助函数：忽略大小写、空格与下划线
const normalizeParamKey = (k) => {
  return (k || '').toString().toLowerCase().replace(/[-_\s]/g, '')
}

// HTML 转义防 XSS
const escapeHtml = (text) => {
  if (!text) return ''
  return String(text)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#039;')
}

// 格式化 tooltip 内容，支持换行并限制最大显示范围
const formatTooltipHtml = (text) => {
  if (!text) return ''
  const escaped = escapeHtml(text).replace(/\r?\n/g, '<br/>')
  return `<div style="max-width: 420px; max-height: 280px; overflow-y: auto; white-space: normal; line-height: 1.6; font-size: 12px; padding: 2px 4px;"><strong>官方文档定义:</strong><br/>${escaped}</div>`
}

// 动态变量与官方文档参数定义融合
const enrichedParameters = computed(() => {
  if (!selectedLog.value) return []
  
  // 如果后端已经返回 enriched_parameters (阶段二/三)，优先直接使用
  if (Array.isArray(selectedLog.value.enriched_parameters) && selectedLog.value.enriched_parameters.length > 0) {
    return selectedLog.value.enriched_parameters.map(p => ({
      name: p.name || p.Name,
      value: p.value || p.Value,
      description: p.description || p.Description || '',
      matched: !!(p.description || p.Description)
    }))
  }

  const rawParams = parsedParameters.value || {}
  const kb = selectedLog.value.knowledge
  let defs = []
  if (kb && kb.parameters) {
    try {
      const parsed = typeof kb.parameters === 'string' ? JSON.parse(kb.parameters) : kb.parameters
      if (Array.isArray(parsed)) {
        defs = parsed
      }
    } catch (e) {}
  }

  // 构建精确查找字典与规范化模糊查找字典
  const exactMap = new Map()
  const normalizedMap = new Map()
  for (const d of defs) {
    const name = d.name || d.Name || ''
    const desc = d.description || d.Description || ''
    if (name) {
      exactMap.set(name, desc)
      normalizedMap.set(normalizeParamKey(name), desc)
    }
  }

  const result = []
  for (const [key, val] of Object.entries(rawParams)) {
    let desc = exactMap.get(key)
    if (!desc) {
      desc = normalizedMap.get(normalizeParamKey(key)) || ''
    }
    result.push({
      name: key,
      value: val,
      description: desc,
      matched: !!desc
    })
  }

  return result
})

const matchedParamCount = computed(() => {
  return enrichedParameters.value.filter(p => p.matched).length
})

// 官方日志消息模板实例化（将占位符替换为提取的真实值并高亮）
const renderedTemplateHtml = computed(() => {
  const log = selectedLog.value
  if (!log || !log.knowledge || !log.knowledge.message) return ''

  const rawTemplate = log.knowledge.message

  // 1. 构建所有可用参数字典，融合 enrichedParameters 与 parsedParameters
  const params = { ...(parsedParameters.value || {}) }
  if (Array.isArray(enrichedParameters.value)) {
    for (const ep of enrichedParameters.value) {
      if (ep.name && ep.value !== undefined && ep.value !== '') {
        params[ep.name] = ep.value
      }
    }
  }

  // 2. 构建规范化映射表
  const normParams = new Map()
  for (const [k, v] of Object.entries(params)) {
    normParams.set(normalizeParamKey(k), v)
  }

  // 3. 构建华为常见变量别名同义词映射表 (双向匹配)
  const aliasGroups = [
    ['bgppeerremoteaddr', 'peerid', 'peeraddr', 'neighbor', 'remoteaddr', 'peerip', 'peeraddress', 'peer'],
    ['bgppeerlocaladdr', 'localaddr', 'localaddress', 'localip', 'local'],
    ['bgppeerlasterror', 'errorcode', 'errorsubcode', 'notifyreason', 'reason', 'lasterror'],
    ['bgppeerstate', 'state', 'laststate', 'currentstate', 'peerstate'],
    ['interfacename', 'interface', 'ifname', 'port', 'portname', 'ifnet'],
    ['nbrrouterid', 'routerid', 'neighborrouterid', 'neighbor', 'nbrip', 'nbr'],
    ['bfddiag', 'diag', 'diagnostic', 'reason', 'diagcode'],
    ['sessid', 'sessionid', 'session']
  ]

  const findActualVal = (keyName) => {
    if (!keyName) return undefined
    // 优先精确匹配
    if (params[keyName] !== undefined) return params[keyName]
    const normK = normalizeParamKey(keyName)
    if (normParams.has(normK)) return normParams.get(normK)

    // 尝试别名群组匹配
    for (const group of aliasGroups) {
      if (group.includes(normK)) {
        for (const alias of group) {
          if (normParams.has(alias)) {
            return normParams.get(alias)
          }
        }
      }
    }

    // 尝试在官方参数定义中寻找关联描述
    if (log.knowledge && log.knowledge.parameters) {
      try {
        const defs = typeof log.knowledge.parameters === 'string' ? JSON.parse(log.knowledge.parameters) : log.knowledge.parameters
        if (Array.isArray(defs)) {
          for (const d of defs) {
            const dName = normalizeParamKey(d.name || d.Name || '')
            const dDesc = normalizeParamKey(d.description || d.Description || '')
            if (dName === normK || dDesc.includes(normK)) {
              for (const [pk, pv] of normParams.entries()) {
                if (dDesc.includes(pk) || dName === pk) {
                  return pv
                }
              }
            }
          }
        }
      } catch (e) {}
    }

    return undefined
  }

  // 4. 允许括号内包含空格的占位符正则，支持 [Var], <Var>, {Var}, %Var%, $Var
  const placeholderRegex = /(\[\s*([a-zA-Z0-9_\-\s]+?)\s*\]|<\s*([a-zA-Z0-9_\-\s]+?)\s*>|\{\s*([a-zA-Z0-9_\-\s]+?)\s*\}|%\s*([a-zA-Z0-9_\-\s]+?)\s*%|\$\s*([a-zA-Z0-9_\-]+))/g

  let lastIdx = 0
  let html = ''
  let match

  while ((match = placeholderRegex.exec(rawTemplate)) !== null) {
    const start = match.index
    const end = placeholderRegex.lastIndex
    const fullMatch = match[0]
    const rawKey = match[2] || match[3] || match[4] || match[5] || match[6]
    const keyName = rawKey ? rawKey.trim() : ''

    // 添加前面的普通文本
    html += escapeHtml(rawTemplate.substring(lastIdx, start))

    // 查找实际值
    const actualVal = findActualVal(keyName)
    if (actualVal !== undefined) {
      html += `<span class="inst-param-injected" title="参数: ${escapeHtml(keyName)} = ${escapeHtml(actualVal)}">${escapeHtml(actualVal)}</span>`
    } else {
      // 保持原占位符样式
      html += `<span class="inst-param-placeholder">${escapeHtml(fullMatch)}</span>`
    }

    lastIdx = end
  }

  // 添加剩余文本
  html += escapeHtml(rawTemplate.substring(lastIdx))

  return html
})

// 现场排查上下文参数注入开关
const contextualizeMode = ref(true)

/**
 * 换行符归一化（处理字面量 \n 与 \r\n），并对"步骤/原因序号"智能补换行。
 * 抽成独立函数后可供 renderContextualizedHtml 与其 computed 缓存共用。
 */
const normalizeLineBreaks = (raw) => {
  let normalized = String(raw)
    .replace(/\\r\\n/g, '\n')
    .replace(/\\n/g, '\n')
    .replace(/\r\n/g, '\n')
    .replace(/\r/g, '\n')

  if (!normalized.includes('\n')) {
    normalized = normalized
      .replace(/(\s+)(\d+[\.、]\s*)/g, '\n$2')
      .replace(/(\s+)(步骤\s*\d+[\.、:：]?\s*)/g, '\n$2')
      .replace(/(\s+)(原因\s*\d+[\.、:：]?\s*)/g, '\n$2')
      .trim()
  }
  return normalized
}

// 对排查步骤、可能原因等文本进行现场参数动态注入与高亮渲染，并保证换行清晰展示
const renderContextualizedHtml = (text) => {
  if (!text) return ''
  if (!contextualizeMode.value) {
    // 关闭注入时无需占位符处理，走纯转义路径
    return escapeHtml(normalizeLineBreaks(String(text))).replace(/\n/g, '<br/>')
  }

  const normalized = normalizeLineBreaks(text)

  const params = parsedParameters.value || {}
  const normParams = new Map()
  for (const [k, v] of Object.entries(params)) {
    normParams.set(normalizeParamKey(k), v)
  }

  const placeholderRegex = /(\[([a-zA-Z0-9_\-]+)\]|<([a-zA-Z0-9_\-]+)>|\{([a-zA-Z0-9_\-]+)\}|%([a-zA-Z0-9_\-]+)%|\$([a-zA-Z0-9_\-]+))/g

  let lastIdx = 0
  let html = ''
  let match

  while ((match = placeholderRegex.exec(normalized)) !== null) {
    const start = match.index
    const end = placeholderRegex.lastIndex
    const fullMatch = match[0]
    const keyName = match[2] || match[3] || match[4] || match[5] || match[6]

    html += escapeHtml(normalized.substring(lastIdx, start)).replace(/\n/g, '<br/>')

    let actualVal = params[keyName]
    if (actualVal === undefined) {
      actualVal = normParams.get(normalizeParamKey(keyName))
    }

    if (actualVal !== undefined) {
      html += `<span class="inst-param-injected" title="现场变量: ${escapeHtml(keyName)} = ${escapeHtml(actualVal)}">${escapeHtml(actualVal)}</span>`
    } else {
      html += `<span class="inst-param-placeholder">${escapeHtml(fullMatch)}</span>`
    }

    lastIdx = end
  }

  html += escapeHtml(normalized.substring(lastIdx)).replace(/\n/g, '<br/>')
  return html
}

/**
 * WEB-11: 知识面板四段长文本的渲染结果缓存。
 *
 * 原实现在模板里直接调用 renderContextualizedHtml(...) 共 4 处，
 * 该函数内含多次 replace + 正则 while 循环 + escapeHtml，
 * 组件每次重渲染（例如切换分页、勾选筛选）都会把四段长文本全部重算一遍
 * 并重建 4 个 v-html 子树。改为 computed 后只在
 * selectedLog / contextualizeMode / parsedParameters 变化时才重算。
 */
const renderedKnowledgeHtml = computed(() => {
  const kb = selectedLog.value?.knowledge
  if (!kb) {
    return { description: '', cause: '', action: '', impact: '' }
  }
  return {
    description: renderContextualizedHtml(kb.description || kb.message),
    cause: renderContextualizedHtml(kb.cause || '官方文档未提供特定原因'),
    action: renderContextualizedHtml(kb.action || '按标准网络排错规范处理'),
    impact: renderContextualizedHtml(kb.impact)
  }
})

// 官方知识库参数字典与现场实际值对照列表
const kbParamDefs = computed(() => {
  const kb = selectedLog.value?.knowledge
  if (!kb || !kb.parameters) return []

  let defs = []
  try {
    const parsed = typeof kb.parameters === 'string' ? JSON.parse(kb.parameters) : kb.parameters
    if (Array.isArray(parsed)) {
      defs = parsed
    }
  } catch (e) {
    return []
  }

  const rawParams = parsedParameters.value || {}
  const normParams = new Map()
  for (const [k, v] of Object.entries(rawParams)) {
    normParams.set(normalizeParamKey(k), v)
  }

  return defs.map(d => {
    const name = d.name || d.Name || ''
    const desc = d.description || d.Description || ''
    let actualVal = rawParams[name]
    if (actualVal === undefined) {
      actualVal = normParams.get(normalizeParamKey(name))
    }

    return {
      name,
      description: desc,
      actualValue: actualVal
    }
  })
})

// WEB-13: 由 store 预建的 logId -> rcaEvent 倒排索引直接命中，O(1) 查询。
// 旧实现每次渲染都要遍历全部 RCA 事件并 JSON.parse 其关联日志列表。
const matchedRCA = computed(() => {
  const logId = selectedLog.value?.id
  if (!logId) return null
  return taskStore.rcaOfLog(logId)
})

/**
 * WEB-06: 只负责拉取任务列表，不再顺带触发一次完整加载。
 *
 * 原实现在拿到列表后会内部再调用 `handleTaskChange(...)`，
 * 而调用方（导入完成、创建任务）紧接着又调一次，
 * 于是一次操作发出 8+ 个请求、页面反复闪烁 loading。
 * 现在"拉列表"与"切任务"职责分离，由调用方显式编排。
 */
const fetchTasks = async () => {
  try {
    const res = await api.getTasks()
    if (res.code !== 0) return
    taskList.value = res.data || []
    if (route.params.id) {
      currentTaskId.value = route.params.id
    } else if (taskList.value.length > 0 && !currentTaskId.value) {
      currentTaskId.value = taskList.value[0].task_id
    }
    // 同步 currentTask 快照，避免列表刷新后标题栏仍显示旧任务名
    currentTask.value = taskList.value.find(t => t.task_id === currentTaskId.value) || null
  } catch (e) {
    // 错误已由 api 拦截器统一弹出
  }
}

const handleTaskChange = async (taskId) => {
  currentTaskId.value = taskId
  currentTask.value = taskList.value.find(t => t.task_id === taskId)
  filter.value.page = 1
  filter.value.sourceFile = ''
  filter.value.deviceId = null
  selectedLog.value = null

  /**
   * WEB-12: 文件 / 设备 / RCA 三个维度彼此独立，旧实现串行 await 需要 3 个 RTT。
   * 这里改为并发拉取；日志列表依赖设备与文件下拉框就绪，仍放在最后串行执行。
   */
  await Promise.all([fetchTaskFiles(), fetchTaskDevices(), fetchRCA()])
  await fetchLogs()
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

const fetchTaskDevices = async () => {
  if (!currentTaskId.value) return
  try {
    const res = await api.getDevices(currentTaskId.value)
    if (res.code === 0) {
      taskDevices.value = res.data || []
    }
  } catch (e) {
    console.error('Fetch task devices failed:', e)
  }
}

const onFilterChange = async () => {
  filter.value.page = 1
  await fetchLogs()
}

const fetchLogs = async () => {
  if (!currentTaskId.value) return
  loadingLogs.value = true
  try {
    // 复用 filter store 的参数组装逻辑：空值一律不下发，
    // 避免把 "" / null 当成有效过滤条件传给后端 (WEB-05)
    const params = filterStore.toLogQueryParams(filter.value.page, filter.value.pageSize)
    const res = await api.queryTaskLogs(currentTaskId.value, params)
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
  // 交由 task store 统一拉取，并在内部同步构建 logId -> rcaEvent 倒排索引 (WEB-13)
  try {
    await taskStore.fetchRCA(currentTaskId.value)
  } catch (e) {
    console.error('Fetch RCA events failed:', e)
  }
}

const selectLog = (log) => {
  selectedLog.value = log
}

const exportingHTML = ref(false)
const handleExportHTML = async () => {
  if (!currentTaskId.value || exportingHTML.value) return
  exportingHTML.value = true
  try {
    await api.downloadTaskReport(currentTaskId.value, 'html')
    ElMessage.success('HTML 报告已成功导出并下载')
  } catch (e) {
    // 错误已由 api 统一拦截器弹出提示
  } finally {
    exportingHTML.value = false
  }
}

// 新建任务
const openNewTaskDialog = () => {
  const nowStr = new Date().toISOString().replace(/[-:T.]/g, '').substring(0, 14)
  newTaskForm.value.taskName = `Audit-${nowStr}`
  newTaskForm.value.deviceType = DEFAULT_DEVICE_TYPE
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
  selectedPaths.value = []
  manualLogText.value = ''
  manualFileName.value = ''
  importTab.value = 'dir'
  showImportDialog.value = true
}

const openImportTextTab = () => {
  openImportDialog()
  importTab.value = 'text'
}

// 打开服务端本地路径选择器（dir: 目录模式，file: 文件模式）
const openPicker = mode => {
  pickerMode.value = mode
  importTab.value = mode
  showPathPicker.value = true
}

// 引导区域快捷触发：直接拉起对应模式的路径选择器
const openGuidePicker = mode => {
  openImportDialog()
  openPicker(mode)
}

const removePath = index => {
  selectedPaths.value.splice(index, 1)
}

// 取路径的末级名称（兼容 Windows 与 Unix 分隔符）
const pathBaseName = p => {
  if (!p) return ''
  const segs = String(p).replace(/\\/g, '/').split('/').filter(Boolean)
  return segs.length ? segs[segs.length - 1] : p
}

const isConflictFile = (fileName) => {
  return taskFiles.value.some(tf => tf.file_name === fileName)
}

// 是否具备开始导入的条件
const canStartImport = computed(() => {
  if (importTab.value === 'text') return manualLogText.value.trim() !== ''
  return selectedPaths.value.length > 0
})

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

  if (selectedPaths.value.length === 0) {
    ElMessage.warning('请选择至少一个日志目录或日志文件')
    return
  }

  // 检查是否有同名文件（目录模式下最终文件名由服务端展开决定，无法预先判断）
  const conflicts = []
  if (importTab.value === 'files') {
    for (const p of selectedPaths.value) {
      const name = pathBaseName(p)
      if (isConflictFile(name)) {
        conflicts.push(name)
      }
    }
  }

  if (conflicts.length > 0) {
    conflictingFileNames.value = conflicts
    showConflictDialog.value = true
  } else {
    executeImportWithConflict('rename')
  }
}

// 执行实际导入
const executeImportWithConflict = async (conflictMode) => {
  if (!currentTaskId.value) return
  submitting.value = true
  showConflictDialog.value = false

  try {
    let res
    if (importTab.value === 'text') {
      res = await api.importTaskLogsText(currentTaskId.value, {
        content: manualLogText.value,
        fileName: manualFileName.value.trim() || 'manual_input.txt',
        conflictMode
      })
    } else {
      // 仅提交路径，由服务端直接读取本地磁盘上的日志文件
      res = await api.importTaskLogsByPaths(currentTaskId.value, {
        paths: selectedPaths.value,
        exts: logExts,
        recursive: true,
        conflictMode
      })
    }

    if (res.code === 0) {
      showImportDialog.value = false
      selectedPaths.value = []
      manualLogText.value = ''
      manualFileName.value = ''

      // 启动阶段进度追踪弹窗
      if (res.data?.job_id) {
        currentJobId.value = res.data.job_id
        showProgressModal.value = true
      } else {
        await handleLogImportCompleted(res.data)
      }
    }
  } finally {
    submitting.value = false
  }
}

// 触发当前任务全量重新分析
// WEB-16: 与 Tasks 共用同一套"重新分析"编排，消除逐行重复与行为分叉
const { reanalyze } = useReanalyze({
  onJobStarted: (jobId) => {
    currentJobId.value = jobId
    showProgressModal.value = true
  },
  onSettled: async (data) => {
    await handleLogImportCompleted(data)
  }
})

const handleReanalyzeTask = () => {
  if (!currentTaskId.value || !currentTask.value) return
  reanalyze(currentTask.value)
}

// 日志导入完成回调：自动刷新并无缝载入审计工作台
const handleLogImportCompleted = async (result) => {
  ElMessage.success({
    message: `🎉 日志审计分析完成！共处理 ${result?.log_count || 0} 行日志，匹配知识 ${result?.matched_count || 0} 条，识别出 ${result?.rca_count || 0} 个 RCA 根因事件`,
    duration: 4000
  })
  await fetchTasks()
  if (currentTaskId.value) {
    await handleTaskChange(currentTaskId.value)
    await refreshActiveSubView()
  }
}

// 监听视图模式切换：确保切换到多设备时间线、设备管理、RCA分析等子视图时展示最新数据
watch(currentViewMode, async (newMode) => {
  if (!currentTaskId.value) return
  if (newMode === VIEW_MODE.DEVICES) {
    await fetchTaskDevices()
    deviceManagerRef.value?.fetchDevices?.()
  } else if (newMode === VIEW_MODE.WORKBENCH) {
    await fetchLogs()
    await fetchTaskFiles()
  }
})

// WEB-16: 时间/体积格式化统一走 utils/format，消除与 Tasks、ServerPathPicker 的重复实现
const formatTime = (ts) => sharedFormatTime(ts, '无法解析')
const formatSize = sharedFormatSize

const getSevClass = (sev) => {
  if (sev <= 2) return 'sev-crit'
  if (sev <= 4) return 'sev-err'
  if (sev <= 5) return 'sev-warn'
  return 'sev-info'
}

// WEB-06: 首屏显式编排——先拉列表，再按解析出的任务 ID 加载一次详情，
// 避免"列表内部偷偷加载一次 + 外部再加载一次"的重复请求。
onMounted(async () => {
  await fetchTasks()
  if (currentTaskId.value) {
    await handleTaskChange(currentTaskId.value)
  }
})

// WEB-03：/audit 与 /audit/:id 指向同一组件，Vue Router 会复用组件实例。
// 此前只在 fetchTasks 里读一次 route.params.id，导致从 /audit/xxx 导航回 /audit
// （或在两个任务间跳转）时页面完全不刷新，仍停留在旧任务。
watch(
  () => route.params.id,
  async (newId, oldId) => {
    if (newId === oldId) return
    // 路由带明确任务 ID 时以路由为准；回到 /audit 时保留当前选择，避免列表被重置
    if (newId) {
      currentTaskId.value = newId
      await handleTaskChange(newId)
    }
  }
)
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
  flex-shrink: 0;
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

.workbench-main-view {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-height: 0;
  overflow: hidden;
}

.workbench-body {
  flex: 1;
  display: flex;
  min-height: 0;
  overflow: hidden;
}

/* 左栏 28% */
.col-left {
  width: 28%;
  border-right: 1px solid #e2e8f0;
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  background: #f8fafc;
}
.filter-panel {
  padding: 10px;
  border-bottom: 1px solid #e2e8f0;
  display: flex;
  flex-direction: column;
  gap: 8px;
  flex-shrink: 0;
}
.filter-row {
  display: flex;
  justify-content: space-between;
}
.log-stream-list {
  flex: 1;
  min-height: 0;
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
.event-summary-box-wb {
  background: #f0f9ff;
  border: 1px solid #bae6fd;
  border-radius: 6px;
}
.event-summary-highlight-wb {
  display: flex;
  align-items: flex-start;
  font-size: 13px;
  font-weight: 500;
  color: #0369a1;
  line-height: 1.5;
  padding: 4px 0;
}
.summary-text-wb {
  word-break: break-word;
}
.log-card-footer {
  font-size: 10px;
  color: #94a3b8;
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 4px;
  margin-top: 4px;
}
.log-time {
  font-family: monospace;
  color: #64748b;
}
.host-tag {
  background: #e0f2fe;
  color: #0369a1;
  padding: 0 4px;
  border-radius: 2px;
  max-width: 90px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
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
  flex-shrink: 0;
}

/* 中栏 36% */
.col-middle {
  width: 36%;
  border-right: 1px solid #e2e8f0;
  overflow-y: auto;
  height: 100%;
  min-height: 0;
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
.flex-between {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.param-count-badge {
  font-size: 11px;
  font-weight: normal;
  color: #0284c7;
  background: #e0f2fe;
  padding: 1px 6px;
  border-radius: 4px;
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
.param-grid-enhanced {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
  gap: 8px;
}
.param-card {
  background: #ffffff;
  border: 1px solid #e2e8f0;
  border-radius: 6px;
  padding: 8px 10px;
  display: flex;
  flex-direction: column;
  gap: 4px;
  transition: all 0.15s ease;
}
.param-card:hover {
  border-color: #38bdf8;
  box-shadow: 0 2px 6px rgba(0,0,0,0.04);
}
.param-card.has-desc {
  border-left: 3px solid #0284c7;
}
.param-card-top {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 6px;
}
.p-key {
  color: #0284c7;
  font-weight: 600;
  font-size: 12px;
}
.p-desc-badge {
  font-size: 11px;
  color: #0369a1;
  background: #f0f9ff;
  padding: 1px 5px;
  border-radius: 3px;
  max-width: 130px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  cursor: help;
}
.p-val-box {
  background: #f8fafc;
  padding: 3px 6px;
  border-radius: 4px;
  font-family: monospace;
  font-size: 12px;
  word-break: break-all;
}
.p-val {
  color: #0f172a;
  font-weight: 500;
}
.template-box {
  background: #f8fafc;
  border: 1px solid #e2e8f0;
  border-radius: 6px;
  padding: 10px 12px;
}
.template-rendered {
  font-size: 13px;
  line-height: 1.6;
  color: #1e293b;
  margin-bottom: 8px;
}
:deep(.inst-param-injected) {
  background: #dcfce7;
  color: #15803d;
  font-weight: 600;
  padding: 1px 5px;
  border-radius: 3px;
  border-bottom: 1.5px solid #22c55e;
}
:deep(.inst-param-placeholder) {
  background: #f1f5f9;
  color: #64748b;
  padding: 1px 4px;
  border-radius: 3px;
}
.template-raw-sub {
  font-size: 11px;
  color: #64748b;
  border-top: 1px dashed #cbd5e1;
  padding-top: 6px;
  word-break: break-all;
}
.template-raw-sub .sub-label {
  margin-right: 6px;
  font-weight: 500;
}
.template-raw-sub code {
  font-family: monospace;
  color: #475569;
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
  height: 100%;
  min-height: 0;
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
.kb-header-top {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.kb-title {
  font-size: 16px;
  font-weight: bold;
  color: #0369a1;
}
.kb-meta {
  margin-top: 6px;
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
  font-size: 11px;
}
.badge-tier { background: #e0f2fe; color: #0284c7; padding: 2px 6px; border-radius: 3px; }
.badge-conf { background: #dcfce7; color: #15803d; padding: 2px 6px; border-radius: 3px; font-weight: bold; }
.badge-ctx {
  background: #dcfce7;
  color: #166534;
  padding: 2px 6px;
  border-radius: 3px;
  font-weight: 500;
}
.dict-count-tag {
  font-size: 11px;
  font-weight: normal;
  color: #64748b;
}
.dict-pname {
  font-family: monospace;
  font-weight: 600;
  color: #0284c7;
}
.dict-pval {
  font-family: monospace;
  background: #f0fdf4;
  color: #15803d;
  font-weight: 600;
  padding: 2px 6px;
  border-radius: 3px;
  border: 1px solid #bbf7d0;
}
.dict-pnone {
  color: #94a3b8;
  font-style: italic;
}
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
  white-space: pre-line;
  word-break: break-word;
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
  white-space: pre-line;
  word-break: break-word;
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
.path-import-pane {
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  padding: 24px 20px;
  text-align: center;
  background: #f8fafc;
}
.pane-title {
  font-size: 14px;
  font-weight: 600;
  color: #1e293b;
  margin: 10px 0 6px 0;
}
.pane-desc {
  font-size: 12px;
  color: #64748b;
  line-height: 1.7;
  margin: 0 auto 14px auto;
  max-width: 470px;
}
.pending-paths-box {
  margin-top: 14px;
  border: 1px solid #e2e8f0;
  border-radius: 6px;
  background: #f8fafc;
  padding: 10px 12px;
}
.pending-paths-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 8px;
  padding-bottom: 6px;
  border-bottom: 1px solid #e2e8f0;
  font-size: 13px;
  color: #334155;
}
.pending-paths-list {
  max-height: 140px;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.pending-path-item {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  padding: 5px 8px;
  background: #fff;
  border-radius: 4px;
  border: 1px solid #e2e8f0;
}
.pending-path-item .path-value {
  flex: 1;
  font-weight: 500;
  color: #1e293b;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.pending-path-item .del-btn {
  cursor: pointer;
  color: #94a3b8;
  flex-shrink: 0;
}
.pending-path-item .del-btn:hover {
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

.workbench-nav-bar {
  display: flex;
  justify-content: flex-start;
  align-items: center;
  background: #ffffff;
  padding: 8px 16px;
  border-radius: 8px;
  border: 1px solid #e2e8f0;
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.03);
  flex-shrink: 0;
}

.workbench-sub-view {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
}

.log-stream-list::-webkit-scrollbar,
.col-middle::-webkit-scrollbar,
.col-right::-webkit-scrollbar,
.workbench-sub-view::-webkit-scrollbar {
  width: 6px;
}
.log-stream-list::-webkit-scrollbar-thumb,
.col-middle::-webkit-scrollbar-thumb,
.col-right::-webkit-scrollbar-thumb,
.workbench-sub-view::-webkit-scrollbar-thumb {
  background-color: #cbd5e1;
  border-radius: 3px;
}
.log-stream-list::-webkit-scrollbar-thumb:hover,
.col-middle::-webkit-scrollbar-thumb:hover,
.col-right::-webkit-scrollbar-thumb:hover,
.workbench-sub-view::-webkit-scrollbar-thumb:hover {
  background-color: #94a3b8;
}

.rca-banner-alert {
  display: flex;
  justify-content: space-between;
  align-items: center;
  background: #fff7ed;
  border: 1px solid #fed7aa;
  border-left: 4px solid #f97316;
  border-radius: 6px;
  padding: 8px 12px;
  margin-bottom: 12px;
}
.rca-banner-alert .banner-left {
  display: flex;
  align-items: center;
  gap: 8px;
}
.rca-banner-alert .banner-text {
  font-size: 12px;
  color: #9a3412;
}
</style>

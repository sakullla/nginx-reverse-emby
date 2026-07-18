<template>
  <div class="rules-page">
    <div class="rules-page__header">
      <div class="rules-page__header-left">
        <h1 class="rules-page__title">L4 规则</h1>
        <p class="rules-page__subtitle">
          <template v-if="hasAgentFilter">
            共 {{ listTotal }} 条 · 本页 {{ rules.length }} 条 · 启用 {{ enabledCount }} 条
          </template>
          <template v-else>
            暂无可用节点
          </template>
        </p>
      </div>
      <div class="rules-page__header-right">
        <ViewToggle v-if="hasAgentFilter && (listTotal > 0 || listQ || searchQuery)" v-model:view="view" />
        <button v-if="canCreate" class="btn btn-primary" @click="startCreate">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
            <line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/>
          </svg>
          <span class="btn-text">添加 L4 规则</span>
        </button>
      </div>
    </div>

    <OperationStatusList />

    <ResourceListFilterBar
      :agent-id="agentFilter || ALL_AGENTS_FILTER"
      :agents="allAgents"
      :q="searchQuery"
      search-placeholder="搜索协议 / 地址 / 端口 / 标签 / #id=..."
      :status-fields="enabledStatusFields"
      :status-values="statusValues"
      @update:agent-id="handleAgentSelect"
      @update:q="searchQuery = $event"
      @update:status="onStatusUpdate"
    />

    <!-- No agents available -->
    <div v-if="!allAgents.length" class="rules-page__prompt">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <rect x="2" y="2" width="20" height="8" rx="2"/><rect x="2" y="14" width="20" height="8" rx="2"/>
      </svg>
      <p>暂无可用节点</p>
      <p class="rules-page__prompt-hint">请先加入节点后再管理 L4 规则</p>
      <RouterLink to="/agents" class="btn btn-primary">加入节点</RouterLink>
    </div>

    <!-- Filter active, no rules -->
    <div v-else-if="hasAgentFilter && !rules.length && !isLoading" class="rules-page__empty">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <rect x="2" y="2" width="20" height="8" rx="2"/><rect x="2" y="14" width="20" height="8" rx="2"/>
      </svg>
      <template v-if="hasActiveFilters">
        <p>没有匹配的 L4 规则</p>
      </template>
      <template v-else>
        <p>暂无 L4 规则</p>
        <button v-if="canCreate" class="btn btn-primary" @click="startCreate">添加第一条规则</button>
        <p v-else class="rules-page__prompt-hint">全部节点视图下请先选择具体节点再新建</p>
      </template>
    </div>

    <!-- No search results -->
    <div v-if="hasAgentFilter && rules.length && !filteredRules.length" class="rules-page__prompt">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>
      </svg>
      <p>没有匹配的 L4 规则</p>
    </div>

    <!-- Rule card grid -->
    <div v-show="hasAgentFilter && filteredRules.length && view === 'card'" class="rule-grid">
      <L4RuleItem
        v-for="rule in filteredRules"
        :key="`${rule.agent_id || ''}:${rule.id}`"
        :rule="rule"
        :agent="selectedAgent"
        :traffic="trafficForRule(rule)"
        :agent-node-total="nodeTotalFor(rule)"
        @edit="startEdit"
        @delete="startDelete"
        @copy="handleCopy"
        @toggle="toggleRule"
        @diagnose="openDiagnostic"
        @traffic-click="openTrendModal"
      />
    </div>

    <!-- Rule list table -->
    <L4RuleTable
      v-show="hasAgentFilter && filteredRules.length && view === 'list'"
      :rules="filteredRules"
      :agent="selectedAgent"
      @edit="startEdit"
      @toggle="toggleRule"
      @delete="startDelete"
    />

    <ListPagination
      v-if="hasAgentFilter && listTotal > 0"
      :page="page"
      :page-size="pageSize"
      :total="listTotal"
      @update:page="page = $event"
    />

    <!-- Loading -->
    <div v-if="isLoading" class="rules-page__loading">
      <div class="spinner"></div>
    </div>

    <!-- Add/Edit Modal -->
    <BaseModal
      :model-value="showAddForm || !!editingRule"
      :title="editingRule ? '编辑 L4 规则' : '添加 L4 规则'"
      size="xl"
      :close-on-click-modal="false"
      @update:model-value="closeForm"
    >
      <L4RuleForm :initial-data="editingRule" :agent-id="formAgentId" :l4-rules="rules" @success="closeForm" />
    </BaseModal>

    <!-- Copy Modal -->
    <BaseModal
      :model-value="showCopyModal"
      title="复制 L4 规则"
      size="xl"
      :close-on-click-modal="false"
      @update:model-value="closeCopy"
    >
      <L4RuleForm v-if="copyingRule" :initial-data="copyingRule" :agent-id="formAgentId" :l4-rules="rules" @success="closeCopy" />
    </BaseModal>

    <!-- Delete Modal -->
    <DeleteConfirmDialog
      :show="!!deletingRule"
      title="确认删除规则"
      message="删除后该规则将立即失效，相关配置将无法恢复。"
      :name="deletingRule?.listen_host + ':' + deletingRule?.listen_port"
      confirm-text="确认删除"
      :loading="deleteL4Rule.isPending?.value"
      @confirm="confirmDelete"
      @cancel="deletingRule = null"
    />

    <RuleDiagnosticModal
      :model-value="showDiagnostic"
      :task="diagnosticTask"
      kind="l4_tcp"
      :rule-label="diagnosticRule?.name || `${diagnosticRule?.listen_host || ''}:${diagnosticRule?.listen_port || ''}`"
      :endpoint-label="diagnosticRule ? l4BackendAddresses(diagnosticRule).join(', ') : ''"
      :agent-label="selectedAgentLabel"
      @update:model-value="closeDiagnostic"
    />

    <TrafficTrendModal
      v-model:visible="trendModal.visible"
      :agent-id="trendModal.agentId"
      :scope-type="trendModal.scopeType"
      :scope-id="trendModal.scopeId"
      :scope-label="trendModal.scopeLabel"
      :direction="trafficDirection"
    />

    <IdCandidateModal
      v-model:visible="candidateModalVisible"
      :id="candidateModalId"
      :candidates="candidateModalCandidates"
      @select="handleCandidateSelect"
    />
  </div>

    <CreateAgentPicker
      :visible="showCreateAgentPicker"
      :agents="allAgents"
      @select="confirmCreateAgent"
      @cancel="showCreateAgentPicker = false"
    />
</template>

<script setup>
import { ref, computed, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAgent } from '../context/AgentContext'
import { useL4RulesList, useCreateL4Rule, useUpdateL4Rule, useDeleteL4Rule } from '../hooks/useL4Rules'
import { useDiagnoseL4Rule, useDiagnosticTask } from '../hooks/useDiagnostics'
import { useAgents } from '../hooks/useAgents'
import { fetchAllAgentsL4Rules } from '../api'
import { findAllMatchesInAgents, shouldStartCrossAgentIdSearch } from '../hooks/useIdSearch'
import { useTrafficSummaryForResources } from '../hooks/useTrafficSummaryForResources'
import IdCandidateModal from '../components/IdCandidateModal.vue'
import L4RuleForm from '../components/L4RuleForm.vue'
import L4RuleItem from '../components/l4/L4RuleItem.vue'
import DeleteConfirmDialog from '../components/DeleteConfirmDialog.vue'
import BaseModal from '../components/base/BaseModal.vue'
import RuleDiagnosticModal from '../components/RuleDiagnosticModal.vue'
import TrafficTrendModal from '../components/traffic/TrafficTrendModal.vue'
import ResourceListFilterBar from '../components/common/ResourceListFilterBar.vue'
import CreateAgentPicker from '../components/common/CreateAgentPicker.vue'
import ViewToggle from '../components/common/ViewToggle.vue'
import ListPagination from '../components/common/ListPagination.vue'
import L4RuleTable from '../components/l4/L4RuleTable.vue'
import OperationStatusList from '../components/operations/OperationStatusList.vue'
import { useViewToggle } from '../composables/useViewToggle'
import { messageStore } from '../stores/messages'
import { ALL_AGENTS_FILTER, isAllAgentsFilter, normalizeAgentFilter } from '../utils/agentFilter.js'
import { resolveCreateAgentId, resolveMutationAgentId, resolveCopyTargetAgentId } from '../utils/resolveResourceAgent.js'

const route = useRoute()
const router = useRouter()
const { view } = useViewToggle('l4rules')
const agentContext = useAgent()
const { selectedAgentId } = agentContext
const systemInfo = agentContext.systemInfo || ref(null)
// Agents list for sync status derivation
const { data: agentsData } = useAgents()
const allAgents = computed(() => agentsData.value ?? [])
const registeredAgentIds = computed(() => new Set((agentsData.value || []).map((agent) => String(agent.id))))
const agentFilter = computed(() => {
  const raw = normalizeAgentFilter(route.query.agentId || selectedAgentId.value)
  if (!raw) return null
  if (isAllAgentsFilter(raw)) return raw
  return registeredAgentIds.value.has(String(raw)) ? raw : null
})
const agentId = computed(() => {
  const filter = agentFilter.value
  if (!filter || isAllAgentsFilter(filter)) return null
  return filter
})
const hasAgentFilter = computed(() => Boolean(agentFilter.value) && allAgents.value.length > 0)
const createResolve = computed(() => resolveCreateAgentId(
  agentFilter.value,
  allAgents.value,
  { systemInfo: systemInfo?.value }
))
const formAgentId = ref(agentId.value || '')
const showCreateAgentPicker = ref(false)
const canCreate = computed(() => (
  hasAgentFilter.value
  && (Boolean(createResolve.value.agentId) || createResolve.value.needsSelection)
))
const selectedAgent = computed(() => agentsData.value?.find(a => a.id === agentId.value))

const page = ref(1)
const pageSize = 20
const searchQuery = ref('')
const listQ = computed(() => {
  const raw = searchQuery.value.trim()
  if (!raw) return ''
  if (/^#id=\S+$/.test(raw)) return ''
  return raw
})

const enabledStatusValue = ref('')
const enabledFilter = computed(() => {
  if (enabledStatusValue.value === 'true') return true
  if (enabledStatusValue.value === 'false') return false
  return undefined
})
const enabledStatusFields = [
  {
    key: 'enabled',
    label: '启用状态',
    options: [
      { value: '', label: '全部' },
      { value: 'true', label: '启用' },
      { value: 'false', label: '停用' }
    ]
  }
]
const statusValues = computed(() => ({ enabled: enabledStatusValue.value }))
function onStatusUpdate({ key, value }) {
  if (key === 'enabled') enabledStatusValue.value = value == null ? '' : String(value)
}
const hasActiveFilters = computed(() => (
  Boolean(listQ.value)
  || Boolean(String(searchQuery.value || '').trim())
  || enabledStatusValue.value !== ''
))

watch([agentFilter, listQ, enabledStatusValue], () => { page.value = 1 })

const { data: _rulesPage, isLoading } = useL4RulesList({
  agentFilter,
  page,
  pageSize,
  q: listQ,
  enabledFilter,
  enabled: hasAgentFilter
})
const rules = computed(() => _rulesPage.value?.items ?? [])

const trafficStatsEnabled = computed(() => !!systemInfo.value && systemInfo.value.traffic_stats_enabled !== false)
const { nodeTotalFor, trafficFor: trafficForRule } = useTrafficSummaryForResources({
  agentId,
  items: rules,
  trafficStatsEnabled,
  mapName: 'l4_rules'
})


function requireMutationAgent(resource, actionLabel = '操作') {
  const resolved = resolveMutationAgentId(resource, agentFilter.value, {
    fallbackAgentId: agentId.value,
  })
  if (!resolved.agentId) {
    messageStore.error(resolved.error || `缺少节点归属，无法${actionLabel}`)
    return null
  }
  return resolved.agentId
}

function startCreate() {
  const resolved = resolveCreateAgentId(agentFilter.value, allAgents.value, {
    systemInfo: systemInfo?.value,
  })
  if (resolved.agentId) {
    formAgentId.value = resolved.agentId
    showAddForm.value = true
    return
  }
  if (resolved.needsSelection) {
    showCreateAgentPicker.value = true
    return
  }
  messageStore.error('请先选择节点后再新建')
}

function confirmCreateAgent(agent) {
  const id = String(agent?.id || agent?.agent_id || '').trim()
  if (!id) {
    messageStore.error('请选择有效节点')
    return
  }
  formAgentId.value = id
  showCreateAgentPicker.value = false
  showAddForm.value = true
}

function handleAgentSelect(id) {
  router.replace({ query: { ...route.query, agentId: id } })
}
const selectedAgentLabel = computed(() => String(selectedAgent.value?.name || agentId.value || '').trim())
const createL4Rule = useCreateL4Rule(agentId)
const updateL4Rule = useUpdateL4Rule(agentId)
const deleteL4Rule = useDeleteL4Rule(agentId)
const diagnoseL4Rule = useDiagnoseL4Rule(agentId)
const listTotal = computed(() => _rulesPage.value?.total ?? 0)

// Pre-fill / clear search only when the route deep-link search param changes.
// Do not use watchEffect over route.query — other keys (agentId) would wipe typed input.
watch(
  () => route.query.search,
  (search) => {
    searchQuery.value = search == null ? '' : String(search)
  },
  { immediate: true },
)

function l4BackendAddresses(rule) {
  if (Array.isArray(rule?.backends) && rule.backends.length > 0) {
    return rule.backends
      .map((backend) => {
        const host = String(backend?.host || '').trim()
        const port = Number(backend?.port)
        return host && Number.isInteger(port) && port > 0 ? `${host}:${port}` : ''
      })
      .filter(Boolean)
  }
  return []
}

const filteredRules = computed(() => {
  const raw = searchQuery.value.trim()
  if (!raw) return rules.value
  const idMatch = raw.match(/^#id=(\S+)$/)
  if (idMatch) return rules.value.filter(rule => String(rule.id) === idMatch[1])
  return rules.value
})

// R3: Cross-agent #id= resolution — if not found in current agent, search all agents
const _crossSearching = ref(false)
const candidateModalVisible = ref(false)
const candidateModalCandidates = ref([])
const candidateModalId = ref('')

watch([filteredRules, isLoading], ([result]) => {
  const idQuery = shouldStartCrossAgentIdSearch({
    search: searchQuery.value,
    currentMatches: result,
    isLoading: isLoading.value,
    isSearching: _crossSearching.value
  })
  if (!idQuery) return
  const agentIds = allAgents.value.map(a => a.id)
  if (!agentIds.length) return
  _crossSearching.value = true
  candidateModalId.value = idQuery.id
  fetchAllAgentsL4Rules(agentIds).then(allData => {
    const allMatches = findAllMatchesInAgents({ l4Rules: allData }, idQuery.id)
    if (allMatches.length === 1) {
      router.replace({ query: { ...route.query, agentId: allMatches[0].agentId, search: searchQuery.value } })
    } else if (allMatches.length > 1) {
      candidateModalCandidates.value = allMatches
      candidateModalVisible.value = true
    }
  }).finally(() => { _crossSearching.value = false })
})

function handleCandidateSelect(candidate) {
  router.replace({ query: { ...route.query, agentId: candidate.agentId, search: searchQuery.value } })
}

const enabledCount = computed(() => rules.value.filter(r => r.enabled).length)

// Modals
const showAddForm = ref(false)
const editingRule = ref(null)
const copyingRule = ref(null)
const showCopyModal = ref(false)
const deletingRule = ref(null)
const showDiagnostic = ref(false)
const diagnosticRule = ref(null)
const diagnosticTaskId = ref('')
const diagnosticAgentId = ref('')
const initialDiagnosticTask = ref(null)
const { data: diagnosticTaskData } = useDiagnosticTask(diagnosticAgentId, diagnosticTaskId)
const diagnosticTask = computed(() => diagnosticTaskData.value?.task || initialDiagnosticTask.value)

const trendModal = ref({ visible: false, agentId: '', scopeType: '', scopeId: '', scopeLabel: '' })
const trafficDirection = ref('both')

function openTrendModal(rule) {
  const id = requireMutationAgent(rule, '查看流量')
  if (!id) return
  trendModal.value = {
    visible: true,
    agentId: id,
    scopeType: 'l4_rule',
    scopeId: String(rule.id),
    scopeLabel: `L4 规则 #${rule.id}`
  }
}

function toggleRule(rule) {
  const target = requireMutationAgent(rule, '启停')
  if (!target) return
  updateL4Rule.mutate({ id: rule.id, enabled: !rule.enabled, agentId: target })
}

function startEdit(rule) {
  const target = requireMutationAgent(rule, '编辑')
  if (!target) return
  formAgentId.value = target
  editingRule.value = rule
}

function handleCopy(rule) {
  const resolved = resolveCopyTargetAgentId(agentFilter.value, allAgents.value, {
    systemInfo: systemInfo?.value,
  })
  if (resolved.agentId) {
    formAgentId.value = resolved.agentId
  } else if (resolved.needsSelection) {
    const source = String(rule?.agent_id || '').trim()
    if (source) formAgentId.value = source
    else {
      messageStore.error('全部节点视图下复制请先选择目标节点')
      return
    }
  } else {
    messageStore.error('请先选择节点后再复制')
    return
  }
  const { id, ...rest } = rule
  copyingRule.value = rest
  showCopyModal.value = true
}

function startDelete(rule) {
  deletingRule.value = rule
}

function closeForm() {
  showAddForm.value = false
  editingRule.value = null
}

function closeCopy() {
  showCopyModal.value = false
  copyingRule.value = null
}

function confirmDelete() {
  if (!deletingRule.value) return
  const target = requireMutationAgent(deletingRule.value, '删除')
  if (!target) return
  deleteL4Rule.mutate({ id: deletingRule.value.id, agentId: target })
  deletingRule.value = null
}

async function openDiagnostic(rule) {
  const target = requireMutationAgent(rule, '诊断')
  if (!target) return
  diagnosticRule.value = rule
  diagnosticAgentId.value = target
  showDiagnostic.value = true
  try {
    const response = await diagnoseL4Rule.mutateAsync({ id: rule.id, agentId: target })
    initialDiagnosticTask.value = response.task || null
    diagnosticTaskId.value = response.task_id
  } catch (error) {
    closeDiagnostic()
    messageStore.error(error, '启动 L4 规则诊断失败')
  }
}

function closeDiagnostic() {
  showDiagnostic.value = false
  diagnosticRule.value = null
  diagnosticAgentId.value = ''
  diagnosticTaskId.value = ''
  initialDiagnosticTask.value = null
}
</script>

<style scoped>
.rules-page {
  max-width: 1200px;
  margin: 0 auto;
  animation: fadeIn var(--duration-normal) var(--ease-default) both;
}

.rules-page__header {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  margin-bottom: 0.85rem;
  gap: 0.75rem 1rem;
  flex-wrap: wrap;
}

.rules-page__header-left { flex: 1; min-width: 0; }

.rules-page__header-right {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  flex-shrink: 0;
}

.rules-page__title {
  font-size: 1.3125rem;
  font-weight: 700;
  margin: 0 0 0.15rem;
  color: var(--color-text-primary);
  letter-spacing: -0.02em;
  line-height: 1.25;
}

.rules-page__subtitle {
  font-size: 0.75rem;
  color: var(--color-text-tertiary);
  margin: 0;
  line-height: 1.35;
  font-variant-numeric: tabular-nums;
}

.rules-page__prompt,
.rules-page__empty,
.rules-page__loading {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 0.75rem;
  padding: 3.25rem 1.5rem;
  color: var(--color-text-muted);
  text-align: center;
  animation: fadeIn 0.3s var(--ease-default) both;
}

.rules-page__prompt-hint {
  font-size: 0.8125rem;
  color: var(--color-text-tertiary);
}

@media (max-width: 640px) {
  .rules-page__header {
    align-items: flex-start;
    gap: 0.5rem;
  }

  .rules-page__header-right {
    width: 100%;
    justify-content: flex-end;
  }
}

.rule-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: 0.75rem;
}

@media (min-width: 1280px) {
  .rule-grid { grid-template-columns: repeat(auto-fill, minmax(340px, 1fr)); }
}

.rule-grid,
.rules-page :deep(.rule-table) {
  animation: viewToggleIn 200ms var(--ease-default) both;
}
@keyframes viewToggleIn {
  from { opacity: 0; transform: translateY(4px); }
  to { opacity: 1; transform: translateY(0); }
}
@media (prefers-reduced-motion: reduce) {
  .rule-grid,
  .rules-page :deep(.rule-table) {
    animation: none;
  }
}
</style>

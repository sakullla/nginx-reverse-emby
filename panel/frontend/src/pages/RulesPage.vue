<template>
  <div class="rules-page">
    <div class="rules-page__header">
      <div class="rules-page__header-left">
        <h1 class="rules-page__title">HTTP 规则</h1>
        <p class="rules-page__subtitle">
          <template v-if="agentId">
            {{ rules.length }} 条规则 · 启用 {{ enabledCount }} 条
          </template>
          <template v-else>
            请先选择一个节点
          </template>
        </p>
      </div>
      <div class="rules-page__header-right">
        <ViewToggle v-if="agentId && rules.length" v-model:view="view" />
        <div class="search-wrapper" v-if="agentId && rules.length" @click="focusSearch">
          <svg class="search-icon-btn" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>
          <input ref="searchInputRef" v-model="searchQuery" name="rule-search" class="search-input" placeholder="搜索 URL / 标签 / #id=...">
          <button v-if="searchQuery" class="clear-btn" @click.stop="searchQuery = ''">
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
          </button>
        </div>
        <button v-if="agentId" class="btn btn-primary" @click="showAddForm = true">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
            <line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/>
          </svg>
          <span class="btn-text">添加规则</span>
        </button>
      </div>
    </div>

    <QuickAgentSelect
      :agentId="agentId"
      :agents="allAgents"
      @update:agentId="handleAgentSelect"
    />

    <!-- No agent selected -->
    <div v-if="!agentId" class="rules-page__prompt">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>
        <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>
      </svg>
      <p>请从上方选择一个节点来管理规则</p>
      <p class="rules-page__prompt-hint">或前往节点管理页面添加新节点</p>
      <RouterLink to="/agents" class="btn btn-primary">加入节点</RouterLink>
    </div>

    <!-- Agent selected, no rules -->
    <div v-else-if="agentId && !rules.length && !isLoading" class="rules-page__empty">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>
        <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>
      </svg>
      <p>暂无规则</p>
      <button class="btn btn-primary" @click="showAddForm = true">添加第一条规则</button>
    </div>

    <!-- No search results -->
    <div v-else-if="agentId && rules.length && !filteredRules.length" class="rules-page__empty">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>
      </svg>
      <p>没有匹配的规则</p>
    </div>

    <!-- Rules card grid -->
    <div v-show="agentId && filteredRules.length && view === 'card'" class="rule-grid">
      <RuleCard
        v-for="rule in filteredRules"
        :key="rule.id"
        :rule="rule"
        :agent="selectedAgent"
        :traffic="trafficForRule(rule)"
        :agent-node-total="agentNodeTotal"
        @edit="startEdit"
        @toggle="toggleRule"
        @copy="handleCopy"
        @diagnose="openDiagnostic"
        @traffic-click="openTrendModal"
        @delete="startDelete"
      />
    </div>

    <!-- Rules list table -->
    <RuleTable
      v-show="agentId && filteredRules.length && view === 'list'"
      :rules="filteredRules"
      :agent="selectedAgent"
      @edit="startEdit"
      @toggle="toggleRule"
      @delete="startDelete"
    />

    <!-- Loading -->
    <div v-if="isLoading" class="rules-page__loading">
      <div class="spinner"></div>
    </div>

    <!-- Add/Edit Form Modal -->
    <BaseModal
      :model-value="showAddForm || !!editingRule"
      :title="editingRule ? '编辑规则' : '添加规则'"
      size="xl"
      :close-on-click-modal="false"
      @update:model-value="closeForm"
    >
      <RuleForm :initial-data="editingRule" :agent-id="agentId" @success="closeForm" />
    </BaseModal>

    <!-- Copy Modal -->
    <BaseModal
      :model-value="showCopyModal"
      title="复制规则"
      size="xl"
      :close-on-click-modal="false"
      @update:model-value="closeForm"
    >
      <RuleForm v-if="copyingRule" :initial-data="copyingRule" :agent-id="agentId" @success="closeForm" />
    </BaseModal>

    <!-- Delete Modal -->
    <DeleteConfirmDialog
      :show="!!deletingRule"
      title="确认删除规则"
      message="删除后该规则将立即失效，相关配置将无法恢复。"
      :name="deletingRule?.frontend_url"
      confirm-text="确认删除"
      :loading="deleteRule.isPending?.value"
      @confirm="confirmDelete"
      @cancel="deletingRule = null"
    />

    <RuleDiagnosticModal
      :model-value="showDiagnostic"
      :task="diagnosticTask"
      kind="http"
      :rule-label="diagnosticRule?.frontend_url || ''"
      :endpoint-label="formatHttpBackend(diagnosticRule || {})"
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
</template>

<script setup>
import { ref, computed, watchEffect, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import { useAgent } from '../context/AgentContext'
import { useRules, useCreateRule, useUpdateRule, useDeleteRule } from '../hooks/useRules'
import { useDiagnoseRule, useDiagnosticTask } from '../hooks/useDiagnostics'
import { useAgents } from '../hooks/useAgents'
import { fetchTrafficSummary, fetchAllAgentsRules } from '../api'
import { parseIdQuery, findRecordInAgents, findAllMatchesInAgents } from '../hooks/useIdSearch'
import IdCandidateModal from '../components/IdCandidateModal.vue'
import RuleForm from '../components/RuleForm.vue'
import RuleCard from '../components/rules/RuleCard.vue'
import DeleteConfirmDialog from '../components/DeleteConfirmDialog.vue'
import BaseModal from '../components/base/BaseModal.vue'
import RuleDiagnosticModal from '../components/RuleDiagnosticModal.vue'
import TrafficTrendModal from '../components/traffic/TrafficTrendModal.vue'
import QuickAgentSelect from '../components/QuickAgentSelect.vue'
import ViewToggle from '../components/common/ViewToggle.vue'
import RuleTable from '../components/rules/RuleTable.vue'
import { useViewToggle } from '../composables/useViewToggle'
import { messageStore } from '../stores/messages'
import { summaryBucketForObject } from '../utils/trafficStats.js'

const route = useRoute()
const router = useRouter()
const { view } = useViewToggle('rules')
const agentContext = useAgent()
const { selectedAgentId } = agentContext
const systemInfo = agentContext.systemInfo || ref(null)

// 优先从 URL query 获取，否则 fall back 到 AgentContext
const selectedOrRouteAgentId = computed(() => route.query.agentId || selectedAgentId.value)

// Agents list for sync status derivation
const { data: agentsData } = useAgents()
const allAgents = computed(() => agentsData.value ?? [])
const registeredAgentIds = computed(() => new Set((agentsData.value || []).map((agent) => String(agent.id))))
const agentId = computed(() => {
  const id = selectedOrRouteAgentId.value
  if (!id) return null
  return registeredAgentIds.value.has(String(id)) ? id : null
})
const selectedAgent = computed(() => agentsData.value?.find(a => a.id === agentId.value))
const selectedAgentLabel = computed(() => String(selectedAgent.value?.name || agentId.value || '').trim())

const { data: _rulesData, isLoading } = useRules(agentId)
const createRule = useCreateRule(agentId)
const updateRule = useUpdateRule(agentId)
const deleteRule = useDeleteRule(agentId)
const diagnoseRule = useDiagnoseRule(agentId)
const rules = computed(() => _rulesData.value ?? [])

const trafficStatsEnabled = computed(() => !!systemInfo.value && systemInfo.value.traffic_stats_enabled !== false)
const { data: trafficSummaryData } = useQuery({
  queryKey: ['traffic-summary', agentId],
  queryFn: () => fetchTrafficSummary(agentId.value),
  enabled: () => !!agentId.value && trafficStatsEnabled.value,
  refetchInterval: 10_000
})

const agentNodeTotal = computed(() => trafficSummaryData.value?.used_bytes || 0)

function trafficForRule(rule) {
  return trafficStatsEnabled.value
    ? summaryBucketForObject(trafficSummaryData.value, 'http_rules', rule?.id)
    : null
}

function handleAgentSelect(id) {
  router.replace({ query: { ...route.query, agentId: id } })
}

// Search
const searchQuery = ref('')
const searchInputRef = ref(null)
function focusSearch() { searchInputRef.value?.focus() }

function httpBackends(rule) {
  if (Array.isArray(rule?.backends) && rule.backends.length > 0) {
    return rule.backends
      .map((backend) => String(backend?.url || '').trim())
      .filter(Boolean)
  }
  return []
}

function formatHttpBackend(rule) {
  const backends = httpBackends(rule)
  if (backends.length === 0) return '-'
  if (backends.length === 1) return backends[0]
  return `${backends[0]} +${backends.length - 1}`
}

// Pre-fill search from global search navigation; reset when param is cleared
watchEffect(() => {
  searchQuery.value = route.query.search ?? ''
})

const filteredRules = computed(() => {
  const raw = searchQuery.value.trim()
  if (!raw) return rules.value
  const idMatch = raw.match(/^#id=(\S+)$/)
  if (idMatch) return rules.value.filter(rule => String(rule.id) === idMatch[1])
  const q = raw.toLowerCase()
  return rules.value.filter(rule =>
    String(rule.frontend_url || '').toLowerCase().includes(q) ||
    httpBackends(rule).some((backend) => backend.toLowerCase().includes(q)) ||
    String(rule.name || '').toLowerCase().includes(q) ||
    (rule.tags || []).some(tag => String(tag).toLowerCase().includes(q))
  )
})

// R3: Cross-agent #id= resolution — if not found in current agent, search all agents
const _crossSearching = ref(false)
const candidateModalVisible = ref(false)
const candidateModalCandidates = ref([])
const candidateModalId = ref('')

watch(filteredRules, (result) => {
  const idQuery = parseIdQuery(searchQuery.value)
  if (!idQuery || result.length > 0 || _crossSearching.value) return
  const agentIds = allAgents.value.map(a => a.id)
  if (!agentIds.length) return
  _crossSearching.value = true
  candidateModalId.value = idQuery.id
  fetchAllAgentsRules(agentIds).then(allData => {
    const allMatches = findAllMatchesInAgents({ rules: allData }, idQuery.id)
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
const initialDiagnosticTask = ref(null)
const { data: diagnosticTaskData } = useDiagnosticTask(agentId, diagnosticTaskId)
const diagnosticTask = computed(() => diagnosticTaskData.value?.task || initialDiagnosticTask.value)

const trendModal = ref({ visible: false, agentId: '', scopeType: '', scopeId: '', scopeLabel: '' })
const trafficDirection = ref('both')

function openTrendModal(rule) {
  const id = selectedAgentId?.value || rule.agent_id
  if (!id) return
  trendModal.value = {
    visible: true,
    agentId: id,
    scopeType: 'http_rule',
    scopeId: String(rule.id),
    scopeLabel: `HTTP 规则 #${rule.id}`
  }
}

function toggleRule(rule) {
  updateRule.mutate({ id: rule.id, enabled: !rule.enabled })
}

function startEdit(rule) {
  editingRule.value = rule
}

function handleCopy(rule) {
  const { id, ...rest } = rule
  copyingRule.value = rest
  showCopyModal.value = true
}

function startDelete(rule) {
  deletingRule.value = rule
}

async function openDiagnostic(rule) {
  diagnosticRule.value = rule
  showDiagnostic.value = true
  try {
    const response = await diagnoseRule.mutateAsync(rule.id)
    initialDiagnosticTask.value = response.task || null
    diagnosticTaskId.value = response.task_id
  } catch (error) {
    closeDiagnostic()
    messageStore.error(error, '启动 HTTP 规则诊断失败')
  }
}

function closeForm() {
  showAddForm.value = false
  editingRule.value = null
  showCopyModal.value = false
  copyingRule.value = null
}

function closeDiagnostic() {
  showDiagnostic.value = false
  diagnosticRule.value = null
  diagnosticTaskId.value = ''
  initialDiagnosticTask.value = null
}

async function confirmDelete() {
  if (deletingRule.value) {
    await deleteRule.mutateAsync(deletingRule.value.id)
    deletingRule.value = null
  }
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
  align-items: center;
  justify-content: space-between;
  margin-bottom: 1.5rem;
  gap: 1rem;
  flex-wrap: wrap;
}

.rules-page__header-left { flex: 1; min-width: 0; }

.rules-page__header-right {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  flex-shrink: 0;
}

.rules-page__title {
  font-size: 1.5rem;
  font-weight: 700;
  margin: 0 0 0.25rem;
  color: var(--color-text-primary);
  letter-spacing: -0.02em;
}

.rules-page__subtitle {
  font-size: 0.875rem;
  color: var(--color-text-tertiary);
  margin: 0;
}

.rules-page__prompt,
.rules-page__empty,
.rules-page__loading {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 1rem;
  padding: 4rem 2rem;
  color: var(--color-text-muted);
  text-align: center;
  animation: fadeIn 0.3s var(--ease-default) both;
}

.rules-page__prompt-hint {
  font-size: 0.875rem;
  color: var(--color-text-tertiary);
}

@media (max-width: 640px) {
  .rules-page__header { gap: 0.5rem; }
}

.rule-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: 1rem;
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

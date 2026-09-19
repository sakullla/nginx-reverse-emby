<template>
  <div class="rules-page">
    <div class="rules-page__header">
      <div class="rules-page__header-left">
        <h1 class="rules-page__title">HTTP 规则</h1>
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
          <span class="btn-text">添加规则</span>
        </button>
      </div>
    </div>

    <OperationStatusList />

    <div v-if="providerCatalogStatus === 'error'" class="provider-catalog-notice provider-catalog-notice--error" role="alert">
      <span :title="providerCatalogErrorTitle">插件状态加载失败</span>
      <button type="button" class="btn btn--sm btn--secondary" @click="refetchHTTPBackendProviders">重试</button>
    </div>
    <div v-else-if="providerCatalogStatus === 'loading' && hasAgentFilter" class="provider-catalog-notice" role="status">
      正在确认插件状态…
    </div>

    <ResourceListFilterBar
      :agent-id="agentFilter || ALL_AGENTS_FILTER"
      :agent-baseline="ALL_AGENTS_FILTER"
      :agents="allAgents"
      :q="searchQuery"
      search-placeholder="搜索 URL / 标签 / #id=..."
      :filter-fields="filterFields"
      :filter-values="filterValues"
      @update:agent-id="handleAgentSelect"
      @update:q="searchQuery = $event"
      @update:filter="onFilterUpdate"
    />

    <!-- No agents available -->
    <div v-if="!allAgents.length" class="rules-page__prompt">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>
        <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>
      </svg>
      <p>暂无可用节点</p>
      <p class="rules-page__prompt-hint">请先加入节点后再管理规则</p>
      <RouterLink to="/agents" class="btn btn-primary">加入节点</RouterLink>
    </div>

    <!-- Filter active, no rules -->
    <div v-else-if="hasAgentFilter && !rules.length && !exactRuleMatch && !isLoading" class="rules-page__empty">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>
        <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>
      </svg>
      <template v-if="hasActiveFilters">
        <p>没有匹配的规则</p>
      </template>
      <template v-else>
        <p>暂无规则</p>
        <button v-if="canCreate" class="btn btn-primary" @click="startCreate">添加第一条规则</button>
        <p v-else class="rules-page__prompt-hint">全部节点视图下请先选择具体节点再新建</p>
      </template>
    </div>

    <!-- No search results -->
    <div v-else-if="hasAgentFilter && rules.length && !filteredRules.length" class="rules-page__empty">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>
      </svg>
      <p>没有匹配的规则</p>
    </div>

    <!-- Rules card grid -->
    <div v-show="hasAgentFilter && filteredRules.length && view === 'card'" class="rule-grid">
      <RuleCard
        v-for="rule in filteredRules"
        :key="`${rule.agent_id || ''}:${rule.id}`"
        :rule="rule"
        :agent="selectedAgent"
        :provider-catalog="httpBackendProviders"
        :provider-catalog-status="providerCatalogStatus"
        :traffic="trafficForRule(rule)"
        :agent-node-total="nodeTotalFor(rule)"
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
      v-show="hasAgentFilter && filteredRules.length && view === 'list'"
      :rules="filteredRules"
      :agent="selectedAgent"
      :provider-catalog="httpBackendProviders"
      :provider-catalog-status="providerCatalogStatus"
      @edit="startEdit"
      @toggle="toggleRule"
      @delete="startDelete"
    />

    <ListPagination
      v-if="hasAgentFilter && listTotal > 0 && !exactRuleMatch"
      :page="page"
      :page-size="pageSize"
      :total="listTotal"
      @update:page="page = $event"
    />

    <!-- Loading -->
    <div v-if="isLoading" class="rules-page__loading">
      <div class="spinner"></div>
    </div>

    <!-- Add/Edit Form Modal -->
    <BaseModal
      :model-value="showAddForm || !!editingRule"
      :title="editingRule ? '编辑规则' : '添加规则'"
      :subtitle="formModalSubtitle"
      size="lg"
      :close-on-click-modal="false"
      @update:model-value="closeForm"
    >
      <RuleForm :initial-data="editingRule" :agent-id="formAgentId" @success="closeForm" />
    </BaseModal>

    <!-- Copy Modal -->
    <BaseModal
      :model-value="showCopyModal"
      title="复制规则"
      :subtitle="formModalSubtitle"
      size="lg"
      :close-on-click-modal="false"
      @update:model-value="closeForm"
    >
      <RuleForm v-if="copyingRule" :initial-data="copyingRule" :agent-id="formAgentId" @success="closeForm" />
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
import { useQuery } from '@tanstack/vue-query'
import { useAgent } from '../context/AgentContext'
import { useRulesList, useCreateRule, useUpdateRule, useDeleteRule } from '../hooks/useRules'
import { useDiagnoseRule, useDiagnosticTask } from '../hooks/useDiagnostics'
import { useAgents } from '../hooks/useAgents'
import { fetchRules, fetchAllAgentsRules, fetchCertificates, fetchRelayListeners, fetchEgressProfiles, fetchAllAgentsCertificates, fetchAllAgentsRelayListeners, fetchHTTPBackendProviders } from '../api'
import { exactIdItems, findAllMatchesInAgents, parseIdQuery, shouldStartCrossAgentIdSearch } from '../hooks/useIdSearch'
import { useTrafficSummaryForResources } from '../hooks/useTrafficSummaryForResources'
import IdCandidateModal from '../components/IdCandidateModal.vue'
import RuleForm from '../components/RuleForm.vue'
import RuleCard from '../components/rules/RuleCard.vue'
import DeleteConfirmDialog from '../components/DeleteConfirmDialog.vue'
import BaseModal from '../components/base/BaseModal.vue'
import RuleDiagnosticModal from '../components/RuleDiagnosticModal.vue'
import TrafficTrendModal from '../components/traffic/TrafficTrendModal.vue'
import ResourceListFilterBar from '../components/common/ResourceListFilterBar.vue'
import CreateAgentPicker from '../components/common/CreateAgentPicker.vue'
import ViewToggle from '../components/common/ViewToggle.vue'
import ListPagination from '../components/common/ListPagination.vue'
import RuleTable from '../components/rules/RuleTable.vue'
import OperationStatusList from '../components/operations/OperationStatusList.vue'
import { useViewToggle } from '../composables/useViewToggle'
import { useListFilterUrl } from '../composables/useListFilterUrl'
import { messageStore } from '../stores/messages'
import { ALL_AGENTS_FILTER, isAllAgentsFilter, normalizeAgentFilter } from '../utils/agentFilter.js'
import { flattenAgentGroupedItems } from '../utils/flattenAgentGroupedItems.js'
import { resolveCreateAgentId, resolveMutationAgentId, resolveCopyTargetAgentId } from '../utils/resolveResourceAgent.js'
import { describeHTTPBackends } from '../utils/httpBackend.js'

const route = useRoute()
const router = useRouter()
const { view } = useViewToggle('rules')
const agentContext = useAgent()
const { selectedAgentId } = agentContext
const systemInfo = agentContext.systemInfo || ref(null)

// Agents list for sync status derivation
const { data: agentsData } = useAgents()
const allAgents = computed(() => agentsData.value ?? [])
const registeredAgentIds = computed(() => new Set((agentsData.value || []).map((agent) => String(agent.id))))

// Prefer route query, else AgentContext; supports concrete id and ALL_AGENTS_FILTER.
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
const selectedAgentLabel = computed(() => String(selectedAgent.value?.name || agentId.value || '').trim())
const providerCatalogAgentIds = computed(() => {
  if (agentId.value) return [String(agentId.value)]
  if (!isAllAgentsFilter(agentFilter.value)) return []
  return allAgents.value.map((agent) => String(agent.id)).filter(Boolean).sort()
})
const providerCatalogRequestKey = computed(() => providerCatalogAgentIds.value.join('\u0000'))
const {
  data: httpBackendProvidersData,
  isLoading: httpBackendProvidersLoading,
  isFetching: httpBackendProvidersFetching,
  isError: httpBackendProvidersError,
  isSuccess: httpBackendProvidersSuccess,
  error: httpBackendProvidersFailure,
  refetch: refetchHTTPBackendProviders
} = useQuery({
  queryKey: computed(() => ['http-backend-providers', providerCatalogRequestKey.value]),
  enabled: computed(() => providerCatalogAgentIds.value.length > 0),
  queryFn: async () => {
    const agentIds = [...providerCatalogAgentIds.value]
    const groups = await Promise.all(agentIds.map(async (targetAgentId) => ({
      agentId: targetAgentId,
      providers: await fetchHTTPBackendProviders(targetAgentId)
    })))
    return {
      requestKey: agentIds.join('\u0000'),
      providers: groups.flatMap(({ agentId: targetAgentId, providers }) => (
        (Array.isArray(providers) ? providers : []).map((provider) => ({
          ...provider,
          agent_id: targetAgentId
        }))
      ))
    }
  }
})
const providerCatalogCurrent = computed(() => (
  httpBackendProvidersSuccess.value === true
  && httpBackendProvidersData.value?.requestKey === providerCatalogRequestKey.value
))
const providerCatalogStatus = computed(() => {
  if (httpBackendProvidersError.value === true) return 'error'
  if (
    httpBackendProvidersLoading.value === true
    || httpBackendProvidersFetching.value === true
    || !providerCatalogCurrent.value
  ) return 'loading'
  return 'ready'
})
const providerCatalogErrorTitle = computed(() => String(httpBackendProvidersFailure.value?.message || '').trim())
const httpBackendProviders = computed(() => (
  providerCatalogStatus.value === 'ready' && Array.isArray(httpBackendProvidersData.value?.providers)
    ? httpBackendProvidersData.value.providers
    : []
))
const formAgent = computed(() => agentsData.value?.find((a) => String(a.id) === String(formAgentId.value)))
const formModalSubtitle = computed(() => {
  const name = String(formAgent.value?.name || formAgentId.value || '').trim()
  return name ? `目标节点 · ${name}` : ''
})

const page = ref(1)
const pageSize = 20

function isPositiveIntString(value) {
  const num = Number(value)
  return Number.isInteger(num) && num > 0
}

// Filter state lives in the URL (key-level watchers; do NOT watch the whole
// route.query object — other keys would wipe in-flight typed input).
const { values: urlFilters, setValue: setUrlFilter } = useListFilterUrl({
  route,
  router,
  schema: {
    search: { key: 'search', type: 'string', baseline: '' },
    enabled: { key: 'enabled', type: 'enum', values: ['true', 'false'], baseline: '' },
    sync: { key: 'sync', type: 'enum', values: ['applied', 'pending'], baseline: '' },
    tags: { key: 'tags', type: 'list', baseline: [] },
    certificate_id: { key: 'cert', type: 'string', baseline: '', validate: isPositiveIntString },
    egress_profile_id: { key: 'egress', type: 'string', baseline: '', validate: isPositiveIntString },
    relay_listener_id: { key: 'relay', type: 'string', baseline: '', validate: isPositiveIntString }
  }
})

const searchQuery = computed({
  get: () => urlFilters.search ?? '',
  set: (value) => setUrlFilter('search', value)
})
const listQ = computed(() => {
  const raw = searchQuery.value.trim()
  if (!raw) return ''
  if (/^#id=\S+$/.test(raw)) return ''
  return raw
})

const enabledStatusValue = computed(() => urlFilters.enabled ?? '')
const enabledFilter = computed(() => {
  if (enabledStatusValue.value === 'true') return true
  if (enabledStatusValue.value === 'false') return false
  return undefined
})
const syncValue = computed(() => urlFilters.sync ?? '')
const tagsValue = computed(() => (Array.isArray(urlFilters.tags) ? urlFilters.tags : []))
const certificateIdValue = computed(() => urlFilters.certificate_id ?? '')
const egressProfileIdValue = computed(() => urlFilters.egress_profile_id ?? '')
const relayListenerIdValue = computed(() => urlFilters.relay_listener_id ?? '')

const filterValues = computed(() => ({
  enabled: enabledStatusValue.value,
  sync: syncValue.value,
  tags: tagsValue.value,
  certificate_id: certificateIdValue.value,
  egress_profile_id: egressProfileIdValue.value,
  relay_listener_id: relayListenerIdValue.value
}))

function onFilterUpdate({ key, value }) {
  setUrlFilter(key, value, { immediate: true })
}

// Option sources for tag / related-resource dimensions.
const optionAgentIds = computed(() => allAgents.value.map((agent) => agent.id))

const { data: rulesForTags } = useQuery({
  queryKey: computed(() => ['rules-tag-options', agentFilter.value || 'all']),
  enabled: hasAgentFilter,
  queryFn: () => (agentId.value
    ? fetchRules(agentId.value)
    : fetchAllAgentsRules(optionAgentIds.value))
})
const tagOptions = computed(() => {
  const collected = new Set(tagsValue.value)
  // Single-agent fetch returns a flat list; all-agents returns [{ agentId, rules }].
  for (const rule of flattenAgentGroupedItems(rulesForTags.value, 'rules')) {
    for (const tag of rule?.tags || []) collected.add(String(tag))
  }
  return [...collected].sort().map((tag) => ({ value: tag, label: tag }))
})

const { data: certsForOptions } = useQuery({
  queryKey: computed(() => ['cert-options', agentFilter.value || 'all']),
  enabled: hasAgentFilter,
  queryFn: () => (agentId.value
    ? fetchCertificates(agentId.value)
    : fetchAllAgentsCertificates(optionAgentIds.value))
})
const certificateOptions = computed(() => {
  const seen = new Set()
  const options = [{ value: '', label: '全部' }]
  for (const cert of flattenAgentGroupedItems(certsForOptions.value, 'certificates')) {
    const id = String(cert?.id ?? '')
    if (!id || seen.has(id)) continue
    seen.add(id)
    options.push({ value: id, label: cert.domain || `#${id}` })
  }
  return options
})

const { data: relaysForOptions } = useQuery({
  queryKey: computed(() => ['relay-options', agentFilter.value || 'all']),
  enabled: hasAgentFilter,
  queryFn: () => (agentId.value
    ? fetchRelayListeners(agentId.value)
    : fetchAllAgentsRelayListeners(optionAgentIds.value))
})
const relayOptions = computed(() => {
  const seen = new Set()
  const options = [{ value: '', label: '全部' }]
  for (const listener of flattenAgentGroupedItems(relaysForOptions.value, 'listeners')) {
    const id = String(listener?.id ?? '')
    if (!id || seen.has(id)) continue
    seen.add(id)
    const label = listener.name || `${listener.listen_host || ''}:${listener.listen_port ?? ''}` || `#${id}`
    options.push({ value: id, label })
  }
  return options
})

const { data: egressProfilesData } = useQuery({
  queryKey: ['egress-profile-options'],
  enabled: hasAgentFilter,
  queryFn: () => fetchEgressProfiles()
})
const egressOptions = computed(() => {
  const raw = egressProfilesData.value
  const profiles = Array.isArray(raw) ? raw : (raw?.profiles || [])
  return [
    { value: '', label: '全部' },
    ...profiles.map((profile) => ({ value: String(profile.id), label: profile.name || `#${profile.id}` }))
  ]
})

const filterFields = computed(() => [
  {
    key: 'enabled',
    label: '启用状态',
    type: 'chip',
    options: [
      { value: '', label: '全部' },
      { value: 'true', label: '启用' },
      { value: 'false', label: '停用' }
    ]
  },
  {
    key: 'sync',
    label: '同步状态',
    type: 'chip',
    options: [
      { value: '', label: '全部' },
      { value: 'applied', label: '已同步' },
      { value: 'pending', label: '待同步' }
    ]
  },
  { key: 'tags', label: '标签', type: 'multi', options: tagOptions.value },
  { key: 'certificate_id', label: '证书（域名近似）', type: 'select', options: certificateOptions.value },
  { key: 'egress_profile_id', label: '出口配置', type: 'select', options: egressOptions.value },
  { key: 'relay_listener_id', label: '中转监听', type: 'select', options: relayOptions.value }
])

const hasActiveFilters = computed(() => (
  Boolean(listQ.value)
  || Boolean(String(searchQuery.value || '').trim())
  || enabledStatusValue.value !== ''
  || syncValue.value !== ''
  || tagsValue.value.length > 0
  || certificateIdValue.value !== ''
  || egressProfileIdValue.value !== ''
  || relayListenerIdValue.value !== ''
))

watch(
  [agentFilter, listQ, enabledStatusValue, syncValue, tagsValue, certificateIdValue, egressProfileIdValue, relayListenerIdValue],
  () => { page.value = 1 }
)

const listFilters = computed(() => ({
  tags: tagsValue.value,
  sync: syncValue.value || undefined,
  certificateId: certificateIdValue.value || undefined,
  egressProfileId: egressProfileIdValue.value || undefined,
  relayListenerId: relayListenerIdValue.value || undefined
}))

const { data: _rulesPage, isLoading } = useRulesList({
  agentFilter,
  page,
  pageSize,
  q: listQ,
  enabledFilter,
  filters: listFilters,
  enabled: hasAgentFilter
})
const createRule = useCreateRule(agentId)
const updateRule = useUpdateRule(agentId)
const deleteRule = useDeleteRule(agentId)
const diagnoseRule = useDiagnoseRule(agentId)
const rules = computed(() => _rulesPage.value?.items ?? [])
const listTotal = computed(() => _rulesPage.value?.total ?? 0)

const trafficStatsEnabled = computed(() => !!systemInfo.value && systemInfo.value.traffic_stats_enabled !== false)
const { nodeTotalFor, trafficFor: trafficForRule } = useTrafficSummaryForResources({
  agentId,
  items: rules,
  trafficStatsEnabled,
  mapName: 'http_rules'
})

function handleAgentSelect(id) {
  router.replace({ query: { ...route.query, agentId: id } })
}

function httpBackends(rule) {
  return describeHTTPBackends(rule, httpBackendProviders.value, providerCatalogStatus.value).map((backend) => (
    backend.kind === 'provider' ? `${backend.label} · ${backend.detail}` : backend.label
  ))
}

function formatHttpBackend(rule) {
  const backends = httpBackends(rule)
  if (backends.length === 0) return '-'
  if (backends.length === 1) return backends[0]
  return `${backends[0]} +${backends.length - 1}`
}

// Search pre-fill / clear is handled by useListFilterUrl's key-level watcher
// on route.query.search (replaces the old per-page watch; same semantics).

const _crossSearching = ref(false)
const lastCrossSearchKey = ref('')
const exactRuleMatch = ref(null)
const candidateModalVisible = ref(false)
const candidateModalCandidates = ref([])
const candidateModalId = ref('')

const filteredRules = computed(() => {
  const raw = searchQuery.value.trim()
  if (!raw) return rules.value
  if (parseIdQuery(raw)) {
    return exactIdItems({
      search: raw,
      pageItems: rules.value,
      resolvedMatch: exactRuleMatch.value,
      agentFilter: agentFilter.value,
      enabled: enabledFilter.value
    })
  }
  // Server-side q already applied for text search.
  return rules.value
})

// R3: Cross-agent #id= resolution — if not found in current agent, search all agents
watch([searchQuery, agentFilter], ([search, filter]) => {
  const idQuery = parseIdQuery(search)
  const match = exactRuleMatch.value
  if (!idQuery) {
    exactRuleMatch.value = null
    lastCrossSearchKey.value = ''
    return
  }
  if (isAllAgentsFilter(filter)) {
    exactRuleMatch.value = null
    lastCrossSearchKey.value = ''
    return
  }
  if (!match || String(match.record?.id) !== idQuery.id ||
    (filter && !isAllAgentsFilter(filter) && String(filter) !== String(match.agentId))) {
    exactRuleMatch.value = null
  }
})

watch([filteredRules, isLoading, _crossSearching, allAgents, enabledFilter], ([result]) => {
  const idQuery = shouldStartCrossAgentIdSearch({
    search: searchQuery.value,
    currentMatches: result,
    isLoading: isLoading.value,
    isSearching: _crossSearching.value
  })
  if (!idQuery) return
  const agentIds = allAgents.value.map(a => a.id)
  if (!agentIds.length) return
  const searchKey = `${idQuery.id}\u0000${agentIds.map(String).sort().join('\u0000')}\u0000enabled=${String(enabledFilter.value ?? '')}`
  if (lastCrossSearchKey.value === searchKey) return
  lastCrossSearchKey.value = searchKey
  _crossSearching.value = true
  candidateModalId.value = idQuery.id
  fetchAllAgentsRules(agentIds).then(allData => {
    if (parseIdQuery(searchQuery.value)?.id !== idQuery.id) return
    const allMatches = findAllMatchesInAgents(
      { rules: allData },
      idQuery.id,
      { enabled: enabledFilter.value }
    )
    if (allMatches.length === 1) {
      exactRuleMatch.value = allMatches[0]
      router.replace({ query: { ...route.query, agentId: allMatches[0].agentId, search: searchQuery.value } })
    } else if (allMatches.length > 1) {
      candidateModalCandidates.value = allMatches
      candidateModalVisible.value = true
    }
  }).finally(() => { _crossSearching.value = false })
})

function handleCandidateSelect(candidate) {
  exactRuleMatch.value = candidate
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

const trendModal = ref({ visible: false, agentId: '', scopeType: '', scopeId: '', scopeLabel: '' })
const trafficDirection = ref('both')

function openTrendModal(rule) {
  const id = requireMutationAgent(rule, '查看流量')
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
  const target = requireMutationAgent(rule, '启停')
  if (!target) return
  updateRule.mutate({ id: rule.id, enabled: !rule.enabled, agentId: target })
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
    // default copy target to source resource agent when available
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

async function openDiagnostic(rule) {
  const target = requireMutationAgent(rule, '诊断')
  if (!target) return
  diagnosticRule.value = rule
  diagnosticAgentId.value = target
  showDiagnostic.value = true
  try {
    const response = await diagnoseRule.mutateAsync({ id: rule.id, agentId: target })
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
  diagnosticAgentId.value = ''
  diagnosticTaskId.value = ''
  initialDiagnosticTask.value = null
}

async function confirmDelete() {
  if (!deletingRule.value) return
  const target = requireMutationAgent(deletingRule.value, '删除')
  if (!target) return
  await deleteRule.mutateAsync({ id: deletingRule.value.id, agentId: target })
  deletingRule.value = null
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

.provider-catalog-notice {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
  margin-bottom: 0.75rem;
  color: var(--color-text-muted);
  font-size: 0.75rem;
}

.provider-catalog-notice--error {
  color: var(--color-danger);
  font-weight: 600;
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
    flex-direction: column;
    align-items: stretch;
    gap: 0.65rem;
  }

  .rules-page__header-right {
    width: 100%;
    justify-content: stretch;
    gap: 0.5rem;
  }

  .rules-page__header-right .btn,
  .rules-page__header-right .btn-primary,
  .rules-page__header-right .btn-secondary {
    flex: 1 1 auto;
    min-width: 0;
    justify-content: center;
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
/* Wide-screen (2K/4K) width steps */
@media (min-width: 1920px) {
  .rules-page { max-width: 1600px; }
}
@media (min-width: 2560px) {
  .rules-page { max-width: 2000px; }
}
</style>

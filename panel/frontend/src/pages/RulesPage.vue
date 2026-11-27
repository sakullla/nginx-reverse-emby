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
        <a
          title="查看文档"
          class="btn btn-secondary docs-link rules-page__docs-link"
          :href="DOC_LINKS.httpRules"
          target="_blank"
          rel="noopener"
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <path d="M4 19.5A2.5 2.5 0 0 1 6.5 17H20"/><path d="M6.5 2H20v20H6.5A2.5 2.5 0 0 1 4 19.5v-15A2.5 2.5 0 0 1 6.5 2z"/>
          </svg>
          <span class="btn-text">查看文档</span>
        </a>
        <button
          v-if="canBulk"
          type="button"
          class="btn btn-secondary rules-page__bulk-toggle"
          :class="{ 'is-active': bulkMode }"
          :aria-pressed="bulkMode ? 'true' : 'false'"
          title="批量操作"
          data-testid="rules-bulk-toggle"
          @click="toggleBulkMode"
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <rect x="3" y="3" width="7" height="7" rx="1.5"/><path d="m5 6.5 1.2 1.2L8.5 5"/>
            <line x1="13" y1="6.5" x2="21" y2="6.5"/>
            <rect x="3" y="14" width="7" height="7" rx="1.5"/>
            <line x1="13" y1="17.5" x2="21" y2="17.5"/>
          </svg>
          <span class="btn-text">{{ bulkMode ? '退出批量' : '批量' }}</span>
        </button>
        <button v-if="canCreate" class="btn btn-primary" title="添加规则" aria-label="添加规则" @click="startCreate">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" aria-hidden="true">
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
      正在核对插件后端…
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
    >
      <template v-if="showViewToggle" #trailing>
        <ViewToggle v-model:view="view" />
      </template>
    </ResourceListFilterBar>

    <!-- No agents available -->
    <EmptyState
      v-if="!allAgents.length"
      title="暂无可用节点"
      description="请先加入节点后再管理规则"
    >
      <template #icon>
        <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
          <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>
          <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>
        </svg>
      </template>
      <template #action>
        <RouterLink to="/agents" class="btn btn-primary">加入节点</RouterLink>
      </template>
    </EmptyState>

    <!-- List failed to load and nothing to show -->
    <ErrorState
      v-else-if="showListError"
      title="规则列表加载失败"
      :message="listErrorMessage"
      :retrying="isListFetching"
      @retry="retryList"
    />

    <!-- Filters active, nothing matches -->
    <EmptyState
      v-else-if="showNoMatch"
      title="没有匹配的结果"
      :description="activeFilterDescription"
    >
      <template #icon>
        <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
          <circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>
        </svg>
      </template>
      <template #action>
        <button type="button" class="btn btn-secondary" data-testid="rules-clear-filters" @click="clearAllFilters">清除筛选</button>
      </template>
    </EmptyState>

    <!-- Truly empty -->
    <EmptyState
      v-else-if="hasAgentFilter && !rules.length && !exactRuleMatch && !isLoading"
      title="暂无规则"
      :description="canCreate ? '添加一条规则，把域名转发到你的后端服务' : '全部节点视图下请先选择具体节点再新建'"
    >
      <template #icon>
        <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
          <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>
          <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>
        </svg>
      </template>
      <template v-if="canCreate" #action>
        <button class="btn btn-primary" @click="startCreate">添加第一条规则</button>
      </template>
    </EmptyState>

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
        :selectable="bulkMode"
        :selected="bulk.isSelected(ruleKey(rule))"
        @select="onSelectRule"
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
      :selectable="bulkMode"
      :selected-keys="bulk.selectedList.value"
      :all-selected="bulk.isAllSelected.value"
      :indeterminate="bulk.isIndeterminate.value"
      :show-traffic="trafficStatsEnabled"
      :traffic-for="trafficForRule"
      :node-total-for="nodeTotalFor"
      @select="onSelectRule"
      @select-all="bulk.toggleAll()"
      @edit="startEdit"
      @toggle="toggleRule"
      @copy="handleCopy"
      @diagnose="openDiagnostic"
      @traffic-click="openTrendModal"
      @delete="startDelete"
    />

    <BulkActionBar
      v-if="bulkMode && hasAgentFilter && filteredRules.length"
      :count="bulk.selectedCount.value"
      :busy="bulkBusy"
      :progress="bulkProgress"
      @enable="runBulkToggle(true)"
      @disable="runBulkToggle(false)"
      @delete="openBulkDelete"
      @cancel="exitBulkMode"
    />

    <ListPagination
      v-if="hasAgentFilter && listTotal > 0 && !exactRuleMatch"
      :page="page"
      :page-size="pageSize"
      :total="listTotal"
      @update:page="page = $event"
    />

    <!-- Loading — skeleton mirrors the target layout so the swap feels seamless -->
    <SkeletonList v-if="isLoading" :variant="view === 'list' ? 'rows' : 'cards'" :count="view === 'list' ? 5 : 6" />

    <!-- Add/Edit Form Modal -->
    <BaseModal
      :model-value="showAddForm || !!editingRule"
      :title="editingRule ? '编辑规则' : '添加规则'"
      :subtitle="formModalSubtitle"
      size="lg"
      :close-on-click-modal="false"
      @update:model-value="closeForm"
    >
      <RuleForm :initial-data="editingRule" :agent-id="formAgentId" @success="closeForm" @cancel="closeForm" />
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
      <RuleForm v-if="copyingRule" :initial-data="copyingRule" :agent-id="formAgentId" @success="closeForm" @cancel="closeForm" />
    </BaseModal>

    <!-- Delete Modal -->
    <DeleteConfirmDialog
      :show="!!deletingRule"
      title="确认删除规则"
      message="删除后该规则将立即失效，相关配置将无法恢复。"
      :name="deletingRule?.frontend_url"
      confirm-text="确认删除"
      :loading="deleteRule.isPending?.value"
      :error="deleteError"
      @confirm="confirmDelete"
      @cancel="cancelDelete"
    />

    <DeleteConfirmDialog
      :show="bulkDeleteOpen"
      title="确认批量删除规则"
      :message="`将删除所选 ${bulk.selectedCount.value} 条规则，删除后立即失效，相关配置将无法恢复。`"
      :confirm-text="`删除 ${bulk.selectedCount.value} 条`"
      :loading="bulkBusy"
      :error="bulkDeleteError"
      @confirm="confirmBulkDelete"
      @cancel="closeBulkDelete"
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
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { useAgent } from '../context/AgentContext'
import { useRulesList, useCreateRule, useUpdateRule, useDeleteRule } from '../hooks/useRules'
import { useCreateQueryParam } from '../hooks/useCreateQueryParam'
import { useDiagnoseRule, useDiagnosticTask } from '../hooks/useDiagnostics'
import { useAgents } from '../hooks/useAgents'
import { updateRule as apiUpdateRule, deleteRule as apiDeleteRule, fetchRules, fetchAllAgentsRules, fetchCertificates, fetchRelayListeners, fetchEgressProfiles, fetchAllAgentsCertificates, fetchAllAgentsRelayListeners, fetchHTTPBackendProviders } from '../api'
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
import SkeletonList from '../components/base/SkeletonList.vue'
import EmptyState from '../components/base/EmptyState.vue'
import ErrorState from '../components/base/ErrorState.vue'
import BulkActionBar from '../components/common/BulkActionBar.vue'
import { useBulkSelection } from '../composables/useBulkSelection'
import { runBulk, summarizeBulk } from '../utils/runBulk.js'
import { invalidateResourceList } from '../hooks/useResourceListQuery'
import RuleTable from '../components/rules/RuleTable.vue'
import OperationStatusList from '../components/operations/OperationStatusList.vue'
import { useViewToggle } from '../composables/useViewToggle'
import { useListFilterUrl } from '../composables/useListFilterUrl'
import { messageStore } from '../stores/messages'
import { DOC_LINKS } from '../constants/docLinks'
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

const {
  data: _rulesPage,
  isLoading,
  isError: isListErrorRef,
  error: listErrorRef,
  isFetching: isListFetchingRef,
  refetch: refetchList
} = useRulesList({
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

// --- Boundary states -------------------------------------------------------
const isListError = computed(() => isListErrorRef?.value === true)
const isListFetching = computed(() => isListFetchingRef?.value === true)
const listErrorMessage = computed(() => {
  const error = listErrorRef?.value
  return String(error?.response?.data?.message || error?.message || '').trim() || '请检查网络或稍后重试'
})
const showListError = computed(() => hasAgentFilter.value && isListError.value && !rules.value.length && !isLoading.value)
const showNoMatch = computed(() => (
  hasAgentFilter.value
  && hasActiveFilters.value
  && !isLoading.value
  && (
    (!rules.value.length && !exactRuleMatch.value)
    || (rules.value.length > 0 && !filteredRules.value.length)
  )
))
const showViewToggle = computed(() => hasAgentFilter.value && (listTotal.value > 0 || Boolean(listQ.value) || Boolean(searchQuery.value)))

function retryList() {
  refetchList?.()
}

// A refresh failure while rows are still on screen keeps the list and reports once.
watch(isListError, (failed) => {
  if (failed && rules.value.length) messageStore.error(listErrorRef?.value, '刷新规则列表失败')
})

const activeFilterDescription = computed(() => {
  const parts = []
  const search = String(searchQuery.value || '').trim()
  if (search) parts.push(`搜索“${search}”`)
  for (const field of filterFields.value) {
    const value = filterValues.value[field.key]
    if (Array.isArray(value) ? !value.length : value === '' || value == null) continue
    const values = Array.isArray(value) ? value : [value]
    const labels = values.map((item) => field.options?.find((option) => String(option.value) === String(item))?.label || item)
    parts.push(`${field.label}：${labels.join('、')}`)
  }
  return parts.length ? `当前条件：${parts.join('；')}` : ''
})

function clearAllFilters() {
  const keys = ['search', 'enabled', 'sync', 'tags', 'certificate_id', 'egress_profile_id', 'relay_listener_id']
  keys.forEach((key, index) => {
    setUrlFilter(key, key === 'tags' ? [] : '', { immediate: index === keys.length - 1 })
  })
}

// --- Bulk selection ----------------------------------------------------------
function ruleKey(rule) {
  return `${rule?.agent_id ?? ''}:${rule?.id}`
}

const queryClient = useQueryClient()
const bulkMode = ref(false)
const bulkBusy = ref(false)
const bulkProgress = ref(null)
const bulkDeleteOpen = ref(false)
const bulkDeleteError = ref('')
const pageRuleKeys = computed(() => filteredRules.value.map(ruleKey))
const bulk = useBulkSelection(pageRuleKeys)
const canBulk = computed(() => hasAgentFilter.value && filteredRules.value.length > 0)

function toggleBulkMode() {
  if (bulkMode.value) exitBulkMode()
  else bulkMode.value = true
}

function exitBulkMode() {
  if (bulkBusy.value) return
  bulkMode.value = false
  bulkDeleteOpen.value = false
  bulkDeleteError.value = ''
  bulk.clear()
}

function onSelectRule(rule) {
  if (!bulkMode.value) return
  bulk.toggle(ruleKey(rule))
}

// Any change of scope makes the previous selection meaningless.
watch(
  [agentFilter, searchQuery, enabledStatusValue, syncValue, tagsValue, certificateIdValue, egressProfileIdValue, relayListenerIdValue, page],
  () => {
    bulk.clear()
    bulkDeleteError.value = ''
  }
)

watch(canBulk, (available) => {
  if (!available && bulkMode.value && !bulkBusy.value) exitBulkMode()
})

function selectedRules() {
  const byKey = new Map(filteredRules.value.map((rule) => [ruleKey(rule), rule]))
  return bulk.selectedList.value.map((key) => byKey.get(key)).filter(Boolean)
}

function bulkTarget(rule) {
  const resolved = resolveMutationAgentId(rule, agentFilter.value, { fallbackAgentId: agentId.value })
  if (!resolved.agentId) throw new Error(resolved.error || '缺少节点归属')
  return resolved.agentId
}

async function executeBulk(rules, task) {
  const byKey = new Map(rules.map((rule) => [ruleKey(rule), rule]))
  bulkBusy.value = true
  bulkProgress.value = { done: 0, total: byKey.size }
  try {
    return await runBulk([...byKey.keys()], (key) => task(byKey.get(key)), {
      onProgress: (progress) => { bulkProgress.value = progress },
      onSettled: () => {
        invalidateResourceList(queryClient, 'rules')
        queryClient.invalidateQueries({ queryKey: ['agents'] })
      }
    })
  } finally {
    bulkBusy.value = false
    bulkProgress.value = null
  }
}

async function runBulkToggle(enabled) {
  const targets = selectedRules()
  if (!targets.length || bulkBusy.value) return
  const result = await executeBulk(targets, (rule) => apiUpdateRule(bulkTarget(rule), rule.id, { enabled }))
  bulk.setSelection(result.failed)
  const summary = summarizeBulk(result, enabled ? '启用' : '禁用')
  if (result.failed.length) messageStore.error(summary, `批量${enabled ? '启用' : '禁用'}未全部成功`)
  else messageStore.success(summary)
}

function openBulkDelete() {
  if (!bulk.selectedCount.value) return
  bulkDeleteError.value = ''
  bulkDeleteOpen.value = true
}

function closeBulkDelete() {
  if (bulkBusy.value) return
  bulkDeleteOpen.value = false
  bulkDeleteError.value = ''
}

async function confirmBulkDelete() {
  const targets = selectedRules()
  if (!targets.length || bulkBusy.value) return
  bulkDeleteError.value = ''
  const result = await executeBulk(targets, (rule) => apiDeleteRule(bulkTarget(rule), rule.id))
  bulk.setSelection(result.failed)
  const summary = summarizeBulk(result, '删除')
  if (result.failed.length) {
    // Keep the dialog open: the selection now holds only the failed rules, so confirming retries them.
    bulkDeleteError.value = summary
    return
  }
  bulkDeleteOpen.value = false
  messageStore.success(summary)
}

function isAnyDialogOpen() {
  return Boolean(
    bulkDeleteOpen.value || deletingRule.value || showAddForm.value || editingRule.value
    || showCopyModal.value || showDiagnostic.value || trendModal.value.visible
    || candidateModalVisible.value || showCreateAgentPicker.value
  )
}

function onDocumentKeydown(event) {
  if (event.key !== 'Escape' || !bulkMode.value || event.defaultPrevented) return
  if (isAnyDialogOpen()) return
  if (document.querySelector('.base-action-menu__panel:not([aria-hidden="true"])')) return
  exitBulkMode()
}

onMounted(() => document.addEventListener('keydown', onDocumentKeydown))
onUnmounted(() => document.removeEventListener('keydown', onDocumentKeydown))

// Modals
const showAddForm = ref(false)
const editingRule = ref(null)
const copyingRule = ref(null)
const showCopyModal = ref(false)
const deletingRule = ref(null)
const deleteError = ref('')
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

// 上手清单等入口通过 ?create=1 直达创建表单。
useCreateQueryParam(route, router, startCreate)

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
  deleteError.value = ''
  deletingRule.value = rule
}

function cancelDelete() {
  deletingRule.value = null
  deleteError.value = ''
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
  deleteError.value = ''
  try {
    await deleteRule.mutateAsync({ id: deletingRule.value.id, agentId: target })
  } catch (error) {
    deleteError.value = String(error?.response?.data?.message || error?.message || '').trim() || '删除失败，请稍后重试'
    return
  }
  deletingRule.value = null
}

</script>

<style scoped>
.rules-page {
  max-width: none;
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
  font-size: var(--text-2xl);
  font-weight: var(--font-bold);
  margin: 0 0 0.15rem;
  color: var(--color-text-primary);
  letter-spacing: -0.03em;
  line-height: 1.25;
}

.rules-page__subtitle {
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
  margin: 0;
  line-height: 1.35;
  font-variant-numeric: tabular-nums;
}

.rules-page__bulk-toggle.is-active {
  color: var(--color-primary);
  border-color: color-mix(in srgb, var(--color-primary) 45%, var(--color-border-default));
  background: color-mix(in srgb, var(--color-primary) 8%, var(--color-bg-surface));
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

@media (max-width: 640px) {
  .rule-grid { grid-template-columns: 1fr; }
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

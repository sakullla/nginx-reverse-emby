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
        <a
          title="查看文档"
          class="btn btn-secondary docs-link rules-page__docs-link"
          :href="DOC_LINKS.l4Rules"
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
          class="btn btn-secondary"
          :class="{ 'btn--active': selectionMode }"
          data-testid="l4-bulk-toggle"
          :aria-pressed="selectionMode ? 'true' : 'false'"
          :title="selectionMode ? '退出批量模式' : '批量操作'"
          @click="toggleSelectionMode"
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <rect x="3" y="3" width="7" height="7" rx="1.5"/><polyline points="4.5 6.5 6 8 8.5 5"/>
            <line x1="13" y1="6.5" x2="21" y2="6.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><line x1="13" y1="17.5" x2="21" y2="17.5"/>
          </svg>
          <span class="btn-text">{{ selectionMode ? '退出批量' : '批量' }}</span>
        </button>
        <button v-if="canCreate" class="btn btn-primary" title="添加 L4 规则" aria-label="添加 L4 规则" @click="startCreate">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" aria-hidden="true">
            <line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/>
          </svg>
          <span class="btn-text">添加 L4 规则</span>
        </button>
      </div>
    </div>

    <OperationStatusList />

    <ResourceListFilterBar
      :agent-id="agentFilter || ALL_AGENTS_FILTER"
      :agent-baseline="ALL_AGENTS_FILTER"
      :agents="allAgents"
      :q="searchQuery"
      search-placeholder="搜索协议 / 地址 / 端口 / 标签 / #id=..."
      :filter-fields="filterFields"
      :filter-values="filterValues"
      @update:agent-id="handleAgentSelect"
      @update:q="searchQuery = $event"
      @update:filter="onFilterUpdate"
    >
      <template v-if="hasAgentFilter && (listTotal > 0 || listQ || searchQuery)" #trailing>
        <ViewToggle v-model:view="view" />
      </template>
    </ResourceListFilterBar>

    <!-- No agents available -->
    <EmptyState
      v-if="!allAgents.length"
      title="暂无可用节点"
      description="请先加入节点后再管理 L4 规则"
    >
      <template #icon>
        <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
          <rect x="2" y="2" width="20" height="8" rx="2"/><rect x="2" y="14" width="20" height="8" rx="2"/>
        </svg>
      </template>
      <template #action>
        <RouterLink to="/agents" class="btn btn-primary">加入节点</RouterLink>
      </template>
    </EmptyState>

    <!-- List load failed and nothing to show -->
    <ErrorState
      v-else-if="showListError"
      title="L4 规则加载失败"
      :message="listErrorMessage"
      :retrying="isRefetching"
      @retry="retryList"
    />

    <!-- Filters active but nothing matched (server-side or #id search) -->
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
        <button type="button" class="btn btn-secondary" data-testid="l4-clear-filters" @click="clearFilters">清除筛选</button>
      </template>
    </EmptyState>

    <!-- Truly empty -->
    <EmptyState
      v-else-if="hasAgentFilter && !rules.length && !exactL4Match && !isLoading && !_crossSearching"
      title="暂无 L4 规则"
      :description="canCreate ? '添加一条 TCP/UDP 转发规则开始使用' : '全部节点视图下请先选择具体节点再新建'"
    >
      <template #icon>
        <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
          <rect x="2" y="2" width="20" height="8" rx="2"/><rect x="2" y="14" width="20" height="8" rx="2"/>
        </svg>
      </template>
      <template v-if="canCreate" #action>
        <button class="btn btn-primary" @click="startCreate">添加第一条规则</button>
      </template>
    </EmptyState>

    <!-- Rule card grid -->
    <div v-show="hasAgentFilter && filteredRules.length && view === 'card'" class="rule-grid">
      <L4RuleItem
        v-for="rule in filteredRules"
        :key="`${rule.agent_id || ''}:${rule.id}`"
        :rule="rule"
        :agent="selectedAgent"
        :traffic="trafficForRule(rule)"
        :agent-node-total="nodeTotalFor(rule)"
        :selectable="selectionMode"
        :selected="selection.isSelected(ruleKey(rule))"
        @select="toggleRuleSelection"
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
      :selectable="selectionMode"
      :selected-keys="selection.selectedList.value"
      :all-selected="selection.isAllSelected.value"
      :indeterminate="selection.isIndeterminate.value"
      :traffic-for="trafficForRule"
      :node-total-for="nodeTotalFor"
      :show-traffic="trafficStatsEnabled"
      @select="toggleRuleSelection"
      @select-all="selection.toggleAll()"
      @edit="startEdit"
      @toggle="toggleRule"
      @copy="handleCopy"
      @diagnose="openDiagnostic"
      @delete="startDelete"
      @traffic-click="openTrendModal"
    />

    <ListPagination
      v-if="hasAgentFilter && listTotal > 0 && !exactL4Match"
      :page="page"
      :page-size="pageSize"
      :total="listTotal"
      @update:page="page = $event"
    />

    <BulkActionBar
      v-if="selectionMode && hasAgentFilter && filteredRules.length"
      :count="selection.selectedCount.value"
      :busy="bulkBusy"
      :progress="bulkProgress"
      @enable="runBulkToggle(true)"
      @disable="runBulkToggle(false)"
      @delete="startBulkDelete"
      @cancel="exitSelectionMode"
    />

    <!-- Loading — skeleton mirrors the target layout so the swap feels seamless -->
    <SkeletonList v-if="isLoading" :variant="view === 'list' ? 'rows' : 'cards'" :count="view === 'list' ? 5 : 6" />

    <!-- Add/Edit Modal -->
    <BaseModal
      :model-value="showAddForm || !!editingRule"
      :title="editingRule ? '编辑 L4 规则' : '添加 L4 规则'"
      :subtitle="formModalSubtitle"
      size="lg"
      :close-on-click-modal="false"
      @update:model-value="closeForm"
    >
      <L4RuleForm :initial-data="editingRule" :agent-id="formAgentId" :l4-rules="rules" @success="closeForm" @cancel="closeForm" />
    </BaseModal>

    <!-- Copy Modal -->
    <BaseModal
      :model-value="showCopyModal"
      title="复制 L4 规则"
      :subtitle="formModalSubtitle"
      size="lg"
      :close-on-click-modal="false"
      @update:model-value="closeCopy"
    >
      <L4RuleForm v-if="copyingRule" :initial-data="copyingRule" :agent-id="formAgentId" :l4-rules="rules" @success="closeCopy" @cancel="closeCopy" />
    </BaseModal>

    <!-- Bulk delete -->
    <DeleteConfirmDialog
      :show="bulkDeleteOpen"
      title="批量删除规则"
      :message="`将删除已选的 ${selection.selectedCount.value} 条 L4 规则，删除后立即失效且无法恢复。`"
      :name="`${selection.selectedCount.value} 条 L4 规则`"
      confirm-text="确认删除"
      :loading="bulkBusy"
      :error="bulkDeleteError"
      @confirm="confirmBulkDelete"
      @cancel="closeBulkDelete"
    />

    <!-- Delete Modal -->
    <DeleteConfirmDialog
      :show="!!deletingRule"
      title="确认删除规则"
      message="删除后该规则将立即失效，相关配置将无法恢复。"
      :name="deletingRule?.listen_host + ':' + deletingRule?.listen_port"
      confirm-text="确认删除"
      :loading="deleteL4Rule.isPending?.value"
      :error="deleteError"
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
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { useAgent } from '../context/AgentContext'
import { useL4RulesList, useCreateL4Rule, useUpdateL4Rule, useDeleteL4Rule } from '../hooks/useL4Rules'
import { useCreateQueryParam } from '../hooks/useCreateQueryParam'
import { useDiagnoseL4Rule, useDiagnosticTask } from '../hooks/useDiagnostics'
import { useAgents } from '../hooks/useAgents'
import { fetchL4Rules, fetchAllAgentsL4Rules, fetchRelayListeners, fetchAllAgentsRelayListeners, fetchEgressProfiles, updateL4Rule as apiUpdateL4Rule, deleteL4Rule as apiDeleteL4Rule } from '../api'
import { invalidateResourceList } from '../hooks/useResourceListQuery'
import { exactIdItems, findAllMatchesInAgents, parseIdQuery, shouldStartCrossAgentIdSearch } from '../hooks/useIdSearch'
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
import SkeletonList from '../components/base/SkeletonList.vue'
import EmptyState from '../components/base/EmptyState.vue'
import ErrorState from '../components/base/ErrorState.vue'
import BulkActionBar from '../components/common/BulkActionBar.vue'
import { useBulkSelection } from '../composables/useBulkSelection'
import { runBulk, summarizeBulk } from '../utils/runBulk'
import ListPagination from '../components/common/ListPagination.vue'
import L4RuleTable from '../components/l4/L4RuleTable.vue'
import OperationStatusList from '../components/operations/OperationStatusList.vue'
import { useViewToggle } from '../composables/useViewToggle'
import { useListFilterUrl } from '../composables/useListFilterUrl'
import { messageStore } from '../stores/messages'
import { DOC_LINKS } from '../constants/docLinks'
import { ALL_AGENTS_FILTER, isAllAgentsFilter, normalizeAgentFilter } from '../utils/agentFilter.js'
import { flattenAgentGroupedItems } from '../utils/flattenAgentGroupedItems.js'
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
const egressProfileIdValue = computed(() => urlFilters.egress_profile_id ?? '')
const relayListenerIdValue = computed(() => urlFilters.relay_listener_id ?? '')

const filterValues = computed(() => ({
  enabled: enabledStatusValue.value,
  sync: syncValue.value,
  tags: tagsValue.value,
  egress_profile_id: egressProfileIdValue.value,
  relay_listener_id: relayListenerIdValue.value
}))

function onFilterUpdate({ key, value }) {
  setUrlFilter(key, value, { immediate: true })
}

// Option sources for tag / related-resource dimensions.
const optionAgentIds = computed(() => allAgents.value.map((agent) => agent.id))

const { data: rulesForTags } = useQuery({
  queryKey: computed(() => ['l4-rules-tag-options', agentFilter.value || 'all']),
  enabled: hasAgentFilter,
  queryFn: () => (agentId.value
    ? fetchL4Rules(agentId.value)
    : fetchAllAgentsL4Rules(optionAgentIds.value))
})
const tagOptions = computed(() => {
  const collected = new Set(tagsValue.value)
  // Single-agent fetch returns a flat list; all-agents returns [{ agentId, l4Rules }].
  for (const rule of flattenAgentGroupedItems(rulesForTags.value, 'l4Rules')) {
    for (const tag of rule?.tags || []) collected.add(String(tag))
  }
  return [...collected].sort().map((tag) => ({ value: tag, label: tag }))
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
  { key: 'egress_profile_id', label: '出口配置', type: 'select', options: egressOptions.value },
  { key: 'relay_listener_id', label: '中转监听', type: 'select', options: relayOptions.value }
])

const hasActiveFilters = computed(() => (
  Boolean(listQ.value)
  || Boolean(String(searchQuery.value || '').trim())
  || enabledStatusValue.value !== ''
  || syncValue.value !== ''
  || tagsValue.value.length > 0
  || egressProfileIdValue.value !== ''
  || relayListenerIdValue.value !== ''
))

watch(
  [agentFilter, listQ, enabledStatusValue, syncValue, tagsValue, egressProfileIdValue, relayListenerIdValue],
  () => { page.value = 1 }
)

const listFilters = computed(() => ({
  tags: tagsValue.value,
  sync: syncValue.value || undefined,
  egressProfileId: egressProfileIdValue.value || undefined,
  relayListenerId: relayListenerIdValue.value || undefined
}))

const {
  data: _rulesPage,
  isLoading,
  isError: listIsError,
  error: listError,
  refetch: refetchList,
  isRefetching: listIsRefetching
} = useL4RulesList({
  agentFilter,
  page,
  pageSize,
  q: listQ,
  enabledFilter,
  filters: listFilters,
  enabled: hasAgentFilter
})
const rules = computed(() => _rulesPage.value?.items ?? [])

// Error / empty states: keep stale rows on refetch failure (hook toasts), show
// ErrorState only when there is nothing to display.
const isRefetching = computed(() => Boolean(listIsRefetching?.value))
const showListError = computed(() => (
  hasAgentFilter.value
  && Boolean(listIsError?.value)
  && !rules.value.length
  && !isLoading.value
))
const listErrorMessage = computed(() => {
  const err = listError?.value
  return err?.response?.data?.message || err?.message || '请检查网络或稍后重试'
})
function retryList() {
  refetchList?.()
}

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

function handleAgentSelect(id) {
  router.replace({ query: { ...route.query, agentId: id } })
}
const selectedAgentLabel = computed(() => String(selectedAgent.value?.name || agentId.value || '').trim())
const createL4Rule = useCreateL4Rule(agentId)
const updateL4Rule = useUpdateL4Rule(agentId)
const deleteL4Rule = useDeleteL4Rule(agentId)
const diagnoseL4Rule = useDiagnoseL4Rule(agentId)
const listTotal = computed(() => _rulesPage.value?.total ?? 0)

// Search pre-fill / clear is handled by useListFilterUrl's key-level watcher
// on route.query.search (replaces the old per-page watch; same semantics).

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
  if (parseIdQuery(raw)) {
    return exactIdItems({
      search: raw,
      pageItems: rules.value,
      resolvedMatch: exactL4Match.value,
      agentFilter: agentFilter.value,
      enabled: enabledFilter.value
    })
  }
  return rules.value
})

// R3: Cross-agent #id= resolution — if not found in current agent, search all agents
const _crossSearching = ref(false)
const lastCrossSearchKey = ref('')
const exactL4Match = ref(null)
const candidateModalVisible = ref(false)
const candidateModalCandidates = ref([])
const candidateModalId = ref('')

watch([searchQuery, agentFilter], ([search, filter]) => {
  const idQuery = parseIdQuery(search)
  const match = exactL4Match.value
  if (!idQuery) {
    exactL4Match.value = null
    lastCrossSearchKey.value = ''
    return
  }
  if (isAllAgentsFilter(filter)) {
    exactL4Match.value = null
    lastCrossSearchKey.value = ''
    return
  }
  if (match && (String(match.record?.id) !== idQuery.id ||
    (filter && !isAllAgentsFilter(filter) && String(filter) !== String(match.agentId)))) {
    exactL4Match.value = null
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
  fetchAllAgentsL4Rules(agentIds).then(allData => {
    if (parseIdQuery(searchQuery.value)?.id !== idQuery.id) return
    const allMatches = findAllMatchesInAgents(
      { l4Rules: allData },
      idQuery.id,
      { enabled: enabledFilter.value }
    )
    if (allMatches.length === 1) {
      exactL4Match.value = allMatches[0]
      router.replace({ query: { ...route.query, agentId: allMatches[0].agentId, search: searchQuery.value } })
    } else if (allMatches.length > 1) {
      candidateModalCandidates.value = allMatches
      candidateModalVisible.value = true
    }
  }).finally(() => { _crossSearching.value = false })
})

function handleCandidateSelect(candidate) {
  exactL4Match.value = candidate
  router.replace({ query: { ...route.query, agentId: candidate.agentId, search: searchQuery.value } })
}

const enabledCount = computed(() => rules.value.filter(r => r.enabled).length)

const showNoMatch = computed(() => (
  hasAgentFilter.value
  && hasActiveFilters.value
  && !filteredRules.value.length
  && !exactL4Match.value
  && !isLoading.value
  && !_crossSearching.value
  && !showListError.value
))

const activeFilterDescription = computed(() => {
  const parts = []
  const q = String(searchQuery.value || '').trim()
  if (q) parts.push(`搜索“${q}”`)
  if (enabledStatusValue.value) parts.push(enabledStatusValue.value === 'true' ? '已启用' : '已停用')
  if (syncValue.value) parts.push(syncValue.value === 'applied' ? '已同步' : '待同步')
  if (tagsValue.value.length) parts.push(`标签 ${tagsValue.value.join('、')}`)
  if (egressProfileIdValue.value) parts.push('出口配置')
  if (relayListenerIdValue.value) parts.push('中转监听')
  return parts.length ? `当前条件：${parts.join(' · ')}` : '调整或清除筛选条件后重试'
})

function clearFilters() {
  setUrlFilter('enabled', '')
  setUrlFilter('sync', '')
  setUrlFilter('tags', [])
  setUrlFilter('egress_profile_id', '')
  setUrlFilter('relay_listener_id', '')
  setUrlFilter('search', '', { immediate: true })
}

// ---- Bulk selection --------------------------------------------------------
const queryClient = useQueryClient()

function ruleKey(rule) {
  return `${rule?.agent_id || ''}:${rule?.id}`
}

const pageRuleKeys = computed(() => filteredRules.value.map(ruleKey))
const selection = useBulkSelection(pageRuleKeys)
const selectionMode = ref(false)
const bulkBusy = ref(false)
const bulkProgress = ref(null)
const bulkDeleteOpen = ref(false)
const bulkDeleteError = ref('')

const canBulk = computed(() => hasAgentFilter.value && filteredRules.value.length > 0)

function toggleSelectionMode() {
  if (selectionMode.value) exitSelectionMode()
  else selectionMode.value = true
}

function exitSelectionMode() {
  if (bulkBusy.value) return
  selectionMode.value = false
  selection.clear()
}

function toggleRuleSelection(rule) {
  if (!selectionMode.value) return
  selection.toggle(ruleKey(rule))
}

// Leaving the result set invalidates the selection.
watch(
  [agentFilter, searchQuery, enabledStatusValue, syncValue, tagsValue, egressProfileIdValue, relayListenerIdValue, page],
  () => { selection.clear() }
)

watch(canBulk, (value) => {
  if (!value && !bulkBusy.value) {
    selectionMode.value = false
    selection.clear()
  }
})

function onSelectionKeydown(event) {
  if (event.key !== 'Escape' || !selectionMode.value) return
  if (bulkDeleteOpen.value || bulkBusy.value) return
  exitSelectionMode()
}
onMounted(() => window.addEventListener('keydown', onSelectionKeydown))
onUnmounted(() => window.removeEventListener('keydown', onSelectionKeydown))

function selectedRules() {
  const byKey = new Map(filteredRules.value.map((rule) => [ruleKey(rule), rule]))
  return selection.selectedList.value.map((key) => byKey.get(key)).filter(Boolean)
}

function mutationTargetFor(rule) {
  const resolved = resolveMutationAgentId(rule, agentFilter.value, { fallbackAgentId: agentId.value })
  if (!resolved.agentId) throw new Error(resolved.error || '缺少节点归属，无法执行该操作')
  return resolved.agentId
}

async function executeBulk(task, actionLabel) {
  const targets = selectedRules()
  if (!targets.length) return null
  const byKey = new Map(targets.map((rule) => [ruleKey(rule), rule]))
  bulkBusy.value = true
  bulkProgress.value = { done: 0, total: targets.length }
  try {
    const result = await runBulk([...byKey.keys()], (key) => task(byKey.get(key)), {
      onProgress: (progress) => { bulkProgress.value = progress },
      onSettled: async () => {
        invalidateResourceList(queryClient, 'l4Rules')
        queryClient.invalidateQueries({ queryKey: ['agents'] })
      }
    })
    // Failed rules stay selected so the user can retry them.
    selection.setSelection(result.failed)
    const summary = summarizeBulk(result, actionLabel)
    if (result.failed.length) messageStore.error(summary, `批量${actionLabel}未全部完成`)
    else messageStore.success(summary)
    return { result, summary }
  } finally {
    bulkBusy.value = false
    bulkProgress.value = null
  }
}

async function runBulkToggle(enabled) {
  if (bulkBusy.value) return
  await executeBulk(
    (rule) => apiUpdateL4Rule(mutationTargetFor(rule), rule.id, { enabled }),
    enabled ? '启用' : '禁用'
  )
}

function startBulkDelete() {
  if (bulkBusy.value || !selection.selectedCount.value) return
  bulkDeleteError.value = ''
  bulkDeleteOpen.value = true
}

function closeBulkDelete() {
  if (bulkBusy.value) return
  bulkDeleteOpen.value = false
  bulkDeleteError.value = ''
}

async function confirmBulkDelete() {
  if (bulkBusy.value) return
  const outcome = await executeBulk(
    (rule) => apiDeleteL4Rule(mutationTargetFor(rule), rule.id),
    '删除'
  )
  if (!outcome) {
    bulkDeleteOpen.value = false
    return
  }
  if (outcome.result.failed.length) {
    bulkDeleteError.value = outcome.summary
    return
  }
  bulkDeleteOpen.value = false
  bulkDeleteError.value = ''
  selectionMode.value = false
}

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
  deleteError.value = ''
}

function closeForm() {
  showAddForm.value = false
  editingRule.value = null
}

function closeCopy() {
  showCopyModal.value = false
  copyingRule.value = null
}

async function confirmDelete() {
  if (!deletingRule.value) return
  const target = requireMutationAgent(deletingRule.value, '删除')
  if (!target) return
  try {
    await deleteL4Rule.mutateAsync({ id: deletingRule.value.id, agentId: target })
    deleteError.value = ''
    deletingRule.value = null
  } catch (error) {
    // Keep the dialog open with the failure reason; the hook also toasts.
    deleteError.value = error?.message || '删除失败，请稍后重试'
  }
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

.rules-page__header-right .btn--active {
  border-color: var(--color-primary);
  color: var(--color-primary);
}

.rules-page__subtitle {
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
  margin: 0;
  line-height: 1.35;
  font-variant-numeric: tabular-nums;
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

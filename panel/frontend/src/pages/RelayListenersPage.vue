<template>
  <div class='relay-page'>
    <div class='relay-page__header'>
      <div class='relay-page__header-left'>
        <h1 class='relay-page__title'>Relay 监听器</h1>
        <p v-if='hasAgentFilter' class='relay-page__subtitle'>共 {{ listTotal }} 个 · 本页 {{ listeners.length }} 个 · 默认自动签发证书</p>
        <p v-else class='relay-page__subtitle'>暂无可用节点</p>
      </div>
      <div class='relay-page__header-right'>
        <ViewToggle v-if='hasAgentFilter && (listTotal > 0 || listQ || searchQuery)' v-model:view='view' />
        <button v-if='canCreate' class='btn btn-primary' @click="startCreate">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
            <line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/>
          </svg>
          <span class="btn-text">新建监听器</span>
        </button>
      </div>
    </div>

    <OperationStatusList />

    <ResourceListFilterBar
      :agent-id="agentFilter || ALL_AGENTS_FILTER"
      :agent-baseline="ALL_AGENTS_FILTER"
      :agents="allAgents"
      :q="searchQuery"
      search-placeholder="搜索名称 / 端口 / 标签 / #id=..."
      :filter-fields="filterFields"
      :filter-values="filterValues"
      @update:agent-id="handleAgentSelect"
      @update:q="searchQuery = $event"
      @update:filter="onFilterUpdate"
    />

    <!-- No agents available -->
    <div v-if='!allAgents.length' class='relay-page__prompt'>
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <path d="M8 12h8"/><path d="M6 8h12"/><path d="M10 16h4"/><circle cx="4" cy="12" r="2"/><circle cx="20" cy="12" r="2"/>
      </svg>
      <p>暂无可用节点</p>
      <p class="relay-page__prompt-hint">请先加入节点后再管理 Relay 监听器</p>
      <RouterLink to="/agents" class="btn btn-primary">加入节点</RouterLink>
    </div>

    <!-- Filter active, no listeners -->
    <div v-else-if='hasAgentFilter && !listeners.length && !exactRelayMatch && !isLoading && !_crossSearching' class='relay-page__empty'>
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <path d="M8 12h8"/><path d="M6 8h12"/><path d="M10 16h4"/><circle cx="4" cy="12" r="2"/><circle cx="20" cy="12" r="2"/>
      </svg>
      <template v-if='hasActiveFilters'>
        <p>没有匹配的 Relay 监听器</p>
      </template>
      <template v-else>
        <p>暂无 Relay 监听器</p>
        <button v-if='canCreate' class='btn btn-primary' @click="startCreate">创建第一个监听器</button>
        <p v-else class='relay-page__prompt-hint'>全部节点视图下请先选择具体节点再新建</p>
      </template>
    </div>

    <div v-else-if='hasAgentFilter && listeners.length && !displayListeners.length && !_crossSearching' class='relay-page__empty'>
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>
      </svg>
      <p>没有匹配的 Relay 监听器</p>
    </div>

    <!-- Listener card grid -->
    <div v-show='hasAgentFilter && displayListeners.length && view === "card"' class='relay-grid'>
      <RelayCard
        v-for='listener in displayListeners'
        :key='`${listener.agent_id || ""}:${listener.id}`'
        :listener='listener'
        :agent='selectedAgent'
        :traffic='trafficForListener(listener)'
        :agent-node-total='nodeTotalFor(listener)'
        @edit='startEdit'
        @toggle='toggleListener'
        @delete='startDelete'
        @traffic-click='openTrendModal'
      />
    </div>

    <!-- Listener list table -->
    <RelayTable
      v-show='hasAgentFilter && displayListeners.length && view === "list"'
      :listeners='displayListeners'
      :agent='selectedAgent'
      @edit='startEdit'
      @toggle='toggleListener'
      @delete='startDelete'
    />

    <ListPagination
      v-if="hasAgentFilter && listTotal > 0 && !exactRelayMatch"
      :page="page"
      :page-size="pageSize"
      :total="listTotal"
      @update:page="page = $event"
    />

    <!-- Loading -->
    <div v-if='isLoading' class='relay-page__loading'>
      <div class="spinner"></div>
    </div>

    <BaseModal
      :model-value="showAddForm || !!editingListener"
      :title="editingListener ? '编辑 Relay 监听器' : '新建 Relay 监听器'"
      :subtitle="formModalSubtitle"
      size="xl"
      :close-on-click-modal="false"
      @update:model-value="closeForm"
    >
      <RelayListenerForm :initial-data="editingListener" :agent-id="formAgentId" @success="closeForm" />
    </BaseModal>

    <DeleteConfirmDialog
      :show="!!deletingListener"
      title="确认删除监听器"
      message="若该监听器已被规则引用，删除会被阻止。删除后相关配置将无法恢复。"
      :name="deletingListener?.name"
      confirm-text="确认删除"
      :loading="deleteRelayListener.isPending?.value"
      @confirm="confirmDelete"
      @cancel="deletingListener = null"
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
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import { useAgent } from '../context/AgentContext'
import { useAgents } from '../hooks/useAgents'
import { useRelayListenersList, useDeleteRelayListener, useUpdateRelayListener } from '../hooks/useRelayListeners'
import { fetchRelayListeners, fetchAllAgentsRelayListeners, fetchCertificates, fetchAllAgentsCertificates } from '../api'
import { exactIdItems, findAllMatchesInAgents, parseIdQuery, shouldStartCrossAgentIdSearch } from '../hooks/useIdSearch'
import { useTrafficSummaryForResources } from '../hooks/useTrafficSummaryForResources'
import IdCandidateModal from '../components/IdCandidateModal.vue'
import RelayListenerForm from '../components/RelayListenerForm.vue'
import DeleteConfirmDialog from '../components/DeleteConfirmDialog.vue'
import BaseModal from '../components/base/BaseModal.vue'
import ResourceListFilterBar from '../components/common/ResourceListFilterBar.vue'
import CreateAgentPicker from '../components/common/CreateAgentPicker.vue'
import RelayCard from '../components/relay/RelayCard.vue'
import ViewToggle from '../components/common/ViewToggle.vue'
import ListPagination from '../components/common/ListPagination.vue'
import RelayTable from '../components/relay/RelayTable.vue'
import { useViewToggle } from '../composables/useViewToggle'
import { useListFilterUrl } from '../composables/useListFilterUrl'
import TrafficTrendModal from '../components/traffic/TrafficTrendModal.vue'
import OperationStatusList from '../components/operations/OperationStatusList.vue'
import { ALL_AGENTS_FILTER, isAllAgentsFilter, normalizeAgentFilter } from '../utils/agentFilter.js'
import { flattenAgentGroupedItems } from '../utils/flattenAgentGroupedItems.js'
import { resolveCreateAgentId, resolveMutationAgentId, resolveCopyTargetAgentId } from '../utils/resolveResourceAgent.js'

const route = useRoute()
const router = useRouter()
const { view } = useViewToggle('relay')
const agentContext = useAgent()
const { selectedAgentId } = agentContext
const systemInfo = agentContext.systemInfo || ref(null)
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
const selectedAgent = computed(() => allAgents.value.find((a) => a.id === agentId.value) || null)
const formAgent = computed(() => allAgents.value.find((a) => String(a.id) === String(formAgentId.value)) || null)
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
    certificate_id: { key: 'cert', type: 'string', baseline: '', validate: isPositiveIntString }
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

const filterValues = computed(() => ({
  enabled: enabledStatusValue.value,
  sync: syncValue.value,
  tags: tagsValue.value,
  certificate_id: certificateIdValue.value
}))

function onFilterUpdate({ key, value }) {
  setUrlFilter(key, value, { immediate: true })
}

// Option sources for tag / certificate dimensions.
const optionAgentIds = computed(() => allAgents.value.map((agent) => agent.id))

const { data: listenersForTags } = useQuery({
  queryKey: computed(() => ['relay-listener-tag-options', agentFilter.value || 'all']),
  enabled: hasAgentFilter,
  queryFn: () => (agentId.value
    ? fetchRelayListeners(agentId.value)
    : fetchAllAgentsRelayListeners(optionAgentIds.value))
})
const tagOptions = computed(() => {
  const collected = new Set(tagsValue.value)
  // Single-agent fetch returns a flat list; all-agents returns [{ agentId, listeners }].
  for (const listener of flattenAgentGroupedItems(listenersForTags.value, 'listeners')) {
    for (const tag of listener?.tags || []) collected.add(String(tag))
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
  { key: 'tags', label: '标签', type: 'multi', options: tagOptions.value },
  { key: 'certificate_id', label: '证书', type: 'select', options: certificateOptions.value },
  {
    key: 'sync',
    label: '同步状态',
    type: 'chip',
    options: [
      { value: '', label: '全部' },
      { value: 'applied', label: '已同步' },
      { value: 'pending', label: '待同步' }
    ]
  }
])

const hasActiveFilters = computed(() => (
  Boolean(listQ.value)
  || Boolean(String(searchQuery.value || '').trim())
  || enabledStatusValue.value !== ''
  || syncValue.value !== ''
  || tagsValue.value.length > 0
  || certificateIdValue.value !== ''
))

watch(
  [agentFilter, listQ, enabledStatusValue, syncValue, tagsValue, certificateIdValue],
  () => { page.value = 1 }
)

const listFilters = computed(() => ({
  tags: tagsValue.value,
  sync: syncValue.value || undefined,
  certificateId: certificateIdValue.value || undefined
}))

const { data: listenersPage, isLoading } = useRelayListenersList({
  agentFilter,
  page,
  pageSize,
  q: listQ,
  enabledFilter,
  filters: listFilters,
  enabled: hasAgentFilter
})
const deleteRelayListener = useDeleteRelayListener(agentId)
const updateRelayListener = useUpdateRelayListener(agentId)
const listeners = computed(() => listenersPage.value?.items ?? [])
const listTotal = computed(() => listenersPage.value?.total ?? 0)

// Search pre-fill / clear is handled by useListFilterUrl's key-level watcher
// on route.query.search (replaces the old per-page watch; same semantics).

const _crossSearching = ref(false)
const lastCrossSearchKey = ref('')
const exactRelayMatch = ref(null)
const candidateModalVisible = ref(false)
const candidateModalCandidates = ref([])
const candidateModalId = ref('')

const displayListeners = computed(() => {
  const idQuery = parseIdQuery(searchQuery.value)
  if (!idQuery) return listeners.value
  return exactIdItems({
    search: searchQuery.value,
    pageItems: listeners.value,
    resolvedMatch: exactRelayMatch.value,
    agentFilter: agentFilter.value,
    enabled: enabledFilter.value
  })
})

watch([searchQuery, agentFilter], ([search, filter]) => {
  const idQuery = parseIdQuery(search)
  const match = exactRelayMatch.value
  if (!idQuery) lastCrossSearchKey.value = ''
  if (idQuery && isAllAgentsFilter(filter)) {
    exactRelayMatch.value = null
    lastCrossSearchKey.value = ''
    return
  }
  if (!idQuery || !match || String(match.record?.id) !== idQuery.id ||
    (filter && !isAllAgentsFilter(filter) && String(filter) !== String(match.agentId))) {
    exactRelayMatch.value = null
  }
})

watch([displayListeners, isLoading, _crossSearching, allAgents, enabledFilter], ([result]) => {
  const idQuery = shouldStartCrossAgentIdSearch({
    search: searchQuery.value,
    currentMatches: result,
    isLoading: isLoading.value,
    isSearching: _crossSearching.value
  })
  if (!idQuery) return
  const agentIds = allAgents.value.map((agent) => agent.id)
  if (!agentIds.length) return
  const searchKey = `${idQuery.id}\u0000${agentIds.map(String).sort().join('\u0000')}\u0000enabled=${String(enabledFilter.value ?? '')}`
  if (lastCrossSearchKey.value === searchKey) return
  lastCrossSearchKey.value = searchKey
  _crossSearching.value = true
  candidateModalId.value = idQuery.id
  fetchAllAgentsRelayListeners(agentIds).then((allData) => {
    if (parseIdQuery(searchQuery.value)?.id !== idQuery.id) return
    const allMatches = findAllMatchesInAgents(
      { relayListeners: allData },
      idQuery.id,
      { enabled: enabledFilter.value }
    )
    if (allMatches.length === 1) {
      exactRelayMatch.value = allMatches[0]
      router.replace({ query: { ...route.query, agentId: allMatches[0].agentId, search: searchQuery.value } })
    } else if (allMatches.length > 1) {
      candidateModalCandidates.value = allMatches
      candidateModalVisible.value = true
    }
  }).finally(() => { _crossSearching.value = false })
})

function handleCandidateSelect(candidate) {
  exactRelayMatch.value = candidate
  router.replace({ query: { ...route.query, agentId: candidate.agentId, search: searchQuery.value } })
}

const trafficStatsEnabled = computed(() => !!systemInfo.value && systemInfo.value.traffic_stats_enabled !== false)
const { nodeTotalFor, trafficFor: trafficForListener } = useTrafficSummaryForResources({
  agentId,
  items: displayListeners,
  trafficStatsEnabled,
  mapName: 'relay_listeners'
})

const showAddForm = ref(false)
const editingListener = ref(null)
const deletingListener = ref(null)
const deleteError = ref('')

const trendModal = ref({ visible: false, agentId: '', scopeType: '', scopeId: '', scopeLabel: '' })
const trafficDirection = ref('both')

function openTrendModal(listener) {
  const id = requireMutationAgent(listener, '查看流量')
  if (!id) return
  trendModal.value = {
    visible: true,
    agentId: id,
    scopeType: 'relay_listener',
    scopeId: String(listener.id),
    scopeLabel: `Relay 监听 #${listener.id}`
  }
}


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

function startEdit(listener) {
  const target = requireMutationAgent(listener, '编辑')
  if (!target) return
  formAgentId.value = target
  editingListener.value = listener
}

function startDelete(listener) {
  deletingListener.value = listener
  deleteError.value = ''
}

function closeForm() {
  showAddForm.value = false
  editingListener.value = null
}

function toggleListener(listener) {
  const target = requireMutationAgent(listener, '启停')
  if (!target) return
  updateRelayListener.mutate({ id: listener.id, enabled: !listener.enabled, agentId: target })
}

function confirmDelete() {
  if (!deletingListener.value) return
  const target = requireMutationAgent(deletingListener.value, '删除')
  if (!target) return
  deleteRelayListener.mutate(
    { id: deletingListener.value.id, agentId: target },
    {
      onSuccess: () => {
        deleteError.value = ''
        deletingListener.value = null
      },
      onError: (err) => {
        deleteError.value = err?.message || '删除失败'
      },
    },
  )
}
</script>

<style scoped>
.relay-page {
  max-width: 1200px;
  margin: 0 auto;
  animation: fadeIn var(--duration-normal) var(--ease-default) both;
}

.relay-page__header {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  margin-bottom: 0.85rem;
  gap: 0.75rem 1rem;
  flex-wrap: wrap;
}

.relay-page__header-left {
  flex: 1;
  min-width: 0;
}

.relay-page__header-right {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  flex-shrink: 0;
}

.relay-page__title {
  font-size: 1.3125rem;
  font-weight: 700;
  margin: 0 0 0.15rem;
  color: var(--color-text-primary);
  letter-spacing: -0.02em;
  line-height: 1.25;
}

.relay-page__subtitle {
  font-size: 0.75rem;
  color: var(--color-text-tertiary);
  margin: 0;
  line-height: 1.35;
  font-variant-numeric: tabular-nums;
}

.relay-page__prompt,
.relay-page__empty,
.relay-page__loading {
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

.relay-page__prompt-hint {
  font-size: 0.8125rem;
  color: var(--color-text-tertiary);
}

.relay-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: 0.75rem;
}

@media (min-width: 1280px) {
  .relay-grid { grid-template-columns: repeat(auto-fill, minmax(340px, 1fr)); }
}

@media (max-width: 640px) {
  .btn-text {
    display: none;
  }
  .relay-grid {
    grid-template-columns: 1fr;
  }
  .relay-page__header {
    align-items: flex-start;
    gap: 0.5rem;
  }
  .relay-page__header-right {
    width: 100%;
    justify-content: flex-end;
  }
}

.relay-grid,
.relay-page :deep(.rule-table) {
  animation: viewToggleIn 200ms var(--ease-default) both;
}
@keyframes viewToggleIn {
  from { opacity: 0; transform: translateY(4px); }
  to { opacity: 1; transform: translateY(0); }
}
@media (prefers-reduced-motion: reduce) {
  .relay-grid,
  .relay-page :deep(.rule-table) {
    animation: none;
  }
}
/* Wide-screen (2K/4K) width steps */
@media (min-width: 1920px) {
  .relay-page { max-width: 1600px; }
}
@media (min-width: 2560px) {
  .relay-page { max-width: 2000px; }
}
</style>

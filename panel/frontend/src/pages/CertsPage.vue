<template>
  <div class='certs-page'>
    <div class='certs-page__header'>
      <div class='certs-page__header-left'>
        <h1 class='certs-page__title'>证书管理</h1>
        <p class='certs-page__subtitle'>
          <template v-if='hasAgentFilter'>
            共 {{ listTotal }} 项 · 本页 {{ certificates.length }} 项 · {{ activeCount }} 生效中<template v-if='issuingCount'> · {{ issuingCount }} 签发中</template>
          </template>
          <template v-else>
            暂无可用节点
          </template>
        </p>
      </div>
      <div class='certs-page__header-right'>
        <RouterLink class='btn btn-secondary' to='/pki'>内部 PKI</RouterLink>
        <ViewToggle v-if='hasAgentFilter && (listTotal > 0 || listQ || searchQuery)' v-model:view='view' />
        <button v-if='canCreate' class='btn btn-primary' @click="startCreate">
          <svg width='16' height='16' viewBox='0 0 24 24' fill='none' stroke='currentColor' stroke-width='2.5'>
            <line x1='12' y1='5' x2='12' y2='19' />
            <line x1='5' y1='12' x2='19' y2='12' />
          </svg>
          <span class='btn-text'>新建证书</span>
        </button>
      </div>
    </div>

    <div class='certs-page__domain-note'>
      此页仅管理网站 ACME 与手动上传的公开业务证书。Relay mTLS 身份、内部 CA、撤销和轮转请前往“内部 PKI”。
    </div>

    <OperationStatusList />

    <ResourceListFilterBar
      :agent-id="agentFilter || ALL_AGENTS_FILTER"
      :agent-baseline="ALL_AGENTS_FILTER"
      :agents="allAgents"
      :q="searchQuery"
      search-placeholder="搜索域名 / 标签 / #id=..."
      :filter-fields="filterFields"
      :filter-values="filterValues"
      @update:agent-id="handleAgentSelect"
      @update:q="searchQuery = $event"
      @update:filter="onFilterUpdate"
    />

    <div v-if='!allAgents.length' class='certs-page__prompt'>
      <svg width='48' height='48' viewBox='0 0 24 24' fill='none' stroke='currentColor' stroke-width='1.5'>
        <rect x='3' y='11' width='18' height='11' rx='2' ry='2' />
        <path d='M7 11V7a5 5 0 0 1 10 0v4' />
      </svg>
      <p>暂无可用节点</p>
      <p class="certs-page__prompt-hint">请先加入节点后再管理证书</p>
      <RouterLink to="/agents" class="btn btn-primary">加入节点</RouterLink>
    </div>

    <div v-else-if='isLoading' class='certs-page__loading'>
      <div class='spinner'></div>
    </div>

    <div v-show='hasAgentFilter && filteredCerts.length && view === "card"' class='cert-grid'>
      <CertCard
        v-for='cert in filteredCerts'
        :key='`${cert.agent_id || ""}:${cert.id}`'
        :cert='cert'
        :agent='selectedAgent'
        @edit='startEdit'
        @delete='startDelete'
        @issue='issueCert'
      />
    </div>

    <CertTable
      v-show='hasAgentFilter && filteredCerts.length && view === "list"'
      :certificates='filteredCerts'
      :agent='selectedAgent'
      @edit='startEdit'
      @delete='startDelete'
    />

    <ListPagination
      v-if="hasAgentFilter && listTotal > 0 && !exactCertMatch"
      :page="page"
      :page-size="pageSize"
      :total="listTotal"
      @update:page="page = $event"
    />

    <div v-if='hasAgentFilter && certificates.length && !filteredCerts.length && !_crossSearching' class='certs-page__empty'>
      <svg width='48' height='48' viewBox='0 0 24 24' fill='none' stroke='currentColor' stroke-width='1.5'>
        <circle cx='11' cy='11' r='8' />
        <line x1='21' y1='21' x2='16.65' y2='16.65' />
      </svg>
      <p>没有匹配的证书</p>
    </div>

    <div v-if='hasAgentFilter && !isLoading && !certificates.length && !exactCertMatch && !_crossSearching' class='certs-page__empty'>
      <svg width='48' height='48' viewBox='0 0 24 24' fill='none' stroke='currentColor' stroke-width='1.5'>
        <rect x='3' y='11' width='18' height='11' rx='2' ry='2' />
        <path d='M7 11V7a5 5 0 0 1 10 0v4' />
      </svg>
      <template v-if='hasActiveFilters'>
        <p>没有匹配的证书</p>
      </template>
      <template v-else>
        <p>暂无证书</p>
        <button v-if='canCreate' class='btn btn-primary' @click="startCreate">从模板创建第一个证书</button>
        <p v-else class='certs-page__prompt-hint'>全部节点视图下请先选择具体节点再新建</p>
      </template>
    </div>

    <BaseModal
      :model-value="showAddForm || !!editingCert"
      :title="editingCert ? '编辑证书' : '新建证书'"
      :subtitle="formModalSubtitle"
      size="xl"
      :close-on-click-modal="false"
      @update:model-value="closeForm"
    >
      <CertificateForm :initial-data="editingCert" :agent-id="formAgentId" @success="closeForm" />
    </BaseModal>

    <DeleteConfirmDialog
      :show='!!deletingCert'
      title='确认删除证书'
      message='删除后该证书将立即失效，相关配置将无法恢复。'
      :name='deletingCert?.domain'
      confirm-text='确认删除'
      :loading='deleteCertificate.isPending?.value'
      @confirm='confirmDelete'
      @cancel='deletingCert = null'
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
import { useCertificatesList, useDeleteCertificate, useIssueCertificate } from '../hooks/useCertificates'
import CertificateForm from '../components/CertificateForm.vue'
import DeleteConfirmDialog from '../components/DeleteConfirmDialog.vue'
import BaseModal from '../components/base/BaseModal.vue'
import ResourceListFilterBar from '../components/common/ResourceListFilterBar.vue'
import CreateAgentPicker from '../components/common/CreateAgentPicker.vue'
import CertCard from '../components/certs/CertCard.vue'
import ViewToggle from '../components/common/ViewToggle.vue'
import ListPagination from '../components/common/ListPagination.vue'
import CertTable from '../components/certs/CertTable.vue'
import { useViewToggle } from '../composables/useViewToggle'
import { useListFilterUrl } from '../composables/useListFilterUrl'
import { ALL_AGENTS_FILTER, isAllAgentsFilter, normalizeAgentFilter } from '../utils/agentFilter.js'
import { flattenAgentGroupedItems } from '../utils/flattenAgentGroupedItems.js'
import { resolveCreateAgentId, resolveMutationAgentId, resolveCopyTargetAgentId } from '../utils/resolveResourceAgent.js'
import {
  isSystemRelayCA
} from '../utils/certificateTemplates'
import { fetchCertificates, fetchAllAgentsCertificates } from '../api'
import { exactIdItems, findAllMatchesInAgents, parseIdQuery, shouldStartCrossAgentIdSearch } from '../hooks/useIdSearch'
import IdCandidateModal from '../components/IdCandidateModal.vue'
import OperationStatusList from '../components/operations/OperationStatusList.vue'

const route = useRoute()
const router = useRouter()
const { view } = useViewToggle('certs')
const agentContext = useAgent()
const systemInfo = agentContext.systemInfo || ref(null)
const { selectedAgentId } = agentContext
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

// Filter state lives in the URL (key-level watchers; do NOT watch the whole
// route.query object — other keys would wipe in-flight typed input).
const { values: urlFilters, setValue: setUrlFilter } = useListFilterUrl({
  route,
  router,
  schema: {
    search: { key: 'search', type: 'string', baseline: '' },
    enabled: { key: 'enabled', type: 'enum', values: ['true', 'false'], baseline: '' },
    status: { key: 'status', type: 'enum', values: ['active', 'pending', 'issuing', 'error'], baseline: '' },
    tags: { key: 'tags', type: 'list', baseline: [] },
    referenced: { key: 'referenced', type: 'enum', values: ['true', 'false'], baseline: '' }
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
const certStatusValue = computed(() => urlFilters.status ?? '')
const enabledFilter = computed(() => {
  if (enabledStatusValue.value === 'true') return true
  if (enabledStatusValue.value === 'false') return false
  return undefined
})
const listStatus = computed(() => String(certStatusValue.value || '').trim())
const tagsValue = computed(() => (Array.isArray(urlFilters.tags) ? urlFilters.tags : []))
const referencedValue = computed(() => urlFilters.referenced ?? '')
const referencedFilter = computed(() => {
  if (referencedValue.value === 'true') return true
  if (referencedValue.value === 'false') return false
  return undefined
})

const filterValues = computed(() => ({
  enabled: enabledStatusValue.value,
  status: certStatusValue.value,
  tags: tagsValue.value,
  referenced: referencedValue.value
}))

function onFilterUpdate({ key, value }) {
  setUrlFilter(key, value, { immediate: true })
}

// Option source for the tag dimension.
const optionAgentIds = computed(() => allAgents.value.map((agent) => agent.id))
const { data: certsForTags } = useQuery({
  queryKey: computed(() => ['certs-tag-options', agentFilter.value || 'all']),
  enabled: hasAgentFilter,
  queryFn: () => (agentId.value
    ? fetchCertificates(agentId.value)
    : fetchAllAgentsCertificates(optionAgentIds.value))
})
const tagOptions = computed(() => {
  const collected = new Set(tagsValue.value)
  // Single-agent fetch returns a flat list; all-agents returns [{ agentId, certificates }].
  for (const cert of flattenAgentGroupedItems(certsForTags.value, 'certificates')) {
    for (const tag of cert?.tags || []) collected.add(String(tag))
  }
  return [...collected].sort().map((tag) => ({ value: tag, label: tag }))
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
    key: 'status',
    label: '证书状态',
    type: 'chip',
    options: [
      { value: '', label: '全部状态' },
      { value: 'active', label: '生效中' },
      { value: 'pending', label: '待签发' },
      { value: 'issuing', label: '签发中' },
      { value: 'error', label: '签发失败' }
    ]
  },
  { key: 'tags', label: '标签', type: 'multi', options: tagOptions.value },
  {
    key: 'referenced',
    label: '引用状态（域名近似）',
    type: 'chip',
    options: [
      { value: '', label: '全部' },
      { value: 'true', label: '被引用' },
      { value: 'false', label: '未引用' }
    ]
  }
])

const hasActiveFilters = computed(() => (
  Boolean(listQ.value)
  || Boolean(String(searchQuery.value || '').trim())
  || enabledStatusValue.value !== ''
  || certStatusValue.value !== ''
  || tagsValue.value.length > 0
  || referencedValue.value !== ''
))

watch(
  [agentFilter, listQ, enabledStatusValue, certStatusValue, tagsValue, referencedValue],
  () => { page.value = 1 }
)

const listFilters = computed(() => ({
  tags: tagsValue.value,
  referenced: referencedFilter.value
}))

const { data: certsPage, isLoading } = useCertificatesList({
  agentFilter,
  page,
  pageSize,
  q: listQ,
  enabledFilter,
  status: listStatus,
  filters: listFilters,
  enabled: hasAgentFilter
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
const deleteCertificate = useDeleteCertificate(agentId)
const issueCertificate = useIssueCertificate(agentId)
const certificates = computed(() => certsPage.value?.items ?? [])
const listTotal = computed(() => certsPage.value?.total ?? 0)
const showAddForm = ref(false)
const editingCert = ref(null)
const deletingCert = ref(null)

// Search pre-fill / clear is handled by useListFilterUrl's key-level watcher
// on route.query.search (replaces the old per-page watch; same semantics).

const filteredCerts = computed(() => {
  const raw = searchQuery.value.trim()
  if (!raw) return certificates.value
  if (parseIdQuery(raw)) {
    return exactIdItems({
      search: raw,
      pageItems: certificates.value,
      resolvedMatch: exactCertMatch.value,
      agentFilter: agentFilter.value,
      enabled: enabledFilter.value,
      status: listStatus.value
    })
  }
  return certificates.value
})

// R3: Cross-agent #id= resolution — if not found in current agent, search all agents
const _crossSearching = ref(false)
const lastCrossSearchKey = ref('')
const exactCertMatch = ref(null)
const candidateModalVisible = ref(false)
const candidateModalCandidates = ref([])
const candidateModalId = ref('')

watch([searchQuery, agentFilter], ([search, filter]) => {
  const idQuery = parseIdQuery(search)
  const match = exactCertMatch.value
  if (!idQuery) {
    exactCertMatch.value = null
    lastCrossSearchKey.value = ''
    return
  }
  if (isAllAgentsFilter(filter)) {
    exactCertMatch.value = null
    lastCrossSearchKey.value = ''
    return
  }
  if (match && (String(match.record?.id) !== idQuery.id ||
    (filter && !isAllAgentsFilter(filter) && String(filter) !== String(match.agentId)))) {
    exactCertMatch.value = null
  }
})

watch([filteredCerts, isLoading, _crossSearching, allAgents, enabledFilter, listStatus], ([result]) => {
  const idQuery = shouldStartCrossAgentIdSearch({
    search: searchQuery.value,
    currentMatches: result,
    isLoading: isLoading.value,
    isSearching: _crossSearching.value
  })
  if (!idQuery) return
  const agentIds = allAgents.value.map(a => a.id)
  if (!agentIds.length) return
  const searchKey = `${idQuery.id}\u0000${agentIds.map(String).sort().join('\u0000')}\u0000enabled=${String(enabledFilter.value ?? '')}\u0000status=${listStatus.value}`
  if (lastCrossSearchKey.value === searchKey) return
  lastCrossSearchKey.value = searchKey
  _crossSearching.value = true
  candidateModalId.value = idQuery.id
  fetchAllAgentsCertificates(agentIds).then(allData => {
    if (parseIdQuery(searchQuery.value)?.id !== idQuery.id) return
    const allMatches = findAllMatchesInAgents(
      { certificates: allData },
      idQuery.id,
      { enabled: enabledFilter.value, status: listStatus.value }
    )
    if (allMatches.length === 1) {
      exactCertMatch.value = allMatches[0]
      router.replace({ query: { ...route.query, agentId: allMatches[0].agentId, search: searchQuery.value } })
    } else if (allMatches.length > 1) {
      candidateModalCandidates.value = allMatches
      candidateModalVisible.value = true
    }
  }).finally(() => { _crossSearching.value = false })
})

function handleCandidateSelect(candidate) {
  exactCertMatch.value = candidate
  router.replace({ query: { ...route.query, agentId: candidate.agentId, search: searchQuery.value } })
}

const activeCount = computed(() => certificates.value.filter((cert) => cert.enabled && cert.status === 'active').length)
const issuingCount = computed(() => certificates.value.filter((cert) => cert.status === 'issuing').length)

function issueCert(cert) {
  {
  const target = requireMutationAgent(cert, '签发')
  if (!target) return
  issueCertificate.mutate({ id: cert.id, agentId: target })
}
}

function startEdit(cert) {
  const target = requireMutationAgent(cert, '编辑')
  if (!target) return
  formAgentId.value = target
  editingCert.value = cert
}

function startDelete(cert) {
  if (isSystemRelayCA(cert)) {
    return
  }
  deletingCert.value = cert
}

function closeForm() {
  showAddForm.value = false
  editingCert.value = null
}

function confirmDelete() {
  if (!deletingCert.value) return
  const target = requireMutationAgent(deletingCert.value, '删除')
  if (!target) return
  deleteCertificate.mutate({ id: deletingCert.value.id, agentId: target })
  deletingCert.value = null
}

</script>

<style scoped>
.certs-page {
  max-width: 1200px;
  margin: 0 auto;
  animation: fadeIn var(--duration-normal) var(--ease-default) both;
}

.certs-page__header {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  margin-bottom: 0.85rem;
  gap: 0.75rem 1rem;
  flex-wrap: wrap;
}

.certs-page__header-left { flex: 1; min-width: 0; }

.certs-page__header-right {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  flex-shrink: 0;
}

.certs-page__title {
  font-size: 1.3125rem;
  font-weight: 700;
  margin: 0 0 0.15rem;
  color: var(--color-text-primary);
  letter-spacing: -0.02em;
  line-height: 1.25;
}

.certs-page__subtitle {
  font-size: 0.75rem;
  color: var(--color-text-tertiary);
  margin: 0;
  line-height: 1.35;
  font-variant-numeric: tabular-nums;
}

.certs-page__domain-note {
  padding: var(--space-3) var(--space-4);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  background: var(--color-bg-subtle);
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
  line-height: 1.5;
}

.certs-page__loading,
.certs-page__empty,
.certs-page__prompt {
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

.certs-page__prompt-hint {
  font-size: 0.8125rem;
  color: var(--color-text-tertiary);
}

@media (max-width: 640px) {
  .certs-page__header {
    flex-direction: column;
    align-items: stretch;
    gap: 0.65rem;
  }

  .certs-page__header-right {
    width: 100%;
    justify-content: stretch;
    gap: 0.5rem;
  }

  .certs-page__header-right .btn,
  .certs-page__header-right .btn-primary,
  .certs-page__header-right .btn-secondary {
    flex: 1 1 auto;
    min-width: 0;
    justify-content: center;
  }
}

.cert-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: 0.75rem;
}

@media (min-width: 1280px) {
  .cert-grid { grid-template-columns: repeat(auto-fill, minmax(340px, 1fr)); }
}

.cert-grid,
.certs-page :deep(.rule-table) {
  animation: viewToggleIn 200ms var(--ease-default) both;
}
@keyframes viewToggleIn {
  from { opacity: 0; transform: translateY(4px); }
  to { opacity: 1; transform: translateY(0); }
}
@media (prefers-reduced-motion: reduce) {
  .cert-grid,
  .certs-page :deep(.rule-table) {
    animation: none;
  }
}
/* Wide-screen (2K/4K) width steps */
@media (min-width: 1920px) {
  .certs-page { max-width: 1600px; }
}
@media (min-width: 2560px) {
  .certs-page { max-width: 2000px; }
}
</style>

<template>
  <div class="services-page">
    <header class="page-header">
      <div class="page-header__left">
        <h1 class="page-title">服务</h1>
        <p class="page-subtitle" data-testid="services-summary">
          <template v-if="services.length">
            共 {{ summaryCounts.total }} 项 · 已就绪 {{ summaryCounts.ready }} · 处理中 {{ summaryCounts.pending }} · 失败 {{ summaryCounts.failed }}
          </template>
          <template v-else>
            添加、查看、修改和删除接入。未完成状态只展示控制面返回的结果。
          </template>
        </p>
      </div>
      <div class="page-header__right">
        <button
          type="button"
          class="btn btn-secondary"
          :disabled="retryingLoad"
          data-testid="services-refresh"
          @click="retryLoad"
        >
          <svg
            :class="{ 'services-page__spin': retryingLoad }"
            width="15"
            height="15"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true"
          >
            <path d="M21 12a9 9 0 1 1-2.64-6.36" />
            <polyline points="21 3 21 9 15 9" />
          </svg>
          <span>{{ retryingLoad ? '刷新中...' : '刷新' }}</span>
        </button>
        <button
          type="button"
          class="btn btn-primary"
          data-testid="services-add"
          @click="startCreate"
        >
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <line x1="12" y1="5" x2="12" y2="19" />
            <line x1="5" y1="12" x2="19" y2="12" />
          </svg>
          <span>添加服务</span>
        </button>
      </div>
    </header>

    <div v-if="loadError && (services.length || selectedId)" class="services-page__alert" role="alert">
      <svg class="services-page__alert-icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <circle cx="12" cy="12" r="9" />
        <path d="M12 8v4" />
        <path d="M12 16h.01" />
      </svg>
      <span class="services-page__alert-text">{{ loadError }}</span>
      <button type="button" class="btn btn-secondary btn-sm" :disabled="retryingLoad" @click="retryLoad">重试</button>
    </div>

    <div v-if="actionError && !formModal && !deleting" class="services-page__alert" role="alert">
      <svg class="services-page__alert-icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <circle cx="12" cy="12" r="9" />
        <path d="M12 8v4" />
        <path d="M12 16h.01" />
      </svg>
      <span class="services-page__alert-text">{{ actionError }}</span>
    </div>

    <template v-if="selectedId">
      <ServiceDetailPanel
        v-if="selected"
        :service="selected"
        :agents="agents"
        :submitting="submitting"
        :refreshing="refreshingDetail"
        @back="closeDetail"
        @refresh="refreshSelected"
        @retry="retrySelected"
        @retire="retireSelected"
        @abandon="abandonSelected"
        @edit="startEdit"
        @delete="startDelete"
        @confirm-client="confirmOpen"
      />

      <SkeletonList v-else-if="loading || missingLookupLoading" variant="cards" :count="1" />

      <ErrorState
        v-else-if="missingLookupError && !missingNotFound"
        title="加载服务详情失败"
        :message="missingLookupError"
        :retrying="missingLookupLoading"
        @retry="retryMissingLookup"
      >
        <template #action>
          <button
            type="button"
            class="btn btn-secondary btn-sm"
            :disabled="missingLookupLoading"
            @click="retryMissingLookup"
          >
            重试
          </button>
          <button
            type="button"
            class="btn btn-primary btn-sm"
            data-testid="service-not-found-back"
            @click="closeDetail"
          >
            返回服务列表
          </button>
        </template>
      </ErrorState>

      <EmptyState
        v-else
        data-testid="service-not-found"
        title="未找到对应服务"
        :description="missingDescription"
      >
        <template #icon>
          <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="11" cy="11" r="8" />
            <line x1="21" y1="21" x2="16.65" y2="16.65" />
          </svg>
        </template>
        <template #action>
          <button
            type="button"
            class="btn btn-primary"
            data-testid="service-not-found-back"
            @click="closeDetail"
          >
            返回服务列表
          </button>
        </template>
      </EmptyState>
    </template>

    <template v-else>
      <ResourceListFilterBar
        v-if="services.length"
        :agent-id="agentFilter"
        :agent-baseline="ALL_AGENTS_FILTER"
        :agents="agents"
        :q="searchQuery"
        search-placeholder="搜索名称 / 入口地址 / 后端 URL..."
        :filter-fields="statusFilterFields"
        :filter-values="filterValues"
        @update:agent-id="agentFilter = $event"
        @update:q="searchQuery = $event"
        @update:filter="onFilterUpdate"
      />

      <SkeletonList v-if="loading" variant="cards" :count="3" />

      <ErrorState
        v-else-if="loadError && !services.length"
        title="加载服务列表失败"
        :message="loadError"
        :retrying="retryingLoad"
        @retry="retryLoad"
      />

      <EmptyState
        v-else-if="!services.length"
        title="还没有服务"
        description="添加后，进度会留在这个列表里。"
      >
        <template #action>
          <button type="button" class="btn btn-primary" @click="startCreate">添加服务</button>
        </template>
      </EmptyState>

      <EmptyState
        v-else-if="!filteredServices.length"
        data-testid="services-filter-empty"
        title="没有匹配的服务"
        description="当前筛选条件下没有结果，可以调整条件或清除筛选。"
      >
        <template #icon>
          <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="11" cy="11" r="8" />
            <line x1="21" y1="21" x2="16.65" y2="16.65" />
          </svg>
        </template>
        <template #action>
          <button
            type="button"
            class="btn btn-primary"
            data-testid="services-clear-filters"
            @click="clearFilters"
          >
            清除筛选
          </button>
        </template>
      </EmptyState>

      <div v-else class="service-grid">
        <ServiceCard
          v-for="service in filteredServices"
          :key="service.id"
          :service="service"
          :entry-agent-label="agentLabel(service.entry_agent_id)"
          :service-agent-label="agentLabel(service.service_agent_id)"
          @copy="copyEntry"
          @detail="openDetail"
          @edit="startEdit"
          @delete="startDelete"
        />
      </div>
    </template>

    <ServiceFormModal
      :model-value="Boolean(formModal)"
      :session-key="formModal?.session || ''"
      :service="formModal?.service || null"
      :agents="agents"
      :submitting="submitting"
      :form-error="formModal ? actionError : ''"
      @cancel="closeFormModal"
      @submit="submitForm"
    />

    <ServiceDeleteModal
      :service="deleting"
      :agents="agents"
      :submitting="submitting"
      :error="deleting ? actionError : ''"
      @cancel="closeDeleteModal"
      @confirm="confirmDelete"
    />
  </div>
</template>

<script setup>
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { useAgents } from '../hooks/useAgents'
import {
  abandonAccessService,
  confirmAccessServiceClient,
  correctAccessService,
  createAccessService,
  deleteAccessService,
  fetchAccessService,
  fetchAccessServices,
  retireAccessService,
  retryAccessService,
  updateAccessService,
} from '../api'
import ServiceCard from '../components/services/ServiceCard.vue'
import ServiceFormModal from '../components/services/ServiceFormModal.vue'
import ServiceDeleteModal from '../components/services/ServiceDeleteModal.vue'
import ServiceDetailPanel from '../components/services/ServiceDetailPanel.vue'
import ResourceListFilterBar from '../components/common/ResourceListFilterBar.vue'
import EmptyState from '../components/base/EmptyState.vue'
import ErrorState from '../components/base/ErrorState.vue'
import SkeletonList from '../components/base/SkeletonList.vue'
import { copyText } from '../utils/clipboard.js'
import { messageStore } from '../stores/messages.js'
import { ALL_AGENTS_FILTER } from '../utils/agentFilter.js'
import {
  accessErrorMessage,
  accessErrorService,
  errorRequestsRelayInput,
  filterAccessServices,
  resolveServiceSubmitAction,
  reuseIdempotencyKey,
  summarizeServices,
} from '../utils/accessService.js'

const route = useRoute()
const router = useRouter()
const queryClient = useQueryClient()
const { data: agentsData } = useAgents()
const agents = computed(() => (Array.isArray(agentsData.value) ? agentsData.value : []))

const servicesQuery = useQuery({
  queryKey: ['access-services'],
  queryFn: () => fetchAccessServices(),
})

const services = computed(() => (Array.isArray(servicesQuery.data.value) ? servicesQuery.data.value : []))
const loading = computed(() => servicesQuery.isLoading.value)
const retryingLoad = computed(() => Boolean(servicesQuery.isFetching?.value))
const loadError = computed(() => (servicesQuery.error.value ? accessErrorMessage(servicesQuery.error.value) : ''))

const agentFilter = ref(ALL_AGENTS_FILTER)
const searchQuery = ref('')
const statusFilter = ref('')

const statusFilterFields = [
  {
    key: 'status',
    label: '状态',
    type: 'chip',
    defaultValue: '',
    options: [
      { value: '', label: '全部' },
      { value: 'ready', label: '已就绪' },
      { value: 'pending', label: '处理中' },
      { value: 'failed', label: '失败' },
    ],
  },
]

const filterValues = computed(() => ({ status: statusFilter.value }))
const summaryCounts = computed(() => summarizeServices(services.value))
const filteredServices = computed(() => filterAccessServices(services.value, {
  agentId: agentFilter.value,
  q: searchQuery.value,
  status: statusFilter.value,
}))

const formModal = ref(null)
const persistedId = ref('')
const deleting = ref(null)
const submitting = ref(false)
const refreshingDetail = ref(false)
const actionError = ref('')
const idempotency = ref({})
let formSessionSerial = 0

const selectedId = computed(() => String(route.query.id || '').trim())
const selected = computed(() => services.value.find((service) => service.id === selectedId.value) || null)

const lookedUpId = ref('')
const missingLookupLoading = ref(false)
const missingLookupError = ref('')
const missingNotFound = ref(false)
const missingDescription = computed(() => (
  selectedId.value
    ? `服务 ${selectedId.value} 不存在或已被删除，可以返回服务列表查看当前接入。`
    : '对应服务不存在或已被删除。'
))

function isNotFoundError(error) {
  return error?.response?.status === 404 || error?.status === 404
}

function onFilterUpdate({ key, value }) {
  if (key === 'status') {
    statusFilter.value = String(value || '')
  }
}

function clearFilters() {
  agentFilter.value = ALL_AGENTS_FILTER
  searchQuery.value = ''
  statusFilter.value = ''
}

function retryLoad() {
  return servicesQuery.refetch?.()
}

function agentLabel(id) {
  const agent = agents.value.find((item) => String(item.id) === String(id))
  return agent?.name || id || '—'
}

function rememberService(service) {
  if (!service?.id) return
  queryClient.setQueryData(['access-services'], (current) => {
    const list = Array.isArray(current) ? current.slice() : []
    const index = list.findIndex((item) => item.id === service.id)
    if (index >= 0) list.splice(index, 1, service)
    else list.unshift(service)
    return list
  })
}

function forgetService(id) {
  if (!id) return
  queryClient.setQueryData(['access-services'], (current) => (
    Array.isArray(current) ? current.filter((item) => item.id !== id) : []
  ))
}

async function lookupSelectedById(id) {
  if (!id) return
  lookedUpId.value = id
  missingLookupLoading.value = true
  missingLookupError.value = ''
  missingNotFound.value = false
  try {
    const fetched = await fetchAccessService(id)
    if (selectedId.value !== id) return
    if (fetched?.id) {
      rememberService(fetched)
      return
    }
    missingNotFound.value = true
  } catch (error) {
    if (selectedId.value !== id) return
    if (isNotFoundError(error)) {
      missingNotFound.value = true
      return
    }
    missingLookupError.value = accessErrorMessage(error)
  } finally {
    if (selectedId.value === id) {
      missingLookupLoading.value = false
    }
  }
}

function retryMissingLookup() {
  if (!selectedId.value) return
  lookupSelectedById(selectedId.value)
}

watch(selectedId, () => {
  lookedUpId.value = ''
  missingLookupLoading.value = false
  missingLookupError.value = ''
  missingNotFound.value = false
})

watch(
  [selectedId, selected, loading],
  ([id, current, isLoading]) => {
    if (!id || current || isLoading) return
    if (lookedUpId.value === id) return
    lookupSelectedById(id)
  },
  { immediate: true },
)

function keyFor(operation, body) {
  const next = reuseIdempotencyKey(idempotency.value, operation, body)
  idempotency.value = next
  return next[operation].key
}

function dropKey(operation) {
  const next = { ...idempotency.value }
  delete next[operation]
  idempotency.value = next
}

function startCreate() {
  actionError.value = ''
  persistedId.value = ''
  deleting.value = null
  formSessionSerial += 1
  formModal.value = { service: null, session: `create-${formSessionSerial}` }
}

function startEdit(service) {
  actionError.value = ''
  persistedId.value = service.id
  deleting.value = null
  formSessionSerial += 1
  formModal.value = { service, session: `edit-${service.id}-${formSessionSerial}` }
}

function holdRelayFollowUp(saved) {
  if (!errorRequestsRelayInput(saved?.last_error)) return false
  actionError.value = String(saved.last_error)
  persistedId.value = saved.id
  formModal.value = {
    session: formModal.value?.session || `edit-${saved.id}-${++formSessionSerial}`,
    service: saved,
  }
  return true
}

function closeFormModal() {
  formModal.value = null
  persistedId.value = ''
  actionError.value = ''
}

function openDetail(service) {
  actionError.value = ''
  router.replace({ query: { id: service.id } })
}

function closeDetail() {
  actionError.value = ''
  router.replace({ query: {} })
}

function startDelete(service) {
  actionError.value = ''
  deleting.value = service
}

function closeDeleteModal() {
  deleting.value = null
  actionError.value = ''
}

async function copyEntry(service) {
  const text = String(service?.entry_url || '').trim()
  if (!text) {
    messageStore.warning('没有可复制的访问地址')
    return
  }
  const result = await copyText(text)
  if (result.ok) {
    messageStore.success('已复制访问地址')
    return
  }
  messageStore.error(result.error || '复制失败，请手动复制', '复制失败')
}

async function runMutation(work) {
  submitting.value = true
  actionError.value = ''
  try {
    const service = await work()
    if (service?.id) rememberService(service)
    return service
  } catch (error) {
    const saved = accessErrorService(error)
    if (saved?.id) {
      rememberService(saved)
      persistedId.value = saved.id
      if (formModal.value) {
        formModal.value = { ...formModal.value, service: saved }
      }
    }
    actionError.value = accessErrorMessage(error)
    if (errorRequestsRelayInput(actionError.value) && !formModal.value && saved) {
      formSessionSerial += 1
      formModal.value = { service: saved, session: `edit-${saved.id}-${formSessionSerial}` }
    }
    return null
  } finally {
    submitting.value = false
  }
}

async function submitForm({ payload }) {
  const current = formModal.value?.service || null
  const serviceId = current?.id || persistedId.value
  const currentForRoute = current || (serviceId ? { id: serviceId, last_error: actionError.value } : null)
  const action = resolveServiceSubmitAction(currentForRoute, payload)
  if (action === 'blocked_advanced') {
    actionError.value = current?.advanced_reason
      ? `简化转发编辑已禁用。${current.advanced_reason}`
      : '简化转发编辑已禁用。请到高级规则页修改。'
    return
  }
  if (action === 'create') {
    const operation = 'create'
    const key = keyFor(operation, payload)
    const created = await runMutation(() => createAccessService(payload, key))
    if (!created) return
    dropKey(operation)
    if (holdRelayFollowUp(created)) return
    formModal.value = null
    router.replace({ query: { id: created.id } })
    return
  }
  const useCorrect = action === 'correct'
  const operation = `${useCorrect ? 'correct' : 'update'}:${serviceId}`
  const key = keyFor(operation, payload)
  const saved = await runMutation(() => (
    useCorrect
      ? correctAccessService(serviceId, payload, key)
      : updateAccessService(serviceId, payload, key)
  ))
  if (!saved) return
  dropKey(operation)
  if (holdRelayFollowUp(saved)) return
  formModal.value = null
  router.replace({ query: { id: saved.id } })
}

async function refreshSelected() {
  const targetId = selected.value?.id
  if (!targetId || refreshingDetail.value) return
  refreshingDetail.value = true
  actionError.value = ''
  try {
    const fresh = await fetchAccessService(targetId)
    if (fresh?.id) {
      rememberService(fresh)
    }
  } catch (error) {
    if (isNotFoundError(error)) {
      lookedUpId.value = targetId
      missingNotFound.value = true
      forgetService(targetId)
      return
    }
    actionError.value = accessErrorMessage(error)
  } finally {
    refreshingDetail.value = false
  }
}

async function confirmDelete() {
  const service = deleting.value
  if (!service?.id) return
  const operation = `delete:${service.id}`
  const key = keyFor(operation, {})
  const saved = await runMutation(() => deleteAccessService(service.id, key))
  if (!saved) return
  dropKey(operation)
  deleting.value = null
  if (!saved.id) {
    if (selectedId.value === service.id) {
      lookedUpId.value = service.id
      await router.replace({ query: {} })
    }
    forgetService(service.id)
  }
}

async function confirmOpen() {
  if (!selected.value?.id) return
  const body = { confirmed: true }
  const operation = `confirm:${selected.value.id}`
  const key = keyFor(operation, body)
  const saved = await runMutation(() => confirmAccessServiceClient(selected.value.id, body, key))
  if (saved) dropKey(operation)
}

async function retrySelected() {
  if (!selected.value?.id) return
  if (errorRequestsRelayInput(selected.value.last_error)) {
    startEdit(selected.value)
    return
  }
  const operation = `retry:${selected.value.id}`
  const key = keyFor(operation, {})
  const saved = await runMutation(() => retryAccessService(selected.value.id, key))
  if (saved) dropKey(operation)
}

async function retireSelected() {
  if (!selected.value?.id) return
  const operation = `retire:${selected.value.id}`
  const key = keyFor(operation, {})
  const saved = await runMutation(() => retireAccessService(selected.value.id, key))
  if (saved) dropKey(operation)
}

async function abandonSelected() {
  if (!selected.value?.id) return
  const operation = `abandon:${selected.value.id}`
  const key = keyFor(operation, {})
  const saved = await runMutation(() => abandonAccessService(selected.value.id, key))
  if (saved) dropKey(operation)
}
</script>

<style scoped>
.services-page {
  min-width: 0;
}

.services-page__spin {
  animation: services-page-spin 0.9s linear infinite;
}

@keyframes services-page-spin {
  to {
    transform: rotate(360deg);
  }
}

.services-page__alert {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2-5);
  margin-bottom: var(--space-4);
  padding: var(--space-3) var(--space-4);
  border-radius: var(--radius-lg);
  border: 1px solid color-mix(in srgb, var(--color-danger) 28%, transparent);
  background: var(--color-danger-50);
  color: var(--color-danger);
  font-size: var(--text-sm);
}

.services-page__alert-icon {
  flex-shrink: 0;
}

.services-page__alert-text {
  flex: 1 1 12rem;
  min-width: 0;
  overflow-wrap: anywhere;
}

.service-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 22rem), 1fr));
  gap: var(--space-4);
}

@media (prefers-reduced-motion: reduce) {
  .services-page__spin {
    animation: none;
  }
}
</style>

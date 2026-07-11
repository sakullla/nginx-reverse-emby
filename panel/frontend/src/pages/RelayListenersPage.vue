<template>
  <div class='relay-page'>
    <div class='relay-page__header'>
      <div class='relay-page__header-left'>
        <h1 class='relay-page__title'>Relay 监听器</h1>
        <p v-if='hasAgentFilter' class='relay-page__subtitle'>共 {{ listTotal }} 个 · 本页 {{ listeners.length }} 个 · 默认自动签发证书</p>
        <p v-else class='relay-page__subtitle'>暂无可用节点</p>
      </div>
      <div class='relay-page__header-right'>
        <ViewToggle v-if='hasAgentFilter && listTotal > 0' v-model:view='view' />
        <button v-if='canCreate' class='btn btn-primary' @click='showAddForm = true'>
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
            <line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/>
          </svg>
          <span class="btn-text">新建监听器</span>
        </button>
      </div>
    </div>

    <QuickAgentSelect
      :agentId="agentFilter"
      :agents="allAgents"
      @update:agentId="handleAgentSelect"
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
    <div v-else-if='hasAgentFilter && !listeners.length && !isLoading' class='relay-page__empty'>
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <path d="M8 12h8"/><path d="M6 8h12"/><path d="M10 16h4"/><circle cx="4" cy="12" r="2"/><circle cx="20" cy="12" r="2"/>
      </svg>
      <p>暂无 Relay 监听器</p>
      <button v-if='canCreate' class='btn btn-primary' @click='showAddForm = true'>创建第一个监听器</button>
      <p v-else class='relay-page__prompt-hint'>全部节点视图下请先选择具体节点再新建</p>
    </div>

    <!-- Listener card grid -->
    <div v-show='hasAgentFilter && displayListeners.length && view === "card"' class='relay-grid'>
      <RelayCard
        v-for='listener in displayListeners'
        :key='listener.id'
        :listener='listener'
        :traffic='trafficForListener(listener)'
        :agent-node-total='agentNodeTotal'
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
      @edit='startEdit'
      @toggle='toggleListener'
      @delete='startDelete'
    />

    <ListPagination
      v-if="hasAgentFilter && listTotal > 0"
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
      size="xl"
      :close-on-click-modal="false"
      @update:model-value="closeForm"
    >
      <RelayListenerForm :initial-data="editingListener" :agent-id="agentId" @success="closeForm" />
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
  </div>
</template>

<script setup>
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import { useAgent } from '../context/AgentContext'
import { useAgents } from '../hooks/useAgents'
import { useRelayListenersList, useDeleteRelayListener, useUpdateRelayListener } from '../hooks/useRelayListeners'
import { parseIdQuery } from '../hooks/useIdSearch'
import { fetchTrafficSummary } from '../api'
import RelayListenerForm from '../components/RelayListenerForm.vue'
import DeleteConfirmDialog from '../components/DeleteConfirmDialog.vue'
import BaseModal from '../components/base/BaseModal.vue'
import QuickAgentSelect from '../components/QuickAgentSelect.vue'
import RelayCard from '../components/relay/RelayCard.vue'
import ViewToggle from '../components/common/ViewToggle.vue'
import ListPagination from '../components/common/ListPagination.vue'
import RelayTable from '../components/relay/RelayTable.vue'
import { useViewToggle } from '../composables/useViewToggle'
import TrafficTrendModal from '../components/traffic/TrafficTrendModal.vue'
import { summaryBucketForObject } from '../utils/trafficStats.js'
import { isAllAgentsFilter, normalizeAgentFilter } from '../utils/agentFilter.js'

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
const canCreate = computed(() => Boolean(agentId.value))

const page = ref(1)
const pageSize = 20
watch(agentFilter, () => { page.value = 1 })

const { data: listenersPage, isLoading } = useRelayListenersList({
  agentFilter,
  page,
  pageSize,
  enabled: hasAgentFilter
})
const deleteRelayListener = useDeleteRelayListener(agentId)
const updateRelayListener = useUpdateRelayListener(agentId)
const listeners = computed(() => listenersPage.value?.items ?? [])
const listTotal = computed(() => listenersPage.value?.total ?? 0)

// Consume route deep-link search=#id=N (from agent detail / global search).
// Match → filter to that listener; no match / no search → full list (no crash, no redesign).
const displayListeners = computed(() => {
  const idQuery = parseIdQuery(route.query.search)
  if (!idQuery) return listeners.value
  const matched = listeners.value.filter((listener) => String(listener.id) === idQuery.id)
  return matched.length ? matched : listeners.value
})

const trafficStatsEnabled = computed(() => !!systemInfo.value && systemInfo.value.traffic_stats_enabled !== false)
const { data: trafficSummaryData } = useQuery({
  queryKey: ['traffic-summary', agentId],
  queryFn: () => fetchTrafficSummary(agentId.value),
  enabled: () => !!agentId.value && trafficStatsEnabled.value,
  refetchInterval: 10_000
})

const agentNodeTotal = computed(() => trafficSummaryData.value?.used_bytes || 0)

function trafficForListener(listener) {
  return trafficStatsEnabled.value
    ? summaryBucketForObject(trafficSummaryData.value, 'relay_listeners', listener?.id)
    : null
}

const showAddForm = ref(false)
const editingListener = ref(null)
const deletingListener = ref(null)
const deleteError = ref('')

const trendModal = ref({ visible: false, agentId: '', scopeType: '', scopeId: '', scopeLabel: '' })
const trafficDirection = ref('both')

function openTrendModal(listener) {
  const id = listener.agent_id || selectedAgentId?.value
  if (!id) return
  trendModal.value = {
    visible: true,
    agentId: id,
    scopeType: 'relay_listener',
    scopeId: String(listener.id),
    scopeLabel: `Relay 监听 #${listener.id}`
  }
}

function handleAgentSelect(id) {
  router.replace({ query: { ...route.query, agentId: id } })
}

function startEdit(listener) {
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
  updateRelayListener.mutate({ id: listener.id, enabled: !listener.enabled })
}

function confirmDelete() {
  if (!deletingListener.value) return
  deleteRelayListener.mutate(deletingListener.value.id, {
    onSuccess: () => {
      deleteError.value = ''
      deletingListener.value = null
    },
    onError: (err) => {
      deleteError.value = err?.message || '删除失败'
    }
  })
}
</script>

<style scoped>
.relay-page {
  max-width: 1200px;
  margin: 0 auto;
}

.relay-page__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 1.5rem;
  gap: 1rem;
  flex-wrap: wrap;
}

.relay-page__header-left {
  flex: 1;
  min-width: 0;
}

.relay-page__header-right {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  flex-shrink: 0;
}

.relay-page__title {
  font-size: 1.5rem;
  font-weight: 700;
  margin: 0 0 0.25rem;
  color: var(--color-text-primary);
}

.relay-page__subtitle {
  font-size: 0.875rem;
  color: var(--color-text-tertiary);
  margin: 0;
}

.relay-page__prompt,
.relay-page__empty,
.relay-page__loading {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 0.75rem;
  padding: 4rem 2rem;
  color: var(--color-text-muted);
  text-align: center;
}

.relay-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: 1rem;
}

@media (max-width: 640px) {
  .btn-text {
    display: none;
  }
  .relay-grid {
    grid-template-columns: 1fr;
  }
  .relay-page__header {
    margin-bottom: 1rem;
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
</style>

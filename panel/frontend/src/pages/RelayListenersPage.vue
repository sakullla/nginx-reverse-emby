<template>
  <div class='relay-page'>
    <div class='relay-page__header'>
      <div class='relay-page__header-left'>
        <h1 class='relay-page__title'>Relay 监听器</h1>
        <p v-if='agentId' class='relay-page__subtitle'>{{ listeners.length }} 个监听器 · 默认自动签发证书 · 自动 Relay CA + Pin 信任</p>
        <p v-else class='relay-page__subtitle'>请先选择一个节点</p>
      </div>
      <div class='relay-page__header-right'>
        <button v-if='agentId' class='btn btn-primary' @click='showAddForm = true'>
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
            <line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/>
          </svg>
          <span class="btn-text">新建监听器</span>
        </button>
      </div>
    </div>

    <!-- No agent selected -->
    <div v-if='!agentId' class='relay-page__prompt'>
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <path d="M8 12h8"/><path d="M6 8h12"/><path d="M10 16h4"/><circle cx="4" cy="12" r="2"/><circle cx="20" cy="12" r="2"/>
      </svg>
      <p>请选择一个节点来管理 Relay 监听器</p>
      <AgentPicker :agents="allAgents" @select="handleAgentSelect" />
      <p class="relay-page__prompt-hint">或前往节点管理页面添加新节点</p>
      <RouterLink to="/agents" class="btn btn-primary">加入节点</RouterLink>
    </div>

    <!-- Agent selected, no listeners -->
    <div v-else-if='!listeners.length && !isLoading' class='relay-page__empty'>
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <path d="M8 12h8"/><path d="M6 8h12"/><path d="M10 16h4"/><circle cx="4" cy="12" r="2"/><circle cx="20" cy="12" r="2"/>
      </svg>
      <p>暂无 Relay 监听器</p>
      <button class='btn btn-primary' @click='showAddForm = true'>创建第一个监听器</button>
    </div>

    <!-- Listener card grid -->
    <div v-if='agentId && listeners.length' class='relay-grid'>
      <RelayCard
        v-for='listener in listeners'
        :key='listener.id'
        :listener='listener'
        @edit='startEdit'
        @toggle='toggleListener'
        @delete='startDelete'
      />
    </div>

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
  </div>
</template>

<script setup>
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAgent } from '../context/AgentContext'
import { useAgents } from '../hooks/useAgents'
import { useRelayListeners, useDeleteRelayListener, useUpdateRelayListener } from '../hooks/useRelayListeners'
import RelayListenerForm from '../components/relay/RelayListenerForm.vue'
import DeleteConfirmDialog from '../components/dialogs/DeleteConfirmDialog.vue'
import BaseModal from '../components/base/BaseModal.vue'
import AgentPicker from '../components/agents/AgentPicker.vue'
import RelayCard from '../components/relay/RelayCard.vue'

const route = useRoute()
const router = useRouter()
const { selectedAgentId } = useAgent()
const { data: agentsData } = useAgents()
const allAgents = computed(() => agentsData.value ?? [])

const selectedOrRouteAgentId = computed(() => route.query.agentId || selectedAgentId.value)
const registeredAgentIds = computed(() => new Set((agentsData.value || []).map((agent) => String(agent.id))))
const agentId = computed(() => {
  const id = selectedOrRouteAgentId.value
  if (!id) return null
  return registeredAgentIds.value.has(String(id)) ? id : null
})

const { data: listenersData, isLoading } = useRelayListeners(agentId)
const deleteRelayListener = useDeleteRelayListener(agentId)
const updateRelayListener = useUpdateRelayListener(agentId)
const listeners = computed(() => listenersData.value ?? [])

const showAddForm = ref(false)
const editingListener = ref(null)
const deletingListener = ref(null)
const deleteError = ref('')

function handleAgentSelect(agent) {
  router.replace({ query: { ...route.query, agentId: agent.id } })
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
</style>

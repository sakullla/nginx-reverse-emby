<template>
  <div class='relay-page'>
    <div class='relay-page__header'>
      <div>
        <h1 class='relay-page__title'>Relay 监听器</h1>
        <p v-if='agentId' class='relay-page__subtitle'>{{ listeners.length }} 个监听器 · 默认自动签发证书 · 自动 Relay CA + Pin 信任</p>
        <p v-else class='relay-page__subtitle'>请先选择一个节点</p>
      </div>
      <button v-if='agentId' class='btn btn-primary' @click='showAddForm = true'>新建监听器</button>
    </div>

    <template v-if='!agentId'>
      <div class='relay-page__empty'>请在侧栏选择节点后管理 Relay 监听器</div>
    </template>
    <template v-else-if='isLoading'>
      <div class='relay-page__empty'>加载中...</div>
    </template>
    <template v-else-if='!listeners.length'>
      <div class='relay-page__empty'>暂无 Relay 监听器</div>
    </template>
    <template v-else>
      <p v-if='deleteError' class='relay-page__error'>{{ deleteError }}</p>
      <div class='relay-grid'>
        <article v-for='listener in listeners' :key='listener.id' class='relay-card'>
          <div class='relay-card__header'>
            <div>
              <h3 class='relay-card__title'>{{ listener.name }}</h3>
              <p class='relay-card__addr'>{{ listener.listen_host }}:{{ listener.listen_port }}</p>
            </div>
            <div class='relay-card__actions'>
              <button class='icon-btn' @click='startEdit(listener)'>编辑</button>
              <button class='icon-btn icon-btn--danger' @click='startDelete(listener)'>删除</button>
            </div>
          </div>

          <div class='relay-card__meta'>
            <span class='badge'>{{ listener.enabled ? '已启用' : '已禁用' }}</span>
            <span class='badge'>{{ listener.certificate_id ? `证书 #${listener.certificate_id}` : '未绑定证书' }}</span>
            <span class='badge'>{{ trustSummary(listener) }}</span>
            <span v-if='listener.allow_self_signed' class='badge badge--warn'>允许自签</span>
          </div>

          <div class='relay-card__tags'>
            <span v-for='tag in listener.tags || []' :key='tag' class='tag'>{{ tag }}</span>
          </div>
        </article>
      </div>
    </template>

    <Teleport to='body'>
      <div v-if='showAddForm || editingListener' class='modal-overlay'>
        <div class='modal modal--large'>
          <div class='modal__header'>
            <span>{{ editingListener ? '编辑 Relay 监听器' : '新建 Relay 监听器' }}</span>
            <button class='modal__close' @click='closeForm'>✕</button>
          </div>
          <div class='modal__body'>
            <RelayListenerForm :initial-data='editingListener' :agent-id='agentId' @success='closeForm' />
          </div>
        </div>
      </div>
    </Teleport>

    <Teleport to='body'>
      <div v-if='deletingListener' class='modal-overlay' @click.self='deletingListener = null'>
        <div class='modal'>
          <div class='modal__header'>确认删除</div>
          <div class='modal__body'>
            <p>确定删除监听器 <strong>{{ deletingListener.name }}</strong> 吗？</p>
            <p class='relay-page__warning'>若该监听器已被规则引用，删除会被阻止。</p>
          </div>
          <div class='modal__footer'>
            <button class='btn btn-secondary' @click='deletingListener = null'>取消</button>
            <button class='btn btn-danger' @click='confirmDelete'>删除</button>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<script setup>
import { computed, ref } from 'vue'
import { useRoute } from 'vue-router'
import { useAgent } from '../context/AgentContext'
import { useAgents } from '../hooks/useAgents'
import { useRelayListeners, useDeleteRelayListener } from '../hooks/useRelayListeners'
import RelayListenerForm from '../components/RelayListenerForm.vue'

const route = useRoute()
const { selectedAgentId } = useAgent()
const { data: agentsData } = useAgents()

const selectedOrRouteAgentId = computed(() => route.query.agentId || selectedAgentId.value)
const registeredAgentIds = computed(() => new Set((agentsData.value || []).map((agent) => String(agent.id))))
const agentId = computed(() => {
  const id = selectedOrRouteAgentId.value
  if (!id) return null
  return registeredAgentIds.value.has(String(id)) ? id : null
})

const { data: listenersData, isLoading } = useRelayListeners(agentId)
const deleteRelayListener = useDeleteRelayListener(agentId)
const listeners = computed(() => listenersData.value ?? [])

const showAddForm = ref(false)
const editingListener = ref(null)
const deletingListener = ref(null)
const deleteError = ref('')

function trustSummary(listener) {
  if (listener.trust_mode_source === 'auto') return '自动 Relay CA + Pin'
  if (listener.tls_mode === 'pin_and_ca') return 'Pin + CA'
  if (listener.tls_mode === 'pin_only') return '仅 Pin'
  if (listener.tls_mode === 'ca_only') return '仅 CA'
  return '兼容模式'
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

function confirmDelete() {
  if (!deletingListener.value) return
  deleteRelayListener.mutate(deletingListener.value.id, {
    onSuccess: () => {
      deleteError.value = ''
    },
    onError: (err) => {
      deleteError.value = err?.message || '删除失败'
    }
  })
  deletingListener.value = null
}
</script>

<style scoped>
.relay-page {
  max-width: 1200px;
  margin: 0 auto;
}

.relay-page__header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: var(--space-3);
  margin-bottom: var(--space-6);
}

.relay-page__title {
  margin: 0;
  font-size: 1.5rem;
}

.relay-page__subtitle {
  margin: 0;
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
}

.relay-page__empty {
  padding: var(--space-8);
  text-align: center;
  color: var(--color-text-muted);
}

.relay-page__error {
  margin: 0 0 var(--space-4);
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-md);
  background: var(--color-danger-50);
  color: var(--color-danger);
  font-size: var(--text-sm);
}

.relay-page__warning {
  margin: var(--space-3) 0 0;
  font-size: var(--text-sm);
  color: var(--color-text-secondary);
}

.relay-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: var(--space-4);
}

.relay-card {
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  padding: var(--space-4);
  background: var(--color-bg-surface);
}

.relay-card__header {
  display: flex;
  justify-content: space-between;
  gap: var(--space-2);
}

.relay-card__title {
  margin: 0;
  font-size: var(--text-base);
}

.relay-card__addr {
  margin: var(--space-1) 0 0;
  font-size: var(--text-xs);
  color: var(--color-text-muted);
  font-family: var(--font-mono);
}

.relay-card__actions {
  display: flex;
  gap: var(--space-1);
}

.relay-card__meta {
  margin-top: var(--space-3);
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.badge {
  padding: 2px 8px;
  border-radius: var(--radius-full);
  background: var(--color-bg-subtle);
  font-size: var(--text-xs);
}

.badge--warn {
  background: var(--color-warning-50);
  color: var(--color-warning);
}

.relay-card__tags {
  margin-top: var(--space-3);
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.tag {
  font-size: var(--text-xs);
  padding: 2px 8px;
  border-radius: var(--radius-full);
  background: var(--color-primary-subtle);
  color: var(--color-primary);
}

.btn {
  border: none;
  border-radius: var(--radius-md);
  padding: var(--space-2) var(--space-4);
  cursor: pointer;
}

.btn-primary {
  background: var(--gradient-primary);
  color: white;
}

.btn-secondary {
  background: var(--color-bg-subtle);
  border: 1px solid var(--color-border-default);
}

.btn-danger {
  background: var(--color-danger);
  color: white;
}

.icon-btn {
  border: 1px solid var(--color-border-default);
  background: var(--color-bg-surface);
  border-radius: var(--radius-sm);
  padding: 2px 8px;
  font-size: var(--text-xs);
  cursor: pointer;
}

.icon-btn--danger {
  color: var(--color-danger);
}

.modal-overlay {
  position: fixed;
  inset: 0;
  background: rgba(37, 23, 54, 0.4);
  backdrop-filter: blur(8px);
  z-index: var(--z-modal);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--space-4);
}

.modal {
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-3xl);
  width: min(480px, 90vw);
  max-height: calc(100vh - var(--space-8));
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.modal--large {
  width: min(760px, 94vw);
}

.modal__header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: var(--space-5) var(--space-6);
  border-bottom: 1px solid var(--color-border-subtle);
}

.modal__body {
  padding: var(--space-6);
  overflow: auto;
}

.modal__footer {
  padding: var(--space-4) var(--space-6);
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
  border-top: 1px solid var(--color-border-subtle);
}

.modal__close {
  border: none;
  background: transparent;
  cursor: pointer;
}
</style>

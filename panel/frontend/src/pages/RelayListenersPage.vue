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
      <p>请从侧边栏选择一个节点</p>
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
      <article v-for='listener in listeners' :key='listener.id' class='relay-card' :class="{ 'relay-card--disabled': !listener.enabled }">
        <div class='relay-card__header'>
          <div class='relay-card__badges'>
            <span class='relay-card__id'>#{{ listener.id }} · {{ listener.name }}</span>
            <span class='relay-card__status' :class="`relay-card__status--${statusClass(listener)}`">{{ statusLabel(listener) }}</span>
          </div>
          <div class='relay-card__actions'>
            <button class='relay-card__action relay-card__action--toggle' :title="listener.enabled ? '停用' : '启用'" @click='toggleListener(listener)'>
              <svg v-if="listener.enabled" width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><rect x="6" y="4" width="4" height="16" rx="1"/><rect x="14" y="4" width="4" height="16" rx="1"/></svg>
              <svg v-else width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><polygon points="5 3 19 12 5 21 5 3"/></svg>
            </button>
            <button class='relay-card__action' title="编辑" @click='startEdit(listener)'>
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/></svg>
            </button>
            <button class='relay-card__action relay-card__action--delete' title="删除" @click='startDelete(listener)'>
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/></svg>
            </button>
          </div>
        </div>

        <div class='relay-card__mapping'>
          <div class='relay-card__endpoint'>
            <span class='relay-card__url-icon'>
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/><path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/></svg>
            </span>
            <code class='relay-card__addr'>{{ formatPublicEndpoint(listener) }}</code>
          </div>
          <div class='relay-card__endpoint'>
            <span class='relay-card__url-icon'>
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M5 12h14"/><path d="M12 5l7 7-7 7"/></svg>
            </span>
            <code class='relay-card__addr'>{{ formatBindEndpoint(listener) }}</code>
          </div>
        </div>

        <div class='relay-card__meta'>
          <span class='relay-card__badge'>{{ listener.certificate_id ? `证书 #${listener.certificate_id}` : '未绑定证书' }}</span>
          <span class='relay-card__badge'>{{ trustSummary(listener) }}</span>
          <span v-if="listener.allow_self_signed" class='relay-card__badge relay-card__badge--warn'>允许自签</span>
        </div>

        <div v-if="listener.tags?.length" class='relay-card__tags'>
          <span v-for='tag in listener.tags' :key='tag' class='tag'>{{ tag }}</span>
        </div>
      </article>
    </div>

    <!-- Loading -->
    <div v-if='isLoading' class='relay-page__loading'>
      <div class="spinner"></div>
    </div>

    <Teleport to='body'>
      <div v-if='showAddForm || editingListener' class='modal-overlay'>
        <div class='modal modal--large'>
          <div class='modal__header'>
            <span>{{ editingListener ? '编辑 Relay 监听器' : '新建 Relay 监听器' }}</span>
            <button class='modal__close' @click='closeForm'>
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/>
              </svg>
            </button>
          </div>
          <div class='modal__body'>
            <RelayListenerForm :initial-data='editingListener' :agent-id='agentId' @success='closeForm' />
          </div>
        </div>
      </div>
    </Teleport>

    <Teleport to='body'>
      <div v-if='deletingListener' class='modal-overlay' @click.self="deletingListener = null">
        <div class='modal'>
          <div class='modal__header'>
            <span>确认删除</span>
            <button class='modal__close' @click="deletingListener = null">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/>
              </svg>
            </button>
          </div>
          <div class='modal__body'>
            <p>确定删除监听器 <strong>{{ deletingListener.name }}</strong> 吗？</p>
            <p v-if="deleteError" class='relay-page__error'>{{ deleteError }}</p>
            <p class='relay-page__warning'>若该监听器已被规则引用，删除会被阻止。</p>
          </div>
          <div class='modal__footer'>
            <button class='btn btn-secondary' @click="deletingListener = null">取消</button>
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
import { useRelayListeners, useDeleteRelayListener, useUpdateRelayListener } from '../hooks/useRelayListeners'
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
const updateRelayListener = useUpdateRelayListener(agentId)
const listeners = computed(() => listenersData.value ?? [])

const showAddForm = ref(false)
const editingListener = ref(null)
const deletingListener = ref(null)
const deleteError = ref('')

function statusClass(listener) {
  if (!listener.enabled) return 'disabled'
  return 'active'
}

function statusLabel(listener) {
  if (!listener.enabled) return '已禁用'
  return '已启用'
}

function trustSummary(listener) {
  if (listener.trust_mode_source === 'auto') return '自动 Relay CA + Pin'
  if (listener.tls_mode === 'pin_and_ca') return 'Pin + CA'
  if (listener.tls_mode === 'pin_only') return '仅 Pin'
  if (listener.tls_mode === 'ca_only') return '仅 CA'
  return '兼容模式'
}

function normalizePort(port) {
  const value = Number(port)
  return Number.isInteger(value) && value > 0 ? value : null
}

function resolveBindHosts(listener) {
  if (Array.isArray(listener?.bind_hosts) && listener.bind_hosts.length) {
    return listener.bind_hosts
      .map((item) => String(item || '').trim())
      .filter(Boolean)
  }
  const legacyHost = String(listener?.listen_host || '').trim()
  return legacyHost ? [legacyHost] : []
}

function formatPublicEndpoint(listener) {
  const publicHost = String(listener?.public_host || '').trim()
  const bindHosts = resolveBindHosts(listener)
  const host = publicHost || bindHosts[0] || '-'
  const port = normalizePort(listener?.public_port) ?? normalizePort(listener?.listen_port)
  return port ? `${host}:${port}` : host
}

function formatBindEndpoint(listener) {
  const bindHosts = resolveBindHosts(listener)
  const bindLabel = bindHosts.length ? bindHosts.join(', ') : '-'
  const listenPort = normalizePort(listener?.listen_port)
  return listenPort ? `${bindLabel}:${listenPort}` : bindLabel
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

.relay-page__error {
  margin: var(--space-3) 0 0;
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
  gap: 1rem;
}

/* Card styles matching L4 rules */
.relay-card {
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  padding: 1.25rem;
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
  transition: opacity 0.15s;
}

.relay-card--disabled {
  opacity: 0.6;
}

.relay-card__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.relay-card__badges {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}

.relay-card__id {
  font-size: 0.75rem;
  font-family: var(--font-mono);
  color: var(--color-text-tertiary);
}

.relay-card__status {
  font-size: 0.75rem;
  font-weight: 600;
  padding: 2px 8px;
  border-radius: var(--radius-full);
}

.relay-card__status--active {
  background: var(--color-success-50);
  color: var(--color-success);
}

.relay-card__status--disabled {
  background: var(--color-bg-subtle);
  color: var(--color-text-muted);
}

.relay-card__actions {
  display: flex;
  gap: 0.25rem;
  opacity: 0;
  transition: opacity 0.15s;
}

.relay-card:hover .relay-card__actions {
  opacity: 1;
}

.relay-card__action {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: var(--radius-md);
  border: none;
  background: transparent;
  color: var(--color-text-tertiary);
  cursor: pointer;
  transition: all 0.15s;
}

.relay-card__action:hover {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}

.relay-card__action--delete:hover {
  background: var(--color-danger-50);
  color: var(--color-danger);
}

.relay-card__action--toggle:hover {
  background: var(--color-warning-50);
  color: var(--color-warning);
}

.relay-card__mapping {
  display: flex;
  flex-direction: column;
  gap: 0.375rem;
}

.relay-card__endpoint {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  min-width: 0;
}

.relay-card__addr {
  font-family: var(--font-mono);
  font-size: 0.875rem;
  font-weight: 500;
  color: var(--color-text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.relay-card__url-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--color-text-tertiary);
  flex-shrink: 0;
}

.relay-card__meta {
  display: flex;
  gap: 0.25rem;
  flex-wrap: wrap;
}

.relay-card__badge {
  font-size: 0.7rem;
  padding: 1px 6px;
  background: var(--color-bg-subtle);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-sm);
  color: var(--color-text-secondary);
  font-family: var(--font-mono);
}

.relay-card__badge--warn {
  background: var(--color-warning-50);
  color: var(--color-warning);
}

.relay-card__tags {
  display: flex;
  gap: 0.25rem;
  flex-wrap: wrap;
}

.tag {
  font-size: 0.75rem;
  padding: 2px 8px;
  background: var(--color-primary-subtle);
  color: var(--color-primary);
  border-radius: var(--radius-full);
  font-weight: 500;
}

/* Buttons */
.btn {
  padding: 0.5rem 1rem;
  border-radius: var(--radius-lg);
  font-size: 0.875rem;
  font-weight: 500;
  cursor: pointer;
  transition: all 0.15s;
  border: none;
  font-family: inherit;
  display: inline-flex;
  align-items: center;
  gap: 0.375rem;
}

.btn-primary {
  background: var(--gradient-primary);
  color: white;
}

.btn-secondary {
  background: var(--color-bg-subtle);
  color: var(--color-text-primary);
  border: 1px solid var(--color-border-default);
}

.btn-danger {
  background: var(--color-danger);
  color: white;
}

/* Spinner */
.spinner {
  width: 24px;
  height: 24px;
  border: 2px solid var(--color-border-default);
  border-top-color: var(--color-primary);
  border-radius: 50%;
  animation: spin 1s linear infinite;
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}

/* Modals */
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
  box-shadow: var(--shadow-2xl);
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
  align-items: center;
  justify-content: space-between;
  gap: var(--space-4);
  padding: var(--space-5) var(--space-6);
  border-bottom: 1px solid var(--color-border-subtle);
  flex-shrink: 0;
  background: var(--gradient-soft);
  font-weight: 600;
  font-size: var(--text-lg);
  color: var(--color-text-primary);
}

.modal__body {
  padding: var(--space-6);
  overflow-x: hidden;
  overflow-y: auto;
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: var(--space-5);
}

.modal__footer {
  padding: var(--space-4) var(--space-6);
  display: flex;
  justify-content: flex-end;
  gap: var(--space-3);
  border-top: 1px solid var(--color-border-subtle);
  flex-shrink: 0;
}

.modal__close {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  border-radius: var(--radius-full);
  color: var(--color-text-tertiary);
  transition: all var(--duration-normal) var(--ease-bounce);
  flex-shrink: 0;
  border: none;
  background: transparent;
  cursor: pointer;
}

.modal__close:hover {
  background: var(--color-danger-50);
  color: var(--color-danger);
  transform: rotate(90deg);
}

@media (max-width: 640px) {
  .btn-text {
    display: none;
  }
}
</style>

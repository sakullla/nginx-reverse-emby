<template>
  <div class='relay-chain'>
    <!-- 空状态：没有可用监听器 -->
    <div v-if='!listeners.length' class='relay-chain__empty'>
      <div class='relay-chain__empty-icon'>
        <svg width='48' height='48' viewBox='0 0 24 24' fill='none' stroke='currentColor' stroke-width='1.5'>
          <path d='M8 12h8'/>
          <path d='M6 8h12'/>
          <path d='M10 16h4'/>
          <circle cx='4' cy='12' r='2'/>
          <circle cx='20' cy='12' r='2'/>
        </svg>
      </div>
      <p class='relay-chain__empty-title'>暂无可用 Relay 监听器</p>
      <p class='relay-chain__empty-desc'>请先在"Relay 监听器"页面创建监听器</p>
    </div>

    <template v-else>
      <!-- 添加区域 -->
      <div class='relay-chain__add'>
        <div class='relay-chain__select-wrapper'>
          <select
            v-model='pendingId'
            class='relay-chain__select'
            :disabled='disabled || !availableOptions.length'
          >
            <option value=''>{{ availableOptions.length ? '选择要添加的 Relay 监听器...' : '无可用监听器' }}</option>
            <option v-for='listener in availableOptions' :key='listener.id' :value='listener.id'>
              {{ formatListener(listener) }}
            </option>
          </select>
          <svg class='relay-chain__select-icon' width='16' height='16' viewBox='0 0 24 24' fill='none' stroke='currentColor' stroke-width='2'>
            <polyline points='6 9 12 15 18 9'/>
          </svg>
        </div>
        <button
          type='button'
          class='relay-chain__add-btn'
          :disabled='disabled || !pendingId'
          @click='addSelected'
        >
          <svg width='16' height='16' viewBox='0 0 24 24' fill='none' stroke='currentColor' stroke-width='2'>
            <line x1='12' y1='5' x2='12' y2='19'/>
            <line x1='5' y1='12' x2='19' y2='12'/>
          </svg>
          <span>添加</span>
        </button>
      </div>

      <!-- 链路可视化 -->
      <div v-if='selectedListeners.length' class='relay-chain__visualization'>
        <div class='relay-chain__path'>
          <!-- 起点：客户端 -->
          <div class='relay-chain__node relay-chain__node--start'>
            <div class='relay-chain__node-icon'>
              <svg width='16' height='16' viewBox='0 0 24 24' fill='none' stroke='currentColor' stroke-width='2'>
                <rect x='2' y='3' width='20' height='14' rx='2'/>
                <line x1='8' y1='21' x2='16' y2='21'/>
                <line x1='12' y1='17' x2='12' y2='21'/>
              </svg>
            </div>
            <span class='relay-chain__node-label'>客户端</span>
          </div>

          <!-- 连接箭头 -->
          <div class='relay-chain__arrow'>
            <svg width='20' height='20' viewBox='0 0 24 24' fill='none' stroke='currentColor' stroke-width='2'>
              <path d='M5 12h14'/>
              <path d='M12 5l7 7-7 7'/>
            </svg>
          </div>

          <!-- Relay 节点列表 -->
          <template v-for='(listener, index) in selectedListeners' :key='listener.id'>
            <div
              class='relay-chain__node'
              :class='{ "relay-chain__node--disabled": disabled }'
            >
              <div class='relay-chain__node-badge'>{{ index + 1 }}</div>
              <div class='relay-chain__node-icon relay-chain__node-icon--relay'>
                <svg width='16' height='16' viewBox='0 0 24 24' fill='none' stroke='currentColor' stroke-width='2'>
                  <path d='M8 12h8'/>
                  <path d='M6 8h12'/>
                  <path d='M10 16h4'/>
                  <circle cx='4' cy='12' r='2'/>
                  <circle cx='20' cy='12' r='2'/>
                </svg>
              </div>
              <span class='relay-chain__node-label'>{{ formatPublicEndpoint(listener) }}</span>
              <span class='relay-chain__node-meta'>{{ formatListenerSecondary(listener) }}</span>

              <!-- 操作按钮 -->
              <div class='relay-chain__node-actions'>
                <button
                  type='button'
                  class='relay-chain__action-btn'
                  :disabled='disabled || index === 0'
                  @click='moveUp(index)'
                  title='上移'
                >
                  <svg width='14' height='14' viewBox='0 0 24 24' fill='none' stroke='currentColor' stroke-width='2'>
                    <polyline points='18 15 12 9 6 15'/>
                  </svg>
                </button>
                <button
                  type='button'
                  class='relay-chain__action-btn'
                  :disabled='disabled || index === selectedListeners.length - 1'
                  @click='moveDown(index)'
                  title='下移'
                >
                  <svg width='14' height='14' viewBox='0 0 24 24' fill='none' stroke='currentColor' stroke-width='2'>
                    <polyline points='6 9 12 15 18 9'/>
                  </svg>
                </button>
                <button
                  type='button'
                  class='relay-chain__action-btn relay-chain__action-btn--danger'
                  :disabled='disabled'
                  @click='removeAt(index)'
                  title='移除'
                >
                  <svg width='14' height='14' viewBox='0 0 24 24' fill='none' stroke='currentColor' stroke-width='2'>
                    <line x1='18' y1='6' x2='6' y2='18'/>
                    <line x1='6' y1='6' x2='18' y2='18'/>
                  </svg>
                </button>
              </div>
            </div>

            <!-- 连接箭头 -->
            <div class='relay-chain__arrow'>
              <svg width='20' height='20' viewBox='0 0 24 24' fill='none' stroke='currentColor' stroke-width='2'>
                <path d='M5 12h14'/>
                <path d='M12 5l7 7-7 7'/>
              </svg>
            </div>
          </template>

          <!-- 终点：后端服务 -->
          <div class='relay-chain__node relay-chain__node--end'>
            <div class='relay-chain__node-icon'>
              <svg width='16' height='16' viewBox='0 0 24 24' fill='none' stroke='currentColor' stroke-width='2'>
                <rect x='2' y='2' width='20' height='8' rx='2'/>
                <rect x='2' y='14' width='20' height='8' rx='2'/>
                <line x1='6' y1='6' x2='6.01' y2='6'/>
                <line x1='6' y1='18' x2='6.01' y2='18'/>
              </svg>
            </div>
            <span class='relay-chain__node-label'>后端服务</span>
          </div>
        </div>
      </div>

      <!-- 空状态：未配置链路 -->
      <div v-else class='relay-chain__empty-state'>
        <div class='relay-chain__empty-illustration'>
          <svg width='64' height='64' viewBox='0 0 24 24' fill='none' stroke='currentColor' stroke-width='1'>
            <path d='M8 12h8' opacity='0.3'/>
            <path d='M6 8h12' opacity='0.3'/>
            <path d='M10 16h4' opacity='0.3'/>
            <circle cx='4' cy='12' r='2' opacity='0.5'/>
            <circle cx='20' cy='12' r='2' opacity='0.5'/>
            <path d='M12 2v4' opacity='0.3'/>
            <path d='M12 18v4' opacity='0.3'/>
          </svg>
        </div>
        <p class='relay-chain__empty-title'>直接连接模式</p>
        <p class='relay-chain__empty-desc'>未配置 Relay 链路，客户端将直接连接到后端服务</p>
      </div>

      <!-- 统计信息 -->
      <div v-if='selectedListeners.length' class='relay-chain__stats'>
        <div class='relay-chain__stat'>
          <svg width='14' height='14' viewBox='0 0 24 24' fill='none' stroke='currentColor' stroke-width='2'>
            <path d='M13 2L3 14h9l-1 8 10-12h-9l1-8z'/>
          </svg>
          <span>共 {{ selectedListeners.length }} 跳</span>
        </div>
        <div class='relay-chain__stat'>
          <svg width='14' height='14' viewBox='0 0 24 24' fill='none' stroke='currentColor' stroke-width='2'>
            <circle cx='12' cy='12' r='10'/>
            <polyline points='12 6 12 12 16 14'/>
          </svg>
          <span>预计延迟 +{{ selectedListeners.length * 5 }}~{{ selectedListeners.length * 15 }}ms</span>
        </div>
      </div>
    </template>
  </div>
</template>

<script setup>
import { computed, ref } from 'vue'

const props = defineProps({
  modelValue: { type: Array, default: () => [] },
  listeners: { type: Array, default: () => [] },
  disabled: { type: Boolean, default: false }
})

const emit = defineEmits(['update:modelValue'])

const pendingId = ref('')

const selectedIds = computed(() => (props.modelValue || [])
  .map((id) => Number(id))
  .filter((id) => Number.isInteger(id) && id > 0))

const availableOptions = computed(() => {
  const selected = new Set(selectedIds.value)
  return (props.listeners || []).filter((listener) => !selected.has(Number(listener.id)))
})

const selectedListeners = computed(() => {
  const map = new Map((props.listeners || []).map((listener) => [Number(listener.id), listener]))
  return selectedIds.value
    .map((id) => map.get(id) || { id, name: `监听器 ${id}` })
})

function formatListener(listener) {
  const name = listener?.name || `监听器 ${listener?.id}`
  const endpoint = formatPublicEndpoint(listener)
  const agentLabel = String(listener?.agent_name || listener?.agent_id || '').trim()
  return `${agentLabel ? `[${agentLabel}] ` : ''}${name} (${endpoint})`
}

function normalizePort(raw) {
  const value = Number(raw)
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

function formatListenerSecondary(listener) {
  const name = String(listener?.name || `监听器 ${listener?.id}`).trim()
  const bindHosts = resolveBindHosts(listener)
  const bindLabel = bindHosts.length ? bindHosts.join(', ') : '-'
  const listenPort = normalizePort(listener?.listen_port)
  return `${name} · bind ${listenPort ? `${bindLabel}:${listenPort}` : bindLabel}`
}

function updateChain(next) {
  emit('update:modelValue', next)
}

function addSelected() {
  const nextId = Number(pendingId.value)
  if (!Number.isInteger(nextId) || nextId <= 0) return
  updateChain([...selectedIds.value, nextId])
  pendingId.value = ''
}

function moveUp(index) {
  if (index <= 0) return
  const next = [...selectedIds.value]
  const current = next[index]
  next[index] = next[index - 1]
  next[index - 1] = current
  updateChain(next)
}

function moveDown(index) {
  if (index >= selectedIds.value.length - 1) return
  const next = [...selectedIds.value]
  const current = next[index]
  next[index] = next[index + 1]
  next[index + 1] = current
  updateChain(next)
}

function removeAt(index) {
  const next = [...selectedIds.value]
  next.splice(index, 1)
  updateChain(next)
}
</script>

<style scoped>
.relay-chain {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

/* 添加区域 */
.relay-chain__add {
  display: flex;
  gap: var(--space-3);
  align-items: stretch;
}

.relay-chain__select-wrapper {
  position: relative;
  flex: 1;
  min-width: 0;
}

.relay-chain__select {
  width: 100%;
  height: 40px;
  padding: var(--space-2) var(--space-10) var(--space-2) var(--space-3);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  background: var(--color-bg-surface);
  color: var(--color-text-primary);
  font-size: var(--text-sm);
  cursor: pointer;
  appearance: none;
  transition: all var(--duration-fast);
}

.relay-chain__select:focus {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.relay-chain__select:disabled {
  background: var(--color-bg-subtle);
  cursor: not-allowed;
  opacity: 0.7;
}

.relay-chain__select-icon {
  position: absolute;
  right: var(--space-3);
  top: 50%;
  transform: translateY(-50%);
  color: var(--color-text-muted);
  pointer-events: none;
}

.relay-chain__add-btn {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-4);
  border: none;
  border-radius: var(--radius-lg);
  background: var(--gradient-primary);
  color: white;
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  cursor: pointer;
  transition: all var(--duration-fast);
  white-space: nowrap;
}

.relay-chain__add-btn:hover:not(:disabled) {
  opacity: 0.9;
  transform: translateY(-1px);
  box-shadow: var(--shadow-md);
}

.relay-chain__add-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
  background: var(--color-border-strong);
}

/* 链路可视化 */
.relay-chain__visualization {
  background: var(--color-bg-subtle);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-xl);
  padding: var(--space-5);
  overflow-x: auto;
}

.relay-chain__path {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-width: min-content;
}

/* 节点 */
.relay-chain__node {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-1);
  padding: var(--space-3);
  background: var(--color-bg-surface);
  border: 2px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  min-width: 100px;
  position: relative;
  transition: all var(--duration-fast);
}

.relay-chain__node:hover {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-md);
}

.relay-chain__node--disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.relay-chain__node--start,
.relay-chain__node--end {
  background: var(--color-primary-subtle);
  border-color: var(--color-primary);
  border-style: dashed;
}

.relay-chain__node--start .relay-chain__node-icon,
.relay-chain__node--end .relay-chain__node-icon {
  color: var(--color-primary);
}

.relay-chain__node-badge {
  position: absolute;
  top: -8px;
  left: -8px;
  width: 20px;
  height: 20px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--gradient-primary);
  color: white;
  font-size: 10px;
  font-weight: var(--font-bold);
  border-radius: var(--radius-full);
  box-shadow: var(--shadow-sm);
}

.relay-chain__node-icon {
  width: 32px;
  height: 32px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--color-bg-subtle);
  border-radius: var(--radius-md);
  color: var(--color-text-secondary);
}

.relay-chain__node-icon--relay {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
}

.relay-chain__node-label {
  font-size: var(--text-xs);
  font-weight: var(--font-medium);
  color: var(--color-text-primary);
  text-align: center;
  max-width: 120px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.relay-chain__node-meta {
  font-size: 10px;
  color: var(--color-text-muted);
  font-family: var(--font-mono);
}

.relay-chain__node-actions {
  display: flex;
  gap: var(--space-1);
  margin-top: var(--space-1);
  padding-top: var(--space-2);
  border-top: 1px solid var(--color-border-subtle);
}

.relay-chain__action-btn {
  width: 24px;
  height: 24px;
  display: flex;
  align-items: center;
  justify-content: center;
  border: none;
  border-radius: var(--radius-md);
  background: transparent;
  color: var(--color-text-muted);
  cursor: pointer;
  transition: all var(--duration-fast);
}

.relay-chain__action-btn:hover:not(:disabled) {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}

.relay-chain__action-btn:disabled {
  opacity: 0.3;
  cursor: not-allowed;
}

.relay-chain__action-btn--danger:hover:not(:disabled) {
  background: var(--color-danger-50);
  color: var(--color-danger);
}

/* 连接箭头 */
.relay-chain__arrow {
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--color-text-muted);
  flex-shrink: 0;
}

/* 统计信息 */
.relay-chain__stats {
  display: flex;
  gap: var(--space-4);
  padding: var(--space-3) var(--space-4);
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-lg);
}

.relay-chain__stat {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  font-size: var(--text-xs);
  color: var(--color-text-secondary);
}

.relay-chain__stat svg {
  color: var(--color-primary);
}

/* 空状态 */
.relay-chain__empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-3);
  padding: var(--space-8) var(--space-4);
  background: var(--color-bg-subtle);
  border: 2px dashed var(--color-border-default);
  border-radius: var(--radius-xl);
  text-align: center;
}

.relay-chain__empty-icon {
  color: var(--color-text-muted);
  opacity: 0.5;
}

.relay-chain__empty-title {
  margin: 0;
  font-size: var(--text-base);
  font-weight: var(--font-semibold);
  color: var(--color-text-primary);
}

.relay-chain__empty-desc {
  margin: 0;
  font-size: var(--text-sm);
  color: var(--color-text-muted);
}

/* 空状态 - 未配置链路 */
.relay-chain__empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-3);
  padding: var(--space-8) var(--space-4);
  background: var(--color-bg-subtle);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-xl);
  text-align: center;
}

.relay-chain__empty-illustration {
  color: var(--color-text-muted);
}

.relay-chain__empty-state .relay-chain__empty-title {
  color: var(--color-text-secondary);
}

.relay-chain__empty-state .relay-chain__empty-desc {
  max-width: 300px;
}

/* 响应式 */
@media (max-width: 640px) {
  .relay-chain__add {
    flex-direction: column;
  }

  .relay-chain__add-btn {
    width: 100%;
    justify-content: center;
  }

  .relay-chain__visualization {
    padding: var(--space-3);
  }

  .relay-chain__node {
    min-width: 80px;
    padding: var(--space-2);
  }

  .relay-chain__node-label {
    max-width: 80px;
  }
}
</style>

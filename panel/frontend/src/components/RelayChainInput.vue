<template>
  <div class="relay-editor">
    <!-- No listeners available -->
    <div v-if="!listeners.length" class="relay-editor__empty">
      <div class="relay-editor__empty-icon">
        <svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
          <circle cx="12" cy="12" r="10"/>
          <line x1="12" y1="8" x2="12" y2="12"/>
          <line x1="12" y1="16" x2="12.01" y2="16"/>
        </svg>
      </div>
      <p class="relay-editor__empty-title">暂无可用监听器</p>
      <p class="relay-editor__empty-desc">请先在「Relay 监听器」页面创建监听器后再配置链路</p>
    </div>

    <template v-else>
      <!-- Flow visualization -->
      <div class="relay-editor__flow">
        <div class="relay-editor__flow-node relay-editor__flow-node--start">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <rect x="2" y="3" width="20" height="14" rx="2"/>
            <line x1="8" y1="21" x2="16" y2="21"/>
            <line x1="12" y1="17" x2="12" y2="21"/>
          </svg>
          <span>客户端</span>
        </div>

        <div v-for="(layer, i) in displayLayers" :key="`flow-${i}`" class="relay-editor__flow-step">
          <div class="relay-editor__flow-arrow">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M5 12h14"/><path d="M12 5l7 7-7 7"/>
            </svg>
          </div>
          <div class="relay-editor__flow-node" :class="{ 'relay-editor__flow-node--empty': !layer.length }">
            <span class="relay-editor__flow-badge">{{ i + 1 }}</span>
            <span v-if="layer.length" class="relay-editor__flow-label">
              {{ layer.length }} 节点
            </span>
            <span v-else class="relay-editor__flow-label relay-editor__flow-label--muted">空</span>
          </div>
        </div>

        <div class="relay-editor__flow-step">
          <div class="relay-editor__flow-arrow">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M5 12h14"/><path d="M12 5l7 7-7 7"/>
            </svg>
          </div>
          <div class="relay-editor__flow-node relay-editor__flow-node--end">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <rect x="2" y="2" width="20" height="8" rx="2"/>
              <rect x="2" y="14" width="20" height="8" rx="2"/>
            </svg>
            <span>后端</span>
          </div>
        </div>
      </div>

      <!-- Layer cards -->
      <div class="relay-editor__layers">
        <div
          v-for="(layer, layerIndex) in displayLayers"
          :key="layerIndex"
          class="relay-editor__layer"
        >
          <div class="relay-editor__layer-header">
            <div class="relay-editor__layer-title">
              <span class="relay-editor__layer-num">{{ layerIndex + 1 }}</span>
              <span>第 {{ layerIndex + 1 }} 层</span>
              <span v-if="layer.length > 1" class="relay-editor__layer-tag">并行</span>
            </div>
            <div class="relay-editor__layer-actions">
              <button
                type="button"
                class="relay-editor__action"
                :disabled="disabled || layerIndex === 0"
                @click="moveLayerUp(layerIndex)"
                title="上移"
              >
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <polyline points="18 15 12 9 6 15"/>
                </svg>
              </button>
              <button
                type="button"
                class="relay-editor__action"
                :disabled="disabled || layerIndex === displayLayers.length - 1"
                @click="moveLayerDown(layerIndex)"
                title="下移"
              >
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <polyline points="6 9 12 15 18 9"/>
                </svg>
              </button>
              <button
                type="button"
                class="relay-editor__action relay-editor__action--danger"
                :disabled="disabled"
                @click="removeLayer(layerIndex)"
                title="删除"
              >
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/>
                </svg>
              </button>
            </div>
          </div>

          <!-- Nodes in layer -->
          <div class="relay-editor__layer-body">
            <div v-if="layer.length" class="relay-editor__chips">
              <div
                v-for="(listenerId, nodeIndex) in layer"
                :key="`${layerIndex}-${listenerId}-${nodeIndex}`"
                class="relay-editor__chip"
              >
                <span class="relay-editor__chip-id">#{{ listenerId }}</span>
                <span class="relay-editor__chip-name">{{ listenerName(listenerId) }}</span>
                <span class="relay-editor__chip-endpoint">{{ listenerEndpoint(listenerId) }}</span>
                <button
                  type="button"
                  class="relay-editor__chip-remove"
                  :disabled="disabled"
                  @click="removeNode(layerIndex, nodeIndex)"
                >
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
                    <line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/>
                  </svg>
                </button>
              </div>
            </div>
            <div v-else class="relay-editor__layer-hint">
              该层暂无节点，请从下方添加
            </div>

            <!-- Add node dropdown -->
            <div class="relay-editor__add-wrap">
              <div class="relay-editor__add-dropdown" :class="{ 'relay-editor__add-dropdown--open': openDropdownLayer === layerIndex }">
                <button
                  type="button"
                  class="relay-editor__add-btn"
                  :disabled="disabled || !availableForLayer(layerIndex).length"
                  @click="toggleDropdown(layerIndex)"
                >
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
                    <line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/>
                  </svg>
                  <span>{{ availableForLayer(layerIndex).length ? '添加节点' : '无可用节点' }}</span>
                </button>

                <div v-if="openDropdownLayer === layerIndex" class="relay-editor__dropdown-menu">
                  <div class="relay-editor__dropdown-search" @click.stop>
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                      <circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>
                    </svg>
                    <input
                      v-model="layerSearchQueries[layerIndex]"
                      type="text"
                      placeholder="搜索监听器..."
                      class="relay-editor__dropdown-search-input"
                    >
                  </div>
                  <div
                    v-for="listener in filteredAvailableForLayer(layerIndex)"
                    :key="listener.id"
                    class="relay-editor__dropdown-item"
                    @click="addNode(layerIndex, listener.id)"
                  >
                    <span class="relay-editor__dropdown-id">#{{ listener.id }}</span>
                    <span class="relay-editor__dropdown-name">{{ listener.name || `监听器 ${listener.id}` }}</span>
                    <span class="relay-editor__dropdown-endpoint">{{ formatPublicEndpoint(listener) }}</span>
                  </div>
                  <div v-if="!filteredAvailableForLayer(layerIndex).length" class="relay-editor__dropdown-empty">
                    {{ availableForLayer(layerIndex).length ? '无匹配结果' : '该层已包含所有可用监听器' }}
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- Add layer button -->
      <button
        type="button"
        class="relay-editor__add-layer"
        :disabled="disabled || !canAddLayer"
        @click.stop="addLayer"
      >
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
          <line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/>
        </svg>
        <span>添加新层</span>
      </button>

      <!-- Empty state when no layers -->
      <div v-if="!displayLayers.length" class="relay-editor__no-layers">
        <p>点击「添加新层」开始配置 Relay 链路</p>
        <p class="relay-editor__no-layers-sub">客户端流量将按层顺序转发：客户端 → 第 1 层 → 第 2 层 → ... → 后端服务</p>
      </div>

      <!-- Path preview (collapsed by default) -->
      <div v-if="expandedPaths.length" class="relay-editor__preview">
        <button
          type="button"
          class="relay-editor__preview-toggle"
          @click="showPaths = !showPaths"
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" :class="{ 'relay-editor__icon--rotated': showPaths }">
            <polyline points="6 9 12 15 18 9"/>
          </svg>
          <span>展开路径预览</span>
          <span class="relay-editor__preview-count">{{ expandedPaths.length }} 条路径</span>
        </button>
        <div v-if="showPaths" class="relay-editor__paths">
          <div
            v-for="(path, i) in expandedPaths"
            :key="i"
            class="relay-editor__path-row"
          >
            <span class="relay-editor__path-index">{{ i + 1 }}</span>
            <span class="relay-editor__path-chain">
              <span
                v-for="(id, j) in path"
                :key="j"
                class="relay-editor__path-node"
              >
                #{{ id }} {{ listenerName(id) }}
                <span v-if="j < path.length - 1" class="relay-editor__path-arrow">→</span>
              </span>
            </span>
          </div>
        </div>
      </div>
    </template>
  </div>
</template>

<script setup>
import { computed, ref, onMounted, onUnmounted } from 'vue'

const props = defineProps({
  modelValue: { type: Array, default: () => [] },
  listeners: { type: Array, default: () => [] },
  disabled: { type: Boolean, default: false }
})

const emit = defineEmits(['update:modelValue'])

// Raw layers including empty ones (so users can add nodes to them)
const displayLayers = computed(() => {
  const raw = props.modelValue
  if (!Array.isArray(raw)) return []
  return raw.map((layer) =>
    Array.isArray(layer)
      ? layer.map((id) => Number(id)).filter((id) => Number.isInteger(id) && id > 0)
      : []
  )
})

const listenerMap = computed(() => {
  return new Map((props.listeners || []).map((l) => [Number(l.id), l]))
})

const totalNodes = computed(() =>
  displayLayers.value.reduce((sum, layer) => sum + layer.length, 0)
)

// Expand all possible paths through the layers
const expandedPaths = computed(() => {
  const ls = displayLayers.value.filter((layer) => layer.length > 0)
  if (!ls.length) return []
  return ls.reduce((paths, layer) => {
    if (!paths.length) return layer.map((id) => [id])
    const next = []
    for (const path of paths) {
      for (const id of layer) {
        next.push([...path, id])
      }
    }
    return next
  }, [])
})

// IDs used per layer
const usedIdsPerLayer = computed(() =>
  displayLayers.value.map((layer) => new Set(layer.map((id) => Number(id))))
)

// All used IDs globally
const allUsedIds = computed(() => {
  const set = new Set()
  for (const layer of displayLayers.value) {
    for (const id of layer) set.add(Number(id))
  }
  return set
})

const canAddLayer = computed(() => {
  const used = allUsedIds.value
  return (props.listeners || []).some((l) => !used.has(Number(l.id)))
})

// Dropdown state
const openDropdownLayer = ref(-1)
const showPaths = ref(false)
const layerSearchQueries = ref({})

function listenerName(id) {
  const l = listenerMap.value.get(Number(id))
  return l?.name || ''
}

function listenerEndpoint(id) {
  const l = listenerMap.value.get(Number(id))
  return l ? formatPublicEndpoint(l) : ''
}

function formatPublicEndpoint(listener) {
  const publicHost = String(listener?.public_host || '').trim()
  const bindHosts = resolveBindHosts(listener)
  const host = publicHost || bindHosts[0] || '-'
  const port = normalizePort(listener?.public_port) ?? normalizePort(listener?.listen_port)
  return port ? `${host}:${port}` : host
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

function normalizePort(raw) {
  const value = Number(raw)
  return Number.isInteger(value) && value > 0 ? value : null
}

function availableForLayer(layerIndex) {
  // Each listener can only appear once across the entire relay chain
  return (props.listeners || []).filter((l) => !allUsedIds.value.has(Number(l.id)))
}

function filteredAvailableForLayer(layerIndex) {
  const available = availableForLayer(layerIndex)
  const query = String(layerSearchQueries.value[layerIndex] || '').trim().toLowerCase()
  if (!query) return available
  return available.filter((l) => {
    const name = String(l.name || '').toLowerCase()
    const id = String(l.id || '')
    const endpoint = formatPublicEndpoint(l).toLowerCase()
    return name.includes(query) || id.includes(query) || endpoint.includes(query)
  })
}

function updateLayers(next) {
  emit('update:modelValue', next)
}

function addLayer() {
  const nextIndex = displayLayers.value.length
  updateLayers([...displayLayers.value, []])
  openDropdownLayer.value = nextIndex
  layerSearchQueries.value[nextIndex] = ''
}

function removeLayer(index) {
  const next = displayLayers.value.filter((_, i) => i !== index)
  updateLayers(next)
  if (openDropdownLayer.value === index) {
    openDropdownLayer.value = -1
  }
}

function moveLayerUp(index) {
  if (index <= 0) return
  const next = displayLayers.value.map((l) => [...l])
  const tmp = next[index]
  next[index] = next[index - 1]
  next[index - 1] = tmp
  updateLayers(next)
}

function moveLayerDown(index) {
  if (index >= displayLayers.value.length - 1) return
  const next = displayLayers.value.map((l) => [...l])
  const tmp = next[index]
  next[index] = next[index + 1]
  next[index + 1] = tmp
  updateLayers(next)
}

function toggleDropdown(layerIndex) {
  if (openDropdownLayer.value === layerIndex) {
    openDropdownLayer.value = -1
  } else {
    openDropdownLayer.value = layerIndex
    layerSearchQueries.value[layerIndex] = ''
  }
}

function addNode(layerIndex, listenerId) {
  const id = Number(listenerId)
  if (!Number.isInteger(id) || id <= 0) return
  const next = displayLayers.value.map((l) => [...l])
  next[layerIndex] = [...next[layerIndex], id]
  updateLayers(next)
  openDropdownLayer.value = -1
}

function removeNode(layerIndex, nodeIndex) {
  const next = displayLayers.value.map((l) => [...l])
  next[layerIndex] = next[layerIndex].filter((_, i) => i !== nodeIndex)
  updateLayers(next)
}

// Close dropdown when clicking outside
function onDocumentClick(e) {
  if (!e.target.closest('.relay-editor__add-dropdown')) {
    openDropdownLayer.value = -1
  }
}

onMounted(() => {
  document.addEventListener('click', onDocumentClick)
})

onUnmounted(() => {
  document.removeEventListener('click', onDocumentClick)
})
</script>

<style scoped>
.relay-editor {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}

/* Flow visualization */
.relay-editor__flow {
  display: flex;
  align-items: center;
  gap: 0.25rem;
  padding: 0.75rem 1rem;
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  overflow-x: auto;
  box-shadow: var(--shadow-sm);
}

.relay-editor__flow-node {
  display: flex;
  align-items: center;
  gap: 0.375rem;
  padding: 0.375rem 0.625rem;
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  font-size: 0.75rem;
  font-weight: 500;
  color: var(--color-text-primary);
  white-space: nowrap;
  flex-shrink: 0;
  position: relative;
}

.relay-editor__flow-node--start,
.relay-editor__flow-node--end {
  background: var(--color-primary-subtle);
  border-color: var(--color-primary);
  color: var(--color-primary);
}

.relay-editor__flow-node--empty {
  border-style: dashed;
  color: var(--color-text-muted);
}

.relay-editor__flow-badge {
  width: 16px;
  height: 16px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--gradient-primary);
  color: white;
  font-size: 9px;
  font-weight: 700;
  border-radius: var(--radius-full);
}

.relay-editor__flow-label {
  font-size: 0.75rem;
}

.relay-editor__flow-label--muted {
  color: var(--color-text-muted);
}

.relay-editor__flow-arrow {
  display: flex;
  align-items: center;
  color: var(--color-text-muted);
  flex-shrink: 0;
}

.relay-editor__flow-step {
  display: flex;
  align-items: center;
  gap: 0.25rem;
}

/* Layer cards */
.relay-editor__layers {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}

.relay-editor__layer {
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  box-shadow: var(--shadow-sm);
}

.relay-editor__layer-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.625rem 0.875rem;
  background: var(--color-bg-subtle);
  border-bottom: 1px solid var(--color-border-default);
}

.relay-editor__layer-title {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  font-weight: 600;
  font-size: 0.8125rem;
  color: var(--color-text-primary);
}

.relay-editor__layer-num {
  width: 20px;
  height: 20px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--gradient-primary);
  color: white;
  font-size: 10px;
  font-weight: 700;
  border-radius: var(--radius-full);
}

.relay-editor__layer-tag {
  font-size: 10px;
  font-weight: 600;
  color: var(--color-primary);
  background: var(--color-primary-subtle);
  padding: 1px 6px;
  border-radius: var(--radius-full);
}

.relay-editor__layer-actions {
  display: flex;
  gap: 0.25rem;
}

.relay-editor__action {
  width: 26px;
  height: 26px;
  display: flex;
  align-items: center;
  justify-content: center;
  border: none;
  border-radius: var(--radius-md);
  background: transparent;
  color: var(--color-text-muted);
  cursor: pointer;
  transition: all 0.15s;
  padding: 0;
}

.relay-editor__action:hover:not(:disabled) {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}

.relay-editor__action:disabled {
  opacity: 0.3;
  cursor: not-allowed;
}

.relay-editor__action--danger:hover:not(:disabled) {
  background: var(--color-danger-50);
  color: var(--color-danger);
}

.relay-editor__layer-body {
  padding: 0.75rem;
  display: flex;
  flex-direction: column;
  gap: 0.625rem;
}

.relay-editor__layer-hint {
  font-size: 0.8125rem;
  color: var(--color-text-muted);
  text-align: center;
  padding: 0.5rem;
  background: var(--color-bg-subtle);
  border-radius: var(--radius-lg);
  border: 1px dashed var(--color-border-default);
}

/* Chips */
.relay-editor__chips {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
}

.relay-editor__chip {
  display: flex;
  align-items: center;
  gap: 0.375rem;
  padding: 0.375rem 0.625rem;
  background: var(--color-primary-subtle);
  border: 1px solid var(--color-primary-200);
  border-radius: var(--radius-lg);
  font-size: 0.8125rem;
}

.relay-editor__chip-id {
  font-weight: 700;
  color: var(--color-primary);
  font-size: 0.75rem;
}

.relay-editor__chip-name {
  font-weight: 600;
  color: var(--color-text-primary);
}

.relay-editor__chip-endpoint {
  font-size: 0.75rem;
  color: var(--color-text-secondary);
  font-family: var(--font-mono);
}

.relay-editor__chip-remove {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  border: none;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--color-primary);
  cursor: pointer;
  padding: 0;
  margin-left: 0.125rem;
}

.relay-editor__chip-remove:hover {
  background: var(--color-primary);
  color: white;
}

/* Add node dropdown */
.relay-editor__add-wrap {
  position: relative;
}

.relay-editor__add-dropdown {
  position: relative;
  display: inline-block;
}

.relay-editor__add-btn {
  display: inline-flex;
  align-items: center;
  gap: 0.375rem;
  padding: 0.375rem 0.75rem;
  border: 1.5px dashed var(--color-border-default);
  border-radius: var(--radius-lg);
  background: var(--color-bg-surface);
  color: var(--color-text-secondary);
  font-size: 0.8125rem;
  font-weight: 500;
  cursor: pointer;
  transition: all 0.15s;
  font-family: inherit;
}

.relay-editor__add-btn:hover:not(:disabled) {
  border-color: var(--color-primary);
  color: var(--color-primary);
  background: var(--color-primary-subtle);
}

.relay-editor__add-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.relay-editor__dropdown-menu {
  position: absolute;
  top: calc(100% + 4px);
  left: 0;
  z-index: 50;
  min-width: 260px;
  max-height: 280px;
  overflow-y: auto;
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-lg);
  padding: 0.25rem;
}

.relay-editor__dropdown-search {
  display: flex;
  align-items: center;
  gap: 0.375rem;
  padding: 0.375rem 0.5rem;
  margin-bottom: 0.25rem;
  border-bottom: 1px solid var(--color-border-default);
  position: sticky;
  top: 0;
  background: var(--color-bg-surface);
  z-index: 1;
}

.relay-editor__dropdown-search svg {
  color: var(--color-text-muted);
  flex-shrink: 0;
}

.relay-editor__dropdown-search-input {
  flex: 1;
  min-width: 0;
  padding: 0.25rem 0;
  border: none;
  background: transparent;
  font-size: 0.8125rem;
  color: var(--color-text-primary);
  outline: none;
  font-family: inherit;
}

.relay-editor__dropdown-search-input::placeholder {
  color: var(--color-text-muted);
}

.relay-editor__dropdown-item {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.5rem 0.625rem;
  border-radius: var(--radius-md);
  cursor: pointer;
  font-size: 0.8125rem;
  transition: background 0.1s;
}

.relay-editor__dropdown-item:hover {
  background: var(--color-bg-hover);
}

.relay-editor__dropdown-id {
  font-weight: 700;
  color: var(--color-primary);
  font-size: 0.75rem;
  flex-shrink: 0;
}

.relay-editor__dropdown-name {
  font-weight: 500;
  color: var(--color-text-primary);
  flex-shrink: 0;
}

.relay-editor__dropdown-endpoint {
  font-size: 0.75rem;
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  margin-left: auto;
}

.relay-editor__dropdown-empty {
  padding: 0.75rem;
  text-align: center;
  font-size: 0.8125rem;
  color: var(--color-text-muted);
}

/* Add layer button */
.relay-editor__add-layer {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 0.5rem;
  width: 100%;
  padding: 0.625rem;
  border: 1.5px dashed var(--color-border-default);
  border-radius: var(--radius-xl);
  background: var(--color-bg-surface);
  color: var(--color-text-secondary);
  font-size: 0.875rem;
  font-weight: 500;
  cursor: pointer;
  transition: all 0.15s;
  font-family: inherit;
}

.relay-editor__add-layer:hover:not(:disabled) {
  border-color: var(--color-primary);
  color: var(--color-primary);
  background: var(--color-primary-subtle);
}

.relay-editor__add-layer:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

/* No layers state */
.relay-editor__no-layers {
  text-align: center;
  padding: 1.5rem 1rem;
  color: var(--color-text-muted);
  font-size: 0.875rem;
}

.relay-editor__no-layers-sub {
  font-size: 0.8125rem;
  margin-top: 0.25rem;
  opacity: 0.7;
}

/* Path preview */
.relay-editor__preview {
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  overflow: hidden;
  box-shadow: var(--shadow-sm);
}

.relay-editor__preview-toggle {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  width: 100%;
  padding: 0.625rem 0.875rem;
  border: none;
  background: var(--color-bg-subtle);
  color: var(--color-text-secondary);
  font-size: 0.8125rem;
  font-weight: 500;
  cursor: pointer;
  font-family: inherit;
  text-align: left;
}

.relay-editor__preview-toggle:hover {
  background: var(--color-bg-hover);
}

.relay-editor__preview-count {
  margin-left: auto;
  font-size: 0.75rem;
  color: var(--color-text-tertiary);
  background: var(--color-bg-hover);
  padding: 1px 8px;
  border-radius: var(--radius-full);
}

.relay-editor__icon--rotated {
  transform: rotate(180deg);
}

.relay-editor__paths {
  padding: 0.5rem 0.75rem;
  display: flex;
  flex-direction: column;
  gap: 0.375rem;
  max-height: 200px;
  overflow-y: auto;
}

.relay-editor__path-row {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.375rem 0.5rem;
  background: var(--color-bg-subtle);
  border-radius: var(--radius-md);
  font-size: 0.8125rem;
}

.relay-editor__path-index {
  width: 20px;
  height: 20px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--color-bg-hover);
  border-radius: var(--radius-full);
  font-size: 10px;
  font-weight: 700;
  color: var(--color-text-secondary);
  flex-shrink: 0;
}

.relay-editor__path-chain {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 0.25rem;
}

.relay-editor__path-node {
  display: flex;
  align-items: center;
  gap: 0.25rem;
  font-family: var(--font-mono);
  font-size: 0.75rem;
  color: var(--color-text-primary);
}

.relay-editor__path-arrow {
  color: var(--color-text-muted);
}

/* Empty state - no listeners */
.relay-editor__empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 0.75rem;
  padding: 2.5rem 1rem;
  background: var(--color-bg-surface);
  border: 1.5px dashed var(--color-border-default);
  border-radius: var(--radius-xl);
  text-align: center;
}

.relay-editor__empty-icon {
  color: var(--color-text-muted);
  opacity: 0.5;
}

.relay-editor__empty-title {
  margin: 0;
  font-size: 0.9375rem;
  font-weight: 600;
  color: var(--color-text-primary);
}

.relay-editor__empty-desc {
  margin: 0;
  font-size: 0.8125rem;
  color: var(--color-text-muted);
}
</style>

<template>
  <div
    class="rule-card"
    :class="{ 'rule-card--disabled': !rule.enabled }"
  >
    <!-- Gradient top accent bar -->
    <div class="rule-card__accent" :class="`rule-card__accent--${effectiveStatus}`"></div>

    <div class="rule-card__body">
      <!-- Header: Status & Actions -->
      <div class="rule-card__header">
        <div class="rule-card__status">
          <span class="rule-card__id">#{{ rule.id }}</span>
          <span class="rule-card__status-dot" :class="`rule-card__status-dot--${effectiveStatus}`"></span>
          <span class="rule-card__status-text" :class="`rule-card__status-text--${effectiveStatus}`">{{ statusLabel }}</span>
        </div>
        <div class="rule-card__actions">
          <button
            class="rule-card__action"
            :class="rule.enabled ? 'rule-card__action--pause' : 'rule-card__action--play'"
            @click="toggleStatus"
            :title="rule.enabled ? '停用' : '启用'"
          >
            <svg v-if="rule.enabled" width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
              <rect x="6" y="4" width="4" height="16" rx="1"/>
              <rect x="14" y="4" width="4" height="16" rx="1"/>
            </svg>
            <svg v-else width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
              <polygon points="5 3 19 12 5 21 5 3"/>
            </svg>
          </button>
          <button
            class="rule-card__action rule-card__action--edit"
            @click="$emit('edit', rule)"
            title="编辑"
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
              <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
            </svg>
          </button>
          <button
            class="rule-card__action rule-card__action--delete"
            @click="$emit('delete', rule)"
            title="删除"
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <polyline points="3 6 5 6 21 6"/>
              <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
            </svg>
          </button>
        </div>
      </div>

      <!-- Mapping: Listen -> Upstream -->
      <div class="rule-card__mapping">
        <div class="rule-card__endpoint">
          <div class="rule-card__endpoint-label">
            <span class="rule-card__protocol" :class="`rule-card__protocol--${rule.protocol}`">{{ rule.protocol.toUpperCase() }}</span>
            监听地址
          </div>
          <div class="rule-card__endpoint-value">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <rect x="2" y="2" width="20" height="8" rx="2" ry="2"/>
              <rect x="2" y="14" width="20" height="8" rx="2" ry="2"/>
              <line x1="6" y1="6" x2="6.01" y2="6"/>
              <line x1="6" y1="18" x2="6.01" y2="18"/>
            </svg>
            <code>{{ rule.listen_host }}:{{ rule.listen_port }}</code>
          </div>
        </div>
        <div class="rule-card__arrow">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <line x1="5" y1="12" x2="19" y2="12"/>
            <polyline points="12 5 19 12 12 19"/>
          </svg>
        </div>
        <div class="rule-card__endpoint">
          <div class="rule-card__endpoint-label">
            上游目标
            <span v-if="hasMultipleBackends" class="rule-card__backend-badge">
              {{ backendCount }}个后端
            </span>
            <span class="rule-card__lb-badge" :title="loadBalancingTitle">
              {{ loadBalancingLabel }}
            </span>
          </div>
          <div class="rule-card__endpoint-value">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <circle cx="12" cy="12" r="10"/>
              <line x1="2" y1="12" x2="22" y2="12"/>
              <path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/>
            </svg>
            <code v-if="!hasMultipleBackends">{{ primaryBackend }}</code>
            <code v-else class="rule-card__backends-summary" :title="backendsTooltip">
              {{ primaryBackend }} +{{ backendCount - 1 }}
            </code>
          </div>
        </div>
      </div>
    </div>

    <!-- Footer: Tags -->
    <div v-if="rule.tags?.length" class="rule-card__footer">
      <span v-for="tag in rule.tags" :key="tag" class="rule-card__tag">
        {{ tag }}
      </span>
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { useRuleStore } from '../stores/rules'

const props = defineProps({
  rule: { type: Object, required: true }
})

const emit = defineEmits(['edit', 'delete'])

const ruleStore = useRuleStore()

// L4 rules use simple enabled/disabled status (no revision tracking)
const effectiveStatus = computed(() => props.rule.enabled ? 'active' : 'disabled')

const statusLabel = computed(() => ({
  active: '已启用',
  disabled: '已停用'
}[effectiveStatus.value]))

// Multi-backend support
const backends = computed(() => {
  if (Array.isArray(props.rule.backends) && props.rule.backends.length > 0) {
    return props.rule.backends
  }
  // Fallback to legacy single upstream
  if (props.rule.upstream_host && props.rule.upstream_port) {
    return [{ host: props.rule.upstream_host, port: props.rule.upstream_port, weight: 1 }]
  }
  return []
})

const backendCount = computed(() => backends.value.length)
const hasMultipleBackends = computed(() => backendCount.value > 1)
const primaryBackend = computed(() => {
  const b = backends.value[0]
  return b ? `${b.host}:${b.port}` : '-'
})

const backendsTooltip = computed(() => {
  return backends.value.map((b, i) => `${i + 1}. ${b.host}:${b.port}${b.weight > 1 ? ` (权重${b.weight})` : ''}`).join('\n')
})

// Load balancing
const lbStrategy = computed(() => props.rule.load_balancing?.strategy || 'round_robin')

const loadBalancingLabel = computed(() => ({
  round_robin: 'RR',
  least_conn: 'LC',
  random: 'RND',
  hash: 'HASH'
}[lbStrategy.value] || 'RR'))

const loadBalancingTitle = computed(() => {
  const titles = {
    round_robin: '轮询 (Round Robin)',
    least_conn: '最少连接 (Least Connections)',
    random: '随机 (Random)',
    hash: '哈希 (Hash)'
  }
  return titles[lbStrategy.value] || '轮询'
})

const toggleStatus = async () => {
  try {
    await ruleStore.toggleL4Rule(props.rule.id, !props.rule.enabled)
  } catch (err) {
    // Error handled by store
  }
}
</script>

<style scoped>
.rule-card {
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-2xl);
  overflow: hidden;
  transition: border-color var(--duration-normal) var(--ease-default),
              box-shadow var(--duration-normal) var(--ease-default);
  backdrop-filter: blur(12px);
  position: relative;
}

.rule-card:hover {
  border-color: var(--color-border-strong);
  box-shadow: var(--shadow-md);
}

.rule-card--disabled {
  opacity: 0.65;
}

.rule-card--disabled:hover {
  transform: none;
  box-shadow: none;
}

/* Top Accent Bar */
.rule-card__accent {
  height: 3px;
  transition: opacity var(--duration-normal) var(--ease-default);
}

.rule-card__accent--active {
  background: var(--gradient-primary);
}

.rule-card__accent--disabled {
  background: var(--color-border-default);
}

/* Body */
.rule-card__body {
  padding: var(--space-4);
}

/* Header */
.rule-card__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: var(--space-3);
}

.rule-card__status {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.rule-card__id {
  font-size: 10px;
  font-weight: var(--font-semibold);
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  background: var(--color-bg-subtle);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-sm);
  padding: 1px 5px;
  letter-spacing: 0.02em;
  flex-shrink: 0;
}

.rule-card__status-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
}

.rule-card__status-dot--active {
  background: var(--color-success);
  box-shadow: 0 0 0 3px var(--color-success-50);
  animation: pulse 2s ease-in-out infinite;
}

.rule-card__status-dot--disabled {
  background: var(--color-text-muted);
}

.rule-card__status-text {
  font-size: var(--text-xs);
  font-weight: var(--font-semibold);
  color: var(--color-text-secondary);
}

.rule-card__status-text--active {
  color: var(--color-success);
}

.rule-card__status-text--disabled {
  color: var(--color-text-muted);
}

.rule-card__actions {
  display: flex;
  align-items: center;
  gap: var(--space-1);
}

.rule-card__action {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: all var(--duration-normal) var(--ease-default);
  border: none;
  background: transparent;
  color: var(--color-text-muted);
}

.rule-card__action:hover {
  background: var(--color-bg-hover);
}

.rule-card__action--pause:hover {
  color: var(--color-warning);
  background: var(--color-warning-50);
}

.rule-card__action--play:hover {
  color: var(--color-success);
  background: var(--color-success-50);
}

.rule-card__action--edit:hover {
  color: var(--color-primary);
  background: var(--color-primary-subtle);
}

.rule-card__action--delete:hover {
  color: var(--color-danger);
  background: var(--color-danger-50);
}

/* Mapping */
.rule-card__mapping {
  display: flex;
  align-items: center;
  gap: var(--space-3);
}

.rule-card__endpoint {
  flex: 1;
  min-width: 0;
}

.rule-card__endpoint-label {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  font-size: var(--text-xs);
  font-weight: var(--font-medium);
  color: var(--color-text-muted);
  margin-bottom: var(--space-1-5);
}

.rule-card__protocol {
  font-size: 9px;
  font-weight: var(--font-bold);
  padding: 1px 5px;
  border-radius: var(--radius-sm);
  font-family: var(--font-mono);
}

.rule-card__protocol--tcp {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
}

.rule-card__protocol--udp {
  background: var(--color-warning-50);
  color: var(--color-warning);
}

.rule-card__endpoint-value {
  display: flex;
  align-items: flex-start;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  background: var(--color-bg-subtle);
  border-radius: var(--radius-lg);
  border: 1px solid var(--color-border-subtle);
  min-height: 40px;
}

.rule-card__endpoint-value svg {
  color: var(--color-text-muted);
  flex-shrink: 0;
  margin-top: 2px;
}

.rule-card__endpoint-value code {
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  color: var(--color-text-primary);
  overflow-wrap: break-word;
  word-break: break-word;
  line-height: 1.5;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.rule-card__arrow {
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--color-primary);
  flex-shrink: 0;
  margin-top: var(--space-5);
  opacity: 0.7;
}

/* Footer */
.rule-card__footer {
  display: flex;
  gap: var(--space-2);
  flex-wrap: wrap;
  padding: var(--space-3) var(--space-4);
  border-top: 1px solid var(--color-border-subtle);
  background: var(--color-bg-subtle);
}

.rule-card__tag {
  font-size: var(--text-xs);
  font-weight: var(--font-medium);
  padding: var(--space-1) var(--space-2-5);
  background: var(--color-bg-surface);
  color: var(--color-primary);
  border-radius: var(--radius-full);
  border: 1px solid var(--color-border-default);
}

/* Backend badges */
.rule-card__backend-badge {
  font-size: 9px;
  font-weight: var(--font-bold);
  padding: 1px 5px;
  background: var(--color-success-50);
  color: var(--color-success);
  border-radius: var(--radius-sm);
  margin-left: var(--space-2);
}

.rule-card__lb-badge {
  font-size: 9px;
  font-weight: var(--font-bold);
  padding: 1px 5px;
  background: var(--color-primary-subtle);
  color: var(--color-primary);
  border-radius: var(--radius-sm);
  margin-left: var(--space-2);
  cursor: help;
}

.rule-card__backends-summary {
  cursor: help;
}

@keyframes pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.6; }
}
</style>

<template>
  <div class="attention-bar" :class="{ 'attention-bar--alert': signals.length > 0 }">
    <template v-if="signals.length > 0">
      <span class="attention-bar__label">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/>
          <line x1="12" y1="9" x2="12" y2="13"/>
          <line x1="12" y1="17" x2="12.01" y2="17"/>
        </svg>
        需关注
      </span>
      <div class="attention-bar__chips">
        <RouterLink
          v-for="signal in signals"
          :key="signal.key"
          :to="signal.to"
          class="attention-bar__chip"
          :data-testid="`attention-${signal.key}`"
        >
          <span class="attention-bar__chip-count">{{ signal.count }}</span>
          <span class="attention-bar__chip-text">{{ signal.label }}</span>
          <span class="attention-bar__chip-arrow" aria-hidden="true">→</span>
        </RouterLink>
      </div>
    </template>
    <template v-else-if="attention">
      <span class="attention-bar__ok" data-testid="attention-ok">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M22 11.08V12a10 10 0 1 1-5.93-9.14"/>
          <path d="M22 4 12 14.01l-3-3"/>
        </svg>
        一切正常,没有需要关注的异常
      </span>
    </template>
    <template v-else-if="error">
      <span class="attention-bar__error" data-testid="attention-error" role="alert">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <circle cx="12" cy="12" r="10"/>
          <line x1="12" y1="8" x2="12" y2="12"/>
          <line x1="12" y1="16" x2="12.01" y2="16"/>
        </svg>
        集群状态获取失败
      </span>
      <button type="button" class="attention-bar__retry" data-testid="attention-retry" @click="emit('retry')">重试</button>
    </template>
    <template v-else>
      <span class="attention-bar__loading">检查集群状态…</span>
    </template>
  </div>
</template>

<script setup>
import { computed } from 'vue'

const props = defineProps({
  attention: { type: Object, default: null },
  error: { type: Boolean, default: false }
})

const emit = defineEmits(['retry'])

function agentTarget(group) {
  const ids = group?.agent_ids || []
  return ids.length === 1 ? `/agents/${ids[0]}` : '/agents'
}

const signals = computed(() => {
  const a = props.attention
  if (!a) return []
  const list = []
  if (a.offline?.count > 0) {
    list.push({ key: 'offline', count: a.offline.count, label: '个节点离线', to: '/agents?status=offline' })
  }
  if (a.blocked?.count > 0) {
    list.push({ key: 'blocked', count: a.blocked.count, label: '个节点流量阻断', to: agentTarget(a.blocked) })
  }
  if (a.expiring_certs?.count > 0) {
    list.push({ key: 'certs', count: a.expiring_certs.count, label: '个证书即将过期', to: '/certs' })
  }
  if (a.sync_failed?.count > 0) {
    list.push({ key: 'sync', count: a.sync_failed.count, label: '个节点同步失败', to: agentTarget(a.sync_failed) })
  }
  return list
})
</script>

<style scoped>
.attention-bar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-3);
  padding: var(--space-2-5) var(--space-3) var(--space-2-5) var(--space-4);
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-xl);
  box-shadow: var(--shadow-xs);
  min-height: 52px;
  box-sizing: border-box;
}

.attention-bar--alert {
  border-color: color-mix(in srgb, var(--color-warning) 28%, var(--color-border-subtle));
  background:
    linear-gradient(90deg, var(--color-warning-50), transparent 45%),
    var(--color-bg-surface);
}

.attention-bar__label {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  font-size: var(--text-sm);
  font-weight: var(--font-semibold);
  color: var(--color-text-primary);
  flex-shrink: 0;
}

.attention-bar__label svg {
  padding: 5px;
  box-sizing: content-box;
  border-radius: var(--radius-md);
  background: var(--color-warning-subtle);
  color: var(--color-warning);
}

.attention-bar__chips {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  min-width: 0;
}

.attention-bar__chip {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1-5);
  height: 1.875rem;
  padding: 0 var(--space-2-5) 0 var(--space-1);
  border-radius: var(--radius-md);
  background: var(--color-bg-surface);
  color: var(--color-text-secondary);
  border: 1px solid var(--color-border-default);
  box-shadow: var(--shadow-xs);
  font-size: var(--text-xs);
  font-weight: var(--font-medium);
  text-decoration: none;
  box-sizing: border-box;
  transition: border-color var(--duration-fast) var(--ease-default),
    color var(--duration-fast) var(--ease-default);
}

.attention-bar__chip:hover {
  border-color: var(--color-border-strong);
  color: var(--color-text-primary);
}

.attention-bar__chip-count {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 1.375rem;
  height: 1.375rem;
  padding: 0 0.3rem;
  box-sizing: border-box;
  border-radius: var(--radius-sm);
  background: var(--color-danger-subtle);
  color: var(--color-danger);
  font-size: var(--text-xs);
  font-weight: var(--font-semibold);
  font-variant-numeric: tabular-nums;
}

.attention-bar__chip-arrow {
  color: var(--color-text-muted);
  transition: transform var(--duration-fast) var(--ease-default);
}

.attention-bar__chip:hover .attention-bar__chip-arrow {
  transform: translateX(2px);
}

.attention-bar__ok {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  font-size: var(--text-sm);
  font-weight: 500;
  color: color-mix(in srgb, var(--color-success, #34d399) 75%, var(--color-text-primary));
}

.attention-bar__loading {
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
}

.attention-bar__error {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  font-size: var(--text-sm);
  font-weight: 500;
  color: var(--color-danger, #ef4444);
}

.attention-bar__retry {
  min-height: 40px;
  padding: var(--space-1) var(--space-3);
  border-radius: var(--radius-md);
  border: 1px solid color-mix(in srgb, var(--color-danger, #ef4444) 35%, var(--color-border-default));
  background: transparent;
  color: var(--color-danger, #ef4444);
  font-size: var(--text-xs);
  font-weight: 600;
  font-family: inherit;
  cursor: pointer;
  transition: background var(--duration-fast) var(--ease-default);
}

.attention-bar__retry:hover {
  background: color-mix(in srgb, var(--color-danger, #ef4444) 8%, transparent);
}

.attention-bar__retry:focus-visible {
  outline: none;
  box-shadow: var(--shadow-focus);
}

@media (max-width: 640px) {
  .attention-bar {
    align-items: flex-start;
    flex-direction: column;
    gap: var(--space-2);
  }
}
</style>

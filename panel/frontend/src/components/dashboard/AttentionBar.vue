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
    <template v-else>
      <span class="attention-bar__loading">检查集群状态…</span>
    </template>
  </div>
</template>

<script setup>
import { computed } from 'vue'

const props = defineProps({
  attention: { type: Object, default: null }
})

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
  padding: var(--space-3) var(--space-4);
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-2xl);
  box-shadow: var(--shadow-xs);
  min-height: 52px;
}

.attention-bar--alert {
  border-color: color-mix(in srgb, var(--color-danger, #ef4444) 35%, var(--color-border-subtle));
  background: color-mix(in srgb, var(--color-danger, #ef4444) 4%, var(--color-bg-surface));
}

.attention-bar__label {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  font-size: var(--text-sm);
  font-weight: 600;
  color: var(--color-danger, #ef4444);
  flex-shrink: 0;
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
  gap: 0.35rem;
  padding: 0.3rem 0.7rem;
  border-radius: var(--radius-full);
  background: color-mix(in srgb, var(--color-danger, #ef4444) 10%, transparent);
  color: var(--color-danger, #ef4444);
  border: 1px solid color-mix(in srgb, var(--color-danger, #ef4444) 25%, transparent);
  font-size: var(--text-xs);
  font-weight: 600;
  text-decoration: none;
  transition: background var(--duration-fast) var(--ease-default),
    transform var(--duration-fast) var(--ease-default);
}

.attention-bar__chip:hover {
  background: color-mix(in srgb, var(--color-danger, #ef4444) 18%, transparent);
  transform: translateY(-1px);
}

.attention-bar__chip-count {
  font-size: var(--text-sm);
  font-variant-numeric: tabular-nums;
}

.attention-bar__chip-arrow {
  opacity: 0.7;
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

@media (max-width: 640px) {
  .attention-bar {
    align-items: flex-start;
    flex-direction: column;
    gap: var(--space-2);
  }
}
</style>

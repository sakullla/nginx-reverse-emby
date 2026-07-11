<template>
  <div class="traffic-history-manager">
    <div class="traffic-history-manager__summary">
      <div class="traffic-history-manager__summary-title">当前保留策略</div>
      <div class="traffic-history-manager__summary-body">
        <span class="traffic-history-manager__chip">小时 {{ policy.hourly_retention_days }} 天</span>
        <span class="traffic-history-manager__chip">日 {{ policy.daily_retention_months }} 个月</span>
        <span class="traffic-history-manager__chip">月 {{ policy.monthly_retention_months ?? '—' }} 个月</span>
      </div>
    </div>
    <div class="traffic-history-manager__actions">
      <div class="traffic-history-manager__action-group" data-testid="traffic-history-actions-main">
        <button class="btn btn-secondary" type="button" :disabled="calibrating" @click="$emit('calibrate')">校准为指定值</button>
        <button class="btn btn-secondary" type="button" :disabled="calibrating" @click="$emit('calibrate-zero')">从现在归零</button>
      </div>
      <div class="traffic-history-manager__action-group traffic-history-manager__action-group--danger" data-testid="traffic-history-actions-danger">
        <button class="btn btn-danger" type="button" :disabled="cleaning" @click="$emit('cleanup')">清理过期数据</button>
      </div>
    </div>
  </div>
</template>

<script setup>
defineProps({
  policy: {
    type: Object,
    required: true,
    validator: v =>
      typeof v.hourly_retention_days === 'number' &&
      typeof v.daily_retention_months === 'number' &&
      (v.monthly_retention_months === null || v.monthly_retention_months === undefined || typeof v.monthly_retention_months === 'number')
  },
  calibrating: { type: Boolean, default: false },
  cleaning: { type: Boolean, default: false }
})

defineEmits(['calibrate', 'calibrate-zero', 'cleanup'])
</script>

<style scoped>
.traffic-history-manager {
  display: flex;
  flex-direction: column;
  gap: 0.875rem;
}
.traffic-history-manager__summary {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  padding: 0.75rem 0.875rem;
  border: 1px solid color-mix(in srgb, var(--color-border-subtle) 90%, transparent);
  border-radius: var(--radius-md);
  background: color-mix(in srgb, var(--color-bg-surface) 88%, var(--color-bg-subtle));
}
.traffic-history-manager__summary-title {
  color: var(--color-text-primary);
  font-size: 0.8125rem;
  font-weight: 650;
}
.traffic-history-manager__summary-body {
  display: flex;
  flex-wrap: wrap;
  gap: 0.375rem;
}
.traffic-history-manager__chip {
  display: inline-flex;
  align-items: center;
  padding: 0.2rem 0.5rem;
  border-radius: 999px;
  border: 1px solid color-mix(in srgb, var(--color-border-default) 80%, transparent);
  background: var(--color-bg-surface);
  color: var(--color-text-secondary);
  font-size: 0.75rem;
  font-weight: 500;
  line-height: 1.3;
}
.traffic-history-manager__actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 0.625rem;
}
.traffic-history-manager__action-group {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
}
.btn {
  padding: 0.5rem 0.9rem;
  border-radius: var(--radius-lg);
  font-size: 0.875rem;
  font-weight: 500;
  cursor: pointer;
  transition: background 0.15s, border-color 0.15s, color 0.15s, opacity 0.15s;
  border: none;
  font-family: inherit;
  display: inline-flex;
  align-items: center;
  gap: 0.375rem;
}
.btn-secondary {
  background: var(--color-bg-subtle);
  color: var(--color-text-primary);
  border: 1px solid var(--color-border-default);
}
.btn-secondary:hover:not(:disabled) {
  background: color-mix(in srgb, var(--color-bg-subtle) 70%, var(--color-bg-surface));
}
.btn-danger {
  background: var(--color-danger-subtle, #fef2f2);
  color: var(--color-danger, #dc2626);
  border: 1px solid var(--color-danger-muted, #fecaca);
}
.btn-danger:hover:not(:disabled) {
  background: color-mix(in srgb, var(--color-danger-subtle, #fef2f2) 70%, #fff);
}
.btn:disabled { opacity: 0.6; cursor: not-allowed; }
@media (max-width: 720px) {
  .traffic-history-manager__actions {
    flex-direction: column;
    align-items: stretch;
  }
  .traffic-history-manager__action-group {
    width: 100%;
  }
  .traffic-history-manager__action-group .btn {
    flex: 1;
  }
}
</style>

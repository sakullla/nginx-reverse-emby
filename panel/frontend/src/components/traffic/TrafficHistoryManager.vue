<template>
  <div class="traffic-history-manager">
    <div class="traffic-history-manager__summary">
      <div class="traffic-history-manager__summary-heading">
        <span class="traffic-history-manager__summary-kicker">只读摘要</span>
        <div class="traffic-history-manager__summary-title">当前保留策略</div>
      </div>
      <p class="traffic-history-manager__summary-desc">维护操作前先确认保留窗口，避免误清仍需的历史粒度</p>
      <div class="traffic-history-manager__summary-body">
        <span class="traffic-history-manager__chip">小时 {{ policy.hourly_retention_days }} 天</span>
        <span class="traffic-history-manager__chip">日 {{ policy.daily_retention_months }} 个月</span>
        <span class="traffic-history-manager__chip">月 {{ policy.monthly_retention_months ?? '—' }} 个月</span>
      </div>
    </div>
    <div class="traffic-history-manager__actions">
      <div class="traffic-history-manager__action-block">
        <div class="traffic-history-manager__action-label">常规维护</div>
        <div class="traffic-history-manager__action-group" data-testid="traffic-history-actions-main">
          <button class="btn btn-secondary" type="button" :disabled="calibrating" @click="$emit('calibrate')">校准为指定值</button>
          <button class="btn btn-secondary" type="button" :disabled="calibrating" @click="$emit('calibrate-zero')">从现在归零</button>
        </div>
      </div>
      <div
        class="traffic-history-manager__action-block traffic-history-manager__action-block--danger"
        data-testid="traffic-history-danger-block"
      >
        <div class="traffic-history-manager__action-label traffic-history-manager__action-label--danger">危险操作 · 需确认</div>
        <p class="traffic-history-manager__action-hint">点击后需二次确认，误清不可撤销</p>
        <div class="traffic-history-manager__action-group traffic-history-manager__action-group--danger" data-testid="traffic-history-actions-danger">
          <button class="btn btn-danger" type="button" :disabled="cleaning" @click="$emit('cleanup')">清理过期数据</button>
        </div>
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
  gap: 0.45rem;
  padding: 0.75rem 0.875rem;
  border: 1px solid color-mix(in srgb, var(--color-border-subtle) 90%, transparent);
  border-radius: var(--radius-md);
  background: color-mix(in srgb, var(--color-bg-surface) 88%, var(--color-bg-subtle));
}
.traffic-history-manager__summary-heading {
  display: flex;
  align-items: center;
  gap: 0.45rem;
}
.traffic-history-manager__summary-kicker {
  display: inline-flex;
  align-items: center;
  padding: 0.08rem 0.4rem;
  border-radius: 999px;
  border: 1px solid color-mix(in srgb, var(--color-border-default) 85%, transparent);
  background: color-mix(in srgb, var(--color-bg-subtle) 80%, var(--color-bg-surface));
  color: var(--color-text-tertiary);
  font-size: 0.6875rem;
  font-weight: 700;
  letter-spacing: 0.03em;
}
.traffic-history-manager__summary-title {
  color: var(--color-text-primary);
  font-size: 0.8125rem;
  font-weight: 650;
}
.traffic-history-manager__summary-desc {
  margin: 0;
  color: var(--color-text-muted);
  font-size: 0.75rem;
  line-height: 1.4;
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
  align-items: stretch;
  justify-content: flex-start;
  gap: 0.75rem;
}
.traffic-history-manager__action-block {
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
  min-width: 0;
  padding: 0.55rem 0.65rem;
  border: 1px solid transparent;
  border-radius: var(--radius-md);
  background: transparent;
}
.traffic-history-manager__action-block--danger {
  margin-left: auto;
  border: 1px solid color-mix(in srgb, var(--color-danger, #dc2626) 28%, var(--color-danger-muted, #fecaca));
  background:
    linear-gradient(
      180deg,
      color-mix(in srgb, var(--color-danger-subtle, #fef2f2) 88%, #fff),
      color-mix(in srgb, var(--color-danger-subtle, #fef2f2) 55%, transparent)
    );
  box-shadow: 0 1px 2px color-mix(in srgb, var(--color-danger, #dc2626) 8%, transparent);
}
.traffic-history-manager__action-label {
  color: var(--color-text-tertiary);
  font-size: 0.6875rem;
  font-weight: 700;
  letter-spacing: 0.03em;
}
.traffic-history-manager__action-label--danger {
  color: var(--color-danger, #dc2626);
}
.traffic-history-manager__action-hint {
  margin: 0;
  color: color-mix(in srgb, var(--color-danger, #dc2626) 72%, var(--color-text-muted));
  font-size: 0.75rem;
  line-height: 1.35;
}
.traffic-history-manager__action-group {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
}
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
  .traffic-history-manager__action-block--danger {
    width: 100%;
  }
}
</style>

<template>
  <div class="traffic-history-manager">
    <div class="traffic-history-manager__summary">
      <span class="traffic-history-manager__summary-label">保留策略：</span>
      小时 {{ policy.hourly_retention_days }} 天、日 {{ policy.daily_retention_months }} 个月、月 {{ policy.monthly_retention_months ?? '—' }} 个月。
    </div>
    <div class="traffic-history-manager__actions">
      <button class="btn btn-secondary" type="button" :disabled="calibrating" @click="$emit('calibrate')">校准为指定值</button>
      <button class="btn btn-secondary" type="button" :disabled="calibrating" @click="$emit('calibrate-zero')">从现在归零</button>
      <button class="btn btn-danger" type="button" :disabled="cleaning" @click="$emit('cleanup')">清理过期数据</button>
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
.traffic-history-manager { display: flex; flex-direction: column; gap: 1rem; }
.traffic-history-manager__summary {
  font-size: 0.8125rem;
  color: var(--color-text-secondary);
  line-height: 1.5;
}
.traffic-history-manager__summary-label { color: var(--color-text-primary); font-weight: 600; }
.traffic-history-manager__actions { display: flex; gap: 0.5rem; flex-wrap: wrap; }
.btn { padding: 0.5rem 1rem; border-radius: var(--radius-lg); font-size: 0.875rem; font-weight: 500; cursor: pointer; transition: all 0.15s; border: none; font-family: inherit; display: inline-flex; align-items: center; gap: 0.375rem; }
.btn-secondary { background: var(--color-bg-subtle); color: var(--color-text-primary); border: 1px solid var(--color-border-default); }
.btn-danger { background: var(--color-danger-subtle, #fef2f2); color: var(--color-danger, #dc2626); border: 1px solid var(--color-danger-muted, #fecaca); }
.btn:disabled { opacity: 0.6; cursor: not-allowed; }
@media (max-width: 720px) { .traffic-history-manager__actions { justify-content: stretch; } .traffic-history-manager__actions .btn { flex: 1; } }
</style>

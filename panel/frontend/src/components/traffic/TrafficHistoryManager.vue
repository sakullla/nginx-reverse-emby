<template>
  <div class="traffic-history-manager">
    <div class="traffic-history-manager__card">
      <div class="traffic-history-manager__card-header">
        <h4 class="traffic-history-manager__card-title">流量校准</h4>
      </div>
      <p class="traffic-history-manager__card-desc">
        手动校准当前计费周期的已用流量基准值，或立即将已用流量重置为零。
      </p>
      <div class="traffic-history-manager__card-actions">
        <button class="btn btn-secondary" type="button" :disabled="calibrating" @click="$emit('calibrate')">校准为指定值</button>
        <button class="btn btn-secondary" type="button" :disabled="calibrating" @click="$emit('calibrate-zero')">从现在归零</button>
      </div>
    </div>

    <div class="traffic-history-manager__card">
      <div class="traffic-history-manager__card-header">
        <h4 class="traffic-history-manager__card-title">数据清理</h4>
      </div>
      <p class="traffic-history-manager__card-desc">
        按保留策略清理过期历史数据。当前策略：小时 {{ policy.hourly_retention_days }} 天、日 {{ policy.daily_retention_months }} 个月、月 {{ policy.monthly_retention_months }} 个月。
      </p>
      <div class="traffic-history-manager__card-actions">
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
      typeof v.monthly_retention_months === 'number'
  },
  calibrating: { type: Boolean, default: false },
  cleaning: { type: Boolean, default: false }
})

defineEmits(['calibrate', 'calibrate-zero', 'cleanup'])
</script>

<style scoped>
.traffic-history-manager { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 0.75rem; }
.traffic-history-manager__card {
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  padding: 1rem;
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}
.traffic-history-manager__card-header { display: flex; align-items: center; justify-content: space-between; }
.traffic-history-manager__card-title { margin: 0; font-size: 0.9375rem; font-weight: 600; color: var(--color-text-primary); }
.traffic-history-manager__card-desc { margin: 0; font-size: 0.8125rem; color: var(--color-text-secondary); line-height: 1.5; }
.traffic-history-manager__card-actions { display: flex; gap: 0.5rem; flex-wrap: wrap; margin-top: auto; }
.btn { padding: 0.5rem 1rem; border-radius: var(--radius-lg); font-size: 0.875rem; font-weight: 500; cursor: pointer; transition: all 0.15s; border: none; font-family: inherit; display: inline-flex; align-items: center; gap: 0.375rem; }
.btn-secondary { background: var(--color-bg-subtle); color: var(--color-text-primary); border: 1px solid var(--color-border-default); }
.btn-danger { background: var(--color-danger-subtle, #fef2f2); color: var(--color-danger, #dc2626); border: 1px solid var(--color-danger-muted, #fecaca); }
.btn:disabled { opacity: 0.6; cursor: not-allowed; }
@media (max-width: 720px) { .traffic-history-manager { grid-template-columns: 1fr; } }
</style>

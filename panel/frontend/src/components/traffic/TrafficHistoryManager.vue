<template>
  <div class="traffic-history-manager">
    <div class="traffic-history-manager__group">
      <h4 class="traffic-history-manager__heading">校准</h4>
      <div class="traffic-history-manager__actions">
        <button class="btn btn-secondary" type="button" :disabled="calibrating" @click="$emit('calibrate')">校准</button>
        <button class="btn btn-secondary" type="button" :disabled="calibrating" @click="$emit('calibrate-zero')">从现在归零</button>
      </div>
    </div>
    <div class="traffic-history-manager__group">
      <h4 class="traffic-history-manager__heading">历史数据</h4>
      <div class="traffic-history-manager__actions">
        <button class="btn btn-secondary" type="button" :disabled="cleaning" @click="$emit('cleanup')">清理过期数据</button>
      </div>
    </div>
  </div>
</template>

<script setup>
defineProps({
  calibrating: { type: Boolean, default: false },
  cleaning: { type: Boolean, default: false }
})

defineEmits(['calibrate', 'calibrate-zero', 'cleanup'])
</script>

<style scoped>
.traffic-history-manager { display: flex; flex-direction: column; gap: 1rem; }
.traffic-history-manager__heading { font-size: 0.875rem; font-weight: 600; color: var(--color-text-primary); margin: 0 0 0.5rem; }
.traffic-history-manager__actions { display: flex; gap: 0.5rem; flex-wrap: wrap; }
.btn { padding: 0.5rem 1rem; border-radius: var(--radius-lg); font-size: 0.875rem; font-weight: 500; cursor: pointer; transition: all 0.15s; border: none; font-family: inherit; display: inline-flex; align-items: center; gap: 0.375rem; }
.btn-secondary { background: var(--color-bg-subtle); color: var(--color-text-primary); border: 1px solid var(--color-border-default); }
.btn:disabled { opacity: 0.6; cursor: not-allowed; }
</style>

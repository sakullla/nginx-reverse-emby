<script setup>
import { computed } from 'vue'
import { pluginRiskNotices } from '../../api/pluginSecurity'

const props = defineProps({
  packageDetail: { type: Object, default: () => ({}) },
  source: { type: Object, default: () => ({}) }
})
const notices = computed(() => pluginRiskNotices(props.packageDetail, props.source))
</script>

<template>
  <aside class="plugin-risks" aria-label="插件风险提示">
    <strong>安装与运行边界</strong>
    <ul>
      <li v-for="notice in notices" :key="notice">{{ notice }}</li>
    </ul>
  </aside>
</template>

<style scoped>
.plugin-risks { padding: var(--space-4); border: 1px solid var(--color-warning); border-radius: var(--radius-lg); background: var(--color-warning-subtle); }
.plugin-risks strong { color: var(--color-text-primary); }
.plugin-risks ul { display: grid; gap: var(--space-2); margin: var(--space-3) 0 0; padding-left: 1.2rem; color: var(--color-text-secondary); font-size: var(--text-sm); }
</style>

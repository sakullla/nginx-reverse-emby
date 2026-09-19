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
    <header class="plugin-risks__head">
      <span class="plugin-risks__icon" aria-hidden="true">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" />
          <line x1="12" y1="9" x2="12" y2="13" />
          <line x1="12" y1="17" x2="12.01" y2="17" />
        </svg>
      </span>
      <div>
        <strong>安装与运行边界</strong>
        <p>安装、升级或回滚前先看这些限制。</p>
      </div>
    </header>
    <ul>
      <li v-for="notice in notices" :key="notice">{{ notice }}</li>
    </ul>
  </aside>
</template>

<style scoped>
.plugin-risks {
  min-width: 0;
  padding: 0.9rem 1rem;
  border: 1px solid color-mix(in srgb, var(--color-warning) 34%, var(--color-border-subtle));
  border-radius: var(--radius-xl);
  background:
    linear-gradient(180deg, color-mix(in srgb, var(--color-warning) 8%, var(--color-bg-surface)), var(--color-bg-surface));
}

.plugin-risks__head {
  display: flex;
  align-items: flex-start;
  gap: 0.7rem;
}

.plugin-risks__icon {
  display: grid;
  place-items: center;
  width: 1.75rem;
  height: 1.75rem;
  flex-shrink: 0;
  border-radius: var(--radius-md);
  background: color-mix(in srgb, var(--color-warning) 16%, var(--color-bg-subtle));
  color: var(--color-warning);
}

.plugin-risks strong {
  display: block;
  color: var(--color-text-primary);
  font-size: 0.875rem;
}

.plugin-risks p {
  margin: 0.15rem 0 0;
  color: var(--color-text-tertiary);
  font-size: 0.75rem;
}

.plugin-risks ul {
  display: grid;
  gap: 0.45rem;
  margin: 0.8rem 0 0;
  padding: 0;
  list-style: none;
}

.plugin-risks li {
  position: relative;
  padding: 0.45rem 0.65rem 0.45rem 1.35rem;
  border-radius: var(--radius-md);
  background: color-mix(in srgb, var(--color-bg-canvas) 70%, transparent);
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
  line-height: 1.45;
  overflow-wrap: anywhere;
}

.plugin-risks li::before {
  content: '';
  position: absolute;
  left: 0.55rem;
  top: 0.8rem;
  width: 0.35rem;
  height: 0.35rem;
  border-radius: 999px;
  background: var(--color-warning);
}
</style>

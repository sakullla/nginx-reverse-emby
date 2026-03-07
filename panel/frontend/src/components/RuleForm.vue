<template>
  <form @submit.prevent="handleSubmit" class="rule-form-inline">
    <div class="input-wrapper">
      <span class="input-icon" v-html="icons.globe"></span>
      <input
        v-model="frontend_url"
        type="text"
        placeholder="前端 URL (如: https://example.com)"
        required
      />
    </div>

    <div class="separator">
      <span class="arrow-svg" v-html="icons.arrowRight"></span>
    </div>

    <div class="input-wrapper">
      <span class="input-icon" v-html="icons.server"></span>
      <input
        v-model="backend_url"
        type="text"
        placeholder="后端 URL (如: http://backend:8080)"
        required
      />
    </div>

    <button type="submit" :disabled="ruleStore.loading" class="add-button">
      <span v-if="!ruleStore.loading" class="btn-content">
        <span class="icon-btn" v-html="icons.plus"></span>
        添加规则
      </span>
      <span v-else class="loading-mini"></span>
    </button>
  </form>
</template>

<script setup>
import { ref } from 'vue'
import { useRuleStore } from '../stores/rules'

const ruleStore = useRuleStore()
const frontend_url = ref('')
const backend_url = ref('')

const icons = {
  globe: '<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="10"/><line x1="2" y1="12" x2="22" y2="12"/><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/></svg>',
  server: '<svg viewBox="0 0 24 24"><rect x="2" y="2" width="20" height="8" rx="2" ry="2"/><rect x="2" y="14" width="20" height="8" rx="2" ry="2"/><line x1="6" y1="6" x2="6.01" y2="6"/><line x1="6" y1="18" x2="6.01" y2="18"/></svg>',
  arrowRight: '<svg viewBox="0 0 24 24"><line x1="5" y1="12" x2="19" y2="12"/><polyline points="12 5 19 12 12 19"/></svg>',
  plus: '<svg viewBox="0 0 24 24"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>'
}

const handleSubmit = async () => {
  try {
    await ruleStore.addRule(frontend_url.value, backend_url.value)
    frontend_url.value = ''
    backend_url.value = ''
  } catch (err) {
    // 错误已由 store 处理
  }
}
</script>

<style scoped>
.rule-form-inline {
  display: flex;
  align-items: center;
  gap: var(--spacing-md);
  width: 100%;
}

.input-wrapper {
  position: relative;
  flex: 1;
}

.input-icon {
  position: absolute;
  left: var(--spacing-md);
  top: 50%;
  transform: translateY(-50%);
  width: 16px;
  height: 16px;
  pointer-events: none;
  opacity: 0.5;
  color: var(--color-text-primary);
}

.input-icon :deep(svg) {
  width: 100%;
  height: 100%;
  stroke: currentColor;
  stroke-width: 2;
  fill: none;
}

input {
  padding-left: calc(var(--spacing-md) * 2.8) !important;
  height: 46px;
}

.separator {
  color: var(--color-text-muted);
  opacity: 0.3;
  width: 24px;
  height: 24px;
}

.separator :deep(svg) {
  width: 100%;
  height: 100%;
  stroke: currentColor;
  stroke-width: 3;
  fill: none;
}

.add-button {
  height: 46px;
  padding: 0 var(--spacing-lg);
  white-space: nowrap;
  flex-shrink: 0;
}

.btn-content {
  display: flex;
  align-items: center;
  gap: 6px;
}

.icon-btn :deep(svg) {
  width: 16px;
  height: 16px;
  stroke: currentColor;
  stroke-width: 3;
  fill: none;
}

.loading-mini {
  width: 18px;
  height: 18px;
  border: 2px solid rgba(255,255,255,0.3);
  border-top-color: #fff;
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

@media (max-width: 1024px) {
  .rule-form-inline {
    flex-direction: column;
    align-items: stretch;
    gap: var(--spacing-sm);
  }
  .separator {
    display: none;
  }
  .add-button {
    margin-top: var(--spacing-xs);
  }
}
</style>

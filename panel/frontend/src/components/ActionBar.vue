<template>
  <button
    :disabled="ruleStore.loading"
    @click="handleApply"
    class="apply-btn success"
  >
    <span v-if="!ruleStore.loading" class="btn-content">
      <span class="icon-btn" v-html="icons.zap"></span>
      应用配置
    </span>
    <span v-else class="loading-mini"></span>
  </button>
</template>

<script setup>
import { useRuleStore } from '../stores/rules'

const ruleStore = useRuleStore()

const icons = {
  zap: '<svg viewBox="0 0 24 24"><polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"/></svg>'
}

async function handleApply() {
  try {
    await ruleStore.applyNginxConfig()
  } catch (err) {
    // 错误已由 store 处理
  }
}
</script>

<style scoped>
.apply-btn {
  height: 40px;
  padding: 0 var(--spacing-lg);
}

.btn-content {
  display: flex;
  align-items: center;
  gap: 8px;
}

.icon-btn :deep(svg) {
  width: 16px;
  height: 16px;
  stroke: currentColor;
  stroke-width: 2.5;
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

@media (max-width: 768px) {
  .apply-btn {
    width: 100%;
    height: 46px;
  }
}
</style>

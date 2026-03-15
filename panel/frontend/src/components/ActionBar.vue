<template>
  <button
    :disabled="ruleStore.loading || !ruleStore.hasSelectedAgent"
    @click="handleApply"
    class="apply-btn success"
    :title="buttonTitle"
  >
    <span v-if="!ruleStore.loading" class="btn-content">
      <span class="icon-btn" v-html="icons.zap"></span>
      <span class="btn-text">应用节点配置</span>
    </span>
    <span v-else class="loading-mini"></span>
  </button>
</template>

<script setup>
import { computed } from 'vue'
import { useRuleStore } from '../stores/rules'

const ruleStore = useRuleStore()

const icons = {
  zap: '<svg viewBox="0 0 24 24"><polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"/></svg>'
}

const buttonTitle = computed(() => {
  if (!ruleStore.hasSelectedAgent) return '请先选择一个 Agent 节点'
  if (ruleStore.loading) return '正在应用配置...'
  return `应用 ${ruleStore.selectedAgent?.name || '当前节点'} 的配置`
})

async function handleApply() {
  if (!ruleStore.hasSelectedAgent) return
  try {
    await ruleStore.applyNginxConfig()
  } catch (err) {
    // 错误由 store 统一处理
  }
}
</script>

<style scoped>
.apply-btn {
  height: 40px;
  padding: 0 var(--spacing-lg);
  border-radius: var(--radius-md);
  transition: all var(--transition-base);
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

@media (max-width: 480px) {
  .apply-btn {
    width: 40px;
    height: 40px;
    padding: 0;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .btn-text {
    display: none;
  }

  .btn-content {
    gap: 0;
  }
}
</style>

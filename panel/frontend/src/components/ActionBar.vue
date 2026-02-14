<template>
  <button @click="handleApply" :disabled="ruleStore.loading" class="apply-btn">
    <span v-if="ruleStore.loading">⏳ 应用中...</span>
    <span v-else>🚀 应用配置</span>
  </button>
</template>

<script setup>
import { useRuleStore } from '../stores/rules'

const ruleStore = useRuleStore()

async function handleApply() {
  try {
    await ruleStore.applyNginxConfig()
  } catch (err) {
    // Error handled by store
  }
}
</script>

<style scoped>
.apply-btn {
  min-width: 140px;
}
</style>

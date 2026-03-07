<template>
  <BaseButton
    variant="secondary"
    :loading="ruleStore.loading"
    @click="handleApply"
    class="apply-btn"
  >
    <span v-if="ruleStore.loading">应用中...</span>
    <span v-else>🚀 应用配置</span>
  </BaseButton>
</template>

<script setup>
import { useRuleStore } from '../stores/rules'
import BaseButton from './base/BaseButton.vue'

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

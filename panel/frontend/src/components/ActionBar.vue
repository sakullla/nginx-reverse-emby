<template>
  <button
    class="btn btn--icon tooltip"
    :class="buttonClass"
    :disabled="ruleStore.loading || !ruleStore.hasSelectedAgent"
    @click="handleApply"
  >
    <span v-if="ruleStore.loading" class="spinner spinner--sm"></span>
    <svg v-else width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
      <polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"/>
    </svg>
    <span class="tooltip__content">{{ buttonTitle }}</span>
  </button>
</template>

<script setup>
import { computed } from 'vue'
import { useRuleStore } from '../stores/rules'

const ruleStore = useRuleStore()

const buttonTitle = computed(() => {
  if (!ruleStore.hasSelectedAgent) return '请先选择一个 Agent 节点'
  if (ruleStore.loading) return '正在应用配置...'
  return '应用节点配置'
})

const buttonClass = computed(() => {
  return {
    'btn--ghost': true,
    'btn--active': ruleStore.hasSelectedAgent && !ruleStore.loading,
    'btn--loading': ruleStore.loading
  }
})

async function handleApply() {
  if (!ruleStore.hasSelectedAgent) return
  try {
    await ruleStore.applyNginxConfig()
  } catch (err) {
    // Error handled by store
  }
}
</script>

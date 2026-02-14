<template>
  <div v-if="ruleStore.statusMessage" :class="['status-message', ruleStore.statusMessage.type]">
    {{ ruleStore.statusMessage.text }}
  </div>
</template>

<script setup>
import { useRuleStore } from '../stores/rules'

const ruleStore = useRuleStore()
</script>

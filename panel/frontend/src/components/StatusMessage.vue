<template>
  <Transition name="fade">
    <div v-if="ruleStore.statusMessage" data-testid="status-message" :class="['status-message', ruleStore.statusMessage.type]">
      {{ ruleStore.statusMessage.text }}
    </div>
  </Transition>
</template>

<script setup>
import { useRuleStore } from '../stores/rules'

const ruleStore = useRuleStore()
</script>

<style scoped>
.fade-enter-active,
.fade-leave-active {
  transition: all 0.3s ease;
}

.fade-enter-from {
  opacity: 0;
  transform: translateX(100%);
}

.fade-leave-to {
  opacity: 0;
  transform: translateY(-20px);
}
</style>

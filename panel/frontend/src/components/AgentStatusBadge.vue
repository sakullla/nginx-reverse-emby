<template>
  <span class="agent-status-badge" :class="`agent-status-badge--${status}`">
    {{ label }}
  </span>
</template>

<script setup>
import { computed } from 'vue'
import { getAgentStatus, getAgentStatusLabel } from '../utils/agentHelpers.js'

const props = defineProps({
  agent: { type: Object, required: true }
})

const status = computed(() => getAgentStatus(props.agent))
const label = computed(() => getAgentStatusLabel(status.value))
</script>

<style scoped>
.agent-status-badge {
  font-size: 0.75rem;
  font-weight: 600;
  padding: 2px 8px;
  border-radius: var(--radius-full);
  display: inline-block;
}
.agent-status-badge--online {
  background: var(--color-success-50);
  color: var(--color-success);
}
.agent-status-badge--offline {
  background: var(--color-bg-subtle);
  color: var(--color-text-muted);
}
.agent-status-badge--failed {
  background: var(--color-danger-50);
  color: var(--color-danger);
}
.agent-status-badge--pending {
  background: var(--color-warning-50);
  color: var(--color-warning);
}
</style>

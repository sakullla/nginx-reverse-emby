<template>
  <BaseBadge class="agent-status-badge" :tone="tone" subtone="muted" dot size="md">
    {{ label }}
  </BaseBadge>
</template>

<script setup>
import { computed } from 'vue'
import BaseBadge from './base/BaseBadge.vue'
import { getAgentStatus, getAgentStatusLabel } from '../utils/agentHelpers.js'

const props = defineProps({
  agent: { type: Object, required: true },
})

const status = computed(() => getAgentStatus(props.agent))
const label = computed(() => getAgentStatusLabel(status.value))

const TONE_MAP = {
  online: 'success',
  offline: 'neutral',
  failed: 'danger',
  pending: 'warning',
}

const tone = computed(() => TONE_MAP[status.value] || 'neutral')
</script>

<style scoped>
.agent-status-badge {
  position: relative;
  padding: 4px 10px;
  font-weight: var(--font-bold);
  letter-spacing: 0.02em;
}

/* Subtle tone-aware inner border to make the badge feel refined
   in dashboard card headers without changing its semantic color. */
.agent-status-badge::after {
  content: '';
  position: absolute;
  inset: 0;
  border-radius: inherit;
  box-shadow: inset 0 0 0 1px currentColor;
  opacity: 0.16;
  pointer-events: none;
}

.agent-status-badge:deep(.base-badge__dot) {
  width: 7px;
  height: 7px;
}
</style>

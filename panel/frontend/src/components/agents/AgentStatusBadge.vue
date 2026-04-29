<template>
  <BaseBadge :tone="tone" subtone="muted" dot size="md">
    {{ label }}
  </BaseBadge>
</template>

<script setup>
import { computed } from 'vue'
import BaseBadge from '../base/BaseBadge.vue'
import { getAgentStatus, getAgentStatusLabel } from '../../utils/agentHelpers.js'

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

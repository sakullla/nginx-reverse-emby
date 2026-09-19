<template>
  <BaseBadge :tone="tone" :dot="dot" :mono="mono" :size="size">
    {{ label }}
  </BaseBadge>
</template>

<script setup>
import { computed } from 'vue'
import BaseBadge from '../base/BaseBadge.vue'

const props = defineProps({
  status: { type: [String, null], default: '' },
  label: { type: String, default: '' },
  mono: { type: Boolean, default: false },
  dot: { type: Boolean, default: false },
  size: {
    type: String,
    default: 'sm',
    validator: (v) => ['sm', 'md'].includes(v),
  },
})

const tone = computed(() => {
  const value = String(props.status || '').toLowerCase()
  if (['active', 'healthy', 'succeeded', 'success'].includes(value)) return 'success'
  if (['failed', 'revoked', 'critical', 'unavailable', 'failed_closed', 'rejected'].includes(value)) return 'danger'
  if (['blocked', 'warning', 'retiring', 'degraded', 'running', 'accepted', 'prepared'].includes(value)) return 'warning'
  if (['primary', 'info'].includes(value)) return 'primary'
  return 'neutral'
})
</script>

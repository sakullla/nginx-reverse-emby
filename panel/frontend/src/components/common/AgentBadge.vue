<template>
  <BaseBadge
    v-if="label"
    class="agent-badge"
    tone="neutral"
    subtone="secondary"
    size="sm"
    :title="titleText"
  >
    {{ label }}
  </BaseBadge>
</template>

<script setup>
import { computed } from 'vue'
import BaseBadge from '../base/BaseBadge.vue'

const props = defineProps({
  /** Resource item that may carry agent_name / agent_id from list APIs. */
  item: { type: Object, default: null },
  /** Optional page-selected agent fallback when item lacks ownership fields. */
  agent: { type: Object, default: null },
  agentName: { type: [String, Number], default: '' },
  agentId: { type: [String, Number], default: '' },
})

function firstNonEmpty(...values) {
  for (const value of values) {
    if (value == null) continue
    const text = String(value).trim()
    if (text) return text
  }
  return ''
}

const resolvedName = computed(() => firstNonEmpty(
  props.agentName,
  props.item?.agent_name,
  props.agent?.name,
))

const resolvedId = computed(() => firstNonEmpty(
  props.agentId,
  props.item?.agent_id,
  props.agent?.id,
))

const label = computed(() => resolvedName.value || resolvedId.value || '')

const titleText = computed(() => {
  if (resolvedName.value && resolvedId.value && resolvedName.value !== resolvedId.value) {
    return `节点 ${resolvedName.value} (${resolvedId.value})`
  }
  if (label.value) return `节点 ${label.value}`
  return '节点'
})
</script>

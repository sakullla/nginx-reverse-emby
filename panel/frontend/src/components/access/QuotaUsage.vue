<script setup>
import { computed } from 'vue'

const props = defineProps({
  current: { type: Number, required: true },
  limit: { type: Number, required: true },
  recoveryCondition: { type: String, default: '' }
})

const percent = computed(() => {
  if (props.limit < 0) return 0
  if (props.limit === 0) return 100
  return Math.min(100, Math.max(0, (props.current / props.limit) * 100))
})
</script>

<template>
  <div class="quota-usage">
    <div class="quota-values">
      <strong>{{ current }}</strong>
      <span>/ {{ limit < 0 ? '不限' : limit }}</span>
    </div>
    <progress v-if="limit >= 0" :value="percent" max="100" />
    <small v-if="recoveryCondition">恢复条件：{{ recoveryCondition }}</small>
  </div>
</template>

<style scoped>
.quota-usage {
  display: grid;
  gap: 0.35rem;
}

.quota-values {
  display: flex;
  gap: 0.3rem;
  align-items: baseline;
}

progress {
  width: 100%;
}
</style>

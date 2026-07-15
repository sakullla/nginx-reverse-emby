<template>
  <span hidden aria-hidden="true"></span>
</template>

<script setup>
import { toRef } from 'vue'
import { useOperationStatus } from '../../hooks/useOperationStatus'

const props = defineProps({
  operationId: { type: String, required: true }
})

useOperationStatus(toRef(props, 'operationId'))
</script>

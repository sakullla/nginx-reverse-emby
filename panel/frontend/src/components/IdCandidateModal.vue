<template>
  <BaseModal
    :model-value="visible"
    :title="`ID ${id} 存在于多个节点`"
    subtitle="该 ID 在多个 agent 上均有匹配，请选择要跳转的节点"
    size="md"
    @update:model-value="$emit('update:visible', $event)"
  >
    <div class="candidate-list">
      <button
        v-for="(candidate, index) in candidates"
        :key="index"
        class="candidate-item"
        @click="handleSelect(candidate)"
      >
        <div class="candidate-item__agent">
          <span class="candidate-item__label">节点</span>
          <span class="candidate-item__value">{{ candidate.agentId }}</span>
        </div>
        <div class="candidate-item__detail">
          <span class="candidate-item__label">类型</span>
          <span class="candidate-item__value">{{ typeLabel(candidate.type) }}</span>
        </div>
        <div v-if="candidate.record.name || candidate.record.domain" class="candidate-item__detail">
          <span class="candidate-item__label">名称</span>
          <span class="candidate-item__value">{{ candidate.record.name || candidate.record.domain }}</span>
        </div>
      </button>
    </div>

    <template #footer>
      <button class="btn btn--secondary" @click="$emit('update:visible', false)">取消</button>
    </template>
  </BaseModal>
</template>

<script setup>
import BaseModal from './base/BaseModal.vue'

const TYPE_LABELS = {
  rule: 'HTTP 规则',
  l4: 'L4 规则',
  cert: '证书',
  relay: 'Relay 监听器'
}

defineProps({
  visible: { type: Boolean, default: false },
  id: { type: String, default: '' },
  candidates: { type: Array, default: () => [] }
})

const emit = defineEmits(['update:visible', 'select'])

function typeLabel(type) {
  return TYPE_LABELS[type] || type
}

function handleSelect(candidate) {
  emit('select', candidate)
  emit('update:visible', false)
}
</script>

<style scoped>
.candidate-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.candidate-item {
  display: flex;
  align-items: center;
  gap: var(--space-4);
  padding: var(--space-3) var(--space-4);
  border: var(--border-width-thin) solid var(--color-border-default);
  border-radius: var(--radius-sm);
  background: var(--color-bg-surface);
  cursor: pointer;
  transition: border-color var(--duration-fast) var(--ease-default), background var(--duration-fast) var(--ease-default);
  text-align: left;
  width: 100%;
}

.candidate-item:hover {
  border-color: var(--color-primary);
  background: var(--color-primary-subtle);
}

.candidate-item__agent,
.candidate-item__detail {
  display: flex;
  flex-direction: column;
  gap: var(--space-0-5);
}

.candidate-item__label {
  font-size: var(--text-xs);
  color: var(--color-text-secondary);
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.candidate-item__value {
  font-size: var(--text-sm);
  color: var(--color-text-primary);
  font-weight: var(--font-medium);
}
</style>

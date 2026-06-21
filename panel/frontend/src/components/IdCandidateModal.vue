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
  gap: 0.5rem;
}

.candidate-item {
  display: flex;
  align-items: center;
  gap: 1rem;
  padding: 0.75rem 1rem;
  border: 1px solid var(--color-border);
  border-radius: 0.5rem;
  background: var(--color-bg);
  cursor: pointer;
  transition: border-color 0.15s, background 0.15s;
  text-align: left;
  width: 100%;
}

.candidate-item:hover {
  border-color: var(--color-primary);
  background: var(--color-primary-bg, rgba(59, 130, 246, 0.05));
}

.candidate-item__agent,
.candidate-item__detail {
  display: flex;
  flex-direction: column;
  gap: 0.125rem;
}

.candidate-item__label {
  font-size: 0.75rem;
  color: var(--color-text-secondary);
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.candidate-item__value {
  font-size: 0.875rem;
  color: var(--color-text);
  font-weight: 500;
}
</style>

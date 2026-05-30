<template>
  <BaseModal
    :model-value="modelValue"
    :title="title"
    size="md"
    :close-on-click-modal="false"
    @update:model-value="$emit('update:modelValue', $event)"
  >
    <div class="client-qr">
      <div v-if="loading" class="empty-inline">正在生成二维码...</div>
      <template v-else>
        <div v-if="qrImageURL" class="client-qr__image-wrap">
          <img class="client-qr__image" :src="qrImageURL" alt="WireGuard Client QR code">
        </div>
        <p v-if="error" class="form-error">{{ error }}</p>
        <div class="form-group">
          <label class="form-label">配置文本</label>
          <textarea class="input textarea client-qr__config" :value="configText" readonly></textarea>
        </div>
      </template>
    </div>
  </BaseModal>
</template>

<script setup>
import { computed } from 'vue'
import BaseModal from '../base/BaseModal.vue'

const props = defineProps({
  modelValue: { type: Boolean, default: false },
  clientName: { type: String, default: '' },
  configText: { type: String, default: '' },
  qrImageURL: { type: String, default: '' },
  error: { type: String, default: '' },
  loading: { type: Boolean, default: false }
})

defineEmits(['update:modelValue'])

const title = computed(() => {
  return props.clientName ? `${props.clientName} QR` : 'WireGuard Client QR'
})
</script>

<style scoped>
.client-qr {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.client-qr__image-wrap {
  display: flex;
  justify-content: center;
  padding: var(--space-3);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-md);
  background: white;
}

.client-qr__image {
  width: min(280px, 100%);
  height: auto;
}

.client-qr__config {
  min-height: 180px;
  font-family: ui-monospace, SFMono-Regular, Consolas, 'Liberation Mono', monospace;
}

.form-group {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.form-label {
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  color: var(--color-text-secondary);
}

.input {
  width: 100%;
  min-width: 0;
  padding: var(--space-2) var(--space-3);
  font-size: var(--text-sm);
  color: var(--color-text-primary);
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  box-sizing: border-box;
  font-family: inherit;
}

.textarea {
  min-height: 84px;
  resize: vertical;
}

.form-error {
  margin: 0;
  font-size: var(--text-sm);
  color: var(--color-danger);
}

.empty-inline {
  padding: var(--space-3);
  border: 1px dashed var(--color-border-default);
  border-radius: var(--radius-md);
  color: var(--color-text-muted);
  font-size: var(--text-sm);
  text-align: center;
}
</style>

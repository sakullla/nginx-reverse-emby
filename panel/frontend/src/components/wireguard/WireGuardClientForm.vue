<template>
  <form class="wg-client-form" @submit.prevent="handleSubmit">
    <div class="form-group">
      <label class="form-label form-label--required">名称</label>
      <input
        v-model="form.name"
        class="input"
        :class="{ 'input--error': errors.name }"
        placeholder="phone"
        @input="errors.name = ''"
      >
      <p v-if="errors.name" class="form-error">{{ errors.name }}</p>
    </div>

    <div class="form-group">
      <label class="form-label">Allowed IPs（每行一个）</label>
      <textarea v-model="form.allowed_ips_text" class="input textarea" placeholder="0.0.0.0/0\n::/0"></textarea>
    </div>

    <div class="form-group">
      <label class="form-label">DNS（每行一个）</label>
      <textarea v-model="form.dns_text" class="input textarea" placeholder="1.1.1.1"></textarea>
    </div>

    <label class="toggle-row">
      <input v-model="form.enabled" type="checkbox" class="toggle__input">
      <span class="toggle__slider"></span>
      <span class="toggle__label">启用 Client</span>
    </label>

    <p v-if="errors.submit" class="form-error form-error--block">{{ errors.submit }}</p>

    <button type="submit" class="btn btn--primary btn--full" :disabled="isLoading">
      {{ isEdit ? '保存修改' : '创建 Client' }}
    </button>
  </form>
</template>

<script setup>
import { computed, ref } from 'vue'

const props = defineProps({
  initialData: { type: Object, default: null },
  isLoading: { type: Boolean, default: false }
})

const emit = defineEmits(['submit'])

const isEdit = computed(() => !!props.initialData?.id)

function createFormState(client = null) {
  return {
    name: client?.name || '',
    allowed_ips_text: lines(client?.allowed_ips),
    dns_text: lines(client?.dns),
    enabled: client?.enabled !== false
  }
}

function lines(items) {
  return Array.isArray(items) ? items.join('\n') : ''
}

const form = ref(createFormState(props.initialData))
const errors = ref({ name: '', submit: '' })

function splitLines(value) {
  return String(value || '')
    .split(/\r?\n/)
    .map((item) => item.trim())
    .filter(Boolean)
}

function validate() {
  errors.value = { name: '', submit: '' }
  if (!form.value.name.trim()) errors.value.name = '请输入 Client 名称'
  return !errors.value.name
}

function buildPayload() {
  const payload = {
    name: form.value.name.trim(),
    allowed_ips: splitLines(form.value.allowed_ips_text),
    enabled: form.value.enabled
  }
  const dns = splitLines(form.value.dns_text)
  if (dns.length > 0) {
    payload.dns = dns
  }
  return payload
}

function handleSubmit() {
  if (!validate()) return
  emit('submit', buildPayload())
}
</script>

<style scoped>
.wg-client-form {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.form-group {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
}

.form-label {
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  color: var(--color-text-secondary);
}

.form-label--required::after {
  content: ' *';
  color: var(--color-danger);
}

.form-error {
  margin: 0;
  font-size: var(--text-xs);
  color: var(--color-danger);
}

.form-error--block {
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-md);
  background: var(--color-danger-50);
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

.input--error {
  border-color: var(--color-danger);
}

.textarea {
  min-height: 84px;
  resize: vertical;
}

.toggle-row {
  display: flex;
  align-items: center;
  gap: var(--space-3);
}

.toggle__input {
  position: absolute;
  opacity: 0;
  width: 0;
  height: 0;
}

.toggle__slider {
  position: relative;
  width: 44px;
  height: 24px;
  background: var(--color-border-strong);
  border-radius: var(--radius-full);
  flex-shrink: 0;
}

.toggle__slider::after {
  content: '';
  position: absolute;
  top: 3px;
  left: 3px;
  width: 18px;
  height: 18px;
  border-radius: var(--radius-full);
  background: white;
  transition: transform var(--duration-fast) var(--ease-default);
}

.toggle__input:checked + .toggle__slider {
  background: var(--color-primary);
}

.toggle__input:checked + .toggle__slider::after {
  transform: translateX(20px);
}

.toggle__label {
  font-size: var(--text-sm);
  color: var(--color-text-primary);
}

.btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-4);
  border: none;
  border-radius: var(--radius-md);
  font-size: var(--text-sm);
  cursor: pointer;
  font-family: inherit;
}

.btn--primary {
  background: var(--color-primary);
  color: white;
}

.btn--full {
  width: 100%;
}

.btn:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}
</style>

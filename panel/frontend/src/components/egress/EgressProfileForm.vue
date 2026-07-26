<template>
  <form class="egress-form" @submit.prevent="handleSubmit">
    <div v-if="error" class="form-error">{{ error }}</div>

    <div class="form-grid">
      <div class="form-group">
        <label class="form-label form-label--required">名称</label>
        <input v-model="form.name" name="name" class="input" placeholder="office socks">
      </div>

      <div class="form-group">
        <label class="form-label form-label--required">类型</label>
        <select v-model="form.type" name="type" class="input" @change="error = ''">
          <option value="direct">Direct</option>
          <option value="socks">SOCKS</option>
          <option value="http">HTTP CONNECT</option>
        </select>
      </div>
    </div>

    <div v-if="usesProxyURL" class="form-group">
      <label class="form-label form-label--required">代理 URL</label>
      <input
        v-model="form.proxy_url"
        name="proxy_url"
        class="input"
        :placeholder="form.type === 'http' ? 'http://127.0.0.1:8080' : 'socks5://127.0.0.1:1080'"
        autocomplete="off"
        @input="error = ''"
      >
    </div>

    <label class="toggle-row">
      <input v-model="form.enabled" name="enabled" type="checkbox" class="toggle__input">
      <span class="toggle__slider"></span>
      <span class="toggle__label">启用 Profile</span>
    </label>

    <div class="form-group">
      <label class="form-label">描述</label>
      <textarea v-model="form.description" name="description" class="textarea textarea--short"></textarea>
    </div>

    <div class="form-actions">
      <button type="submit" class="btn btn--primary" :disabled="isLoading">
        {{ initialData ? '保存' : '创建' }}
      </button>
    </div>
  </form>
</template>

<script setup>
import { computed, ref, watch } from 'vue'

const props = defineProps({
  initialData: { type: Object, default: null },
  isLoading: { type: Boolean, default: false }
})

const emit = defineEmits(['submit'])

const error = ref('')
const form = ref(createFormState(props.initialData))
const usesProxyURL = computed(() => form.value.type === 'socks' || form.value.type === 'http')

watch(() => props.initialData, (value) => {
  form.value = createFormState(value)
  error.value = ''
}, { immediate: true })

function createFormState(initialData) {
  return {
    name: initialData?.name || '',
    type: initialData?.type || 'direct',
    proxy_url: initialData?.proxy_url || '',
    enabled: initialData?.enabled !== false,
    description: initialData?.description || ''
  }
}

function validate() {
  error.value = ''
  if (!form.value.name.trim()) {
    error.value = '请输入名称'
    return false
  }
  if (usesProxyURL.value) {
    const proxyURL = form.value.proxy_url.trim()
    if (!proxyURL) {
      error.value = '请重新输入代理 URL'
      return false
    }
  }
  return true
}

function handleSubmit() {
  if (!validate()) return

  const payload = {
    name: form.value.name.trim(),
    type: form.value.type,
    proxy_url: '',
    enabled: form.value.enabled,
    description: form.value.description.trim()
  }

  if (usesProxyURL.value) {
    payload.proxy_url = form.value.proxy_url.trim()
  }

  emit('submit', payload)
}
</script>

<style scoped>
.egress-form {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.form-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-3);
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

.form-label--required::after {
  content: ' *';
  color: var(--color-danger);
}

.input,
.textarea {
  width: 100%;
  min-width: 0;
  box-sizing: border-box;
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-surface);
  color: var(--color-text-primary);
  font-size: var(--text-sm);
  font-family: inherit;
}

.input {
  height: 36px;
}

.textarea {
  min-height: 86px;
  resize: vertical;
}

.textarea--short {
  min-height: 64px;
}

.input:focus,
.textarea:focus {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.form-error {
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-md);
  background: var(--color-danger-50);
  color: var(--color-danger);
  font-size: var(--text-sm);
}

.toggle-row {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  cursor: pointer;
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
  transition: background var(--duration-fast) var(--ease-default);
  flex-shrink: 0;
}

.toggle__slider::after {
  content: '';
  position: absolute;
  top: 3px;
  left: 3px;
  width: 18px;
  height: 18px;
  background: white;
  border-radius: var(--radius-full);
  transition: transform var(--duration-fast) var(--ease-default);
  box-shadow: var(--shadow-sm);
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

.form-actions {
  display: flex;
  justify-content: flex-end;
}

@media (max-width: 640px) {
  .form-grid {
    grid-template-columns: 1fr;
  }
}
</style>

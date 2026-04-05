<template>
  <form class='relay-listener-form' @submit.prevent='handleSubmit'>
    <div class='form-row'>
      <div class='form-group'>
        <label class='form-label form-label--required'>名称</label>
        <input v-model='form.name' class='input' :class="{ 'input--error': errors.name }" placeholder='hk-ingress-01'>
        <p v-if='errors.name' class='form-error'>{{ errors.name }}</p>
      </div>
      <div class='form-group'>
        <label class='form-label'>TLS 模式</label>
        <select v-model='form.tls_mode' class='input'>
          <option value='disabled'>关闭</option>
          <option value='server'>服务端 TLS</option>
          <option value='mtls'>双向 TLS</option>
        </select>
      </div>
    </div>

    <div class='form-row'>
      <div class='form-group'>
        <label class='form-label form-label--required'>监听地址</label>
        <input v-model='form.listen_host' class='input' placeholder='0.0.0.0'>
      </div>
      <div class='form-group'>
        <label class='form-label form-label--required'>监听端口</label>
        <input v-model.number='form.listen_port' class='input' type='number' min='1' max='65535' :class="{ 'input--error': errors.listen_port }" placeholder='9443'>
        <p v-if='errors.listen_port' class='form-error'>{{ errors.listen_port }}</p>
      </div>
    </div>

    <div class='form-group'>
      <label class='form-label'>监听证书</label>
      <select v-model='form.certificate_id' class='input'>
        <option :value='null'>不绑定证书</option>
        <option v-for='cert in certificates' :key='cert.id' :value='String(cert.id)'>
          #{{ cert.id }} {{ cert.domain }}
        </option>
      </select>
    </div>

    <div class='form-group'>
      <label class='form-label'>可信 CA 证书</label>
      <div class='checkbox-list'>
        <label v-for='cert in certificates' :key="`ca-${cert.id}`" class='checkbox-item'>
          <input :checked='trustedCaSet.has(String(cert.id))' type='checkbox' @change='toggleTrustedCa(cert.id)'>
          <span>#{{ cert.id }} {{ cert.domain }}</span>
        </label>
      </div>
    </div>

    <div class='form-group'>
      <label class='form-label'>Pin Set（每行一个）</label>
      <textarea v-model='pinSetText' class='input textarea' placeholder='sha256/abc...'></textarea>
    </div>

    <div class='form-group'>
      <label class='form-label'>标签</label>
      <input v-model='tagsText' class='input' placeholder='relay, hk, tls'>
    </div>

    <label class='toggle-row'>
      <input v-model='form.allow_self_signed' type='checkbox' class='toggle__input'>
      <span class='toggle__slider'></span>
      <span class='toggle__label'>允许上游使用自签名证书</span>
    </label>

    <label class='toggle-row'>
      <input v-model='form.enabled' type='checkbox' class='toggle__input'>
      <span class='toggle__slider'></span>
      <span class='toggle__label'>启用监听器</span>
    </label>

    <button type='submit' class='btn btn--primary btn--full' :disabled='isLoading'>
      {{ isEdit ? '保存修改' : '创建监听器' }}
    </button>
  </form>
</template>

<script setup>
import { computed, ref, watch } from 'vue'
import { useCreateRelayListener, useUpdateRelayListener } from '../hooks/useRelayListeners'
import { useCertificates } from '../hooks/useCertificates'

const props = defineProps({
  initialData: { type: Object, default: null },
  agentId: { type: [String, Object], required: true }
})

const emit = defineEmits(['success'])

const createRelayListener = useCreateRelayListener(props.agentId)
const updateRelayListener = useUpdateRelayListener(props.agentId)
const { data: certificatesData } = useCertificates(props.agentId)

const certificates = computed(() => certificatesData.value ?? [])
const isEdit = computed(() => !!props.initialData?.id)
const isLoading = computed(() => createRelayListener.isPending.value || updateRelayListener.isPending.value)

const form = ref(createDefaultForm())
const tagsText = ref('')
const pinSetText = ref('')
const trustedCaSet = ref(new Set())
const errors = ref({ name: '', listen_port: '' })

watch(
  () => props.initialData,
  (value) => {
    form.value = createFormState(value)
    tagsText.value = (form.value.tags || []).join(', ')
    pinSetText.value = (form.value.pin_set || []).join('\n')
    trustedCaSet.value = new Set((form.value.trusted_ca_certificate_ids || []).map((id) => String(id)))
    errors.value = { name: '', listen_port: '' }
  },
  { immediate: true }
)

function createDefaultForm() {
  return {
    name: '',
    listen_host: '0.0.0.0',
    listen_port: 0,
    enabled: true,
    certificate_id: null,
    tls_mode: 'disabled',
    pin_set: [],
    trusted_ca_certificate_ids: [],
    allow_self_signed: false,
    tags: []
  }
}

function createFormState(initialData) {
  if (!initialData) return createDefaultForm()
  return {
    name: initialData.name || '',
    listen_host: initialData.listen_host || '0.0.0.0',
    listen_port: initialData.listen_port || 0,
    enabled: initialData.enabled !== false,
    certificate_id: initialData.certificate_id == null ? null : String(initialData.certificate_id),
    tls_mode: initialData.tls_mode || 'disabled',
    pin_set: Array.isArray(initialData.pin_set) ? [...initialData.pin_set] : [],
    trusted_ca_certificate_ids: Array.isArray(initialData.trusted_ca_certificate_ids) ? [...initialData.trusted_ca_certificate_ids] : [],
    allow_self_signed: initialData.allow_self_signed === true,
    tags: Array.isArray(initialData.tags) ? [...initialData.tags] : []
  }
}

function toggleTrustedCa(certId) {
  const value = String(certId)
  const next = new Set(trustedCaSet.value)
  if (next.has(value)) next.delete(value)
  else next.add(value)
  trustedCaSet.value = next
}

function validate() {
  errors.value = { name: '', listen_port: '' }
  if (!form.value.name.trim()) {
    errors.value.name = '请输入监听器名称'
  }
  if (!Number.isInteger(form.value.listen_port) || form.value.listen_port < 1 || form.value.listen_port > 65535) {
    errors.value.listen_port = '监听端口必须在 1-65535 之间'
  }
  return !errors.value.name && !errors.value.listen_port
}

async function handleSubmit() {
  if (!validate()) return

  const payload = {
    name: form.value.name.trim(),
    listen_host: form.value.listen_host.trim() || '0.0.0.0',
    listen_port: form.value.listen_port,
    enabled: form.value.enabled,
    certificate_id: form.value.certificate_id == null ? null : form.value.certificate_id,
    tls_mode: form.value.tls_mode,
    pin_set: pinSetText.value
      .split(/\r?\n/)
      .map((item) => item.trim())
      .filter(Boolean),
    trusted_ca_certificate_ids: [...trustedCaSet.value],
    allow_self_signed: form.value.allow_self_signed,
    tags: tagsText.value
      .split(',')
      .map((item) => item.trim())
      .filter(Boolean)
  }

  try {
    if (isEdit.value) {
      await updateRelayListener.mutateAsync({ id: props.initialData.id, ...payload })
    } else {
      await createRelayListener.mutateAsync(payload)
    }
    emit('success')
  } catch (err) {
    errors.value.name = err?.message || '操作失败'
  }
}
</script>

<style scoped>
.relay-listener-form {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.form-row {
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
  color: var(--color-text-secondary);
  font-weight: var(--font-medium);
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

.input {
  width: 100%;
  min-width: 0;
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-surface);
  color: var(--color-text-primary);
  font-size: var(--text-sm);
  box-sizing: border-box;
}

.input--error {
  border-color: var(--color-danger);
}

.textarea {
  min-height: 88px;
  resize: vertical;
}

.checkbox-list {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
  gap: var(--space-2);
}

.checkbox-item {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  font-size: var(--text-xs);
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
  background: var(--gradient-primary);
}

.toggle__input:checked + .toggle__slider::after {
  transform: translateX(20px);
}

.toggle__label {
  font-size: var(--text-sm);
  color: var(--color-text-primary);
}

.btn {
  border: none;
  border-radius: var(--radius-md);
  padding: var(--space-2) var(--space-4);
  font-size: var(--text-sm);
  cursor: pointer;
}

.btn--primary {
  background: var(--gradient-primary);
  color: white;
}

.btn--full {
  width: 100%;
}

@media (max-width: 720px) {
  .form-row {
    grid-template-columns: 1fr;
  }
}
</style>

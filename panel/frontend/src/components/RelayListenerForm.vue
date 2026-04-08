<template>
  <form class='relay-listener-form' @submit.prevent='handleSubmit'>
    <div class='form-row'>
      <div class='form-group'>
        <label class='form-label form-label--required'>名称</label>
        <input v-model='form.name' class='input' :class="{ 'input--error': errors.name }" placeholder='relay-a'>
        <p v-if='errors.name' class='form-error'>{{ errors.name }}</p>
      </div>
      <div class='form-group'>
        <label class='form-label'>监听证书来源</label>
        <select v-model='form.certificate_source' class='input'>
          <option value='auto_relay_ca'>自动签发（Relay CA）</option>
          <option value='existing_certificate'>绑定已有证书</option>
        </select>
        <p class='form-hint'>
          默认由控制面自动签发 Relay 监听证书；只有高级场景才需要手动绑定已有证书。
        </p>
      </div>
    </div>

    <div v-if='form.certificate_source === "auto_relay_ca"' class='auto-banner'>
      <strong>默认路径：</strong> 自动签发（Relay CA） + 自动（Relay CA + Pin），无需手动维护 Pin / CA。
    </div>

    <div v-if='form.certificate_source === "existing_certificate"' class='form-group'>
      <label class='form-label' :class="{ 'form-label--required': form.enabled }">绑定监听证书</label>
      <select v-model='form.certificate_id' class='input' :class="{ 'input--error': errors.certificate_id }">
        <option :value='null'>请选择证书</option>
        <option v-for='cert in certificates' :key='cert.id' :value='cert.id'>
          #{{ cert.id }} {{ cert.domain }}
        </option>
      </select>
      <p v-if='errors.certificate_id' class='form-error'>{{ errors.certificate_id }}</p>
    </div>

    <div class='form-row'>
      <div class='form-group'>
        <label class='form-label form-label--required'>监听地址</label>
        <input v-model='form.listen_host' class='input' placeholder='0.0.0.0'>
      </div>
      <div class='form-group'>
        <label class='form-label form-label--required'>监听端口</label>
        <input
          v-model.number='form.listen_port'
          class='input'
          type='number'
          min='1'
          max='65535'
          :class="{ 'input--error': errors.listen_port }"
          placeholder='7443'
        >
        <p v-if='errors.listen_port' class='form-error'>{{ errors.listen_port }}</p>
      </div>
    </div>

    <div class='form-group'>
      <label class='form-label'>信任策略</label>
      <select v-model='form.trust_mode_source' class='input'>
        <option value='auto'>自动（Relay CA + Pin）</option>
        <option value='custom'>高级自定义</option>
      </select>
      <p class='form-hint'>
        常规情况下保持自动即可；只有需要手工控制 TLS 模式、Pin 或 CA 时再切到高级自定义。
      </p>
    </div>

    <label class='toggle-row'>
      <input
        v-model='form.allow_self_signed'
        type='checkbox'
        class='toggle__input'
        :disabled='form.trust_mode_source === "auto"'
      >
      <span class='toggle__slider'></span>
      <span class='toggle__label'>允许上游使用自签名证书</span>
    </label>

    <label class='toggle-row'>
      <input v-model='form.enabled' type='checkbox' class='toggle__input'>
      <span class='toggle__slider'></span>
      <span class='toggle__label'>启用监听器</span>
    </label>

    <button type='button' class='advanced-toggle' @click='showAdvanced = !showAdvanced'>
      {{ showAdvanced ? '收起高级设置' : '显示高级设置' }}
    </button>

    <section v-if='form.trust_mode_source === "custom" || showAdvanced' class='advanced-panel'>
      <p class='form-hint'>
        {{ form.trust_mode_source === 'auto'
          ? '自动模式下会由系统派生 Relay CA + Pin；切回高级自定义后，下面这些字段才会参与编辑和提交。'
          : '高级自定义模式会直接提交你填写的 TLS 模式、Pin Set 和 CA 配置。' }}
      </p>

      <div class='form-row'>
        <div class='form-group'>
          <label class='form-label'>TLS 模式</label>
          <select v-model='form.tls_mode' class='input' :disabled='form.trust_mode_source === "auto"'>
            <option value='pin_and_ca'>Pin + CA</option>
            <option value='pin_only'>仅证书 Pin</option>
            <option value='ca_only'>仅 CA 信任链</option>
            <option value='pin_or_ca'>证书 Pin 或 CA</option>
          </select>
        </div>
        <div class='form-group'>
          <label class='form-label'>标签</label>
          <input v-model='tagsText' class='input' placeholder='relay, shared'>
        </div>
      </div>

      <div class='form-group'>
        <label class='form-label'>Pin Set（每行一个，格式 type:value）</label>
        <textarea
          v-model='pinSetText'
          class='input textarea'
          placeholder='spki_sha256:abc123'
          :disabled='form.trust_mode_source === "auto"'
        ></textarea>
      </div>

      <div class='form-group'>
        <label class='form-label'>可信 CA 证书</label>
        <div class='checkbox-list'>
          <label v-for='cert in certificates' :key="`ca-${cert.id}`" class='checkbox-item'>
            <input
              :checked='trustedCaSet.has(Number(cert.id))'
              type='checkbox'
              :disabled='form.trust_mode_source === "auto"'
              @change='toggleTrustedCa(cert.id)'
            >
            <span>#{{ cert.id }} {{ cert.domain }}</span>
          </label>
        </div>
      </div>
    </section>

    <p v-if='errors.trust_material' class='form-error form-error--block'>{{ errors.trust_material }}</p>
    <p v-if='errors.submit' class='form-error form-error--block'>{{ errors.submit }}</p>

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
const showAdvanced = ref(false)
const tagsText = ref('')
const pinSetText = ref('')
const trustedCaSet = ref(new Set())
const errors = ref({
  name: '',
  listen_port: '',
  certificate_id: '',
  trust_material: '',
  submit: ''
})

watch(
  () => props.initialData,
  (value) => {
    form.value = createFormState(value)
    showAdvanced.value = false
    tagsText.value = (form.value.tags || []).join(', ')
    pinSetText.value = (form.value.pin_set || [])
      .map((item) => `${item.type}:${item.value}`)
      .join('\n')
    trustedCaSet.value = new Set((form.value.trusted_ca_certificate_ids || []).map((id) => Number(id)))
    resetErrors()
  },
  { immediate: true }
)

watch(
  certificates,
  (items) => {
    if (!items.length) return
    if (form.value.certificate_source === 'existing_certificate' && form.value.certificate_id == null) {
      form.value.certificate_id = Number(items[0].id)
    }
  },
  { immediate: true }
)

watch(
  () => form.value.certificate_source,
  (value, oldValue) => {
    if (value === 'auto_relay_ca') {
      form.value.certificate_id = null
      return
    }
    if (
      value === 'existing_certificate'
      && form.value.certificate_id == null
      && certificates.value.length
      && oldValue !== undefined
    ) {
      form.value.certificate_id = Number(certificates.value[0].id)
    }
  }
)

watch(
  () => form.value.trust_mode_source,
  (value, oldValue) => {
    if (value === 'auto') {
      form.value.tls_mode = 'pin_and_ca'
      form.value.allow_self_signed = true
      if (oldValue && oldValue !== 'auto') {
        pinSetText.value = ''
        trustedCaSet.value = new Set()
      }
    }
  }
)

function createDefaultForm() {
  return {
    name: '',
    listen_host: '0.0.0.0',
    listen_port: 0,
    enabled: true,
    certificate_id: null,
    certificate_source: 'auto_relay_ca',
    trust_mode_source: 'auto',
    tls_mode: 'pin_and_ca',
    pin_set: [],
    trusted_ca_certificate_ids: [],
    allow_self_signed: true,
    tags: []
  }
}

function inferCertificateSource(initialData) {
  if (initialData?.certificate_source === 'auto_relay_ca' || initialData?.certificate_source === 'existing_certificate') {
    return initialData.certificate_source
  }
  return initialData ? 'existing_certificate' : 'auto_relay_ca'
}

function inferTrustModeSource(initialData) {
  if (initialData?.trust_mode_source === 'auto' || initialData?.trust_mode_source === 'custom') {
    return initialData.trust_mode_source
  }
  if (!initialData) return 'auto'
  return 'custom'
}

function createFormState(initialData) {
  if (!initialData) return createDefaultForm()
  return {
    name: initialData.name || '',
    listen_host: initialData.listen_host || '0.0.0.0',
    listen_port: initialData.listen_port || 0,
    enabled: initialData.enabled !== false,
    certificate_id: initialData.certificate_id == null ? null : Number(initialData.certificate_id),
    certificate_source: inferCertificateSource(initialData),
    trust_mode_source: inferTrustModeSource(initialData),
    tls_mode: initialData.tls_mode || 'pin_and_ca',
    pin_set: Array.isArray(initialData.pin_set)
      ? initialData.pin_set
        .map((item) => ({
          type: String(item?.type || '').trim(),
          value: String(item?.value || '').trim()
        }))
        .filter((item) => item.type && item.value)
      : [],
    trusted_ca_certificate_ids: Array.isArray(initialData.trusted_ca_certificate_ids)
      ? initialData.trusted_ca_certificate_ids.map((id) => Number(id)).filter((id) => Number.isInteger(id))
      : [],
    allow_self_signed: initialData.allow_self_signed === true,
    tags: Array.isArray(initialData.tags) ? [...initialData.tags] : []
  }
}

function resetErrors() {
  errors.value = {
    name: '',
    listen_port: '',
    certificate_id: '',
    trust_material: '',
    submit: ''
  }
}

function toggleTrustedCa(certId) {
  if (form.value.trust_mode_source === 'auto') return
  const value = Number(certId)
  const next = new Set(trustedCaSet.value)
  if (next.has(value)) next.delete(value)
  else next.add(value)
  trustedCaSet.value = next
}

function parsePinSetRows() {
  return pinSetText.value
    .split(/\r?\n/)
    .map((item) => item.trim())
    .filter(Boolean)
    .map((row) => {
      const separator = row.indexOf(':')
      if (separator === -1) {
        return { type: 'spki_sha256', value: row }
      }
      return {
        type: row.slice(0, separator).trim(),
        value: row.slice(separator + 1).trim()
      }
    })
    .filter((item) => item.type && item.value)
}

function validateCustomTrustMaterial(pinSet, trustedCaIds) {
  if (form.value.tls_mode === 'pin_only' && pinSet.length === 0) {
    return '仅 Pin 模式至少需要一个 Pin Set'
  }
  if (form.value.tls_mode === 'ca_only' && trustedCaIds.length === 0) {
    return '仅 CA 模式至少需要一个可信 CA'
  }
  if (form.value.tls_mode === 'pin_and_ca' && (pinSet.length === 0 || trustedCaIds.length === 0)) {
    return 'Pin + CA 模式需要同时提供 Pin Set 和可信 CA'
  }
  if (form.value.tls_mode === 'pin_or_ca' && pinSet.length === 0 && trustedCaIds.length === 0) {
    return '证书 Pin 或 CA 模式至少需要提供一项信任材料'
  }
  return ''
}

function validate() {
  resetErrors()

  if (!form.value.name.trim()) {
    errors.value.name = '请输入监听器名称'
  }
  if (!Number.isInteger(form.value.listen_port) || form.value.listen_port < 1 || form.value.listen_port > 65535) {
    errors.value.listen_port = '监听端口必须在 1-65535 之间'
  }
  if (form.value.enabled && form.value.certificate_source === 'existing_certificate' && form.value.certificate_id == null) {
    errors.value.certificate_id = '启用监听器时必须绑定监听证书'
  }

  const pinSet = parsePinSetRows()
  const trustedCaIds = [...trustedCaSet.value]
  if (form.value.trust_mode_source === 'custom') {
    errors.value.trust_material = validateCustomTrustMaterial(pinSet, trustedCaIds)
  }

  return !errors.value.name && !errors.value.listen_port && !errors.value.certificate_id && !errors.value.trust_material
}

async function handleSubmit() {
  if (!validate()) return

  const pinSet = form.value.trust_mode_source === 'auto' ? [] : parsePinSetRows()
  const trustedCaIds = form.value.trust_mode_source === 'auto'
    ? []
    : [...trustedCaSet.value].map((id) => Number(id))
  const payload = {
    name: form.value.name.trim(),
    listen_host: form.value.listen_host.trim() || '0.0.0.0',
    listen_port: form.value.listen_port,
    enabled: form.value.enabled,
    certificate_id: form.value.certificate_source === 'existing_certificate'
      ? (form.value.certificate_id == null ? null : Number(form.value.certificate_id))
      : null,
    certificate_source: form.value.certificate_source,
    trust_mode_source: form.value.trust_mode_source,
    tls_mode: form.value.trust_mode_source === 'auto' ? 'pin_and_ca' : form.value.tls_mode,
    pin_set: pinSet,
    trusted_ca_certificate_ids: trustedCaIds,
    allow_self_signed: form.value.trust_mode_source === 'auto' ? true : form.value.allow_self_signed,
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
    errors.value.submit = err?.message || '操作失败'
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

.form-hint {
  margin: 0;
  font-size: var(--text-xs);
  color: var(--color-text-tertiary);
}

.auto-banner {
  padding: var(--space-3) var(--space-4);
  border-radius: var(--radius-xl);
  background: var(--color-bg-subtle);
  border: 1px solid var(--color-border-subtle);
  font-size: var(--text-sm);
  color: var(--color-text-secondary);
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
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-surface);
  color: var(--color-text-primary);
  font-size: var(--text-sm);
  box-sizing: border-box;
  font-family: inherit;
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
  padding: var(--space-2);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-surface);
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

.advanced-toggle {
  align-self: flex-start;
  border: 1px solid var(--color-border-default);
  background: var(--color-bg-surface);
  border-radius: var(--radius-md);
  padding: var(--space-2) var(--space-3);
  font-size: var(--text-sm);
  cursor: pointer;
}

.advanced-panel {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  padding: var(--space-4);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-xl);
  background: var(--color-bg-subtle);
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

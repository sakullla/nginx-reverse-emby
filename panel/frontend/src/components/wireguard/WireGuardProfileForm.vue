<template>
  <form class="wg-profile-form" @submit.prevent="handleSubmit">
    <section class="form-section">
      <div class="section-heading">
        <h3>基础配置</h3>
        <p>WireGuard 配置的核心参数</p>
      </div>

      <div class="form-row">
        <div class="form-group">
          <label class="form-label form-label--required">名称</label>
          <input
            v-model="form.name"
            class="input"
            :class="{ 'input--error': errors.name }"
            placeholder="edge-wg"
            @input="errors.name = ''"
          >
          <p v-if="errors.name" class="form-error">{{ errors.name }}</p>
        </div>
        <div class="form-group">
          <label class="form-label">监听端口</label>
          <input
            v-model.number="form.listen_port"
            class="input"
            type="number"
            min="1"
            max="65535"
            placeholder="51820"
          >
        </div>
      </div>

      <div class="form-row">
        <div class="form-group">
          <label class="form-label">Private Key</label>
          <input
            v-model="form.private_key"
            class="input"
            type="password"
            autocomplete="new-password"
            placeholder="留空自动生成"
          >
        </div>
        <div class="form-group">
          <label class="form-label">MTU</label>
          <input
            v-model.number="form.mtu"
            class="input"
            type="number"
            min="576"
            max="9000"
            placeholder="1420"
          >
        </div>
      </div>

      <div class="form-row">
        <div class="form-group">
          <label class="form-label">Addresses（每行一个）</label>
          <textarea v-model="form.addresses_text" class="input textarea" placeholder="192.168.0.109"></textarea>
          <p class="form-hint">本机实际 UDP 监听地址；留空默认监听 0.0.0.0。</p>
        </div>
        <div class="form-group">
          <label class="form-label">WG 分配地址（每行一个）</label>
          <textarea v-model="form.interface_addresses_text" class="input textarea" placeholder="10.8.0.1/24&#10;fd10:8::1/64"></textarea>
          <p class="form-hint">留空时按控制面地址池自动分配 IPv4/IPv6。</p>
        </div>
      </div>

      <div class="form-row">
        <div class="form-group">
          <label class="form-label">DNS（每行一个）</label>
          <textarea v-model="form.dns_text" class="input textarea" placeholder="1.1.1.1"></textarea>
        </div>
      </div>

      <div class="form-group">
        <label class="form-label">Public Endpoint</label>
        <input v-model="form.public_endpoint" class="input" placeholder="vpn.example.com:51820">
        <p class="form-hint">用于生成客户端配置的 Endpoint。</p>
      </div>
    </section>

    <section class="form-section form-section--compact">
      <div class="form-group">
        <label class="form-label">标签</label>
        <div class="tag-input">
          <div class="tag-input__container">
            <span v-for="(tag, index) in form.tags" :key="tag" class="tag">
              {{ tag }}
              <button type="button" class="tag__remove" @click="removeTag(index)">×</button>
            </span>
            <input
              v-model="tagInput"
              type="text"
              class="tag-input__field"
              placeholder="输入标签后回车"
              @keydown.enter.prevent="addTag"
            >
          </div>
        </div>
      </div>

      <label class="toggle-row">
        <input v-model="form.enabled" type="checkbox" class="toggle__input">
        <span class="toggle__slider"></span>
        <span class="toggle__label">启用 Profile</span>
      </label>
    </section>

    <p v-if="errors.submit" class="form-error form-error--block">{{ errors.submit }}</p>

    <button type="submit" class="btn btn--primary btn--full" :disabled="isLoading">
      {{ isEdit ? '保存修改' : '创建 Profile' }}
    </button>
  </form>
</template>

<script setup>
import { computed, ref } from 'vue'

const props = defineProps({
  initialData: { type: Object, default: null },
  isLoading: { type: Boolean, default: false }
})

const emit = defineEmits(['submit', 'cancel'])

const isEdit = computed(() => !!props.initialData?.id)
const tagInput = ref('')

function createFormState(profile = null) {
  return {
    name: profile?.name || '',
    private_key: profile?.private_key || '',
    listen_port: profile?.listen_port ?? null,
    public_endpoint: profile?.public_endpoint || '',
    addresses_text: lines(profile?.addresses),
    interface_addresses_text: lines(profile?.interface_addresses),
    dns_text: lines(profile?.dns),
    mtu: profile?.mtu ?? null,
    enabled: profile?.enabled !== false,
    tags: Array.isArray(profile?.tags) ? [...profile.tags] : []
  }
}

const form = ref(createFormState(props.initialData))
const errors = ref({ name: '', submit: '' })

function lines(items) {
  return Array.isArray(items) ? items.join('\n') : ''
}

function splitLines(value) {
  return String(value || '')
    .split(/\r?\n/)
    .map((item) => item.trim())
    .filter(Boolean)
}

function buildBindAddresses() {
  const addresses = splitLines(form.value.addresses_text)
  return addresses.length > 0 ? addresses : ['0.0.0.0']
}

function buildInterfaceAddresses() {
  const addresses = splitLines(form.value.interface_addresses_text)
  if (addresses.length > 0 || !isEdit.value) return addresses
  return Array.isArray(props.initialData?.interface_addresses)
    ? [...props.initialData.interface_addresses]
    : []
}

function addTag() {
  const tag = tagInput.value.trim()
  if (tag && !form.value.tags.includes(tag)) form.value.tags.push(tag)
  tagInput.value = ''
}

function removeTag(index) {
  form.value.tags.splice(index, 1)
}

function validate() {
  errors.value = { name: '', submit: '' }
  if (!form.value.name.trim()) errors.value.name = '请输入 Profile 名称'
  return !errors.value.name
}

function buildPayload() {
  return {
    name: form.value.name.trim(),
    mode: 'generic_wireguard',
    private_key: form.value.private_key,
    listen_port: form.value.listen_port == null || form.value.listen_port === '' ? null : Number(form.value.listen_port),
    public_endpoint: form.value.public_endpoint.trim(),
    addresses: buildBindAddresses(),
    interface_addresses: buildInterfaceAddresses(),
    peers: Array.isArray(props.initialData?.peers) ? props.initialData.peers.map((peer) => ({
      name: String(peer.name || '').trim(),
      public_key: String(peer.public_key || '').trim(),
      preshared_key: peer.preshared_key || '',
      endpoint: String(peer.endpoint || '').trim(),
      allowed_ips: Array.isArray(peer.allowed_ips) ? peer.allowed_ips : [],
      persistent_keepalive_seconds: peer.persistent_keepalive_seconds ?? null
    })) : [],
    dns: splitLines(form.value.dns_text),
    mtu: form.value.mtu == null || form.value.mtu === '' ? null : Number(form.value.mtu),
    enabled: form.value.enabled,
    tags: [...form.value.tags]
  }
}

function handleSubmit() {
  if (!validate()) return
  emit('submit', buildPayload())
}
</script>

<style scoped>
.wg-profile-form {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.form-section {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  padding: var(--space-4);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-xl);
  background: var(--color-bg-subtle);
}

.form-section--compact {
  gap: var(--space-2);
}

.section-heading h3 {
  margin: 0;
  font-size: var(--text-base);
  color: var(--color-text-primary);
}

.section-heading p {
  margin: var(--space-1) 0 0;
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
}

.section-title-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
}

.form-row {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
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

.form-hint {
  margin: 0;
  font-size: var(--text-xs);
  color: var(--color-text-muted);
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

.textarea--sm {
  min-height: 64px;
}

.tag-input {
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
}

.tag-input__container {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  padding: var(--space-1) var(--space-2);
  min-height: 40px;
  align-items: center;
}

.tag-input__field {
  flex: 1;
  min-width: 120px;
  border: none;
  background: transparent;
  padding: var(--space-1);
  font-size: var(--text-sm);
  color: var(--color-text-primary);
  outline: none;
}

.tag {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  padding: 2px 8px;
  background: var(--color-bg-subtle);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-full);
  font-size: var(--text-xs);
}

.tag__remove {
  border: none;
  background: transparent;
  color: var(--color-text-muted);
  cursor: pointer;
  padding: 0;
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

.empty-inline {
  padding: var(--space-3);
  border: 1px dashed var(--color-border-default);
  border-radius: var(--radius-md);
  color: var(--color-text-muted);
  font-size: var(--text-sm);
  text-align: center;
}

.btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  border: none;
  border-radius: var(--radius-md);
  padding: var(--space-2) var(--space-4);
  font-size: var(--text-sm);
  cursor: pointer;
  font-family: inherit;
}

.btn--secondary {
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  color: var(--color-text-primary);
}

.btn--danger {
  background: var(--color-danger-50);
  color: var(--color-danger);
}

.btn--sm {
  padding: var(--space-1) var(--space-3);
  font-size: var(--text-xs);
}

.btn--full {
  width: 100%;
}

.btn:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.btn--primary {
  background: var(--color-primary);
  color: white;
}
</style>

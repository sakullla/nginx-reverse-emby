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
          <option value="wireguard">WireGuard</option>
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

    <div v-if="form.type === 'wireguard'" class="wireguard-fields">
      <div class="form-group">
        <label class="form-label form-label--required">Private Key</label>
        <input v-model="form.private_key" name="private_key" class="input" autocomplete="off">
      </div>

      <div class="form-grid">
        <div class="form-group">
          <label class="form-label form-label--required">Addresses</label>
          <textarea v-model="form.addresses" name="addresses" class="textarea" placeholder="10.42.0.2/32"></textarea>
        </div>
        <div class="form-group">
          <label class="form-label">DNS</label>
          <textarea v-model="form.dns" name="dns" class="textarea" placeholder="1.1.1.1"></textarea>
        </div>
      </div>

      <div class="form-grid">
        <div class="form-group">
          <label class="form-label form-label--required">Peer Public Key</label>
          <input v-model="form.peer_public_key" name="peer_public_key" class="input" autocomplete="off">
        </div>
        <div class="form-group">
          <label class="form-label form-label--required">Peer Endpoint</label>
          <input v-model="form.peer_endpoint" name="peer_endpoint" class="input" placeholder="127.0.0.1:51820">
        </div>
      </div>

      <div class="form-grid">
        <div class="form-group">
          <label class="form-label form-label--required">Peer Allowed IPs</label>
          <textarea v-model="form.peer_allowed_ips" name="peer_allowed_ips" class="textarea" placeholder="0.0.0.0/0"></textarea>
        </div>
        <div class="form-group">
          <label class="form-label">MTU</label>
          <input v-model.number="form.mtu" name="mtu" class="input" type="number" min="0" max="65535" placeholder="1420">
        </div>
      </div>
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
  const wg = initialData?.wireguard_config || {}
  const peer = Array.isArray(wg.peers) ? (wg.peers[0] || {}) : {}
  return {
    name: initialData?.name || '',
    type: initialData?.type || 'direct',
    proxy_url: initialData?.proxy_url || '',
    enabled: initialData?.enabled !== false,
    description: initialData?.description || '',
    private_key: wg.private_key || '',
    addresses: linesText(wg.addresses),
    peer_public_key: peer.public_key || '',
    peer_endpoint: peer.endpoint || '',
    peer_allowed_ips: linesText(peer.allowed_ips),
    dns: linesText(wg.dns),
    mtu: wg.mtu || ''
  }
}

function linesText(value) {
  return Array.isArray(value) ? value.join('\n') : ''
}

function splitLines(value) {
  return String(value || '')
    .split(/\r?\n|,/)
    .map((item) => item.trim())
    .filter(Boolean)
}

function existingWireGuardPeer() {
  const wg = props.initialData?.wireguard_config || {}
  return Array.isArray(wg.peers) ? { ...(wg.peers[0] || {}) } : {}
}

function existingWireGuardPeers() {
  const wg = props.initialData?.wireguard_config || {}
  return Array.isArray(wg.peers) ? wg.peers.map((peer) => ({ ...peer })) : []
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
  if (form.value.type === 'wireguard') {
    if (!form.value.private_key.trim() || !splitLines(form.value.addresses).length) {
      error.value = 'WireGuard Private Key 和 Addresses 不能为空'
      return false
    }
    if (!form.value.peer_public_key.trim() || !form.value.peer_endpoint.trim() || !splitLines(form.value.peer_allowed_ips).length) {
      error.value = 'WireGuard Peer 配置不能为空'
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

  if (form.value.type === 'wireguard') {
    const peers = existingWireGuardPeers()
    const firstPeer = peers[0] || existingWireGuardPeer()
    peers[0] = {
      ...firstPeer,
      public_key: form.value.peer_public_key.trim(),
      endpoint: form.value.peer_endpoint.trim(),
      allowed_ips: splitLines(form.value.peer_allowed_ips)
    }
    payload.wireguard_config = {
      private_key: form.value.private_key.trim(),
      addresses: splitLines(form.value.addresses),
      peers,
      dns: splitLines(form.value.dns),
      mtu: Number(form.value.mtu) || 0
    }
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

.form-group,
.wireguard-fields {
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

.btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-4);
  border: none;
  border-radius: var(--radius-md);
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  cursor: pointer;
  font-family: inherit;
}

.btn--primary {
  background: var(--color-primary);
  color: white;
}

.btn:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

@media (max-width: 640px) {
  .form-grid {
    grid-template-columns: 1fr;
  }
}
</style>

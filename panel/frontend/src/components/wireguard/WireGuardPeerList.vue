<template>
  <div class="peer-section">
    <div class="peer-section__header">
      <div>
        <h3 class="peer-section__title">手动 Peers</h3>
        <p class="peer-section__subtitle">手动配置的 WireGuard 对等节点，用于兼容非本系统生成的客户端</p>
      </div>
      <button class="btn btn--primary" @click="startAdd">
        + 添加 Peer
      </button>
    </div>

    <div v-if="!peers.length" class="empty-inline">
      暂无手动 Peer，点击右上角添加
    </div>

    <div v-if="peers.length" class="peer-list">
      <div
        v-for="(peer, index) in peers"
        :key="index"
        class="peer-item"
      >
        <div class="peer-item__main">
          <div class="peer-item__identity">
            <span class="peer-index">#{{ index + 1 }}</span>
            <strong class="peer-name">{{ peer.name || '未命名' }}</strong>
          </div>
          <div class="peer-item__meta">
            <span v-if="peer.endpoint" class="peer-endpoint">{{ peer.endpoint }}</span>
            <span v-if="peer.public_key" class="peer-pubkey">{{ truncateKey(peer.public_key) }}</span>
            <span v-if="peer.allowed_ips?.length" class="peer-ips">{{ peer.allowed_ips.join(', ') }}</span>
          </div>
        </div>
        <div class="peer-item__actions">
          <BaseIconButton title="编辑" @click="startEdit(index)">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
              <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
            </svg>
          </BaseIconButton>
          <BaseIconButton tone="danger" title="删除" @click="remove(index)">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <polyline points="3 6 5 6 21 6"/>
              <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
            </svg>
          </BaseIconButton>
        </div>
      </div>
    </div>

    <BaseModal
      v-model="showModal"
      :title="editingIndex >= 0 ? '编辑 Peer' : '添加 Peer'"
      size="md"
      :close-on-click-modal="false"
    >
      <div class="peer-form">
        <div class="form-row">
          <div class="form-group">
            <label class="form-label">名称</label>
            <input v-model="form.name" class="input" placeholder="client-a">
          </div>
          <div class="form-group">
            <label class="form-label">Endpoint</label>
            <input v-model="form.endpoint" class="input" placeholder="vpn.example.com:51820">
          </div>
        </div>
        <div class="form-row">
          <div class="form-group">
            <label class="form-label">Public Key</label>
            <input v-model="form.public_key" class="input" placeholder="base64-encoded public key">
          </div>
          <div class="form-group">
            <label class="form-label">Preshared Key</label>
            <input v-model="form.preshared_key" class="input" type="password" autocomplete="new-password" placeholder="可选">
          </div>
        </div>
        <div class="form-row">
          <div class="form-group">
            <label class="form-label">Allowed IPs（每行一个）</label>
            <textarea v-model="form.allowed_ips_text" class="input textarea textarea--sm" placeholder="10.8.0.2/32"></textarea>
          </div>
          <div class="form-group">
            <label class="form-label">Keepalive 秒</label>
            <input v-model.number="form.persistent_keepalive_seconds" class="input" type="number" min="0" placeholder="25">
          </div>
        </div>
        <div class="peer-form__actions">
          <button class="btn btn--ghost" @click="cancelForm">取消</button>
          <button class="btn btn--primary" :disabled="isSaving" @click="confirmSave">
            {{ editingIndex >= 0 ? '保存修改' : '添加' }}
          </button>
        </div>
      </div>
    </BaseModal>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import BaseIconButton from '../base/BaseIconButton.vue'
import BaseModal from '../base/BaseModal.vue'

const props = defineProps({
  peers: { type: Array, default: () => [] },
  isSaving: { type: Boolean, default: false }
})

const emit = defineEmits(['save'])

const showModal = ref(false)
const editingIndex = ref(-1)

const form = ref(createEmptyForm())

function createEmptyForm() {
  return {
    name: '',
    public_key: '',
    preshared_key: '',
    endpoint: '',
    allowed_ips_text: '',
    persistent_keepalive_seconds: null
  }
}

function lines(items) {
  return Array.isArray(items) ? items.join('\n') : ''
}

function splitLines(value) {
  return String(value || '')
    .split(/\r?\n/)
    .map((item) => item.trim())
    .filter(Boolean)
}

function truncateKey(key) {
  if (!key || key.length <= 16) return key || '-'
  return key.slice(0, 8) + '...' + key.slice(-8)
}

function startAdd() {
  editingIndex.value = -1
  form.value = createEmptyForm()
  showModal.value = true
}

function startEdit(index) {
  const peer = props.peers[index]
  if (!peer) return
  editingIndex.value = index
  form.value = {
    name: peer.name || '',
    public_key: peer.public_key || '',
    preshared_key: peer.preshared_key || '',
    endpoint: peer.endpoint || '',
    allowed_ips_text: lines(peer.allowed_ips),
    persistent_keepalive_seconds: peer.persistent_keepalive_seconds ?? null
  }
  showModal.value = true
}

function cancelForm() {
  showModal.value = false
  editingIndex.value = -1
}

function confirmSave() {
  const newPeer = {
    name: form.value.name.trim(),
    public_key: form.value.public_key.trim(),
    preshared_key: form.value.preshared_key,
    endpoint: form.value.endpoint.trim(),
    allowed_ips: splitLines(form.value.allowed_ips_text),
    persistent_keepalive_seconds: form.value.persistent_keepalive_seconds == null || form.value.persistent_keepalive_seconds === ''
      ? null
      : Number(form.value.persistent_keepalive_seconds)
  }

  const nextPeers = [...props.peers]
  if (editingIndex.value >= 0) {
    nextPeers[editingIndex.value] = newPeer
  } else {
    nextPeers.push(newPeer)
  }

  emit('save', nextPeers)
  showModal.value = false
  editingIndex.value = -1
}

function remove(index) {
  const nextPeers = [...props.peers]
  nextPeers.splice(index, 1)
  emit('save', nextPeers)
}
</script>

<style scoped>
.peer-section {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  padding: var(--space-4);
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-sm);
}

.peer-section__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-3);
  flex-wrap: wrap;
}

.peer-section__title {
  margin: 0;
  font-size: var(--text-base);
  color: var(--color-text-primary);
}

.peer-section__subtitle {
  margin: var(--space-1) 0 0;
  font-size: var(--text-sm);
  color: var(--color-text-secondary);
}

.peer-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.peer-item {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-md);
  background: var(--color-bg-surface);
  transition: border-color 150ms ease, background 150ms ease;
}

.peer-item:hover {
  border-color: var(--color-border-default);
  background: var(--color-bg-subtle);
}

.peer-item__main {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.peer-item__identity {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-width: 0;
}

.peer-index {
  font-size: var(--text-xs);
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  flex-shrink: 0;
}

.peer-name {
  font-size: var(--text-sm);
  color: var(--color-text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.peer-item__meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-2);
  font-size: var(--text-xs);
  color: var(--color-text-secondary);
  min-width: 0;
}

.peer-endpoint,
.peer-pubkey,
.peer-ips {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.peer-pubkey {
  font-family: var(--font-mono);
}

.peer-item__actions {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  flex-shrink: 0;
}

.peer-form {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.peer-form__actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-3);
  margin-top: var(--space-2);
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
  min-height: 64px;
  resize: vertical;
}

.textarea--sm {
  min-height: 48px;
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

.btn--primary {
  background: var(--color-primary);
  color: white;
}

.btn--ghost {
  background: transparent;
  color: var(--color-text-secondary);
  border: 1px solid var(--color-border-default);
}

.btn:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

@media (max-width: 640px) {
  .peer-item {
    grid-template-columns: 1fr;
    align-items: stretch;
  }

  .peer-item__actions {
    justify-content: flex-end;
  }
}
</style>

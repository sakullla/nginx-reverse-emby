<template>
  <div class="wg-page">
    <div class="wg-page__header">
      <div>
        <h1 class="wg-page__title">WireGuard Profile</h1>
        <p v-if="agentId" class="wg-page__subtitle">{{ profiles.length }} 个配置 · {{ enabledCount }} 个启用</p>
        <p v-else class="wg-page__subtitle">请先选择一个节点</p>
      </div>
      <button v-if="agentId" class="btn btn--primary" @click="startCreate">
        <span>+</span>
        新建 Profile
      </button>
    </div>

    <QuickAgentSelect
      :agentId="agentId"
      :agents="allAgents"
      @update:agentId="handleAgentSelect"
    />

    <div v-if="!agentId" class="wg-page__empty">
      <p>请从上方选择一个节点来管理 WireGuard Profile</p>
      <RouterLink to="/agents" class="btn btn--primary">加入节点</RouterLink>
    </div>

    <div v-else-if="!profiles.length && !isLoading" class="wg-page__empty">
      <p>暂无 WireGuard Profile</p>
      <button class="btn btn--primary" @click="startCreate">创建第一个 Profile</button>
    </div>

    <div v-if="agentId && profiles.length" class="profile-grid">
      <article v-for="profile in profiles" :key="profile.id" class="profile-card">
        <div class="profile-card__header">
          <div>
            <h2>{{ profile.name || profile.id }}</h2>
            <p>#{{ profile.id }} · {{ profile.enabled === false ? '停用' : '启用' }}</p>
          </div>
          <span class="status-pill" :class="{ 'status-pill--off': profile.enabled === false }">
            {{ profile.enabled === false ? 'Off' : 'On' }}
          </span>
        </div>
        <dl class="profile-meta">
          <div>
            <dt>Listen</dt>
            <dd>{{ profile.listen_port || '-' }}</dd>
          </div>
          <div>
            <dt>Addresses</dt>
            <dd>{{ listText(profile.addresses) || '-' }}</dd>
          </div>
          <div>
            <dt>Peers</dt>
            <dd>{{ Array.isArray(profile.peers) ? profile.peers.length : 0 }}</dd>
          </div>
          <div>
            <dt>MTU</dt>
            <dd>{{ profile.mtu || '-' }}</dd>
          </div>
        </dl>
        <div class="tag-row">
          <span v-for="tag in profile.tags || []" :key="tag" class="tag">{{ tag }}</span>
        </div>
        <div class="profile-card__actions">
          <button class="btn btn--secondary" @click="startEdit(profile)">编辑</button>
          <button class="btn btn--danger" @click="deletingProfile = profile">删除</button>
        </div>
      </article>
    </div>

    <div v-if="isLoading" class="wg-page__empty">
      <div class="spinner"></div>
    </div>

    <BaseModal
      :model-value="showForm"
      :title="editingProfile ? '编辑 WireGuard Profile' : '新建 WireGuard Profile'"
      size="xl"
      :close-on-click-modal="false"
      @update:model-value="closeForm"
    >
      <form class="wg-form" @submit.prevent="handleSubmit">
        <div class="form-row">
          <div class="form-group">
            <label class="form-label form-label--required">名称</label>
            <input v-model="form.name" class="input" :class="{ 'input--error': errors.name }" placeholder="edge-wg">
            <p v-if="errors.name" class="form-error">{{ errors.name }}</p>
          </div>
          <div class="form-group">
            <label class="form-label">监听端口</label>
            <input v-model.number="form.listen_port" class="input" type="number" min="1" max="65535" placeholder="51820">
          </div>
        </div>

        <div class="form-row">
          <div class="form-group">
            <label class="form-label">Private Key</label>
            <input v-model="form.private_key" class="input" type="password" autocomplete="new-password" placeholder="xxxxx">
          </div>
          <div class="form-group">
            <label class="form-label">MTU</label>
            <input v-model.number="form.mtu" class="input" type="number" min="576" max="9000" placeholder="1420">
          </div>
        </div>

        <div class="form-row">
          <div class="form-group">
            <label class="form-label">Addresses（每行一个）</label>
            <textarea v-model="form.addresses_text" class="input textarea" placeholder="10.8.0.1/24"></textarea>
          </div>
          <div class="form-group">
            <label class="form-label">DNS（每行一个）</label>
            <textarea v-model="form.dns_text" class="input textarea" placeholder="1.1.1.1"></textarea>
          </div>
        </div>

        <div class="form-group">
          <div class="section-title-row">
            <label class="form-label">Peers</label>
            <button type="button" class="btn btn--secondary btn--sm" @click="addPeer">添加 Peer</button>
          </div>
          <div v-if="!form.peers.length" class="empty-inline">暂无 Peer</div>
          <div v-for="(peer, index) in form.peers" :key="peer.local_id" class="peer-panel">
            <div class="peer-panel__header">
              <strong>Peer {{ index + 1 }}</strong>
              <button type="button" class="btn btn--danger btn--sm" @click="removePeer(index)">删除</button>
            </div>
            <div class="form-row">
              <div class="form-group">
                <label class="form-label">名称</label>
                <input v-model="peer.name" class="input" placeholder="client-a">
              </div>
              <div class="form-group">
                <label class="form-label">Endpoint</label>
                <input v-model="peer.endpoint" class="input" placeholder="vpn.example.com:51820">
              </div>
            </div>
            <div class="form-row">
              <div class="form-group">
                <label class="form-label">Public Key</label>
                <input v-model="peer.public_key" class="input">
              </div>
              <div class="form-group">
                <label class="form-label">Preshared Key</label>
                <input v-model="peer.preshared_key" class="input" type="password" autocomplete="new-password" placeholder="xxxxx">
              </div>
            </div>
            <div class="form-row">
              <div class="form-group">
                <label class="form-label">Allowed IPs（每行一个）</label>
                <textarea v-model="peer.allowed_ips_text" class="input textarea textarea--sm" placeholder="10.8.0.2/32"></textarea>
              </div>
              <div class="form-group">
                <label class="form-label">Keepalive 秒</label>
                <input v-model.number="peer.persistent_keepalive_seconds" class="input" type="number" min="0" placeholder="25">
              </div>
            </div>
          </div>
        </div>

        <div class="form-group">
          <label class="form-label">标签</label>
          <div class="tag-input">
            <span v-for="(tag, index) in form.tags" :key="tag" class="tag">
              {{ tag }}
              <button type="button" class="tag__remove" @click="removeTag(index)">x</button>
            </span>
            <input
              v-model="tagInput"
              class="tag-input__field"
              placeholder="输入标签按回车..."
              @keydown.enter.prevent="addTag"
            >
          </div>
        </div>

        <label class="toggle-row">
          <input v-model="form.enabled" type="checkbox" class="toggle__input">
          <span class="toggle__slider"></span>
          <span class="toggle__label">启用 Profile</span>
        </label>

        <p v-if="errors.submit" class="form-error form-error--block">{{ errors.submit }}</p>

        <button type="submit" class="btn btn--primary btn--full" :disabled="isSaving">
          {{ editingProfile ? '保存修改' : '创建 Profile' }}
        </button>
      </form>
    </BaseModal>

    <DeleteConfirmDialog
      :show="!!deletingProfile"
      title="确认删除 WireGuard Profile"
      message="如果该 Profile 已被 Relay 或 L4 规则引用，删除可能会被后端阻止。"
      :name="deletingProfile?.name"
      confirm-text="确认删除"
      :loading="deleteProfile.isPending?.value"
      @confirm="confirmDelete"
      @cancel="deletingProfile = null"
    />
  </div>
</template>

<script setup>
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAgent } from '../context/AgentContext'
import { useAgents } from '../hooks/useAgents'
import {
  useWireGuardProfiles,
  useCreateWireGuardProfile,
  useUpdateWireGuardProfile,
  useDeleteWireGuardProfile
} from '../hooks/useWireGuardProfiles'
import QuickAgentSelect from '../components/QuickAgentSelect.vue'
import BaseModal from '../components/base/BaseModal.vue'
import DeleteConfirmDialog from '../components/DeleteConfirmDialog.vue'

const route = useRoute()
const router = useRouter()
const { selectedAgentId } = useAgent()
const { data: agentsData } = useAgents()
const allAgents = computed(() => agentsData.value ?? [])
const selectedOrRouteAgentId = computed(() => route.query.agentId || selectedAgentId.value)
const registeredAgentIds = computed(() => new Set(allAgents.value.map((agent) => String(agent.id))))
const agentId = computed(() => {
  const id = selectedOrRouteAgentId.value
  if (!id) return null
  return registeredAgentIds.value.has(String(id)) ? id : null
})

const { data: profilesData, isLoading } = useWireGuardProfiles(agentId)
const createProfile = useCreateWireGuardProfile(agentId)
const updateProfile = useUpdateWireGuardProfile(agentId)
const deleteProfile = useDeleteWireGuardProfile(agentId)

const profiles = computed(() => profilesData.value ?? [])
const enabledCount = computed(() => profiles.value.filter((profile) => profile.enabled !== false).length)
const isSaving = computed(() => createProfile.isPending.value || updateProfile.isPending.value)

const showForm = ref(false)
const editingProfile = ref(null)
const deletingProfile = ref(null)
const tagInput = ref('')
const form = ref(createFormState())
const errors = ref({ name: '', submit: '' })
let peerIdCounter = 0

watch(agentId, () => {
  closeForm()
  deletingProfile.value = null
})

function listText(items) {
  return Array.isArray(items) ? items.join(', ') : ''
}

function splitLines(value) {
  return String(value || '')
    .split(/\r?\n/)
    .map((item) => item.trim())
    .filter(Boolean)
}

function lines(items) {
  return Array.isArray(items) ? items.join('\n') : ''
}

function createPeerState(peer = {}) {
  return {
    local_id: `peer-${++peerIdCounter}`,
    name: peer.name || '',
    public_key: peer.public_key || '',
    preshared_key: peer.preshared_key || '',
    endpoint: peer.endpoint || '',
    allowed_ips_text: lines(peer.allowed_ips),
    persistent_keepalive_seconds: peer.persistent_keepalive_seconds ?? null
  }
}

function createFormState(profile = null) {
  return {
    name: profile?.name || '',
    private_key: profile?.private_key || '',
    listen_port: profile?.listen_port ?? null,
    addresses_text: lines(profile?.addresses),
    peers: Array.isArray(profile?.peers) ? profile.peers.map((peer) => createPeerState(peer)) : [],
    dns_text: lines(profile?.dns),
    mtu: profile?.mtu ?? null,
    enabled: profile?.enabled !== false,
    tags: Array.isArray(profile?.tags) ? [...profile.tags] : []
  }
}

function resetErrors() {
  errors.value = { name: '', submit: '' }
}

function handleAgentSelect(id) {
  router.replace({ query: { ...route.query, agentId: id } })
}

function startCreate() {
  editingProfile.value = null
  form.value = createFormState()
  tagInput.value = ''
  resetErrors()
  showForm.value = true
}

function startEdit(profile) {
  editingProfile.value = profile
  form.value = createFormState(profile)
  tagInput.value = ''
  resetErrors()
  showForm.value = true
}

function closeForm() {
  showForm.value = false
  editingProfile.value = null
}

function addPeer() {
  form.value.peers.push(createPeerState())
}

function removePeer(index) {
  form.value.peers.splice(index, 1)
}

function addTag() {
  const tag = tagInput.value.trim()
  if (tag && !form.value.tags.includes(tag)) form.value.tags.push(tag)
  tagInput.value = ''
}

function removeTag(index) {
  form.value.tags.splice(index, 1)
}

function buildPayload() {
  return {
    name: form.value.name.trim(),
    mode: 'generic_wireguard',
    private_key: form.value.private_key,
    listen_port: form.value.listen_port == null || form.value.listen_port === '' ? null : Number(form.value.listen_port),
    addresses: splitLines(form.value.addresses_text),
    peers: form.value.peers.map((peer) => ({
      name: peer.name.trim(),
      public_key: peer.public_key.trim(),
      preshared_key: peer.preshared_key,
      endpoint: peer.endpoint.trim(),
      allowed_ips: splitLines(peer.allowed_ips_text),
      persistent_keepalive_seconds: peer.persistent_keepalive_seconds == null || peer.persistent_keepalive_seconds === ''
        ? null
        : Number(peer.persistent_keepalive_seconds)
    })),
    dns: splitLines(form.value.dns_text),
    mtu: form.value.mtu == null || form.value.mtu === '' ? null : Number(form.value.mtu),
    enabled: form.value.enabled,
    tags: [...form.value.tags]
  }
}

function validate() {
  resetErrors()
  if (!form.value.name.trim()) errors.value.name = '请输入 Profile 名称'
  return !errors.value.name
}

async function handleSubmit() {
  if (!validate()) return
  const payload = buildPayload()
  try {
    if (editingProfile.value) {
      await updateProfile.mutateAsync({ id: editingProfile.value.id, ...payload })
    } else {
      await createProfile.mutateAsync(payload)
    }
    closeForm()
  } catch (error) {
    errors.value.submit = error?.message || '操作失败'
  }
}

function confirmDelete() {
  if (!deletingProfile.value) return
  deleteProfile.mutate(deletingProfile.value.id, {
    onSuccess: () => {
      deletingProfile.value = null
    }
  })
}
</script>

<style scoped>
.wg-page {
  max-width: 1200px;
  margin: 0 auto;
}

.wg-page__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-4);
  margin-bottom: var(--space-5);
  flex-wrap: wrap;
}

.wg-page__title {
  margin: 0 0 var(--space-1);
  font-size: 1.5rem;
  color: var(--color-text-primary);
}

.wg-page__subtitle {
  margin: 0;
  color: var(--color-text-tertiary);
  font-size: var(--text-sm);
}

.wg-page__empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-3);
  padding: 4rem 2rem;
  color: var(--color-text-muted);
  text-align: center;
}

.profile-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: var(--space-4);
}

.profile-card {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  padding: var(--space-4);
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-sm);
}

.profile-card__header,
.profile-card__actions,
.section-title-row,
.peer-panel__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
}

.profile-card h2 {
  margin: 0;
  font-size: var(--text-base);
  color: var(--color-text-primary);
}

.profile-card p {
  margin: var(--space-1) 0 0;
  font-size: var(--text-xs);
  color: var(--color-text-muted);
}

.profile-meta {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-2);
  margin: 0;
}

.profile-meta div {
  min-width: 0;
}

.profile-meta dt {
  font-size: var(--text-xs);
  color: var(--color-text-muted);
}

.profile-meta dd {
  margin: 2px 0 0;
  font-size: var(--text-sm);
  color: var(--color-text-primary);
  overflow-wrap: anywhere;
}

.status-pill {
  padding: 2px 8px;
  border-radius: var(--radius-full);
  background: var(--color-success-50);
  color: var(--color-success);
  font-size: var(--text-xs);
}

.status-pill--off {
  background: var(--color-bg-subtle);
  color: var(--color-text-muted);
}

.wg-form,
.form-group,
.peer-panel {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.form-row {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-3);
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
  min-height: 84px;
  resize: vertical;
}

.textarea--sm {
  min-height: 64px;
}

.peer-panel {
  padding: var(--space-3);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-lg);
  background: var(--color-bg-subtle);
}

.tag-row,
.tag-input {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.tag-input {
  padding: var(--space-2);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-surface);
}

.tag-input__field {
  flex: 1;
  min-width: 120px;
  border: 0;
  outline: 0;
  background: transparent;
  color: var(--color-text-primary);
}

.tag {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  padding: 2px 8px;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-full);
  background: var(--color-bg-subtle);
  color: var(--color-text-primary);
  font-size: var(--text-xs);
}

.tag__remove {
  border: 0;
  background: transparent;
  color: var(--color-text-muted);
  cursor: pointer;
}

.empty-inline {
  padding: var(--space-3);
  border: 1px dashed var(--color-border-default);
  border-radius: var(--radius-md);
  color: var(--color-text-muted);
  font-size: var(--text-sm);
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

.toggle__label,
.form-error {
  font-size: var(--text-sm);
}

.form-error {
  margin: 0;
  color: var(--color-danger);
}

.form-error--block {
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-md);
  background: var(--color-danger-50);
}

.btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  border: 0;
  border-radius: var(--radius-md);
  padding: var(--space-2) var(--space-4);
  font-size: var(--text-sm);
  cursor: pointer;
}

.btn--primary {
  background: var(--color-primary);
  color: white;
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

@media (max-width: 720px) {
  .form-row,
  .profile-meta {
    grid-template-columns: 1fr;
  }
}
</style>

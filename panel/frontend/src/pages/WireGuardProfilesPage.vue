<template>
  <div class="wg-page">
    <div class="wg-page__header">
      <div>
        <h1 class="wg-page__title">WireGuard 配置</h1>
        <p v-if="hasAgentFilter" class="wg-page__subtitle">
          共 {{ listTotal }} 个 · 本页 {{ profiles.length }} 个 · {{ enabledCount }} 个启用
        </p>
        <p v-else class="wg-page__subtitle">暂无可用节点</p>
      </div>
      <div class="wg-page__header-actions">
        <ViewToggle v-if="hasAgentFilter && (listTotal > 0 || listQ || searchQuery) && !selectedProfileId" v-model:view="view" />
        <button v-if="canCreate && !selectedProfileId" class="btn btn--primary" @click="startCreateProfile">
          <span>+</span>
          新建 Profile
        </button>
      </div>
    </div>

    <ResourceListFilterBar
      v-if="!selectedProfileId"
      :agent-id="agentFilter || ALL_AGENTS_FILTER"
      :agents="allAgents"
      :q="searchQuery"
      search-placeholder="搜索名称 / 接口 / 标签..."
      :status-fields="enabledStatusFields"
      :status-values="statusValues"
      @update:agent-id="handleAgentSelect"
      @update:q="searchQuery = $event"
      @update:status="onStatusUpdate"
    />

    <div v-if="!allAgents.length" class="wg-page__empty">
      <p>暂无可用节点</p>
      <RouterLink to="/agents" class="btn btn--primary">加入节点</RouterLink>
    </div>

    <div v-else-if="isLoading && !selectedProfileId" class="wg-page__empty">
      <div class="spinner" />
    </div>

    <!-- Profile List View -->
    <template v-else-if="hasAgentFilter && !selectedProfileId">
      <div v-if="!profiles.length" class="wg-page__empty">
        <template v-if="hasActiveFilters">
          <p>没有匹配的 WireGuard 配置</p>
        </template>
        <template v-else>
          <p>暂无 WireGuard 配置</p>
          <button v-if="canCreate" class="btn btn--primary" @click="startCreateProfile">创建第一个 Profile</button>
          <p v-else class="wg-page__subtitle">全部节点视图下请先选择具体节点再新建</p>
        </template>
      </div>

      <div v-show="view === 'card' && profiles.length" class="profile-grid">
        <WireGuardProfileCard
          v-for="profile in profiles"
          :key="profile.id"
          :profile="profile"
          :client-count="Number(profile.client_count || 0)"
          @toggle="toggleProfileEnabled"
          @edit="startEditProfile"
          @delete="deletingProfile = profile"
        />
      </div>

      <WGProfileTable
        v-show="view === 'list' && profiles.length"
        :profiles="profiles"
        @toggle="toggleProfileEnabled"
        @edit="startEditProfile"
        @delete="deletingProfile = $event"
      />

      <ListPagination
        v-if="listTotal > 0"
        :page="page"
        :page-size="pageSize"
        :total="listTotal"
        @update:page="page = $event"
      />
    </template>

    <!-- Client Management View -->
    <template v-else-if="selectedProfileId">
      <div class="client-view">
        <button class="btn btn--secondary btn--sm back-btn" @click="closeClientView">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <polyline points="15 18 9 12 15 6" />
          </svg>
          返回 Profile 列表
        </button>

        <div class="profile-summary">
          <div class="profile-summary__info">
            <h2>{{ selectedProfile?.name || selectedProfile?.id }}</h2>
            <div class="profile-summary__meta">
              <span>端口 {{ selectedProfile?.listen_port || '-' }}</span>
              <span>{{ formatList(selectedProfile?.addresses) || '-' }}</span>
              <span>{{ formatList(selectedProfile?.interface_addresses) || '-' }}</span>
              <span>{{ selectedProfile?.public_endpoint || '-' }}</span>
              <BaseBadge :tone="selectedProfile?.enabled === false ? 'neutral' : 'success'" dot>
                {{ selectedProfile?.enabled === false ? '停用' : '启用' }}
              </BaseBadge>
            </div>
          </div>
        </div>

        <section class="clients-section">
          <div class="section-title-row">
            <div>
              <h3 class="section-heading">Clients</h3>
              <p class="section-subtitle">管理客户端配置；私钥和 PSK 仅在下载的 .conf 中返回。</p>
            </div>
            <button class="btn btn--primary" @click="startCreateClient">+ 新建 Client</button>
          </div>

          <div v-if="isClientsLoading" class="empty-inline">正在加载 Clients...</div>
          <WireGuardClientList
            v-else
            :clients="clients"
            :profile-id="selectedProfileId"
            :pending-client-ids="pendingClientRowIds"
            @edit="startEditClient"
            @toggle="toggleClientEnabled"
            @download="downloadClientConfig"
            @qr="showClientQRCode"
            @copy-u-r-i="copyClientURI"
            @delete="deleteClientRow"
          />
        </section>

        <WireGuardPeerList
          :peers="manualPeers"
          :is-saving="updateProfile.isPending.value"
          @save="handlePeersSave"
        />
      </div>
    </template>

    <!-- Profile Form Modal -->
    <BaseModal
      v-model="showProfileForm"
      :title="editingProfile ? '编辑 WireGuard 配置' : '新建 WireGuard 配置'"
      size="xl"
      :close-on-click-modal="false"
    >
      <WireGuardProfileForm
        :initial-data="editingProfile"
        :is-loading="isProfileSaving"
        @submit="handleProfileSubmit"
      />
    </BaseModal>

    <!-- Client Form Modal -->
    <BaseModal
      v-model="showClientForm"
      :title="editingClient ? `编辑 Client: ${editingClient.name}` : '新建 WireGuard Client'"
      size="md"
      :close-on-click-modal="false"
    >
      <WireGuardClientForm
        :initial-data="editingClient"
        :is-loading="isClientSaving"
        @submit="handleClientSubmit"
      />
    </BaseModal>

    <!-- QR Code Modal -->
    <WireGuardClientQRModal
      v-model="showQRCodeModal"
      :client-name="qrClientName"
      :config-text="qrConfigText"
      :qr-image-u-r-l="qrImageURL"
      :error="qrError"
      :loading="qrLoading"
    />

    <!-- Delete Confirm -->
    <DeleteConfirmDialog
      :show="!!deletingProfile"
      title="确认删除 WireGuard 配置"
      message="如果该 Profile 已被 Relay 或 L4 规则引用，删除可能会被后端阻止。"
      :name="deletingProfile?.name"
      confirm-text="确认删除"
      :loading="deleteProfile.isPending?.value"
      @confirm="confirmDeleteProfile"
      @cancel="deletingProfile = null"
    />
  </div>

    <div v-if="showCreateAgentPicker" class="modal-overlay" @click.self="showCreateAgentPicker = false">
      <div class="modal create-agent-picker">
        <h3>选择新建所属节点</h3>
        <p class="create-agent-picker__hint">全部节点视图下必须指定资源归属节点。</p>
        <div class="create-agent-picker__list">
          <button
            v-for="agent in allAgents"
            :key="agent.id"
            type="button"
            class="btn btn-secondary create-agent-picker__item"
            @click="confirmCreateAgent(agent)"
          >
            {{ agent.name || agent.id }}
          </button>
        </div>
        <div class="modal-actions">
          <button type="button" class="btn btn-secondary" @click="showCreateAgentPicker = false">取消</button>
        </div>
      </div>
    </div>
</template>

<script setup>
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import QRCode from 'qrcode'
import { useAgent } from '../context/AgentContext'
import { useAgents } from '../hooks/useAgents'
import { fetchWireGuardClients, fetchWireGuardClientConfig, fetchWireGuardClientURI } from '../api'
import { messageStore } from '../stores/messages'
import {
  useWireGuardProfilesList,
  useCreateWireGuardProfile,
  useUpdateWireGuardProfile,
  useDeleteWireGuardProfile,
  useCreateWireGuardClient,
  useUpdateWireGuardClient,
  useDeleteWireGuardClient
} from '../hooks/useWireGuardProfiles'
import ResourceListFilterBar from '../components/common/ResourceListFilterBar.vue'
import BaseModal from '../components/base/BaseModal.vue'
import BaseBadge from '../components/base/BaseBadge.vue'
import DeleteConfirmDialog from '../components/DeleteConfirmDialog.vue'
import WireGuardProfileCard from '../components/wireguard/WireGuardProfileCard.vue'
import ViewToggle from '../components/common/ViewToggle.vue'
import ListPagination from '../components/common/ListPagination.vue'
import WGProfileTable from '../components/wireguard/WGProfileTable.vue'
import { useViewToggle } from '../composables/useViewToggle'
import { ALL_AGENTS_FILTER, isAllAgentsFilter, normalizeAgentFilter } from '../utils/agentFilter.js'
import { resolveCreateAgentId, resolveMutationAgentId, resolveCopyTargetAgentId } from '../utils/resolveResourceAgent.js'
import WireGuardProfileForm from '../components/wireguard/WireGuardProfileForm.vue'
import WireGuardClientList from '../components/wireguard/WireGuardClientList.vue'
import WireGuardClientForm from '../components/wireguard/WireGuardClientForm.vue'
import WireGuardClientQRModal from '../components/wireguard/WireGuardClientQRModal.vue'
import WireGuardPeerList from '../components/wireguard/WireGuardPeerList.vue'

const route = useRoute()
const router = useRouter()
const { view } = useViewToggle('wg-profiles')
const agentContext = useAgent()
const { selectedAgentId } = agentContext
const systemInfo = agentContext.systemInfo || ref(null)
const { data: agentsData } = useAgents()

const allAgents = computed(() => agentsData.value ?? [])
const registeredAgentIds = computed(() => new Set(allAgents.value.map((a) => String(a.id))))
const agentFilter = computed(() => {
  const raw = normalizeAgentFilter(route.query.agentId || selectedAgentId.value)
  if (!raw) return null
  if (isAllAgentsFilter(raw)) return raw
  return registeredAgentIds.value.has(String(raw)) ? raw : null
})
const agentId = computed(() => {
  const filter = agentFilter.value
  if (!filter || isAllAgentsFilter(filter)) return null
  return filter
})
const hasAgentFilter = computed(() => Boolean(agentFilter.value) && allAgents.value.length > 0)
const createResolve = computed(() => resolveCreateAgentId(
  agentFilter.value,
  allAgents.value,
  { systemInfo: systemInfo?.value }
))
const formAgentId = ref(agentId.value || '')
const showCreateAgentPicker = ref(false)
const canCreate = computed(() => (
  hasAgentFilter.value
  && (Boolean(createResolve.value.agentId) || createResolve.value.needsSelection)
))

const page = ref(1)
const pageSize = 20
const searchQuery = ref('')
const listQ = computed(() => {
  const raw = searchQuery.value.trim()
  if (!raw) return ''
  return raw
})

const enabledStatusValue = ref('')
const enabledFilter = computed(() => {
  if (enabledStatusValue.value === 'true') return true
  if (enabledStatusValue.value === 'false') return false
  return undefined
})
const enabledStatusFields = [
  {
    key: 'enabled',
    label: '启用状态',
    options: [
      { value: '', label: '全部' },
      { value: 'true', label: '启用' },
      { value: 'false', label: '停用' }
    ]
  }
]
const statusValues = computed(() => ({ enabled: enabledStatusValue.value }))
function onStatusUpdate({ key, value }) {
  if (key === 'enabled') enabledStatusValue.value = value == null ? '' : String(value)
}
const hasActiveFilters = computed(() => (
  Boolean(listQ.value)
  || Boolean(String(searchQuery.value || '').trim())
  || enabledStatusValue.value !== ''
))

watch([agentFilter, listQ, enabledStatusValue], () => { page.value = 1 })

const { data: profilesPage, isLoading } = useWireGuardProfilesList({
  agentFilter,
  page,
  pageSize,
  q: listQ,
  enabledFilter,
  enabled: hasAgentFilter
})
const createProfile = useCreateWireGuardProfile(agentId)
const updateProfile = useUpdateWireGuardProfile(agentId)
const deleteProfile = useDeleteWireGuardProfile(agentId)

const profiles = computed(() => profilesPage.value?.items ?? [])
const listTotal = computed(() => profilesPage.value?.total ?? 0)
const enabledCount = computed(() => profiles.value.filter((p) => p.enabled !== false).length)
const isProfileSaving = computed(() => createProfile.isPending.value || updateProfile.isPending.value)

const selectedProfileId = ref(route.params?.id || null)

watch(() => route.params?.id, (id) => {
  selectedProfileId.value = id || null
})
const selectedProfile = computed(() =>
  profiles.value.find((p) => String(p.id) === String(selectedProfileId.value)) || null
)

const {
  data: clientsData,
  isLoading: isClientsLoading
} = useQuery({
  queryKey: ['wireGuardClients', agentId, selectedProfileId],
  queryFn: () => {
    if (!agentId.value || !selectedProfileId.value) return []
    return fetchWireGuardClients(agentId.value, selectedProfileId.value)
  },
  enabled: computed(() => Boolean(agentId.value && selectedProfileId.value))
})

const clients = computed(() => clientsData.value ?? [])
const clientPublicKeys = computed(() => new Set(
  clients.value
    .map((client) => String(client.public_key || '').trim())
    .filter(Boolean)
))
const manualPeers = computed(() => {
  const peers = Array.isArray(selectedProfile.value?.peers) ? selectedProfile.value.peers : []
  if (!clientPublicKeys.value.size) return peers
  return peers.filter((peer) => !clientPublicKeys.value.has(String(peer.public_key || '').trim()))
})
const createClient = useCreateWireGuardClient(agentId, selectedProfileId)
const updateClient = useUpdateWireGuardClient(agentId, selectedProfileId)
const deleteClient = useDeleteWireGuardClient(agentId, selectedProfileId)
const isClientSaving = computed(() => createClient.isPending.value || updateClient.isPending.value)

const showProfileForm = ref(false)
const showClientForm = ref(false)
const showQRCodeModal = ref(false)
const editingProfile = ref(null)
const editingClient = ref(null)
const deletingProfile = ref(null)
const pendingClientRowIds = ref(new Set())

const qrClientName = ref('')
const qrConfigText = ref('')
const qrImageURL = ref('')
const qrError = ref('')
const qrLoading = ref(false)
let qrRequestGeneration = 0

watch(agentId, () => {
  if (route.params.id) {
    router.replace('/wireguard-profiles')
  }
  closeProfileForm()
  closeClientForm()
  closeQRCode()
  deletingProfile.value = null
  pendingClientRowIds.value = new Set()
})

watch(profiles, () => {
  if (selectedProfileId.value && !selectedProfile.value) {
    selectedProfileId.value = null
  }
})

function formatList(items) {
  return Array.isArray(items) ? items.join(', ') : ''
}


function requireMutationAgent(resource, actionLabel = '操作') {
  const resolved = resolveMutationAgentId(resource, agentFilter.value, {
    fallbackAgentId: agentId.value,
  })
  if (!resolved.agentId) {
    messageStore.error(resolved.error || `缺少节点归属，无法${actionLabel}`)
    return null
  }
  return resolved.agentId
}

function startCreate() {
  startCreateProfile()
}

function confirmCreateAgent(agent) {
  const id = String(agent?.id || agent?.agent_id || '').trim()
  if (!id) {
    messageStore.error('请选择有效节点')
    return
  }
  formAgentId.value = id
  showCreateAgentPicker.value = false
  editingProfile.value = null
  showProfileForm.value = true
}

function handleAgentSelect(id) {
  router.replace({ query: { ...route.query, agentId: id } })
}

function startCreateProfile() {
  const resolved = resolveCreateAgentId(agentFilter.value, allAgents.value, {
    systemInfo: systemInfo?.value,
  })
  if (resolved.agentId) {
    formAgentId.value = resolved.agentId
    editingProfile.value = null
    showProfileForm.value = true
    return
  }
  if (resolved.needsSelection) {
    showCreateAgentPicker.value = true
    return
  }
  messageStore.error('请先选择节点后再新建')
}

function startEditProfile(profile) {
  const target = requireMutationAgent(profile, '编辑')
  if (!target) return
  formAgentId.value = target

  editingProfile.value = profile
  showProfileForm.value = true
}

function closeProfileForm() {
  showProfileForm.value = false
  editingProfile.value = null
}

async function handleProfileSubmit(payload) {
  try {
    if (editingProfile.value) {
      const target = requireMutationAgent(editingProfile.value, '编辑')
      if (!target) return
      await updateProfile.mutateAsync({ id: editingProfile.value.id, agentId: target, ...payload })
    } else {
      const target = String(formAgentId.value || '').trim()
      if (!target) {
        messageStore.error('请先选择节点后再新建')
        return
      }
      await createProfile.mutateAsync({ agentId: target, ...payload })
    }
    closeProfileForm()
  } catch (error) {
    // Error handled by hook
  }
}

function wireGuardProfileUpdatePayload(profile, overrides = {}) {
  return {
    id: profile.id,
    name: profile.name,
    mode: profile.mode || 'generic_wireguard',
    private_key: profile.private_key || 'xxxxx',
    listen_port: profile.listen_port,
    public_endpoint: profile.public_endpoint || '',
    addresses: Array.isArray(profile.addresses) ? [...profile.addresses] : [],
    interface_addresses: Array.isArray(profile.interface_addresses) ? [...profile.interface_addresses] : [],
    peers: Array.isArray(profile.peers) ? [...profile.peers] : [],
    dns: Array.isArray(profile.dns) ? [...profile.dns] : [],
    mtu: profile.mtu,
    enabled: profile.enabled !== false,
    tags: Array.isArray(profile.tags) ? [...profile.tags] : [],
    ...overrides
  }
}

async function toggleProfileEnabled(profile) {
  const target = requireMutationAgent(profile, '启停')
  if (!target) return

  if (!profile?.id) return
  try {
    await updateProfile.mutateAsync({
      agentId: target,
      ...wireGuardProfileUpdatePayload(profile, {
        enabled: profile.enabled === false,
      }),
    })
  } catch (error) {
    // Error handled by hook
  }
}

function closeClientView() {
  router.push('/wireguard-profiles')
}

function startCreateClient() {
  editingClient.value = null
  showClientForm.value = true
}

function startEditClient(client) {
  editingClient.value = client
  showClientForm.value = true
}

function closeClientForm() {
  showClientForm.value = false
  editingClient.value = null
}

async function handleClientSubmit(payload) {
  try {
    if (editingClient.value) {
      await updateClient.mutateAsync({ clientId: editingClient.value.id, ...payload })
    } else {
      await createClient.mutateAsync(payload)
    }
    closeClientForm()
  } catch (error) {
    // Error handled by hook
  }
}

async function handlePeersSave(nextPeers) {
  if (!selectedProfile.value || !agentId.value) return
  const profile = selectedProfile.value
  try {
    await updateProfile.mutateAsync(wireGuardProfileUpdatePayload(profile, { peers: nextPeers }))
  } catch (error) {
    // Error handled by hook
  }
}

function clientRowKey(profileID, clientOrID) {
  return `${String(profileID)}:${String(typeof clientOrID === 'object' ? clientOrID?.id : clientOrID)}`
}

function isClientRowPending(client) {
  return pendingClientRowIds.value.has(clientRowKey(selectedProfileId.value, client))
}

function setClientRowPending(profileID, clientID, pending) {
  const next = new Set(pendingClientRowIds.value)
  const key = clientRowKey(profileID, clientID)
  if (pending) {
    next.add(key)
  } else {
    next.delete(key)
  }
  pendingClientRowIds.value = next
}

function toggleClientEnabled(client) {
  if (!client?.id || isClientRowPending(client)) return
  const profileID = selectedProfileId.value
  setClientRowPending(profileID, client.id, true)
  void updateClient
    .mutateAsync({ clientId: client.id, enabled: client.enabled === false })
    .finally(() => {
      setClientRowPending(profileID, client.id, false)
    })
    .catch(() => {})
}

async function downloadClientConfig(client) {
  if (!agentId.value || !selectedProfileId.value || !client?.id) return
  if (isClientRowPending(client)) return
  let url = ''
  let link = null
  try {
    const config = await fetchWireGuardClientConfig(agentId.value, selectedProfileId.value, client.id)
    const blob = new Blob([config], { type: 'text/plain;charset=utf-8' })
    url = URL.createObjectURL(blob)
    link = document.createElement('a')
    link.href = url
    link.download = `${client.name || `client-${client.id}`}.conf`
    document.body.appendChild(link)
    link.click()
  } catch (error) {
    messageStore.error(error, '下载 WireGuard Client 配置失败')
  } finally {
    if (link?.parentNode) link.remove()
    if (url) URL.revokeObjectURL(url)
  }
}

async function showClientQRCode(client) {
  if (!agentId.value || !selectedProfileId.value || !client?.id) return
  if (pendingClientRowIds.value.has(client.id)) return
  const requestGeneration = ++qrRequestGeneration
  qrClientName.value = client.name || `client-${client.id}`
  qrConfigText.value = ''
  qrImageURL.value = ''
  qrError.value = ''
  qrLoading.value = true
  showQRCodeModal.value = true
  try {
    const config = await fetchWireGuardClientConfig(agentId.value, selectedProfileId.value, client.id)
    if (!isActiveQRRequest(requestGeneration)) return
    qrConfigText.value = config
    try {
      const imageURL = await QRCode.toDataURL(config, {
        errorCorrectionLevel: 'M',
        margin: 2,
        width: 280
      })
      if (!isActiveQRRequest(requestGeneration)) return
      qrImageURL.value = imageURL
    } catch {
      if (!isActiveQRRequest(requestGeneration)) return
      qrError.value = '二维码生成失败，请使用配置文本。'
    }
  } catch (error) {
    if (!isActiveQRRequest(requestGeneration)) return
    closeQRCode()
    messageStore.error(error, '生成 WireGuard Client 二维码失败')
  } finally {
    if (isActiveQRRequest(requestGeneration)) qrLoading.value = false
  }
}

function isActiveQRRequest(requestGeneration) {
  return showQRCodeModal.value && qrRequestGeneration === requestGeneration
}

function closeQRCode() {
  qrRequestGeneration += 1
  showQRCodeModal.value = false
  qrClientName.value = ''
  qrConfigText.value = ''
  qrImageURL.value = ''
  qrError.value = ''
  qrLoading.value = false
}

async function copyTextToClipboard(text) {
  if (navigator.clipboard && window.isSecureContext) {
    try {
      await navigator.clipboard.writeText(text)
      return
    } catch {
      // Fall back below for browsers that expose Clipboard API but reject writes.
    }
  }

  const textarea = document.createElement('textarea')
  textarea.value = text
  textarea.style.position = 'fixed'
  textarea.style.left = '-999999px'
  document.body.appendChild(textarea)
  try {
    textarea.select()
    const success = document.execCommand('copy')
    if (!success) throw new Error('execCommand failed')
  } finally {
    document.body.removeChild(textarea)
  }
}

async function copyClientURI(client) {
  if (!agentId.value || !selectedProfileId.value || !client?.id) return
  if (isClientRowPending(client)) return
  try {
    const uri = await fetchWireGuardClientURI(agentId.value, selectedProfileId.value, client.id)
    await copyTextToClipboard(uri)
    messageStore.success('WireGuard URI 已复制')
  } catch (error) {
    messageStore.error(error, '复制 WireGuard URI 失败')
  }
}

function deleteClientRow(client) {
  if (!client?.id || isClientRowPending(client)) return
  const profileID = selectedProfileId.value
  setClientRowPending(profileID, client.id, true)
  void deleteClient
    .mutateAsync(client.id)
    .finally(() => {
      setClientRowPending(profileID, client.id, false)
    })
    .catch(() => {})
}

function confirmDeleteProfile() {
  if (!deletingProfile.value) return
  const target = requireMutationAgent(deletingProfile.value, '删除')
  if (!target) return
  deleteProfile.mutate(
    { id: deletingProfile.value.id, agentId: target },
    {
      onSuccess: () => {
        deletingProfile.value = null
      },
    },
  )
}

defineExpose({ selectedProfileId })
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
.wg-page__header-actions {
  display: flex;
  align-items: center;
  gap: var(--space-3);
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
  grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
  gap: var(--space-4);
}

.profile-grid,
.wg-page :deep(.rule-table) {
  animation: viewToggleIn 200ms var(--ease-default) both;
}
@keyframes viewToggleIn {
  from { opacity: 0; transform: translateY(4px); }
  to { opacity: 1; transform: translateY(0); }
}
@media (prefers-reduced-motion: reduce) {
  .profile-grid,
  .wg-page :deep(.rule-table) {
    animation: none;
  }
}

.client-view {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.back-btn {
  align-self: flex-start;
}

.profile-summary {
  padding: var(--space-4);
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-sm);
}

.profile-summary__info h2 {
  margin: 0 0 var(--space-2);
  font-size: var(--text-lg);
  color: var(--color-text-primary);
}

.profile-summary__meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-3);
  font-size: var(--text-sm);
  color: var(--color-text-secondary);
}

.clients-section {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  padding: var(--space-4);
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-sm);
}

.section-title-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  flex-wrap: wrap;
}

.section-heading {
  margin: 0;
  font-size: var(--text-lg);
  color: var(--color-text-primary);
}

.section-subtitle {
  margin: var(--space-1) 0 0;
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
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

.btn--secondary {
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  color: var(--color-text-primary);
}

.btn--sm {
  padding: var(--space-1) var(--space-3);
  font-size: var(--text-xs);
}

.btn:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.spinner {
  width: 40px;
  height: 40px;
  border: 3px solid var(--color-border-subtle);
  border-top-color: var(--color-primary);
  border-radius: 50%;
  animation: spin 1s linear infinite;
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}
</style>

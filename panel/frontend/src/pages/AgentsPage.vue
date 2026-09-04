<template>
  <div class="agents-page">
    <div class="agents-page__header">
      <div class="agents-page__header-left">
        <h1 class="agents-page__title">节点管理</h1>
        <p class="agents-page__subtitle">{{ agents.length }} 个节点 · {{ onlineCount }} 在线 · 累计 {{ totalHttpRules }} HTTP 规则 · 累计 {{ totalL4Rules }} L4 规则</p>
      </div>
      <div class="agents-page__header-right">
        <div class="search-wrapper" v-if="agents.length" @click="focusSearch">
          <svg class="search-icon-btn" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>
          <input ref="searchInputRef" v-model="searchQuery" name="agent-search" class="search-input" placeholder="搜索节点名称 / IP / 标签 / #id=...">
          <button v-if="searchQuery" class="clear-btn" @click.stop="searchQuery = ''">
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
          </button>
        </div>
        <button v-if="selectedAgentId" class="btn btn-secondary" :disabled="applying" @click="handleApply">
          <svg v-if="!applying" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><polyline points="23 4 23 10 17 10"/><polyline points="1 20 1 14 7 14"/><path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"/></svg>
          <svg v-else width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 12a9 9 0 1 1-6.219-8.56"/></svg>
          {{ applying ? '推送中...' : '推送配置' }}
        </button>
        <button class="btn btn-primary" @click="openJoinModal">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
            <line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/>
          </svg>
          加入节点
        </button>
      </div>
    </div>

    <OperationStatusList />

    <!-- Filter Bar -->
    <AgentFilterBar
      v-model:view="view"
      v-model:status-filter="statusFilter"
      v-model:mode-filter="modeFilter"
      v-model:tag-filter="tagFilter"
      v-model:sort-field="sortField"
      v-model:sort-order="sortOrder"
      :available-tags="availableTags"
      :has-active-filters="hasActiveFilters"
      @clear-filters="clearFilters"
      @toggle-sort-order="toggleSortOrder"
    />

    <!-- Empty with filters -->
    <div v-if="agents.length && !filteredAgents.length" class="agents-page__empty">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>
      </svg>
      <p>没有符合筛选条件的节点</p>
      <button class="btn btn-secondary" @click="clearFilters">清除筛选</button>
    </div>

    <!-- Monitor View -->
    <transition-group
      v-else-if="view === 'monitor' && filteredAgents.length"
      name="agent-list"
      tag="div"
      class="agent-grid"
    >
      <AgentMonitorCard
        v-for="agent in filteredAgents"
        :key="agent.id"
        :agent="agent"
        @details="agent => router.push(`/agents/${agent.id}`)"
      />
    </transition-group>

    <!-- List View -->
    <AgentTable
      v-else-if="view === 'list' && filteredAgents.length"
      :agents="filteredAgents"
      :clickable="true"
      @click="agent => router.push(`/agents/${agent.id}`)"
      @rename="startEdit"
      @delete="startDelete"
    />

    <div v-if="!agents.length && !isLoading" class="agents-page__empty">
      <p>暂无节点</p>
    </div>

    <div v-if="isLoading" class="agents-page__loading">
      <div class="spinner"></div>
    </div>

    <BaseModal
      :model-value="showJoinModal"
      title="加入 Agent 节点"
      subtitle="复制命令到目标主机执行，节点上线后会出现在列表中"
      size="lg"
      :close-on-click-modal="true"
      @update:model-value="onJoinModalChange"
    >
      <div class="join-modal">
        <section class="join-section">
          <div class="join-section__label">目标平台</div>
          <div class="join-platforms" role="tablist" aria-label="目标平台">
            <button
              v-for="p in platforms"
              :key="p.id"
              type="button"
              class="join-platform"
              :class="{ 'join-platform--active': selectedPlatform === p.id }"
              role="tab"
              :aria-selected="selectedPlatform === p.id"
              @click="selectedPlatform = p.id"
            >
              <span class="join-platform__icon" aria-hidden="true">
                <svg v-if="p.id === 'linux'" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="2" y="3" width="20" height="14" rx="2"/><path d="M8 21h8"/><path d="M12 17v4"/></svg>
                <svg v-else-if="p.id === 'macos'" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 3c1.5 0 3 1 3.5 2.5C16 7 15 8.5 13.5 9"/><path d="M9 20c-2 0-4-2-4-5 0-3 2-5 5-6 1 0 2 .3 3 .8"/><path d="M15 20c2 0 4-2 4-5 0-2.5-1.5-4.5-3.5-5.5"/></svg>
                <svg v-else width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="4" width="18" height="12" rx="1"/><path d="M8 20h8"/><path d="M12 16v4"/></svg>
              </span>
              <span>{{ p.label }}</span>
            </button>
          </div>
        </section>

        <section class="join-section">
          <div class="join-section__head">
            <div class="join-section__label">加入命令</div>
            <button
              type="button"
              class="btn btn--primary btn--sm"
              :disabled="!canCopyJoinCommand"
              :class="{ 'btn--copied': copied }"
              @click="copyCommand"
            >
              <svg v-if="!copied" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                <rect x="9" y="9" width="13" height="13" rx="2"/>
                <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/>
              </svg>
              <svg v-else width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                <polyline points="20 6 9 17 4 12"/>
              </svg>
              {{ copied ? '已复制' : (selectedPlatform === 'windows' ? '复制令牌' : '复制命令') }}
            </button>
          </div>
          <div
            class="join-command-card"
            :class="{
              'join-command-card--loading': joinCommandState === 'loading',
              'join-command-card--unavailable': joinCommandState === 'unavailable',
            }"
          >
            <code class="join-command-line">{{ displayJoinCommand }}</code>
          </div>
        </section>

        <section class="join-section">
          <label class="join-token-card" :class="{ 'join-token-card--active': useOneTimeToken }">
            <input
              class="join-token-card__checkbox"
              type="checkbox"
              :checked="useOneTimeToken"
              @change="onOneTimeTokenToggle($event.target.checked)"
            >
            <span class="join-token-card__body">
              <span class="join-token-card__title-row">
                <strong>一次性令牌</strong>
                <span class="join-token-card__badge">默认</span>
              </span>
              <span class="join-token-card__desc">
                勾选后签发 10 分钟有效的一次性登记令牌；取消后改用固定令牌（兼容旧式注册，不签发 Relay mTLS）
              </span>
            </span>
          </label>

          <div
            class="join-status"
            :class="{
              'join-status--error': Boolean(joinStatusTone === 'error'),
              'join-status--ok': joinStatusTone === 'ok',
              'join-status--loading': joinStatusTone === 'loading',
            }"
          >
            <div class="join-status__main">
              <span class="join-status__dot" aria-hidden="true"></span>
              <span class="join-status__text">{{ joinStatusText }}</span>
            </div>
            <button
              v-if="useOneTimeToken && !joinTokenBusy"
              type="button"
              class="btn btn--secondary btn--sm"
              @click="createJoinEnrollmentToken"
            >{{ joinTokenError ? '重试生成' : '重新生成' }}</button>
          </div>
        </section>

        <section class="join-section">
          <div class="join-section__label">执行步骤</div>
          <ol class="join-steps">
            <li v-for="(step, index) in getCurrentSteps()" :key="step">
              <span class="join-steps__index">{{ index + 1 }}</span>
              <span class="join-steps__text">{{ step }}</span>
            </li>
          </ol>
        </section>
      </div>
    </BaseModal>

    <!-- Edit Modal -->
    <Teleport to="body">
      <div v-if="editingAgent" class="modal-overlay">
        <div class="modal">
          <div class="modal__header">
            <div class="edit-modal__heading">
              <span>编辑节点</span>
              <span class="edit-modal__subtitle">{{ editingAgent.name || editingAgent.id }}</span>
            </div>
            <button class="modal__close" aria-label="关闭" @click="editingAgent = null">✕</button>
          </div>
          <div class="modal__body">
            <div class="form-group">
              <label>节点名称</label>
              <input v-model="editName" class="input-base" placeholder="输入节点名称" @keyup.enter="confirmEdit" />
            </div>
            <div v-if="!editingAgent?.is_local" class="form-group">
              <label>出网代理</label>
              <input
                v-model="editOutboundProxy"
                class="input-base"
                placeholder="socks://user:pass@127.0.0.1:1080"
                @keyup.enter="confirmEdit"
              />
              <span class="edit-modal__hint">可选；节点访问外网时经由该代理</span>
            </div>
            <div v-if="!editingAgent?.is_local" class="form-group">
              <label>标签</label>
              <div class="edit-modal__tag-editor">
                <span v-for="(tag, index) in editTags" :key="tag" class="tag">
                  {{ tag }}
                  <button
                    type="button"
                    class="edit-modal__tag-remove"
                    :aria-label="`移除标签 ${tag}`"
                    @click="removeEditTag(index)"
                  >
                    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                      <line x1="18" y1="6" x2="6" y2="18"/>
                      <line x1="6" y1="6" x2="18" y2="18"/>
                    </svg>
                  </button>
                </span>
                <input
                  v-model="editTagInput"
                  class="edit-modal__tag-input"
                  placeholder="回车添加，如 edge / hk"
                  @keydown.enter.prevent="addEditTag"
                />
              </div>
            </div>
          </div>
          <div class="modal__footer">
            <button class="btn btn-secondary" @click="editingAgent = null">取消</button>
            <button class="btn btn-primary" :disabled="updateAgent.isPending.value" @click="confirmEdit">
              {{ updateAgent.isPending.value ? '保存中...' : '保存' }}
            </button>
          </div>
        </div>
      </div>
    </Teleport>

    <DeleteConfirmDialog
      :show="!!deletingAgent"
      title="确认删除节点"
      message="删除后该节点将立即下线，相关的规则和配置将无法恢复。"
      :name="deletingAgent?.name"
      confirm-text="确认删除"
      :loading="deleteAgent.isPending?.value"
      @confirm="confirmDelete"
      @cancel="deletingAgent = null"
    />
  </div>
</template>

<script setup>
import { ref, computed, watch, onScopeDispose } from 'vue'
import { useRouter } from 'vue-router'
import { useAgents, useUpdateAgent, useDeleteAgent } from '../hooks/useAgents'
import { useAgentMonitorStream } from '../hooks/useAgentMonitorStream'
import { mergeAgentsWithMonitor } from '../utils/agentMonitor.js'
import { buildOutboundProxyPayload } from './outboundProxyURL.js'
import { useAgentFilters } from '../hooks/useAgentFilters'
import AgentFilterBar from '../components/AgentFilterBar.vue'
import AgentMonitorCard from '../components/AgentMonitorCard.vue'
import AgentTable from '../components/AgentTable.vue'
import BaseModal from '../components/base/BaseModal.vue'
import DeleteConfirmDialog from '../components/DeleteConfirmDialog.vue'
import OperationStatusList from '../components/operations/OperationStatusList.vue'
import { fetchSystemInfo, applyConfig } from '../api'
import { createPkiEnrollmentToken } from '../api/pki'
import { useAgent } from '../context/AgentContext'
import { messageStore } from '../stores/messages'

const router = useRouter()
const { selectedAgentId } = useAgent()

const { data, isLoading } = useAgents()
const updateAgent = useUpdateAgent()
const deleteAgent = useDeleteAgent()

const monitorStreamEnabled = ref(false)
const { data: monitorAgents } = useAgentMonitorStream({ enabled: monitorStreamEnabled })

const agents = computed(() => mergeAgentsWithMonitor(data.value, monitorAgents.value))

// Filter/sort state
const {
  view,
  statusFilter,
  modeFilter,
  tagFilter,
  sortField,
  sortOrder,
  searchQuery,
  availableTags,
  filteredAgents,
  hasActiveFilters,
  clearFilters,
  toggleSortOrder
} = useAgentFilters(agents)

watch(view, () => {
  monitorStreamEnabled.value = view.value === 'monitor'
}, { immediate: true })

const showJoinModal = ref(false)
const selectedPlatform = ref('linux')
const copied = ref(false)
const useOneTimeToken = ref(true)
const joinEnrollmentToken = ref(null)
const joinTokenBusy = ref(false)
const joinTokenError = ref('')
let joinTokenRequest = 0
const editingAgent = ref(null)
const editName = ref('')
const editOutboundProxy = ref('')
const editTags = ref([])
const editTagInput = ref('')
const deletingAgent = ref(null)
const applying = ref(false)

// Scope disposal guard for async callbacks and timers
let disposed = false
let copyTimeout = null

function clearCopyTimeout() {
  if (copyTimeout) {
    clearTimeout(copyTimeout)
    copyTimeout = null
  }
}

onScopeDispose(() => {
  disposed = true
  clearCopyTimeout()
})

// Search
const searchInputRef = ref(null)
function focusSearch() { searchInputRef.value?.focus() }

async function handleApply() {
  if (!selectedAgentId.value || applying.value) return
  applying.value = true
  try {
    await applyConfig(selectedAgentId.value)
  } finally {
    if (!disposed) {
      applying.value = false
    }
  }
}

const systemInfo = ref(null)
fetchSystemInfo().then(info => {
  if (!disposed) {
    systemInfo.value = info
  }
}).catch(() => {})

const activeJoinToken = computed(() => useOneTimeToken.value
  ? joinEnrollmentToken.value?.token || ''
  : systemInfo.value?.master_register_token || '')

const joinScriptUrl = computed(() => {
  const origin = typeof window !== 'undefined' ? window.location.origin : ''
  return `${origin}/panel-api/public/join-agent.sh`
})

const joinCommandModeFlag = computed(() => (useOneTimeToken.value ? '' : '--fixed-register-token'))

const joinInstallFlag = computed(() => {
  if (selectedPlatform.value === 'macos') return '--install-launchd'
  if (selectedPlatform.value === 'linux') return '--install-systemd'
  return ''
})

const joinCommandState = computed(() => {
  if (useOneTimeToken.value && joinTokenBusy.value) return 'loading'
  if (activeJoinToken.value) return 'ready'
  return 'unavailable'
})

const displayJoinCommand = computed(() => {
  if (selectedPlatform.value === 'windows') {
    if (activeJoinToken.value) return activeJoinToken.value
    if (joinCommandState.value === 'loading') return '正在创建令牌...'
    return joinTokenError.value || 'TOKEN_UNAVAILABLE'
  }
  const token = activeJoinToken.value
    || (joinCommandState.value === 'loading' ? '正在创建令牌...' : 'TOKEN_UNAVAILABLE')
  const modeFlag = joinCommandModeFlag.value ? ` ${joinCommandModeFlag.value}` : ''
  return `curl -fsSL ${joinScriptUrl.value} | sh -s -- --register-token '${token}'${modeFlag} ${joinInstallFlag.value}`
})

const canCopyJoinCommand = computed(() => Boolean(activeJoinToken.value))

const joinStatusTone = computed(() => {
  if (useOneTimeToken.value) {
    if (joinTokenBusy.value) return 'loading'
    if (joinTokenError.value || !joinEnrollmentToken.value) return 'error'
    return 'ok'
  }
  return activeJoinToken.value ? 'ok' : 'error'
})

const joinStatusText = computed(() => {
  if (useOneTimeToken.value) {
    if (joinTokenBusy.value) return '正在创建一次性登记令牌…'
    if (joinTokenError.value) return joinTokenError.value
    if (joinEnrollmentToken.value) {
      return `一次性令牌可用 · 默认 10 分钟有效 · 有效期至 ${new Date(joinEnrollmentToken.value.expires_at).toLocaleString()}`
    }
    return '尚未取得一次性令牌，请重新生成后再复制命令。'
  }
  if (activeJoinToken.value) return '已使用固定令牌；可重复加入，但不会签发 Relay mTLS 凭据。'
  return '固定令牌不可用：请检查控制面 master register token 配置后重试。'
})

async function createJoinEnrollmentToken() {
  if (joinTokenBusy.value) return
  const request = ++joinTokenRequest
  joinTokenBusy.value = true
  joinTokenError.value = ''
  joinEnrollmentToken.value = null
  try {
    const token = await createPkiEnrollmentToken({ scope: 'new_agent' })
    if (request === joinTokenRequest && showJoinModal.value && useOneTimeToken.value) {
      joinEnrollmentToken.value = token
    }
  } catch (error) {
    if (request === joinTokenRequest) {
      joinTokenError.value = error?.message || '一次性登记令牌创建失败，请检查内部 PKI 是否可用后重试'
    }
  } finally {
    if (!disposed && request === joinTokenRequest) joinTokenBusy.value = false
  }
}

function openJoinModal() {
  showJoinModal.value = true
  useOneTimeToken.value = true
  createJoinEnrollmentToken()
}

function closeJoinModal() {
  joinTokenRequest += 1
  showJoinModal.value = false
  joinTokenBusy.value = false
  joinEnrollmentToken.value = null
  joinTokenError.value = ''
}

function onJoinModalChange(open) {
  if (open) {
    showJoinModal.value = true
    return
  }
  closeJoinModal()
}

function onOneTimeTokenToggle(checked) {
  copied.value = false
  if (!checked) {
    joinTokenRequest += 1
    joinTokenBusy.value = false
    joinEnrollmentToken.value = null
    joinTokenError.value = ''
    useOneTimeToken.value = false
    return
  }
  useOneTimeToken.value = true
  if (!joinEnrollmentToken.value) createJoinEnrollmentToken()
}

const platforms = computed(() => [
  {
    id: 'linux',
    label: 'Linux',
    steps: [
      '在目标主机上执行命令',
      '脚本会下载 Go nre-agent 二进制',
      '自动注册并安装 systemd 服务',
      '节点上线后会出现在列表中',
    ],
  },
  {
    id: 'macos',
    label: 'macOS',
    steps: [
      '在目标主机上执行命令',
      '脚本会下载 Go nre-agent 二进制',
      '自动注册并安装 launchd 服务',
    ],
  },
  {
    id: 'windows',
    label: 'Windows',
    steps: [
      '准备单独构建或发布的 nre-agent.exe',
      '获取控制面的 register token 或已生成的 agent_token',
      '在 Windows 服务或计划任务中启动 agent 并确保可访问控制面',
    ],
  },
])

const onlineCount = computed(() => agents.value.filter(a => a.status === 'online').length)

const totalHttpRules = computed(() => {
  return (agents.value || []).reduce((sum, a) => sum + (a.http_rules_count || 0), 0)
})
const totalL4Rules = computed(() => {
  return (agents.value || []).reduce((sum, a) => sum + (a.l4_rules_count || 0), 0)
})

function getCurrentSteps() {
  return platforms.value.find(p => p.id === selectedPlatform.value)?.steps || []
}

async function copyCommand() {
  if (!activeJoinToken.value) {
    messageStore.error('命令尚未就绪')
    return
  }
  const text = displayJoinCommand.value
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text)
    } else {
      const textarea = document.createElement('textarea')
      textarea.value = text
      textarea.style.position = 'fixed'
      textarea.style.left = '-999999px'
      document.body.appendChild(textarea)
      textarea.select()
      const success = document.execCommand('copy')
      document.body.removeChild(textarea)
      if (!success) throw new Error('execCommand failed')
    }
    messageStore.success(selectedPlatform.value === 'windows' ? '已复制令牌' : '已复制到剪贴板')
    copied.value = true
    clearCopyTimeout()
    copyTimeout = setTimeout(() => {
      copyTimeout = null
      if (!disposed) {
        copied.value = false
      }
    }, 1500)
  } catch (err) {
    console.error('Copy failed:', err)
    messageStore.error('复制失败，请手动选择复制')
  }
}

function startEdit(agent) {
  editingAgent.value = agent
  editName.value = agent.name
  editOutboundProxy.value = agent.is_local ? '' : agent.outbound_proxy_url || ''
  editTags.value = agent.is_local ? [] : (Array.isArray(agent.tags) ? [...agent.tags] : [])
  editTagInput.value = ''
}

function addEditTag() {
  const tag = editTagInput.value.trim()
  if (tag && !editTags.value.includes(tag)) {
    editTags.value.push(tag)
  }
  editTagInput.value = ''
}

function removeEditTag(index) {
  editTags.value.splice(index, 1)
}

async function confirmEdit() {
  if (!editingAgent.value) return
  const payload = {}
  const name = editName.value.trim()
  if (name && name !== editingAgent.value.name) {
    payload.name = name
  }
  if (!editingAgent.value.is_local) {
    try {
      const proxyPayload = buildOutboundProxyPayload(
        editingAgent.value.outbound_proxy_url,
        editOutboundProxy.value
      )
      Object.assign(payload, proxyPayload)
    } catch (error) {
      messageStore.warning(error.message, '出网代理密码已隐藏')
      editingAgent.value = null
      editName.value = ''
      editOutboundProxy.value = ''
      return
    }
    addEditTag()
    const currentTags = Array.isArray(editingAgent.value.tags) ? editingAgent.value.tags : []
    if (JSON.stringify(editTags.value) !== JSON.stringify(currentTags)) {
      payload.tags = [...editTags.value]
    }
  }
  if (Object.keys(payload).length > 0) {
    await updateAgent.mutateAsync({
      agentId: editingAgent.value.id,
      payload
    })
  }
  editingAgent.value = null
  editName.value = ''
  editOutboundProxy.value = ''
}

function startDelete(agent) {
  deletingAgent.value = agent
}

function confirmDelete() {
  if (deletingAgent.value) {
    deleteAgent.mutate(deletingAgent.value.id)
  }
  deletingAgent.value = null
}
</script>

<style scoped>
.agents-page {
  max-width: 1280px;
  margin: 0 auto;
  animation: fadeInUp var(--duration-normal) var(--ease-default) both;
}

.agents-page__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 1.5rem;
  gap: 1rem;
  flex-wrap: wrap;
}

.agents-page__header-left { flex: 1; min-width: 0; }

.agents-page__header-right {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  flex-shrink: 0;
  flex-wrap: wrap;
}

.agents-page__title {
  font-size: 1.5rem;
  font-weight: 700;
  margin: 0 0 0.25rem;
  color: var(--color-text-primary);
  letter-spacing: -0.02em;
}

.agents-page__subtitle {
  font-size: 0.875rem;
  color: var(--color-text-tertiary);
  margin: 0;
}

.agent-list-move {
  transition: transform 0.3s ease;
}

/* Card grid */
.agent-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: 1.25rem;
  /* Avoid equal-height stretch: shorter cards otherwise show empty footer gaps. */
  align-items: start;
}

@media (min-width: 1280px) {
  .agent-grid { grid-template-columns: repeat(auto-fill, minmax(340px, 1fr)); }
}

.agents-page__empty,
.agents-page__loading {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 0.75rem;
  padding: 4rem 2rem;
  color: var(--color-text-muted);
  text-align: center;
  border: 1.5px dashed var(--color-border-default);
  border-radius: var(--radius-2xl);
  animation: fadeIn 0.3s var(--ease-default) both;
}

.agents-page__loading {
  border: none;
}

/* Join modal redesign */
.join-modal {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.join-section {
  display: flex;
  flex-direction: column;
  gap: 0.55rem;
}

.join-section__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
}

.join-section__label {
  font-size: 0.75rem;
  font-weight: 700;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--color-text-tertiary);
}

.join-platforms {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 0.5rem;
}

.join-platform {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 0.4rem;
  min-height: 2.5rem;
  padding: 0.55rem 0.7rem;
  border: 1px solid var(--color-border-default);
  border-radius: 0.75rem;
  background: var(--color-bg-surface);
  color: var(--color-text-secondary);
  font: inherit;
  font-size: 0.875rem;
  font-weight: 600;
  cursor: pointer;
  transition: border-color var(--duration-fast) var(--ease-default),
              color var(--duration-fast) var(--ease-default),
              background var(--duration-fast) var(--ease-default),
              box-shadow var(--duration-fast) var(--ease-default);
}

.join-platform:hover {
  border-color: color-mix(in srgb, var(--color-primary) 35%, var(--color-border-default));
  color: var(--color-primary);
}

.join-platform--active {
  border-color: color-mix(in srgb, var(--color-primary) 55%, var(--color-border-default));
  background: color-mix(in srgb, var(--color-primary-subtle) 75%, var(--color-bg-surface));
  color: var(--color-primary);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--color-primary) 12%, transparent);
}

.join-platform__icon {
  display: inline-flex;
  opacity: 0.9;
}

.join-command-card {
  padding: 0.85rem 1rem;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-xl);
  background:
    linear-gradient(
      180deg,
      color-mix(in srgb, var(--color-bg-subtle) 70%, var(--color-bg-surface)) 0%,
      var(--color-bg-surface) 100%
    );
  overflow-x: auto;
}

.join-command-card--loading {
  border-color: color-mix(in srgb, var(--color-primary) 22%, var(--color-border-default));
}

.join-command-card--unavailable {
  border-color: color-mix(in srgb, var(--color-danger) 18%, var(--color-border-default));
}

.join-command-line {
  display: block;
  font-family: var(--font-mono);
  font-size: 0.8125rem;
  line-height: 1.6;
  color: var(--color-text-primary);
  white-space: nowrap;
  word-break: normal;
}

.join-token-card {
  display: flex;
  align-items: flex-start;
  gap: 0.7rem;
  padding: 0.8rem 0.9rem;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  background: var(--color-bg-surface);
  cursor: pointer;
  transition: border-color var(--duration-fast) var(--ease-default),
              background var(--duration-fast) var(--ease-default),
              box-shadow var(--duration-fast) var(--ease-default);
}

.join-token-card--active {
  border-color: color-mix(in srgb, var(--color-primary) 42%, var(--color-border-default));
  background: color-mix(in srgb, var(--color-primary-subtle) 55%, var(--color-bg-surface));
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--color-primary) 10%, transparent);
}

.join-token-card__checkbox {
  margin-top: 0.2rem;
  width: 1.05rem;
  height: 1.05rem;
  accent-color: var(--color-primary);
  flex-shrink: 0;
}

.join-token-card__body {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
  min-width: 0;
}

.join-token-card__title-row {
  display: flex;
  align-items: center;
  gap: 0.45rem;
}

.join-token-card__title-row strong {
  color: var(--color-text-primary);
  font-size: 0.9rem;
}

.join-token-card__badge {
  display: inline-flex;
  align-items: center;
  padding: 0.1rem 0.4rem;
  border-radius: 999px;
  background: color-mix(in srgb, var(--color-primary) 12%, transparent);
  color: var(--color-primary);
  font-size: 0.68rem;
  font-weight: 700;
  letter-spacing: 0.02em;
}

.join-token-card__desc {
  color: var(--color-text-tertiary);
  font-size: 0.75rem;
  line-height: 1.45;
}

.join-status {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
  padding: 0.7rem 0.85rem;
  border-radius: var(--radius-xl);
  border: 1px solid var(--color-border-subtle);
  background: var(--color-bg-subtle);
  color: var(--color-text-secondary);
  font-size: 0.8125rem;
  line-height: 1.45;
}

.join-status__main {
  display: flex;
  align-items: flex-start;
  gap: 0.5rem;
  min-width: 0;
  flex: 1;
}

.join-status__dot {
  width: 0.5rem;
  height: 0.5rem;
  margin-top: 0.4rem;
  border-radius: 50%;
  background: var(--color-text-tertiary);
  flex-shrink: 0;
}

.join-status__text {
  min-width: 0;
}

.join-status--ok {
  background: color-mix(in srgb, var(--color-success) 8%, var(--color-bg-surface));
  border-color: color-mix(in srgb, var(--color-success) 22%, var(--color-border-default));
  color: color-mix(in srgb, var(--color-success) 70%, var(--color-text-primary));
}

.join-status--ok .join-status__dot {
  background: var(--color-success);
}

.join-status--loading {
  background: color-mix(in srgb, var(--color-primary) 7%, var(--color-bg-surface));
  border-color: color-mix(in srgb, var(--color-primary) 20%, var(--color-border-default));
}

.join-status--loading .join-status__dot {
  background: var(--color-primary);
  box-shadow: 0 0 0 4px color-mix(in srgb, var(--color-primary) 14%, transparent);
}

.join-status--error {
  background: color-mix(in srgb, var(--color-danger) 8%, var(--color-bg-surface));
  border-color: color-mix(in srgb, var(--color-danger) 22%, var(--color-border-default));
  color: var(--color-danger);
}

.join-status--error .join-status__dot {
  background: var(--color-danger);
}

.join-steps {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.45rem;
}

.join-steps li {
  display: flex;
  align-items: flex-start;
  gap: 0.6rem;
  padding: 0.55rem 0.65rem;
  border-radius: var(--radius-lg);
  background: color-mix(in srgb, var(--color-bg-subtle) 70%, var(--color-bg-surface));
}

.join-steps__index {
  width: 1.35rem;
  height: 1.35rem;
  border-radius: 999px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  background: color-mix(in srgb, var(--color-primary) 12%, transparent);
  color: var(--color-primary);
  font-size: 0.75rem;
  font-weight: 700;
}

.join-steps__text {
  color: var(--color-text-secondary);
  font-size: 0.875rem;
  line-height: 1.45;
}

@media (max-width: 640px) {
  .join-platforms {
    grid-template-columns: 1fr;
  }

  .join-section__head {
    flex-direction: column;
    align-items: stretch;
  }

  .join-section__head .btn {
    width: 100%;
    justify-content: center;
  }

  .join-status {
    flex-direction: column;
    align-items: stretch;
  }

  .join-status .btn {
    width: 100%;
    justify-content: center;
  }
}
.form-group { display: flex; flex-direction: column; gap: 0.375rem; }
.form-group label { font-size: 0.875rem; font-weight: 500; color: var(--color-text-secondary); }

/* Edit-node modal chrome */
.edit-modal__heading {
  display: flex;
  align-items: baseline;
  gap: 0.5rem;
  min-width: 0;
}
.edit-modal__subtitle {
  font-size: 0.75rem;
  font-weight: 400;
  color: var(--color-text-tertiary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.edit-modal__hint {
  font-size: 0.75rem;
  color: var(--color-text-muted);
}
.edit-modal__tag-editor {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.375rem;
  padding: 0.375rem 0.625rem;
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  background: var(--color-bg-surface);
  transition: border-color var(--duration-fast) var(--ease-default),
    box-shadow var(--duration-fast) var(--ease-default);
}
.edit-modal__tag-editor:focus-within {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}
.edit-modal__tag-remove {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: none;
  background: transparent;
  color: inherit;
  padding: 0;
  cursor: pointer;
  opacity: 0.7;
}
.edit-modal__tag-remove:hover { opacity: 1; }
.edit-modal__tag-input {
  flex: 1;
  min-width: 8rem;
  border: none;
  outline: none;
  background: transparent;
  padding: 0.25rem 0;
  font-size: 0.875rem;
  color: var(--color-text-primary);
  font-family: inherit;
}
.input-base {
  width: 100%;
  padding: 0.5rem 0.75rem;
  border-radius: var(--radius-lg);
  border: 1.5px solid var(--color-border-default);
  background: var(--color-bg-surface);
  font-size: 0.875rem;
  color: var(--color-text-primary);
  outline: none;
  font-family: inherit;
  box-sizing: border-box;
  transition: border-color var(--duration-fast) var(--ease-default),
              box-shadow var(--duration-fast) var(--ease-default);
}
.input-base:focus {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

@media (max-width: 640px) {
  .agents-page__header {
    flex-direction: column;
    align-items: stretch;
    margin-bottom: 0.85rem;
    gap: 0.65rem;
  }
  .agents-page__header-right {
    width: 100%;
    gap: 0.5rem;
  }
  .agents-page__header-right :deep(.search-wrapper) {
    flex-shrink: 0;
  }
  .agents-page__header-right .btn,
  .agents-page__header-right .btn-primary,
  .agents-page__header-right .btn-secondary {
    flex: 1 1 auto;
    min-width: 0;
    justify-content: center;
  }
  .agents-page__title {
    font-size: 1.25rem;
  }
  .agents-page__subtitle {
    font-size: 0.75rem;
    line-height: 1.35;
    /* One visual line with ellipsis keeps header compact on narrow phones */
    display: -webkit-box;
    -webkit-box-orient: vertical;
    -webkit-line-clamp: 2;
    overflow: hidden;
  }
  .agent-grid {
    grid-template-columns: 1fr;
    gap: 0.75rem;
  }
}
/* Wide-screen (2K/4K) width steps */
@media (min-width: 1920px) {
  .agents-page { max-width: 1600px; }
}
@media (min-width: 2560px) {
  .agents-page { max-width: 2000px; }
}
</style>

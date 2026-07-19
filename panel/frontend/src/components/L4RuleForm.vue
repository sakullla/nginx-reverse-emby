<template>
  <form class="rule-form" @submit.prevent="handleSubmit">
    <div class="form-tabs">
      <button
        type="button"
        class="form-tabs__btn"
        :class="{ 'form-tabs__btn--active': activeTab === 'basic' }"
        @click="activeTab = 'basic'"
      >
        基础配置
      </button>
      <button
        type="button"
        class="form-tabs__btn"
        :class="{ 'form-tabs__btn--active': activeTab === 'protocol' }"
        @click="activeTab = 'protocol'"
      >
        协议与监听
        <span v-if="hasProtocolTuning" class="form-tabs__dot" title="已配置"></span>
      </button>
      <button
        type="button"
        class="form-tabs__btn"
        :class="{ 'form-tabs__btn--active': activeTab === 'relay' }"
        @click="activeTab = 'relay'"
      >
        Relay 配置
        <span v-if="hasRelayConfig" class="form-tabs__dot" title="已配置"></span>
      </button>
    </div>

    <div v-if="error" class="form-error">
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
        <circle cx="12" cy="12" r="10"/>
        <line x1="12" y1="8" x2="12" y2="12"/>
        <line x1="12" y1="16" x2="12.01" y2="16"/>
      </svg>
      {{ error }}
    </div>

    <!-- Tab 1: 基础配置 -->
    <div v-show="activeTab === 'basic'" class="form-tab-panel">
      <div class="settings-card">
        <div class="section-header">
          <div>
            <h3 class="section-title">协议与监听</h3>
            <p class="section-description">配置入口协议、监听地址与端口</p>
          </div>
        </div>

        <div class="form-group form-group--block">
          <label class="form-label form-label--required">监听地址</label>
          <div class="protocol-input-group protocol-input-group--listen">
            <select
              v-model="form.protocol"
              class="input input--protocol"
              @change="handleProtocolChange"
            >
              <option value="tcp">TCP</option>
              <option value="udp">UDP</option>
            </select>
            <input
              v-model="form.listen_host"
              class="input protocol-input-group__host"
              placeholder="0.0.0.0"
            >
            <input
              v-model.number="form.listen_port"
              class="input protocol-input-group__port"
              type="number"
              :min="allowsWildcardListenPort ? 0 : 1"
              max="65535"
              placeholder="25565"
              @input="updateAutoTags"
            >
          </div>
          <p v-if="allowsWildcardListenPort" class="field-hint">端口 0 表示透明代理捕获全部目标端口</p>
          <p v-else class="field-hint">协议 + 地址 + 端口组成 L4 入口</p>
        </div>

        <div v-if="requiresBackends" class="form-group form-group--block">
          <div class="backends-header">
            <label class="form-label form-label--required">后端服务器</label>
            <button type="button" class="btn btn--sm btn--secondary" @click="addBackend">
              添加后端
            </button>
          </div>

          <div class="backends-list" :class="{ 'backends-list--multi': form.backends.length > 1 }">
            <div
              v-for="(backend, index) in form.backends"
              :key="backend.id"
              class="backend-item"
              :class="{
                'backend-item--flat': form.backends.length === 1,
                'backend-item--dragging': dragState.from === index,
                'backend-item--drag-over': dragState.to === index && dragState.from !== index
              }"
              :draggable="form.backends.length > 1"
              @dragstart="onDragStart(index)"
              @dragover.prevent="onDragOver(index)"
              @drop="onDrop(index)"
              @dragend="onDragEnd"
            >
              <div v-if="form.backends.length > 1" class="backend-drag-handle" title="拖动排序">
                <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <circle cx="9" cy="5" r="1"/>
                  <circle cx="9" cy="12" r="1"/>
                  <circle cx="9" cy="19" r="1"/>
                  <circle cx="15" cy="5" r="1"/>
                  <circle cx="15" cy="12" r="1"/>
                  <circle cx="15" cy="19" r="1"/>
                </svg>
              </div>

              <input
                v-model="backend.address"
                class="input backend-address-input"
                :class="{ 'backend-address-input--flat': form.backends.length === 1 }"
                placeholder="IP:端口 或 域名:端口"
                @blur="parseBackendAddress(index)"
              >

              <button
                v-if="form.backends.length > 1"
                type="button"
                class="btn btn--icon btn--danger-ghost"
                title="删除后端"
                @click="removeBackend(index)"
              >
                <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <polyline points="3 6 5 6 21 6"/>
                  <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
                </svg>
              </button>
            </div>
          </div>
          <p class="field-hint">支持多个后端并按负载策略分发</p>
        </div>
      </div>

      <div class="form-secondary-grid">
        <div class="settings-card settings-card--compact">
          <div class="section-header section-header--inline">
            <h3 class="section-title">分类标签</h3>
            <p class="section-description">回车添加</p>
          </div>

          <div class="tag-input">
            <div class="tag-input__container">
              <span
                v-for="(tag, index) in form.tags"
                :key="tag"
                class="tag"
              >
                {{ tag }}
                <button
                  type="button"
                  class="tag__remove"
                  @click="removeTag(index)"
                >
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <line x1="18" y1="6" x2="6" y2="18"/>
                    <line x1="6" y1="6" x2="18" y2="18"/>
                  </svg>
                </button>
              </span>
              <input
                v-model="tagInput"
                type="text"
                class="tag-input__field"
                placeholder="例如 game / minecraft"
                @keydown.enter.prevent="addTag"
              >
            </div>
          </div>
        </div>

        <div class="settings-card settings-card--compact settings-card--status">
          <label class="toggle toggle--inline" :class="{ 'toggle--active': form.enabled }">
            <input
              v-model="form.enabled"
              type="checkbox"
              class="toggle__input"
            >
            <span class="toggle__slider"></span>
            <span class="toggle__content">
              <span class="toggle__label">启用此规则</span>
              <span class="toggle__desc">创建后立即生效</span>
            </span>
          </label>
        </div>
      </div>
    </div>

    <!-- Tab 2: 协议与监听 -->
    <div v-show="activeTab === 'protocol'" class="form-tab-panel">
      <div class="settings-card">
        <div class="section-header">
          <div>
            <h3 class="section-title">监听模式</h3>
            <p class="section-description">转发、代理入口或 WireGuard 入站</p>
          </div>
        </div>

        <div class="field-block">
          <label class="form-label form-label--required">模式</label>
          <select v-model="form.listen_mode" class="input">
            <option value="tcp">{{ form.protocol === 'udp' ? 'UDP 转发' : 'TCP 转发' }}</option>
            <option value="proxy">SOCKS / HTTP 代理</option>
            <option value="wireguard">WireGuard</option>
          </select>
        </div>

        <div v-if="form.protocol === 'udp' && form.listen_mode === 'proxy'" class="relay-alert relay-alert--warning">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/>
            <line x1="12" y1="9" x2="12" y2="13"/>
            <line x1="12" y1="17" x2="12.01" y2="17"/>
          </svg>
          <span>UDP SOCKS5 入口依赖同监听地址、同端口的 TCP SOCKS5 入口规则完成认证与 UDP ASSOCIATE</span>
        </div>

        <template v-if="isWireGuardInbound">
          <div class="form-row">
            <div class="form-group">
              <label class="form-label form-label--required">WireGuard 配置</label>
              <select v-model.number="form.wireguard_profile_id" class="input">
                <option value="">请选择配置</option>
                <option v-for="profile in enabledWireGuardProfiles" :key="profile.id" :value="Number(profile.id)">
                  {{ profile.name || profile.id }}
                </option>
              </select>
            </div>

            <div class="form-group">
              <label class="form-label">入站模式</label>
              <select v-model="form.wireguard_inbound_mode" class="input">
                <option value="transparent">透明</option>
                <option value="address">内网入口</option>
              </select>
            </div>
          </div>
          <p class="field-hint">
            <template v-if="form.wireguard_inbound_mode === 'address'">
              监听 Host 自动使用所选 Profile 的第一个地址
            </template>
            <template v-else>
              透明入口匹配已接入所选 Profile 的客户端流量
            </template>
          </p>
        </template>

        <template v-if="isProxyEntryAuthAvailable">
          <div class="option-list">
            <label class="option-row" :class="{ 'option-row--active': form.proxy_entry_auth.enabled }">
              <input
                v-model="form.proxy_entry_auth.enabled"
                type="checkbox"
                class="toggle__input"
              >
              <span class="toggle__slider"></span>
              <span class="option-row__content">
                <span class="option-row__label">启用入口认证</span>
                <span class="option-row__desc">为 SOCKS / HTTP 代理入口配置用户名密码</span>
              </span>
            </label>
          </div>

          <div v-if="form.proxy_entry_auth.enabled" class="form-row">
            <div class="form-group">
              <label class="form-label">用户名</label>
              <input v-model="form.proxy_entry_auth.username" class="input" autocomplete="off">
            </div>
            <div class="form-group">
              <label class="form-label">密码</label>
              <input v-model="form.proxy_entry_auth.password" class="input" type="password" autocomplete="new-password">
            </div>
          </div>
        </template>
      </div>

      <div v-if="form.protocol === 'tcp'" class="settings-card">
        <div class="section-header">
          <div>
            <h3 class="section-title">PROXY Protocol</h3>
            <p class="section-description">解析或向上游传递真实客户端 IP</p>
          </div>
        </div>

        <div class="option-list">
          <label class="option-row" :class="{ 'option-row--active': form.tuning.proxy_protocol.decode }">
            <input
              v-model="form.tuning.proxy_protocol.decode"
              type="checkbox"
              class="toggle__input"
            >
            <span class="toggle__slider"></span>
            <span class="option-row__content">
              <span class="option-row__label">接收 PROXY Protocol</span>
              <span class="option-row__desc">从客户端 / 前置代理解析真实 IP</span>
            </span>
          </label>

          <label class="option-row" :class="{ 'option-row--active': form.tuning.proxy_protocol.send }">
            <input
              v-model="form.tuning.proxy_protocol.send"
              type="checkbox"
              class="toggle__input"
            >
            <span class="toggle__slider"></span>
            <span class="option-row__content">
              <span class="option-row__label">发送到上游</span>
              <span class="option-row__desc">向后端传递客户端真实 IP</span>
            </span>
          </label>
        </div>
      </div>

      <div class="settings-card">
        <div class="section-header">
          <div>
            <h3 class="section-title">负载均衡</h3>
            <p class="section-description">多后端时生效</p>
          </div>
        </div>

        <div class="field-block">
          <label class="form-label">策略</label>
          <select v-model="form.load_balancing.strategy" class="input" @change="handleStrategyChange">
            <option value="adaptive">自适应 (Adaptive)</option>
            <option value="round_robin">轮询 (Round Robin)</option>
            <option value="random">随机 (Random)</option>
          </select>
          <p class="field-hint">自适应 / 轮询 / 随机</p>
        </div>
      </div>

      <!-- 更多：出口 Profile，默认折叠 -->
      <div class="settings-card settings-card--more" :class="{ 'settings-card--more-open': advancedMoreOpen }">
        <button
          type="button"
          class="more-toggle"
          :aria-expanded="advancedMoreOpen ? 'true' : 'false'"
          @click="advancedMoreOpen = !advancedMoreOpen"
        >
          <span class="more-toggle__main">
            <span class="more-toggle__title">更多</span>
            <span class="more-toggle__summary">{{ advancedMoreSummary }}</span>
          </span>
          <svg
            class="more-toggle__chevron"
            width="14"
            height="14"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
          >
            <polyline points="6 9 12 15 18 9"/>
          </svg>
        </button>

        <div v-if="advancedMoreOpen" class="more-panel">
          <div class="field-block">
            <label class="form-label">出口 Profile</label>
            <select v-model.number="form.egress_profile_id" name="egress-profile" class="input">
              <option :value="0">Direct</option>
              <option v-for="profile in filteredEgressProfiles" :key="profile.id" :value="Number(profile.id)">
                {{ profile.name || profile.id }} ({{ profile.type }})
              </option>
            </select>
            <p class="field-hint">仅影响 Agent 访问后端的出站路径</p>
          </div>
        </div>
      </div>
    </div>

    <!-- Tab 3: Relay 配置 -->
    <div v-show="activeTab === 'relay'" class="form-tab-panel">
      <div v-if="!relayListeners.length" class="relay-alert relay-alert--warning">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/>
          <line x1="12" y1="9" x2="12" y2="13"/>
          <line x1="12" y1="17" x2="12.01" y2="17"/>
        </svg>
        <span>当前没有可用的 Relay 监听器，请先创建监听器后再配置链路</span>
      </div>

      <div v-else-if="!hasRelayConfig" class="relay-alert relay-alert--info">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <circle cx="12" cy="12" r="10"/>
          <line x1="12" y1="16" x2="12" y2="12"/>
          <line x1="12" y1="8" x2="12.01" y2="8"/>
        </svg>
        <span>当前为直连模式，{{ form.protocol === 'udp' ? 'UDP' : 'TCP' }} 流量将直接转发到后端服务，不经过 Relay 中转</span>
      </div>

      <div class="settings-card">
        <div class="section-header section-header--split">
          <div>
            <h3 class="section-title">链路配置</h3>
            <p class="section-description">按顺序添加 Relay 监听器，构建转发路径</p>
          </div>
          <router-link
            v-if="relayListeners.length"
            to="/relay-listeners"
            class="relay-link"
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/>
              <polyline points="15 3 21 3 21 9"/>
              <line x1="10" y1="14" x2="21" y2="3"/>
            </svg>
            管理监听器
          </router-link>
        </div>

        <RelayChainInput
          v-model="form.relay_layers"
          :listeners="relayListeners"
        />
      </div>

      <div class="settings-card settings-card--compact">
        <div class="section-header section-header--split">
          <div>
            <h3 class="section-title">隐私增强</h3>
            <p class="section-description">仅首跳 TLS/TCP 可用，隐藏内层握手特征</p>
          </div>
          <label class="toggle toggle--inline" :class="{ 'toggle--active': form.relay_obfs, 'toggle--disabled': relayObfsDisabled }">
            <input
              v-model="form.relay_obfs"
              type="checkbox"
              class="toggle__input"
              :disabled="relayObfsDisabled"
            >
            <span class="toggle__slider"></span>
            <span class="toggle__content">
              <span class="toggle__label">启用</span>
            </span>
          </label>
        </div>
        <p v-if="relayObfsDisabled" class="form-help-text">{{ relayObfsUnsupportedReason }}</p>
        <p v-else class="field-hint">按层顺序转发；链路越长延迟越高</p>
      </div>
    </div>

    <button
      type="submit"
      class="btn btn--primary btn--full"
      :disabled="createL4Rule.isPending.value || updateL4Rule.isPending.value"
    >
      {{ isEdit ? '保存修改' : '创建规则' }}
    </button>
  </form>
</template>

<script setup>
import { computed, ref, watch } from 'vue'
import { useCreateL4Rule, useUpdateL4Rule } from '../hooks/useL4Rules'
import { useAllRelayListeners } from '../hooks/useRelayListeners'
import { useWireGuardProfiles } from '../hooks/useWireGuardProfiles'
import { useEgressProfiles } from '../hooks/useEgressProfiles'
import RelayChainInput from './RelayChainInput.vue'
import { buildProxyEntryAuthPayload } from './l4/proxyEntryAuth'
import { getDefaultTuning, mergeTuning, resetTuningForProtocol } from './l4/tuningState'

const props = defineProps({
  initialData: { type: Object, default: null },
  l4Rules: { type: Array, default: () => [] },
  agentId: { type: [String, Object], required: true }
})
const emit = defineEmits(['success'])

const createL4Rule = useCreateL4Rule(props.agentId)
const updateL4Rule = useUpdateL4Rule(props.agentId)
const { data: relayListenersData } = useAllRelayListeners()
const { data: wireGuardProfilesData } = useWireGuardProfiles(props.agentId)
const { data: egressProfilesData } = useEgressProfiles()
const isEdit = computed(() => !!props.initialData?.id)
const relayListeners = computed(() => relayListenersData.value ?? [])
const wireGuardProfiles = computed(() => wireGuardProfilesData.value ?? [])
const egressProfiles = computed(() => egressProfilesData.value ?? [])
const enabledWireGuardProfiles = computed(() => wireGuardProfiles.value.filter((profile) => {
  const id = Number(profile.id)
  return Number.isInteger(id) && id > 0 && profile.enabled !== false
}))
const enabledEgressProfiles = computed(() => egressProfiles.value.filter((profile) => {
  const id = Number(profile.id)
  return Number.isInteger(id) && id > 0 && profile.enabled !== false
}))

let backendIdCounter = 0

function isIpAddress(value) {
  if (!value) return false
  if (/^(\d{1,3}\.){3}\d{1,3}$/.test(value)) return true
  if (/^[0-9A-Fa-f:]+$/.test(value) && value.includes(':')) return true
  return false
}

function createBackend(data = {}) {
  const host = data.host || ''
  const port = data.port || 0
  const address = host && port ? `${host}:${port}` : (data.address || '')
  return {
    id: `b-${Date.now()}-${backendIdCounter++}`,
    address,
    host,
    port,
    resolve: data.resolve || false,
    backup: data.backup || false,
    max_conns: data.max_conns || 0,
  }
}

const SUPPORTED_L4_STRATEGIES = new Set(['adaptive', 'round_robin', 'random'])

function normalizeL4Strategy(value) {
  const strategy = String(value || '').trim().toLowerCase()
  return SUPPORTED_L4_STRATEGIES.has(strategy) ? strategy : 'adaptive'
}

function normalizeInitialBackends(initialData) {
  if (initialData?.backends?.length > 0) {
    return initialData.backends.map(b => createBackend(b))
  }
  return [createBackend()]
}

function createFormState(initialData) {
  const protocol = initialData?.protocol || 'tcp'
  const initialListenMode = ['proxy', 'wireguard'].includes(initialData?.listen_mode)
    ? initialData.listen_mode
    : 'tcp'
  return {
    protocol,
    listen_host: initialData?.listen_host || '0.0.0.0',
    listen_port: initialData?.listen_port || 0,
    backends: normalizeInitialBackends(initialData),
    load_balancing: {
      strategy: normalizeL4Strategy(initialData?.load_balancing?.strategy),
    },
    tuning: mergeTuning(initialData?.tuning, protocol),
    egress_profile_id: initialData?.egress_profile_id == null ? 0 : Number(initialData.egress_profile_id),
    enabled: initialData?.enabled !== false,
    tags: Array.isArray(initialData?.tags) ? [...initialData.tags] : [],
    listen_mode: initialListenMode,
    proxy_entry_auth: {
      enabled: initialData?.proxy_entry_auth?.enabled === true,
      username: initialData?.proxy_entry_auth?.username || '',
      password: initialData?.proxy_entry_auth?.password || '',
    },
    wireguard_profile_id: initialData?.wireguard_profile_id == null ? '' : Number(initialData.wireguard_profile_id),
    wireguard_inbound_mode: initialData?.wireguard_inbound_mode || 'transparent',
    relay_layers: getRelayLayers(initialData),
    relay_obfs: initialData?.relay_obfs === true,
  }
}

const form = ref(createFormState(props.initialData))

const activeTab = ref('basic')
const tagInput = ref('')
const error = ref('')
const advancedMoreOpen = ref(false)
const dragState = ref({ from: -1, to: -1 })
const wireGuardModeHydratedFromInitialData = ref(false)
const wireGuardProfileHydratedFromInitialData = ref(false)
const wireGuardProfileRequiresExplicitSelection = ref(false)

function onDragStart(index) {
  dragState.value = { from: index, to: index }
}

function onDragOver(index) {
  if (dragState.value.from === -1) return
  dragState.value.to = index
}

function onDrop(index) {
  const from = dragState.value.from
  if (from === -1 || from === index) return
  const item = form.value.backends.splice(from, 1)[0]
  form.value.backends.splice(index, 0, item)
  dragState.value = { from: -1, to: -1 }
}

function onDragEnd() {
  dragState.value = { from: -1, to: -1 }
}

const hasTuningChanges = computed(() => {
  const defaults = getDefaultTuning(form.value.protocol)
  const t = form.value.tuning
  const hasBackendExtensions = form.value.backends.some(b => b.backup || (b.max_conns && b.max_conns > 0))
  return (
    hasBackendExtensions ||
    t.proxy.connect_timeout !== defaults.proxy.connect_timeout ||
    t.proxy.idle_timeout !== defaults.proxy.idle_timeout ||
    t.proxy.buffer_size !== defaults.proxy.buffer_size ||
    t.upstream.max_conns !== defaults.upstream.max_conns ||
    t.upstream.max_fails !== defaults.upstream.max_fails ||
    t.upstream.fail_timeout !== defaults.upstream.fail_timeout ||
    t.limit_conn.count !== defaults.limit_conn.count ||
    t.listen.reuseport !== defaults.listen.reuseport ||
    t.listen.tcp_nodelay !== defaults.listen.tcp_nodelay ||
    t.listen.so_keepalive !== defaults.listen.so_keepalive ||
    (t.listen.backlog !== null && t.listen.backlog !== defaults.listen.backlog) ||
    t.proxy_protocol.decode !== defaults.proxy_protocol.decode ||
    t.proxy_protocol.send !== defaults.proxy_protocol.send
  )
})

const isWireGuardInbound = computed(() => form.value.listen_mode === 'wireguard')
const isProxyEntry = computed(() => form.value.listen_mode === 'proxy')
const isProxyEntryAuthAvailable = computed(() => form.value.listen_mode === 'proxy')
const isWireGuardTransparentForward = computed(() => isWireGuardInbound.value
  && form.value.wireguard_inbound_mode === 'transparent')
const allowsWildcardListenPort = computed(() => isWireGuardTransparentForward.value)
const requiresBackends = computed(() => !isProxyEntry.value && !isWireGuardTransparentForward.value)
const usesWireGuard = computed(() => isWireGuardInbound.value)
const isWireGuardAdvancedProfileOverride = computed(() => isWireGuardInbound.value && form.value.wireguard_inbound_mode === 'address')
const requiresWireGuardProfile = computed(() => isWireGuardInbound.value)
const selectedWireGuardProfileID = computed(() => {
  const id = Number(form.value.wireguard_profile_id)
  if (!Number.isInteger(id) || id <= 0) return null
  return enabledWireGuardProfiles.value.some((profile) => Number(profile.id) === id) ? id : null
})
const filteredEgressProfiles = computed(() => enabledEgressProfiles.value.filter((profile) => {
  if (String(form.value.protocol).toLowerCase() !== 'udp') return true
  return profile.type !== 'http'
}))
const selectedEgressProfileID = computed(() => {
  const id = Number(form.value.egress_profile_id)
  if (!Number.isInteger(id) || id <= 0) return null
  return filteredEgressProfiles.value.some((profile) => Number(profile.id) === id) ? id : null
})
const advancedMoreSummary = computed(() => {
  const id = Number(form.value.egress_profile_id)
  if (!Number.isInteger(id) || id <= 0) return '出口 Direct'
  const profile = filteredEgressProfiles.value.find((p) => Number(p.id) === id)
  if (!profile) return '出口 Direct'
  return `出口 ${profile.name || profile.id}`
})
const samePortTCPProxyRule = computed(() => {
  if (!(form.value.protocol === 'udp' && form.value.listen_mode === 'proxy')) return true
  const currentId = props.initialData?.id
  const listenPort = Number(form.value.listen_port)
  const listenHost = String(form.value.listen_host || '0.0.0.0').trim()
  return (props.l4Rules || []).some((rule) =>
    rule?.id !== currentId
    && rule?.protocol === 'tcp'
    && rule?.listen_mode === 'proxy'
    && Number(rule?.listen_port) === listenPort
    && String(rule?.listen_host || '0.0.0.0').trim() === listenHost
  )
})

const hasProtocolTuning = computed(() => {
  const defaults = getDefaultTuning(form.value.protocol)
  const t = form.value.tuning
  return (
    t.proxy_protocol.decode !== defaults.proxy_protocol.decode ||
    t.proxy_protocol.send !== defaults.proxy_protocol.send ||
    isProxyEntry.value ||
    usesWireGuard.value ||
    selectedEgressProfileID.value != null ||
    t.listen.reuseport !== defaults.listen.reuseport ||
    t.listen.tcp_nodelay !== defaults.listen.tcp_nodelay ||
    t.listen.so_keepalive !== defaults.listen.so_keepalive ||
    (t.listen.backlog !== null && t.listen.backlog !== defaults.listen.backlog) ||
    (form.value.protocol === 'udp' && (
      (t.proxy.udp_proxy_requests !== null && t.proxy.udp_proxy_requests !== defaults.proxy.udp_proxy_requests) ||
      (t.proxy.udp_proxy_responses !== null && t.proxy.udp_proxy_responses !== defaults.proxy.udp_proxy_responses)
    ))
  )
})

function getRelayLayers(value) {
  if (Array.isArray(value?.relay_layers) && value.relay_layers.length > 0) {
    return value.relay_layers
  }
  return []
}

const hasRelayConfig = computed(() => {
  return Array.isArray(form.value.relay_layers) && form.value.relay_layers.length > 0
})
const selectedRelayListeners = computed(() => {
  const listenerMap = new Map(relayListeners.value.map((listener) => [Number(listener.id), listener]))
  const layers = getRelayLayers(form.value)
  if (!layers.length) return []
  return (layers[0] || [])
    .map((id) => listenerMap.get(Number(id)) || null)
    .filter(Boolean)
})
const firstRelayListener = computed(() => {
  return selectedRelayListeners.value[0] ?? null
})
const relayObfsUnsupportedReason = computed(() => {
  const layers = getRelayLayers(form.value)
  if (!layers.length || !layers[0]?.length) {
    return '当前为直连模式，此选项不会生效'
  }
  if (form.value.protocol !== 'tcp') {
    return 'UDP Relay 不支持隐私增强'
  }
  if (!firstRelayListener.value) {
    return '首跳 Relay 监听器不存在，无法启用隐私增强'
  }
  if (firstRelayListener.value.transport_mode !== 'tls_tcp') {
    return '首跳 Relay 使用 QUIC 传输，隐私增强仅适用于 TLS/TCP'
  }
  return ''
})
const relayObfsDisabled = computed(() => Boolean(relayObfsUnsupportedReason.value))

watch(() => props.initialData, (value) => {
  form.value = createFormState(value)
  wireGuardModeHydratedFromInitialData.value = !!value?.id && requiresWireGuardProfile.value
  wireGuardProfileHydratedFromInitialData.value = !!value?.id
    && requiresWireGuardProfile.value
    && form.value.wireguard_profile_id !== ''
  wireGuardProfileRequiresExplicitSelection.value = !!value?.id
    && requiresWireGuardProfile.value
    && form.value.wireguard_profile_id === ''
  tagInput.value = ''
  dragState.value = { from: -1, to: -1 }
  error.value = ''
  advancedMoreOpen.value = false
}, { immediate: true })

watch(() => form.value.protocol, (newProto) => {
  form.value.tuning = resetTuningForProtocol(form.value.tuning, newProto)
  if (newProto === 'udp') {
    form.value.relay_obfs = false
  }
})

watch(() => form.value.listen_mode, (mode, previousMode) => {
  if (!isEdit.value) updateAutoTags()
})

watch(requiresWireGuardProfile, (enabled, wasEnabled) => {
  if (!enabled) return
  if (selectedWireGuardProfileID.value != null) return
  if (wireGuardModeHydratedFromInitialData.value) {
    wireGuardModeHydratedFromInitialData.value = false
    return
  }
  if (!wasEnabled) {
    wireGuardProfileRequiresExplicitSelection.value = false
    if (form.value.wireguard_profile_id === '') {
      selectFirstEnabledWireGuardProfile()
    }
    return
  }
  form.value.wireguard_profile_id = ''
})

watch(enabledWireGuardProfiles, (profiles) => {
  if (wireGuardProfilesData.value == null) return
  if (!requiresWireGuardProfile.value) return
  if (selectedWireGuardProfileID.value != null) return
  if (form.value.wireguard_profile_id === '') {
    if (!wireGuardProfileRequiresExplicitSelection.value) {
      selectFirstEnabledWireGuardProfile()
    }
    return
  }
  if (wireGuardProfileHydratedFromInitialData.value) {
    wireGuardProfileRequiresExplicitSelection.value = true
  }
  form.value.wireguard_profile_id = ''
}, { immediate: true })

function selectFirstEnabledWireGuardProfile() {
  form.value.wireguard_profile_id = enabledWireGuardProfiles.value.length
    ? Number(enabledWireGuardProfiles.value[0].id)
    : ''
}

watch([() => form.value.relay_layers, firstRelayListener], ([relayLayers]) => {
  if (
    !Array.isArray(relayLayers)
    || relayLayers.length === 0
    || firstRelayListener.value?.transport_mode !== 'tls_tcp'
  ) {
    form.value.relay_obfs = false
  }
  if (!isEdit.value) updateAutoTags()
})

const LB_TAG_MAP = { adaptive: 'ADP', round_robin: 'RR', random: 'RND' }
const LB_TAG_SET = new Set(Object.values(LB_TAG_MAP))
const LISTEN_MODE_LABELS = { tcp: 'TCP转发', udp: 'UDP转发', proxy: '代理', wireguard: 'WG' }
const LISTEN_MODE_LABEL_SET = new Set(Object.values(LISTEN_MODE_LABELS))

function isL4AutoTag(t) {
  return t === 'TCP' || t === 'UDP' || /^:\d+$/.test(t) ||
    /^(TCP|UDP) 监听端口 \d+/.test(t) ||
    t.startsWith('监听端口') || t.startsWith('上游端口') ||
    LB_TAG_SET.has(t) ||
    LISTEN_MODE_LABEL_SET.has(t) ||
    t === 'Relay'
}

function getListenModeTag(mode, protocol) {
  const m = String(mode || '').toLowerCase()
  const p = String(protocol || '').toLowerCase()
  if (m === 'proxy') return '代理'
  if (m === 'wireguard') return 'WG'
  return p === 'udp' ? 'UDP转发' : 'TCP转发'
}

function updateAutoTags() {
  if (isEdit.value) return
  const protocol = form.value.protocol.toUpperCase()
  const listenPort = form.value.listen_port
  const lbTag = LB_TAG_MAP[form.value.load_balancing.strategy]
  const modeTag = getListenModeTag(form.value.listen_mode, form.value.protocol)
  const relayTag = (Array.isArray(form.value.relay_layers) && form.value.relay_layers.length > 0) ? 'Relay' : null
  form.value.tags = form.value.tags.filter(t => !isL4AutoTag(t))
  const sysTags = [
    protocol,
    ...(listenPort ? [`:${listenPort}`] : []),
    ...(lbTag ? [lbTag] : []),
    modeTag,
    ...(relayTag ? [relayTag] : []),
  ]
  form.value.tags = [...sysTags, ...form.value.tags]
}

function handleProtocolChange() {
  if (!isEdit.value) updateAutoTags()
}

function handleStrategyChange() {
  form.value.load_balancing.strategy = normalizeL4Strategy(form.value.load_balancing.strategy)
  if (!isEdit.value) updateAutoTags()
}

function addBackend() {
  form.value.backends.push(createBackend())
}

function removeBackend(index) {
  if (form.value.backends.length > 1) {
    form.value.backends.splice(index, 1)
  }
}

function parseBackendAddress(index) {
  const backend = form.value.backends[index]
  const address = backend.address?.trim() || ''
  const match = address.match(/^(.+):(\d+)$/)
  if (match) {
    backend.host = match[1]
    backend.port = parseInt(match[2], 10)
  } else {
    backend.host = address
    backend.port = 0
  }
  const cleanHost = backend.host?.replace(/^\[|\]$/g, '') || ''
  backend.resolve = !isIpAddress(cleanHost)
}

function addTag() {
  const tag = tagInput.value.trim()
  if (tag && !form.value.tags.includes(tag)) {
    form.value.tags.push(tag)
  }
  tagInput.value = ''
}

function removeTag(index) {
  form.value.tags.splice(index, 1)
}

function cleanValue(v) {
  if (v === '' || v === null || v === undefined) return undefined
  if (typeof v === 'number' && isNaN(v)) return undefined
  return v
}

function buildPayload() {
  form.value.backends.forEach((_, index) => parseBackendAddress(index))

  const protocol = form.value.protocol.toUpperCase()
  const listenPort = form.value.listen_port
  const lbTag = LB_TAG_MAP[form.value.load_balancing.strategy]
  const modeTag = getListenModeTag(form.value.listen_mode, form.value.protocol)
  const relayTag = (Array.isArray(form.value.relay_layers) && form.value.relay_layers.length > 0) ? 'Relay' : null
  const userTags = form.value.tags.filter(t => !isL4AutoTag(t))
  const sysTags = [
    protocol,
    ...(listenPort ? [`:${listenPort}`] : []),
    ...(lbTag ? [lbTag] : []),
    modeTag,
    ...(relayTag ? [relayTag] : []),
  ]

  const validBackends = form.value.backends
    .filter(b => b.host && b.port)
    .map(b => ({
      host: b.host.trim(),
      port: Number(b.port),
    }))

  const proxyEntryAuth = isProxyEntryAuthAvailable.value
    ? buildProxyEntryAuthPayload(props.initialData?.proxy_entry_auth, form.value.proxy_entry_auth)
    : { enabled: false, username: '', password: '' }

  const payload = {
    protocol: form.value.protocol,
    listen_host: form.value.listen_host.trim(),
    listen_port: listenPort,
    backends: requiresBackends.value ? validBackends : [],
    load_balancing: {
      strategy: normalizeL4Strategy(form.value.load_balancing.strategy),
    },
    enabled: form.value.enabled,
    tags: [...sysTags, ...userTags],
    listen_mode: form.value.listen_mode,
    relay_layers: Array.isArray(form.value.relay_layers) ? form.value.relay_layers.map((l) => [...l]) : [],
    relay_obfs: form.value.protocol === 'tcp'
      && firstRelayListener.value?.transport_mode === 'tls_tcp'
      && Array.isArray(form.value.relay_layers)
      && form.value.relay_layers.length > 0
      && form.value.relay_obfs === true,
  }
  if (proxyEntryAuth !== undefined) {
    payload.proxy_entry_auth = proxyEntryAuth
  }
  if (requiresWireGuardProfile.value) {
    payload.wireguard_profile_id = selectedWireGuardProfileID.value
  }
  if (isWireGuardInbound.value) {
    payload.wireguard_inbound_mode = form.value.wireguard_inbound_mode
  }
  if (selectedEgressProfileID.value != null) {
    payload.egress_profile_id = selectedEgressProfileID.value
  } else if (isEdit.value && Number(form.value.egress_profile_id) === 0) {
    payload.egress_profile_id = 0
  }
  if (hasTuningChanges.value || isEdit.value) {
    const t = form.value.tuning
    const tuning = {
      listen: {
        reuseport: t.listen.reuseport,
        backlog: cleanValue(t.listen.backlog),
        so_keepalive: t.listen.so_keepalive,
        tcp_nodelay: t.listen.tcp_nodelay,
      },
      proxy: {
        connect_timeout: cleanValue(t.proxy.connect_timeout),
        idle_timeout: cleanValue(t.proxy.idle_timeout),
        buffer_size: cleanValue(t.proxy.buffer_size),
      },
      upstream: {
        max_conns: cleanValue(t.upstream.max_conns),
        max_fails: cleanValue(t.upstream.max_fails),
        fail_timeout: cleanValue(t.upstream.fail_timeout),
      },
      limit_conn: {
        key: cleanValue(t.limit_conn.key),
        count: cleanValue(t.limit_conn.count),
        zone_size: cleanValue(t.limit_conn.zone_size),
      },
      proxy_protocol: {
        decode: form.value.protocol === 'udp' ? false : t.proxy_protocol.decode,
        send: form.value.protocol === 'udp' ? false : t.proxy_protocol.send,
      },
    }
    if (form.value.protocol === 'udp') {
      tuning.proxy.udp_proxy_requests = cleanValue(t.proxy.udp_proxy_requests)
      tuning.proxy.udp_proxy_responses = cleanValue(t.proxy.udp_proxy_responses)
    }
    payload.tuning = tuning
  }

  return payload
}

async function handleSubmit() {
  error.value = ''
  form.value.backends.forEach((_, index) => parseBackendAddress(index))
  const validBackends = form.value.backends.filter(b => b.host && b.port)
  if (requiresBackends.value && validBackends.length === 0) {
    error.value = '至少需要一个有效的后端服务器'
    activeTab.value = 'basic'
    return
  }
  if (requiresWireGuardProfile.value && selectedWireGuardProfileID.value == null) {
    error.value = 'WireGuard 入站必须选择当前 Agent 已启用的 Profile'
    activeTab.value = 'protocol'
    return
  }
  if (!samePortTCPProxyRule.value) {
    error.value = '需要先维护同端口 TCP SOCKS5 入口规则'
    activeTab.value = 'protocol'
    return
  }
  const listenPort = Number(form.value.listen_port)
  if (!Number.isInteger(listenPort) || listenPort < 0 || listenPort > 65535 || (listenPort === 0 && !allowsWildcardListenPort.value)) {
    error.value = allowsWildcardListenPort.value ? '监听端口必须在 0-65535 之间' : '监听端口必须在 1-65535 之间'
    activeTab.value = 'basic'
    return
  }
  try {
    const payload = buildPayload()
    if (isEdit.value) {
      await updateL4Rule.mutateAsync({ id: props.initialData.id, ...payload })
    } else {
      await createL4Rule.mutateAsync(payload)
    }
    emit('success')
  } catch (e) {
    error.value = e.message || '提交失败'
  }
}
</script>

<style scoped>
.rule-form {
  display: flex;
  flex-direction: column;
  gap: 0.65rem;
}

.form-tabs {
  display: flex;
  gap: 2px;
  margin-bottom: 0;
  flex-shrink: 0;
  padding: 2px;
  background: var(--color-bg-subtle);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
}

.form-tabs__btn {
  padding: 0.4rem 0.75rem;
  border: none;
  background: transparent;
  cursor: pointer;
  font-size: 0.8125rem;
  font-weight: 550;
  color: var(--color-text-muted);
  border-radius: var(--radius-md);
  transition: all var(--duration-fast);
  display: flex;
  align-items: center;
  gap: 0.35rem;
  flex: 1;
  justify-content: center;
  white-space: nowrap;
  line-height: 1.3;
}

.form-tabs__btn:hover {
  color: var(--color-text-secondary);
}

.form-tabs__btn--active {
  color: var(--color-primary);
  background: var(--color-bg-surface);
  font-weight: 650;
  box-shadow: var(--shadow-sm);
}

.form-tabs__dot {
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: var(--color-success);
  flex-shrink: 0;
}

.form-tab-panel {
  display: flex;
  flex-direction: column;
  gap: 0.65rem;
  padding-top: 0;
}

.form-secondary-grid {
  display: grid;
  grid-template-columns: minmax(0, 1.55fr) minmax(13rem, 0.85fr);
  gap: 0.65rem;
  align-items: stretch;
}

.form-row {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0.5rem;
}

.form-group {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
  min-width: 0;
}

.form-label {
  font-size: 0.75rem;
  font-weight: 600;
  color: var(--color-text-secondary);
  line-height: 1.35;
}

.form-label--required::after {
  content: ' *';
  color: var(--color-danger);
}

.field-hint,
.form-help-text {
  margin: 0.3rem 0 0;
  font-size: 0.6875rem;
  color: var(--color-text-muted);
  line-height: 1.4;
}

.form-help-text {
  color: var(--color-text-tertiary);
}

.form-group--block {
  display: block;
  width: 100%;
}

.form-group--block + .form-group--block {
  margin-top: 0.6rem;
}

.section-header {
  display: flex;
  flex-direction: column;
  gap: 0.1rem;
}

.section-header--split {
  flex-direction: row;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
}

.section-header--inline {
  flex-direction: row;
  align-items: baseline;
  justify-content: space-between;
  gap: 0.5rem;
}

.section-title {
  margin: 0;
  font-size: 0.8125rem;
  font-weight: 650;
  color: var(--color-text-primary);
  line-height: 1.3;
  letter-spacing: -0.01em;
}

.section-description {
  margin: 0;
  font-size: 0.6875rem;
  color: var(--color-text-muted);
  line-height: 1.35;
}

.settings-card {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  padding: 0.65rem 0.75rem;
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
}

.settings-card--compact {
  gap: 0.45rem;
  padding: 0.7rem 0.8rem;
  justify-content: center;
}

.settings-card--status {
  min-height: 100%;
  justify-content: center;
}

.form-tab-panel > .settings-card {
  gap: 0.5rem;
  padding: 0.65rem 0.75rem;
}

.form-tab-panel > .settings-card .section-header {
  margin-bottom: 0;
}

.option-list {
  display: flex;
  flex-direction: column;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-md);
  overflow: hidden;
  background: var(--color-bg-surface);
}

.option-row {
  display: flex;
  align-items: flex-start;
  gap: 0.65rem;
  padding: 0.65rem 0.75rem;
  cursor: pointer;
  border-bottom: 1px solid var(--color-border-subtle);
  transition: background var(--duration-fast) var(--ease-default);
}

.option-row:last-child {
  border-bottom: none;
}

.option-row:hover {
  background: color-mix(in srgb, var(--color-bg-subtle) 55%, transparent);
}

.option-row__content {
  display: flex;
  flex-direction: column;
  gap: 0.15rem;
  min-width: 0;
}

.option-row__label {
  font-size: 0.8125rem;
  font-weight: 600;
  color: var(--color-text-primary);
  line-height: 1.35;
}

.option-row__desc {
  font-size: 0.6875rem;
  color: var(--color-text-tertiary);
  line-height: 1.4;
}

.field-block {
  display: flex;
  flex-direction: column;
  gap: 0.3rem;
  min-width: 0;
}

.toggle--inline {
  display: flex;
  align-items: center;
  gap: 0.55rem;
  min-width: 0;
}

.toggle--inline .toggle__content {
  display: flex;
  flex-direction: column;
  gap: 0.1rem;
  min-width: 0;
}

.toggle--inline .toggle__label {
  font-size: 0.8125rem;
  font-weight: 600;
  line-height: 1.3;
  color: var(--color-text-primary);
}

.toggle--inline .toggle__desc {
  font-size: 0.6875rem;
  color: var(--color-text-muted);
  line-height: 1.3;
}

.toggle--inline .toggle__slider {
  margin-top: 0;
}

.form-error {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  padding: 0.4rem 0.55rem;
  border-radius: var(--radius-md);
  font-size: 0.75rem;
  background: var(--color-danger-50);
  color: var(--color-danger);
}

.input {
  width: 100%;
  min-width: 0;
  padding: 0.35rem 0.55rem;
  font-size: 0.8125rem;
  color: var(--color-text-primary);
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  transition: all var(--duration-fast) var(--ease-default);
  font-family: inherit;
  box-sizing: border-box;
  height: 32px;
}

.input:focus {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.input::placeholder {
  color: var(--color-text-muted);
}

/* 协议 + 地址 + 端口：一体控件 */
.protocol-input-group {
  display: flex;
  align-items: stretch;
  min-width: 0;
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  overflow: hidden;
  transition: border-color var(--duration-fast) var(--ease-default),
              box-shadow var(--duration-fast) var(--ease-default);
}

.protocol-input-group:focus-within {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.input--protocol {
  width: auto;
  min-width: 4.5rem;
  flex-shrink: 0;
  height: 32px;
  padding: 0 0.55rem;
  border: none;
  border-right: 1px solid var(--color-border-subtle);
  border-radius: 0;
  background: color-mix(in srgb, var(--color-bg-subtle) 70%, transparent);
  box-shadow: none;
  cursor: pointer;
  font-size: 0.75rem;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  color: var(--color-text-secondary);
}

.input--protocol:focus {
  border-color: transparent;
  box-shadow: none;
  background: color-mix(in srgb, var(--color-primary-subtle) 55%, transparent);
  color: var(--color-primary);
}

.protocol-input-group__host {
  flex: 1;
  min-width: 0;
  height: 32px;
  border: none;
  border-radius: 0;
  box-shadow: none;
  background: transparent;
}

.protocol-input-group__host:focus {
  border-color: transparent;
  box-shadow: none;
}

.protocol-input-group__port {
  width: 5.5rem;
  flex-shrink: 0;
  height: 32px;
  border: none;
  border-left: 1px solid var(--color-border-subtle);
  border-radius: 0;
  box-shadow: none;
  background: transparent;
  text-align: center;
  font-variant-numeric: tabular-nums;
}

.protocol-input-group__port:focus {
  border-color: transparent;
  box-shadow: none;
  background: color-mix(in srgb, var(--color-primary-subtle) 35%, transparent);
}

.backends-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
  margin-bottom: 0.4rem;
}

.backends-list {
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
}

.backends-list--multi {
  gap: 0.35rem;
}

.backend-item {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  padding: 0.35rem 0.45rem;
  background: color-mix(in srgb, var(--color-bg-subtle) 45%, transparent);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-md);
  transition: all var(--duration-fast);
  cursor: grab;
}

.backend-item--flat {
  padding: 0;
  background: transparent;
  border: none;
  border-radius: 0;
  cursor: default;
}

.backend-item:active {
  cursor: grabbing;
}

.backend-item--flat:active {
  cursor: default;
}

.backend-item--dragging {
  opacity: 0.5;
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.backend-item--drag-over {
  border-top: 2px solid var(--color-primary);
}

.backend-drag-handle {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 0.15rem;
  color: var(--color-text-muted);
  cursor: grab;
  border-radius: var(--radius-sm);
  flex-shrink: 0;
}

.backend-drag-handle:hover {
  background: var(--color-bg-hover);
  color: var(--color-text-secondary);
}

.backend-drag-handle:active {
  cursor: grabbing;
}

.backend-address-input {
  flex: 1;
  min-width: 0;
}

.backend-address-input--flat {
  height: 32px;
}

.tag-input {
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  transition: all var(--duration-fast) var(--ease-default);
  overflow: hidden;
}

.tag-input:focus-within {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.tag-input__container {
  display: flex;
  flex-wrap: wrap;
  gap: 0.25rem;
  padding: 0.25rem 0.4rem;
  align-items: center;
  min-height: 32px;
}

.tag-input__field {
  flex: 1;
  min-width: 80px;
  border: none;
  background: transparent;
  padding: 0.15rem;
  font-size: 0.8125rem;
  color: var(--color-text-primary);
  outline: none;
  max-width: 100%;
}

.tag-input__field::placeholder {
  color: var(--color-text-muted);
}

.tag {
  display: inline-flex;
  align-items: center;
  gap: 0.2rem;
  padding: 1px 7px;
  background: var(--color-primary-subtle);
  border: none;
  border-radius: var(--radius-full);
  font-size: 0.6875rem;
  font-weight: 600;
  font-family: var(--font-mono);
  color: var(--color-primary);
  line-height: 1.35;
}

.tag__remove {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 14px;
  height: 14px;
  border: none;
  background: transparent;
  color: var(--color-text-muted);
  cursor: pointer;
  padding: 0;
  border-radius: 50%;
  transition: all var(--duration-fast);
}

.tag__remove:hover {
  background: var(--color-danger-50);
  color: var(--color-danger);
}

.toggle {
  display: flex;
  align-items: flex-start;
  gap: 0.55rem;
  cursor: pointer;
}

.toggle--disabled {
  cursor: not-allowed;
  opacity: 0.72;
}

.toggle__input {
  position: absolute;
  opacity: 0;
  width: 0;
  height: 0;
}

.toggle__slider {
  position: relative;
  width: 36px;
  height: 20px;
  background: var(--color-border-strong);
  border-radius: var(--radius-full);
  transition: background var(--duration-fast) var(--ease-default);
  flex-shrink: 0;
  margin-top: 1px;
}

.toggle__slider::after {
  content: '';
  position: absolute;
  top: 2px;
  left: 2px;
  width: 16px;
  height: 16px;
  background: white;
  border-radius: var(--radius-full);
  transition: transform var(--duration-fast) var(--ease-bounce);
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.12);
}

.toggle__input:checked + .toggle__slider {
  background: var(--color-primary);
}

.toggle__input:checked + .toggle__slider::after {
  transform: translateX(16px);
}

.toggle__input:focus-visible + .toggle__slider {
  box-shadow: var(--shadow-focus);
}

.toggle__input:disabled + .toggle__slider {
  opacity: 0.75;
}

.toggle__label {
  font-size: 0.8125rem;
  color: var(--color-text-primary);
}

.more-toggle {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
  width: 100%;
  padding: 0;
  border: none;
  background: transparent;
  color: inherit;
  cursor: pointer;
  font: inherit;
  text-align: left;
}

.more-toggle:hover .more-toggle__title {
  color: var(--color-primary);
}

.more-toggle__main {
  display: flex;
  flex-direction: column;
  gap: 0.15rem;
  min-width: 0;
}

.more-toggle__title {
  font-size: 0.8125rem;
  font-weight: 650;
  color: var(--color-text-primary);
  line-height: 1.3;
  transition: color var(--duration-fast) var(--ease-default);
}

.more-toggle__summary {
  font-size: 0.6875rem;
  color: var(--color-text-muted);
  line-height: 1.35;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.more-toggle__chevron {
  flex-shrink: 0;
  color: var(--color-text-tertiary);
  transition: transform var(--duration-fast) var(--ease-default);
}

.settings-card--more-open .more-toggle__chevron {
  transform: rotate(180deg);
}

.more-panel {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
  margin-top: 0.7rem;
  padding-top: 0.7rem;
  border-top: 1px solid var(--color-border-subtle);
}

.settings-card--more {
  gap: 0;
  padding: 0.7rem 0.8rem;
}

.btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 0.35rem;
  padding: 0.4rem 0.75rem;
  border: none;
  border-radius: var(--radius-md);
  font-size: 0.8125rem;
  font-weight: 600;
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-default);
  font-family: inherit;
  line-height: 1.3;
}

.btn--sm {
  padding: 0.25rem 0.55rem;
  font-size: 0.75rem;
}

.btn--icon {
  padding: 0.3rem;
  border-radius: var(--radius-md);
}

.btn--primary {
  background: var(--color-primary);
  color: white;
}

.btn--primary:hover:not(:disabled) {
  opacity: 0.92;
}

.btn--secondary {
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  color: var(--color-text-primary);
}

.btn--secondary:hover {
  background: var(--color-bg-hover);
  border-color: var(--color-border-strong);
}

.btn--danger-ghost {
  background: transparent;
  color: var(--color-text-muted);
}

.btn--danger-ghost:hover {
  color: var(--color-danger);
  background: var(--color-danger-50);
}

.btn--full {
  width: 100%;
  height: 2.25rem;
  margin-top: 0.15rem;
}

.btn:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.relay-alert {
  display: flex;
  align-items: flex-start;
  gap: 0.55rem;
  padding: 0.7rem 0.8rem;
  border-radius: var(--radius-md);
  font-size: 0.75rem;
  line-height: 1.45;
}

.relay-alert svg {
  flex-shrink: 0;
  margin-top: 0.05rem;
}

.relay-alert--warning {
  background: color-mix(in srgb, var(--color-warning-50) 80%, transparent);
  border: 1px solid color-mix(in srgb, var(--color-warning) 35%, transparent);
  color: var(--color-text-secondary);
}

.relay-alert--warning svg {
  color: var(--color-warning);
}

.relay-alert--info {
  background: color-mix(in srgb, var(--color-primary-subtle) 55%, transparent);
  border: 1px solid color-mix(in srgb, var(--color-primary) 25%, transparent);
  color: var(--color-text-secondary);
}

.relay-alert--info svg {
  color: var(--color-primary);
}

.relay-link {
  display: inline-flex;
  align-items: center;
  gap: 0.3rem;
  padding: 0.25rem 0.5rem;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-surface);
  color: var(--color-text-secondary);
  font-size: 0.6875rem;
  font-weight: 600;
  text-decoration: none;
  transition: all var(--duration-fast);
}

.relay-link:hover {
  border-color: var(--color-primary);
  color: var(--color-primary);
  background: var(--color-primary-subtle);
}

@media (max-width: 720px) {
  .form-row,
  .form-secondary-grid {
    grid-template-columns: 1fr;
  }

  .section-header--split,
  .backend-item:not(.backend-item--flat),
  .backends-header {
    flex-direction: column;
  }

  .backend-item .btn--icon {
    align-self: flex-end;
  }

  .form-tab-panel {
    gap: 0.55rem;
  }

  .protocol-input-group--listen {
    flex-wrap: wrap;
  }

  .protocol-input-group__port {
    width: 100%;
    border-left: none;
    border-top: 1px solid var(--color-border-subtle);
  }
}
</style>

<template>
  <form class="rule-form" @submit.prevent="handleSubmit">
    <div v-if="error" class="form-error">
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
        <circle cx="12" cy="12" r="10"/>
        <line x1="12" y1="8" x2="12" y2="12"/>
        <line x1="12" y1="16" x2="12.01" y2="16"/>
      </svg>
      {{ error }}
    </div>

    <!-- Tab Bar -->
    <div class="form-tabs">
      <button
        type="button"
        class="form-tabs__btn"
        :class="{ active: activeTab === 'basic' }"
        @click="activeTab = 'basic'"
      >
        基础配置
      </button>
      <button
        type="button"
        class="form-tabs__btn"
        :class="{ active: activeTab === 'protocol' }"
        @click="activeTab = 'protocol'"
      >
        协议与监听
      </button>
      <button
        type="button"
        class="form-tabs__btn"
        :class="{ active: activeTab === 'relay' }"
        @click="activeTab = 'relay'"
      >
        Relay 配置
      </button>
    </div>

    <!-- Tab 1: 基础配置 -->
    <div v-show="activeTab === 'basic'" class="form-tab-panel">
      <!-- Protocol + Listen Address/Port -->
      <div class="form-section">
        <h3 class="form-section__title">协议与监听地址</h3>

        <div class="form-row">
          <div class="form-group">
            <label class="form-label form-label--required">协议</label>
            <select v-model="form.protocol" class="input" @change="handleProtocolChange">
              <option value="tcp">TCP</option>
              <option value="udp">UDP</option>
            </select>
          </div>
          <div class="form-group">
            <label class="form-label form-label--required">监听地址</label>
            <input v-model="form.listen_host" class="input" placeholder="0.0.0.0">
          </div>
          <div class="form-group">
            <label class="form-label form-label--required">监听端口</label>
            <input v-model.number="form.listen_port" class="input" type="number" min="1" max="65535" placeholder="25565" @input="updateAutoTags">
          </div>
        </div>
      </div>

      <!-- Backends -->
      <div v-if="requiresBackends" class="form-section">
        <div class="backends-header">
          <h3 class="form-section__title">后端服务器</h3>
          <button type="button" class="btn btn--sm btn--secondary" @click="addBackend">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <line x1="12" y1="5" x2="12" y2="19"/>
              <line x1="5" y1="12" x2="19" y2="12"/>
            </svg>
            添加后端
          </button>
        </div>

        <div class="backends-list">
          <div
            v-for="(backend, index) in form.backends"
            :key="backend.id"
            class="backend-item"
            :class="{
              'backend-item--dragging': dragState.from === index,
              'backend-item--drag-over': dragState.to === index && dragState.from !== index
            }"
            draggable="true"
            @dragstart="onDragStart(index)"
            @dragover.prevent="onDragOver(index)"
            @drop="onDrop(index)"
            @dragend="onDragEnd"
          >
            <div class="backend-drag-handle" title="拖动排序">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <circle cx="9" cy="5" r="1"/>
                <circle cx="9" cy="12" r="1"/>
                <circle cx="9" cy="19" r="1"/>
                <circle cx="15" cy="5" r="1"/>
                <circle cx="15" cy="12" r="1"/>
                <circle cx="15" cy="19" r="1"/>
              </svg>
            </div>

            <div class="backend-fields--inline">
              <input
                v-model="backend.address"
                class="input backend-address-input"
                placeholder="IP:端口 或 域名:端口"
                @blur="parseBackendAddress(index)"
              >
            </div>

            <button
              v-if="form.backends.length > 1"
              type="button"
              class="btn btn--icon btn--danger-ghost"
              @click="removeBackend(index)"
              title="删除后端"
            >
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <polyline points="3 6 5 6 21 6"/>
                <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
              </svg>
            </button>
          </div>
        </div>
      </div>

      <!-- Load Balancing + Tags + Enabled + Submit -->
      <div class="form-section">
        <h3 class="form-section__title">其他设置</h3>

        <div class="form-group">
          <label class="form-label">负载均衡策略</label>
          <select v-model="form.load_balancing.strategy" class="input" @change="handleStrategyChange">
            <option value="adaptive">自适应 (Adaptive)</option>
            <option value="round_robin">轮询 (Round Robin)</option>
            <option value="random">随机 (Random)</option>
          </select>
        </div>

        <div class="form-group">
          <label class="form-label">分类标签</label>
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
                placeholder="输入标签按回车..."
                @keydown.enter.prevent="addTag"
              >
            </div>
          </div>
        </div>

        <label class="toggle-row">
          <input v-model="form.enabled" type="checkbox" class="toggle__input">
          <span class="toggle__slider"></span>
          <span class="toggle__label">启用规则</span>
        </label>

        <button type="submit" class="btn btn--primary btn--full" :disabled="createL4Rule.isPending.value || updateL4Rule.isPending.value">
          {{ isEdit ? '保存修改' : '创建规则' }}
        </button>
      </div>
    </div>

    <!-- Tab 2: 协议与监听 -->
    <div v-show="activeTab === 'protocol'" class="form-tab-panel">
      <!-- Listen Mode -->
      <div class="form-section">
        <h3 class="form-section__title">监听模式</h3>

        <div class="form-group">
          <label class="form-label form-label--required">监听模式</label>
          <select v-model="form.listen_mode" class="input">
            <option value="tcp">{{ form.protocol === 'udp' ? 'UDP 转发' : 'TCP 转发' }}</option>
            <option value="proxy">SOCKS / HTTP 代理</option>
            <option value="wireguard">WireGuard</option>
          </select>
        </div>

        <div v-if="form.protocol === 'udp' && form.listen_mode === 'proxy'" class="form-help form-help--warning">
          UDP SOCKS5 入口依赖同监听地址、同端口的 TCP SOCKS5 入口规则完成认证与 UDP ASSOCIATE。
        </div>

        <!-- WireGuard inbound config -->
        <div v-if="isWireGuardInbound" class="form-row">
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
            <label class="form-label">WireGuard 入站模式</label>
            <select v-model="form.wireguard_inbound_mode" class="input">
              <option v-if="form.protocol === 'tcp'" value="transparent">透明</option>
              <option value="address">地址</option>
            </select>
          </div>
        </div>
        <p v-if="isWireGuardInbound && form.wireguard_inbound_mode === 'address'" class="form-help">
          监听 Host 自动使用所选 WireGuard 配置的第一个地址。
        </p>
        <div v-if="isWireGuardInbound" class="form-help">
          WireGuard 透明入口会匹配已接入所选 Profile 的客户端流量；地址入口监听 Host 自动使用所选 Profile 的第一个地址。
        </div>

        <!-- Proxy entry auth -->
        <div v-if="isProxyEntryAuthAvailable" class="advanced-checks">
          <label class="backend-checkbox">
            <input v-model="form.proxy_entry_auth.enabled" type="checkbox">
            <span>启用入口认证</span>
          </label>
        </div>
        <div v-if="isProxyEntryAuthAvailable && form.proxy_entry_auth.enabled" class="form-row">
          <div class="form-group">
            <label class="form-label">用户名</label>
            <input v-model="form.proxy_entry_auth.username" class="input" autocomplete="off">
          </div>
          <div class="form-group">
            <label class="form-label">密码</label>
            <input v-model="form.proxy_entry_auth.password" class="input" type="password" autocomplete="new-password">
          </div>
        </div>
      </div>

      <!-- Egress Mode -->
      <div v-if="supportsProxyEgress" class="form-section">
        <h3 class="form-section__title">出口模式</h3>

        <div class="form-group">
          <label class="form-label">出口模式</label>
          <select v-model="form.proxy_egress_mode" class="input">
            <option v-if="form.listen_mode === 'wireguard'" value="">后端转发</option>
            <option value="relay">Relay</option>
            <option value="proxy">SOCKS / HTTP 代理</option>
            <option value="wireguard">WireGuard</option>
          </select>
        </div>

        <!-- Proxy egress URL -->
        <div v-if="isProxyEntry && form.proxy_egress_mode === 'proxy'" class="form-group">
          <label class="form-label">出口代理 URL</label>
          <input v-model="form.proxy_egress_url" class="input" placeholder="socks://user:pass@127.0.0.1:1080">
        </div>

        <!-- WireGuard egress config -->
        <div v-if="isWireGuardEgress" class="form-row">
          <div v-if="canSelectWireGuardEgressProfile" class="form-group">
            <label class="form-label form-label--required">WireGuard 出口来源</label>
            <select v-model="form.wireguard_egress_source" class="input">
              <option value="uri">WireGuard URI</option>
              <option value="profile">WireGuard 配置</option>
            </select>
          </div>

          <div v-if="isWireGuardEgressProfileSource" class="form-group">
            <label class="form-label form-label--required">WireGuard 配置</label>
            <select v-model.number="form.wireguard_profile_id" class="input">
              <option value="">请选择配置</option>
              <option v-for="profile in enabledWireGuardProfiles" :key="profile.id" :value="Number(profile.id)">
                {{ profile.name || profile.id }}
              </option>
            </select>
          </div>

          <div v-if="isWireGuardEgressUriSource" class="form-group">
            <label class="form-label form-label--required">WireGuard 连接 URI</label>
            <input v-model="form.wireguard_egress_uri" class="input" placeholder="wireguard://user:pass@host:51820" autocomplete="off">
          </div>
        </div>

        <div v-if="isWireGuardEgressProfileSource" class="form-help">
          代理出口将通过所选 WireGuard 配置转发。
        </div>
        <div v-if="isWireGuardEgressUriSource" class="form-help">
          代理出口将通过 WireGuard URI 自动生成出口 Profile。
        </div>
      </div>

      <!-- PROXY Protocol -->
      <div v-if="form.protocol === 'tcp'" class="form-section">
        <h3 class="form-section__title">代理协议</h3>
        <div class="advanced-checks">
          <label class="backend-checkbox">
            <input v-model="form.tuning.proxy_protocol.decode" type="checkbox">
            <span>接收 PROXY Protocol</span>
          </label>
          <label class="backend-checkbox">
            <input v-model="form.tuning.proxy_protocol.send" type="checkbox">
            <span>发送 PROXY Protocol 到上游</span>
          </label>
        </div>
        <div class="form-help">接收: 从客户端/前置代理解析真实 IP；发送: 向后端传递客户端真实 IP</div>
      </div>
    </div>

    <!-- Tab 3: Relay 配置 -->
    <div v-show="activeTab === 'relay'" class="form-tab-panel">
      <div class="form-section">
        <h3 class="form-section__title">Relay 配置</h3>

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

        <div class="settings-card">
          <div class="section-header">
            <div>
              <h3 class="section-title">隐私增强</h3>
              <p class="section-description">仅当首跳 Relay 使用 TLS/TCP 时可启用，用于隐藏内层 SS/TLS 握手特征</p>
            </div>
          </div>
          <label class="toggle toggle--card" :class="{ 'toggle--active': form.relay_obfs, 'toggle--disabled': relayObfsDisabled }">
            <input
              v-model="form.relay_obfs"
              type="checkbox"
              class="toggle__input"
              :disabled="relayObfsDisabled"
            >
            <span class="toggle__slider"></span>
            <span class="toggle__content">
              <span class="toggle__label">启用 Relay 隐私增强</span>
              <span class="toggle__desc">仅对首跳为 TLS/TCP 的 TCP Relay 链路生效</span>
            </span>
          </label>
          <p v-if="relayObfsDisabled" class="form-help">{{ relayObfsUnsupportedReason }}</p>
        </div>

        <div class="relay-help">
          <div class="relay-help__title">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <circle cx="12" cy="12" r="10"/>
              <line x1="12" y1="16" x2="12" y2="12"/>
              <line x1="12" y1="8" x2="12.01" y2="8"/>
            </svg>
            使用说明
          </div>
          <ul class="relay-help__list">
            <li>Relay 链路支持 TCP 和 UDP；UDP 会通过 UOT 或 QUIC Relay 进行中继</li>
            <li>链路按层顺序转发：客户端 → 第 1 层 → 第 2 层 → ... → 后端服务，每层可配置多个并行节点</li>
            <li>每个中继节点需要配置对应的 Relay 监听器</li>
            <li>可通过上下按钮调整链路顺序</li>
            <li>隐私增强仅对首跳为 TLS/TCP 的 TCP 中继生效，UDP 会自动关闭</li>
          </ul>
        </div>
      </div>
    </div>
  </form>
</template>

<script setup>
import { computed, ref, watch } from 'vue'
import { useCreateL4Rule, useUpdateL4Rule } from '../hooks/useL4Rules'
import { useAllRelayListeners } from '../hooks/useRelayListeners'
import { useWireGuardProfiles } from '../hooks/useWireGuardProfiles'
import RelayChainInput from './RelayChainInput.vue'
import { buildProxyEntryAuthPayload } from './l4/proxyEntryAuth'
import { buildProxyEgressURLPayload } from './l4/proxyEgressURL'
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
const isEdit = computed(() => !!props.initialData?.id)
const relayListeners = computed(() => relayListenersData.value ?? [])
const wireGuardProfiles = computed(() => wireGuardProfilesData.value ?? [])
const enabledWireGuardProfiles = computed(() => wireGuardProfiles.value.filter((profile) => {
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
  const initialProxyEgressMode = initialData?.proxy_egress_mode || (initialListenMode === 'wireguard' ? '' : 'relay')
  const initialWireGuardEgressURI = String(initialData?.wireguard_egress_uri || '').trim()
  const initialWireGuardEgressSource = initialWireGuardEgressURI
    ? 'uri'
    : initialData?.wireguard_profile_id == null ? 'uri' : 'profile'
  return {
    protocol,
    listen_host: initialData?.listen_host || '0.0.0.0',
    listen_port: initialData?.listen_port || 0,
    backends: normalizeInitialBackends(initialData),
    load_balancing: {
      strategy: normalizeL4Strategy(initialData?.load_balancing?.strategy),
    },
    tuning: mergeTuning(initialData?.tuning, protocol),
    enabled: initialData?.enabled !== false,
    tags: Array.isArray(initialData?.tags) ? [...initialData.tags] : [],
    listen_mode: initialListenMode,
    proxy_entry_auth: {
      enabled: initialData?.proxy_entry_auth?.enabled === true,
      username: initialData?.proxy_entry_auth?.username || '',
      password: initialData?.proxy_entry_auth?.password || '',
    },
    proxy_egress_mode: initialProxyEgressMode,
    proxy_egress_url: initialData?.proxy_egress_url || '',
    wireguard_egress_source: initialWireGuardEgressSource,
    wireguard_egress_uri: initialWireGuardEgressURI,
    wireguard_profile_id: initialWireGuardEgressSource === 'uri'
      ? ''
      : initialData?.wireguard_profile_id == null ? '' : Number(initialData.wireguard_profile_id),
    wireguard_inbound_mode: initialData?.wireguard_inbound_mode || 'transparent',
    relay_layers: getRelayLayers(initialData),
    relay_obfs: initialData?.relay_obfs === true,
  }
}

const form = ref(createFormState(props.initialData))

const activeTab = ref('basic')
const tagInput = ref('')
const error = ref('')
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

const supportsProxyEgress = computed(() => form.value.listen_mode === 'proxy' || form.value.listen_mode === 'wireguard')
const isProxyEntry = computed(() => form.value.listen_mode === 'proxy' || (form.value.listen_mode === 'wireguard' && form.value.proxy_egress_mode !== ''))
const isProxyEntryAuthAvailable = computed(() => form.value.listen_mode === 'proxy')
const isWireGuardInbound = computed(() => form.value.listen_mode === 'wireguard')
const isWireGuardEgress = computed(() => isProxyEntry.value && form.value.proxy_egress_mode === 'wireguard')
const isWireGuardTransparentForward = computed(() => isWireGuardInbound.value
  && form.value.wireguard_inbound_mode === 'transparent'
  && !isProxyEntry.value)
const requiresBackends = computed(() => !isProxyEntry.value && !isWireGuardTransparentForward.value)
const isWireGuardEgressProfileSource = computed(() => isWireGuardEgress.value && form.value.wireguard_egress_source === 'profile')
const isWireGuardEgressUriSource = computed(() => isWireGuardEgress.value && form.value.wireguard_egress_source === 'uri')
const usesWireGuard = computed(() => isWireGuardInbound.value || isWireGuardEgress.value)
const canSelectWireGuardEgressProfile = computed(() => isEdit.value
  && props.initialData?.proxy_egress_mode === 'wireguard'
  && props.initialData?.wireguard_profile_id != null
  && !String(props.initialData?.wireguard_egress_uri || '').trim())
const isWireGuardAdvancedProfileOverride = computed(() => (isWireGuardInbound.value && form.value.wireguard_inbound_mode === 'address') || isWireGuardEgressProfileSource.value)
const requiresWireGuardProfile = computed(() => isWireGuardInbound.value || isWireGuardEgressProfileSource.value)
const selectedWireGuardProfileID = computed(() => {
  const id = Number(form.value.wireguard_profile_id)
  if (!Number.isInteger(id) || id <= 0) return null
  return enabledWireGuardProfiles.value.some((profile) => Number(profile.id) === id) ? id : null
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
}, { immediate: true })

watch(() => form.value.protocol, (newProto) => {
  form.value.tuning = resetTuningForProtocol(form.value.tuning, newProto)
  if (newProto === 'udp') {
    form.value.relay_obfs = false
    if (form.value.listen_mode === 'wireguard' && form.value.wireguard_inbound_mode !== 'transparent') {
      form.value.wireguard_inbound_mode = 'address'
    }
  }
})

watch([isWireGuardInbound, isWireGuardEgress], ([inbound, egress]) => {
  if (inbound && form.value.protocol === 'udp' && form.value.wireguard_inbound_mode === 'transparent') {
    return
  }
}, { immediate: true })

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
})

const LB_TAG_MAP = { adaptive: 'ADP', round_robin: 'RR', random: 'RND' }
const LB_TAG_SET = new Set(Object.values(LB_TAG_MAP))

function isL4AutoTag(t) {
  return t === 'TCP' || t === 'UDP' || /^:\d+$/.test(t) ||
    /^(TCP|UDP) 监听端口 \d+/.test(t) ||
    t.startsWith('监听端口') || t.startsWith('上游端口') ||
    LB_TAG_SET.has(t)
}

function updateAutoTags() {
  if (isEdit.value) return
  const protocol = form.value.protocol.toUpperCase()
  const listenPort = form.value.listen_port
  const lbTag = LB_TAG_MAP[form.value.load_balancing.strategy]
  form.value.tags = form.value.tags.filter(t => !isL4AutoTag(t))
  const sysTags = [protocol, ...(listenPort ? [`:${listenPort}`] : []), ...(lbTag ? [lbTag] : [])]
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
  const userTags = form.value.tags.filter(t => !isL4AutoTag(t))
  const sysTags = [protocol, ...(listenPort ? [`:${listenPort}`] : []), ...(lbTag ? [lbTag] : [])]

  const validBackends = form.value.backends
    .filter(b => b.host && b.port)
    .map(b => ({
      host: b.host.trim(),
      port: Number(b.port),
    }))

  const proxyEntryAuth = isProxyEntryAuthAvailable.value
    ? buildProxyEntryAuthPayload(props.initialData?.proxy_entry_auth, form.value.proxy_entry_auth)
    : { enabled: false, username: '', password: '' }
  const proxyEgressURL = isProxyEntry.value && form.value.proxy_egress_mode === 'proxy'
    ? buildProxyEgressURLPayload(props.initialData?.proxy_egress_url, form.value.proxy_egress_url)
    : ''

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
    proxy_egress_mode: isProxyEntry.value ? form.value.proxy_egress_mode : '',
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
  if (proxyEgressURL !== undefined) {
    payload.proxy_egress_url = proxyEgressURL
  }
  if (isWireGuardEgressUriSource.value) {
    payload.wireguard_egress_uri = form.value.wireguard_egress_uri.trim()
  }
  if (requiresWireGuardProfile.value) {
    payload.wireguard_profile_id = selectedWireGuardProfileID.value
    if (isWireGuardEgressProfileSource.value) {
      payload.wireguard_profile_override = true
    }
  }
  if (isWireGuardInbound.value) {
    payload.wireguard_inbound_mode = form.value.wireguard_inbound_mode
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
    return
  }
  if (requiresWireGuardProfile.value && selectedWireGuardProfileID.value == null) {
    error.value = 'WireGuard 入站或出口必须选择当前 Agent 已启用的 Profile'
    return
  }
  if (isWireGuardEgressUriSource.value && !form.value.wireguard_egress_uri.trim()) {
    error.value = 'WireGuard URI 不能为空'
    return
  }
  if (!samePortTCPProxyRule.value) {
    error.value = '需要先维护同端口 TCP SOCKS5 入口规则'
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
  gap: var(--space-4);
}

.form-section {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  padding: var(--space-4);
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
}

.form-section__title {
  margin: 0 0 var(--space-1);
  font-size: var(--text-base);
  font-weight: var(--font-semibold);
  color: var(--color-text-primary);
}

.form-row {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
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

.form-help {
  font-size: var(--text-xs);
  color: var(--color-text-muted);
}

.form-help--warning {
  color: var(--color-warning);
  padding: var(--space-2) var(--space-3);
  background: var(--color-warning-50);
  border-radius: var(--radius-md);
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
  transition: all var(--duration-fast) var(--ease-default);
  box-sizing: border-box;
}

.input:focus {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

/* Backends */
.backends-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.backends-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.backend-item {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  transition: all var(--duration-fast);
}

.backend-item:hover {
  border-color: var(--color-border-strong);
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
  padding: var(--space-1);
  color: var(--color-text-muted);
  cursor: grab;
  border-radius: var(--radius-sm);
}

.backend-drag-handle:hover {
  background: var(--color-bg-hover);
  color: var(--color-text-secondary);
}

.backend-drag-handle:active {
  cursor: grabbing;
}

.backend-fields--inline {
  flex: 1;
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-width: 0;
  flex-wrap: wrap;
}

.backend-address-input {
  flex: 1;
  min-width: 120px;
}

.backend-checkbox {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  font-size: var(--text-xs);
  color: var(--color-text-secondary);
  cursor: pointer;
  flex-shrink: 0;
}

.backend-checkbox input[type="checkbox"] {
  width: 16px;
  height: 16px;
  accent-color: var(--color-primary);
}

/* Buttons */
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
  transition: all var(--duration-fast) var(--ease-default);
}

.btn--sm {
  padding: var(--space-1) var(--space-3);
  font-size: var(--text-xs);
}

.btn--primary {
  background: var(--color-primary);
  color: white;
}

.btn--primary:hover:not(:disabled) {
  opacity: 0.9;
  transform: translateY(-1px);
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
  padding: var(--space-2);
}

.btn--danger-ghost:hover {
  color: var(--color-danger);
  background: var(--color-danger-50);
}

.btn--icon {
  padding: var(--space-2);
  border-radius: var(--radius-md);
}

.btn--full {
  width: 100%;
  padding: var(--space-3);
}

.btn:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

/* Tags */
.tag-input {
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  transition: all var(--duration-fast) var(--ease-default);
  max-width: 100%;
  overflow: hidden;
}

.tag-input:focus-within {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.tag-input__container {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  padding: var(--space-1) var(--space-2);
  align-items: center;
  min-height: 36px;
}

.tag-input__field {
  flex: 1;
  min-width: 80px;
  max-width: 200px;
  border: none;
  background: transparent;
  padding: var(--space-1);
  font-size: var(--text-sm);
  color: var(--color-text-primary);
  outline: none;
}

.tag-input__field::placeholder {
  color: var(--color-text-muted);
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
  color: var(--color-text-primary);
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

.form-error {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  background: var(--color-danger-50);
  color: var(--color-danger);
  border-radius: var(--radius-md);
  font-size: var(--text-sm);
}

/* Toggle */
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
  transition: all var(--duration-normal) var(--ease-bounce);
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
  transition: all var(--duration-normal) var(--ease-bounce);
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
  color: var(--color-text-secondary);
}

/* Advanced checks */
.advanced-checks {
  display: flex;
  gap: var(--space-4);
  flex-wrap: wrap;
}

/* Relay alerts */
.relay-alert {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
  border-radius: var(--radius-lg);
  font-size: var(--text-sm);
}

.relay-alert--warning {
  background: var(--color-bg-surface);
  border: 1px solid var(--color-warning);
  color: var(--color-warning);
}

.relay-alert--info {
  background: var(--color-bg-surface);
  border: 1px solid var(--color-primary);
  color: var(--color-primary);
}

.relay-link {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-surface);
  color: var(--color-text-secondary);
  font-size: var(--text-xs);
  text-decoration: none;
  transition: all var(--duration-fast);
}

.relay-link:hover {
  border-color: var(--color-primary);
  color: var(--color-primary);
  background: var(--color-primary-subtle);
}

.relay-help {
  padding: var(--space-3) var(--space-4);
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-xl);
}

.relay-help__title {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  margin-bottom: var(--space-3);
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  color: var(--color-text-primary);
}

.relay-help__title svg {
  color: var(--color-primary);
}

.relay-help__list {
  margin: 0;
  padding-left: var(--space-5);
  font-size: var(--text-sm);
  color: var(--color-text-secondary);
  line-height: 1.8;
}

.relay-help__list li {
  margin-bottom: var(--space-1);
}

/* Settings card (Relay section) */
.settings-card {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  padding: var(--space-4);
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  box-shadow: var(--shadow-sm);
}

/* Section header (Relay section) */
.section-header {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.section-header--split {
  flex-direction: row;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}

.section-title {
  margin: 0;
  font-size: 14px;
  font-weight: var(--font-semibold);
  color: var(--color-text-primary);
  line-height: 1.4;
}

.section-description {
  margin: 0;
  font-size: 13px;
  color: var(--color-text-muted);
  line-height: 1.4;
}

/* Toggle card variant (Relay section) */
.toggle {
  display: flex;
  align-items: flex-start;
  gap: var(--space-3);
  cursor: pointer;
}

.toggle--disabled {
  cursor: not-allowed;
}

.toggle__content {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.toggle--card {
  padding: 10px var(--space-3);
  background: var(--color-bg-surface);
  border-radius: var(--radius-lg);
  border: 1px solid var(--color-border-default);
}

.toggle--card .toggle__label {
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  color: var(--color-text-primary);
}

.toggle--card .toggle__desc {
  font-size: var(--text-xs);
  color: var(--color-text-secondary);
  line-height: 1.5;
  margin-top: var(--space-1);
}

.form-help-text {
  margin: var(--space-2) 0 0 0;
  font-size: var(--text-sm);
  color: var(--color-text-secondary);
  line-height: 1.5;
}

/* Tabs */
.form-tabs {
  display: flex;
  gap: 2px;
  padding: 4px;
  background: var(--color-bg-subtle);
  border-radius: var(--radius-lg);
  border: 1px solid var(--color-border-default);
}

.form-tabs__btn {
  flex: 1;
  padding: var(--space-2) var(--space-3);
  border: none;
  background: transparent;
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-default);
  text-align: center;
}

.form-tabs__btn:hover {
  color: var(--color-text-primary);
  background: var(--color-bg-hover);
}

.form-tabs__btn.active {
  background: var(--color-bg-surface);
  color: var(--color-primary);
  box-shadow: var(--shadow-sm);
  font-weight: var(--font-semibold);
}

.form-tab-panel {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}
</style>
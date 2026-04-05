<template>
  <form class="rule-form" @submit.prevent="handleSubmit">
    <!-- Tab Bar -->
    <div class="form-tabs">
      <button type="button" class="form-tabs__btn" :class="{ 'form-tabs__btn--active': activeTab === 'basic' }" @click="activeTab = 'basic'">基础配置</button>
      <button type="button" class="form-tabs__btn" :class="{ 'form-tabs__btn--active': activeTab === 'advanced' }" @click="activeTab = 'advanced'">高级调优 <span v-if="hasAdvancedTuning" class="form-tabs__badge">已配置</span></button>
      <button type="button" class="form-tabs__btn" :class="{ 'form-tabs__btn--active': activeTab === 'protocol' }" @click="activeTab = 'protocol'">协议与监听 <span v-if="hasProtocolTuning" class="form-tabs__badge">已配置</span></button>
    </div>

    <!-- Tab 1: Basic -->
    <div v-if="activeTab === 'basic'" class="form-tab-panel">
      <!-- Protocol -->
      <div class="form-row">
        <div class="form-group">
          <label class="form-label form-label--required">协议</label>
          <select v-model="form.protocol" class="input" @change="handleProtocolChange">
            <option value="tcp">TCP</option>
            <option value="udp">UDP</option>
          </select>
        </div>
      </div>

      <!-- Listen Address -->
      <div class="form-row">
        <div class="form-group">
          <label class="form-label form-label--required">监听地址</label>
          <input v-model="form.listen_host" class="input" placeholder="0.0.0.0">
        </div>
        <div class="form-group">
          <label class="form-label form-label--required">监听端口</label>
          <input v-model.number="form.listen_port" class="input" type="number" min="1" max="65535" placeholder="25565" @input="updateAutoTags">
        </div>
      </div>

      <!-- Load Balancing Strategy -->
      <div class="form-group">
        <label class="form-label">负载均衡策略</label>
        <select v-model="form.load_balancing.strategy" class="input" @change="handleStrategyChange">
          <option value="round_robin">轮询 (Round Robin)</option>
          <option value="least_conn">最少连接 (Least Connections)</option>
          <option value="random">随机 (Random)</option>
          <option value="hash">哈希 (Hash)</option>
        </select>
      </div>

      <!-- Hash Key (only when strategy is hash) -->
      <div v-if="form.load_balancing.strategy === 'hash'" class="form-group">
        <label class="form-label">哈希键</label>
        <input v-model="form.load_balancing.hash_key" class="input" placeholder="$binary_remote_addr">
        <div class="form-help">例如: $remote_addr, $binary_remote_addr, $ssl_session_id</div>
      </div>

      <!-- Backends List -->
      <div class="form-group">
        <div class="backends-header">
          <label class="form-label form-label--required">后端服务器</label>
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
            :class="{ 'backend-item--dragging': draggingIndex === index }"
          >
            <div class="backend-drag-handle" @mousedown="startDrag(index)" title="拖动排序">
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
              <div class="backend-weight-wrapper">
                <span class="backend-weight-label">权重</span>
                <input
                  v-model.number="backend.weight"
                  class="input backend-weight-input"
                  type="number"
                  min="1"
                  max="100"
                >
              </div>
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

      <!-- Tags -->
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

      <div class="form-group">
        <label class="form-label">Relay 链路</label>
        <RelayChainInput
          v-model="form.relay_chain"
          :listeners="relayListeners"
          :disabled="form.protocol === 'udp'"
        />
        <div class="form-help" v-if="form.protocol === 'udp'">UDP 当前不支持 Relay 链路，已强制直连</div>
      </div>
    </div>

    <!-- Tab 2: Advanced Tuning -->
    <div v-if="activeTab === 'advanced'" class="form-tab-panel">
      <!-- Timeouts -->
      <div class="advanced-group">
        <div class="advanced-group__title">超时设置</div>
        <div class="form-row">
          <div class="form-group">
            <label class="form-label">proxy_connect_timeout</label>
            <input v-model="form.tuning.proxy.connect_timeout" class="input" placeholder="10s">
          </div>
          <div class="form-group">
            <label class="form-label">proxy_timeout</label>
            <input v-model="form.tuning.proxy.idle_timeout" class="input" :placeholder="form.protocol === 'udp' ? '20s' : '10m'">
            <div class="form-help">如: 10s, 5m, 1h</div>
          </div>
        </div>
      </div>

      <!-- Health Check -->
      <div class="advanced-group">
        <div class="advanced-group__title">健康检查</div>
        <div class="form-row">
          <div class="form-group">
            <label class="form-label">max_fails</label>
            <input v-model.number="form.tuning.upstream.max_fails" class="input" type="number" min="0" placeholder="3">
          </div>
          <div class="form-group">
            <label class="form-label">fail_timeout</label>
            <input v-model="form.tuning.upstream.fail_timeout" class="input" placeholder="30s">
          </div>
        </div>
      </div>

      <!-- Connection Limit -->
      <div class="advanced-group">
        <div class="advanced-group__title">连接限制</div>
        <div class="form-row">
          <div class="form-group">
            <label class="form-label">limit_conn count</label>
            <input v-model.number="form.tuning.limit_conn.count" class="input" type="number" min="0" placeholder="不限制">
          </div>
          <div class="form-group">
            <label class="form-label">limit_conn_zone key</label>
            <input v-model="form.tuning.limit_conn.key" class="input" placeholder="$binary_remote_addr">
          </div>
          <div class="form-group">
            <label class="form-label">zone_size</label>
            <input v-model="form.tuning.limit_conn.zone_size" class="input" placeholder="10m">
          </div>
        </div>
      </div>

      <!-- Buffer -->
      <div class="advanced-group">
        <div class="advanced-group__title">缓冲与上游</div>
        <div class="form-row">
          <div class="form-group">
            <label class="form-label">proxy_buffer_size</label>
            <input v-model="form.tuning.proxy.buffer_size" class="input" placeholder="16k">
          </div>
          <div class="form-group">
            <label class="form-label">upstream max_conns</label>
            <input v-model.number="form.tuning.upstream.max_conns" class="input" type="number" min="0" placeholder="0 (不限制)">
          </div>
        </div>
      </div>

      <!-- Backend Extensions (backup / max_conns per backend) -->
      <div v-if="form.backends.length > 0" class="advanced-group">
        <div class="advanced-group__title">后端扩展</div>
        <div class="advanced-backends-list">
          <div
            v-for="(backend, index) in form.backends"
            :key="'adv-' + backend.id"
            class="advanced-backend-row"
          >
            <span class="advanced-backend-label">{{ backend.address || `backend ${index + 1}` }}</span>
            <label class="backend-checkbox" title="backup server">
              <input v-model="backend.backup" type="checkbox">
              <span>backup</span>
            </label>
            <div class="backend-weight-wrapper">
              <span class="backend-weight-label">max_conns</span>
              <input
                v-model.number="backend.max_conns"
                class="input backend-weight-input"
                type="number"
                min="0"
                placeholder="0"
              >
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- Tab 3: Protocol & Listen -->
    <div v-if="activeTab === 'protocol'" class="form-tab-panel">
      <!-- Proxy Protocol -->
      <div v-if="form.protocol === 'tcp'" class="advanced-group">
        <div class="advanced-group__title">代理协议 (PROXY Protocol)</div>
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

      <!-- Listen Options -->
      <div class="advanced-group">
        <div class="advanced-group__title">监听选项</div>
        <div class="advanced-checks">
          <label class="backend-checkbox">
            <input v-model="form.tuning.listen.reuseport" type="checkbox">
            <span>reuseport</span>
          </label>
          <label class="backend-checkbox">
            <input v-model="form.tuning.listen.tcp_nodelay" type="checkbox">
            <span>tcp_nodelay</span>
          </label>
          <label class="backend-checkbox">
            <input v-model="form.tuning.listen.so_keepalive" type="checkbox">
            <span>so_keepalive</span>
          </label>
        </div>
        <div class="form-row">
          <div class="form-group">
            <label class="form-label">backlog</label>
            <input v-model.number="form.tuning.listen.backlog" class="input" type="number" min="0" placeholder="默认">
          </div>
        </div>
      </div>

      <!-- UDP-specific -->
      <div v-if="form.protocol === 'udp'" class="advanced-group">
        <div class="advanced-group__title">UDP 专属</div>
        <div class="form-row">
          <div class="form-group">
            <label class="form-label">proxy_requests</label>
            <input v-model.number="form.tuning.proxy.udp_proxy_requests" class="input" type="number" min="0" placeholder="默认">
          </div>
          <div class="form-group">
            <label class="form-label">proxy_responses</label>
            <input v-model.number="form.tuning.proxy.udp_proxy_responses" class="input" type="number" min="0" placeholder="默认">
          </div>
        </div>
      </div>
    </div>

    <div v-if="error" class="form-error">
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
        <circle cx="12" cy="12" r="10"/>
        <line x1="12" y1="8" x2="12" y2="12"/>
        <line x1="12" y1="16" x2="12.01" y2="16"/>
      </svg>
      {{ error }}
    </div>

    <!-- Enabled Toggle -->
    <label class="toggle-row">
      <input v-model="form.enabled" type="checkbox" class="toggle__input">
      <span class="toggle__slider"></span>
      <span class="toggle__label">启用规则</span>
    </label>

    <!-- Submit -->
    <button type="submit" class="btn btn--primary btn--full" :disabled="createL4Rule.isPending.value || updateL4Rule.isPending.value">
      {{ isEdit ? '保存修改' : '创建规则' }}
    </button>
  </form>
</template>

<script setup>
import { computed, ref, watch } from 'vue'
import { useCreateL4Rule, useUpdateL4Rule } from '../hooks/useL4Rules'
import { useRelayListeners } from '../hooks/useRelayListeners'
import RelayChainInput from './RelayChainInput.vue'

const props = defineProps({
  initialData: { type: Object, default: null },
  agentId: { type: [String, Object], required: true }
})
const emit = defineEmits(['success'])

// Pass agentId directly - hooks use unref() to handle both strings and refs
const createL4Rule = useCreateL4Rule(props.agentId)
const updateL4Rule = useUpdateL4Rule(props.agentId)
const { data: relayListenersData } = useRelayListeners(props.agentId)
const isEdit = computed(() => !!props.initialData?.id)
const relayListeners = computed(() => relayListenersData.value ?? [])

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
    weight: data.weight || 1,
    resolve: data.resolve || false,
    backup: data.backup || false,
    max_conns: data.max_conns || 0,
  }
}

function getDefaultTuning(protocol = 'tcp') {
  const isUdp = protocol === 'udp'
  return {
    listen: {
      reuseport: isUdp,
      backlog: null,
      so_keepalive: false,
      tcp_nodelay: true,
    },
    proxy: {
      connect_timeout: '10s',
      idle_timeout: isUdp ? '20s' : '10m',
      buffer_size: '16k',
      udp_proxy_requests: null,
      udp_proxy_responses: null,
    },
    upstream: {
      max_conns: 0,
      max_fails: 3,
      fail_timeout: '30s',
    },
    limit_conn: {
      key: '$binary_remote_addr',
      count: null,
      zone_size: '10m',
    },
    proxy_protocol: {
      decode: false,
      send: false,
    },
  }
}

function mergeTuning(saved, protocol) {
  const defaults = getDefaultTuning(protocol)
  if (!saved || typeof saved !== 'object') return defaults
  return {
    listen: { ...defaults.listen, ...saved.listen },
    proxy: { ...defaults.proxy, ...saved.proxy },
    upstream: { ...defaults.upstream, ...saved.upstream },
    limit_conn: { ...defaults.limit_conn, ...saved.limit_conn },
    proxy_protocol: { ...defaults.proxy_protocol, ...saved.proxy_protocol },
  }
}

const initialProtocol = props.initialData?.protocol || 'tcp'

const initialBackends = props.initialData?.backends?.length > 0
  ? props.initialData.backends.map(b => createBackend(b))
  : (props.initialData?.upstream_host
    ? [createBackend({
        address: `${props.initialData.upstream_host}:${props.initialData.upstream_port || ''}`,
        weight: 1,
        resolve: false,
      })]
    : [createBackend()])

const form = ref({
  protocol: initialProtocol,
  listen_host: props.initialData?.listen_host || '0.0.0.0',
  listen_port: props.initialData?.listen_port || 0,
  backends: initialBackends,
  load_balancing: {
    strategy: props.initialData?.load_balancing?.strategy || 'round_robin',
    hash_key: props.initialData?.load_balancing?.hash_key || '$binary_remote_addr',
    zone_size: props.initialData?.load_balancing?.zone_size || '128k',
  },
  tuning: mergeTuning(props.initialData?.tuning, initialProtocol),
  enabled: props.initialData?.enabled !== false,
  tags: Array.isArray(props.initialData?.tags) ? [...props.initialData.tags] : [],
  relay_chain: Array.isArray(props.initialData?.relay_chain) ? [...props.initialData.relay_chain] : [],
})

const showAdvanced = ref(!!props.initialData?.tuning)
const tagInput = ref('')
const draggingIndex = ref(-1)
const error = ref('')

// Detect if tuning has non-default values (including backend extensions)
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

const activeTab = ref('basic')

const hasAdvancedTuning = computed(() => {
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
    t.limit_conn.count !== defaults.limit_conn.count
  )
})

const hasProtocolTuning = computed(() => {
  const defaults = getDefaultTuning(form.value.protocol)
  const t = form.value.tuning
  return (
    t.proxy_protocol.decode !== defaults.proxy_protocol.decode ||
    t.proxy_protocol.send !== defaults.proxy_protocol.send ||
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

// Clear UDP-specific fields when switching to TCP
watch(() => form.value.protocol, (newProto) => {
  if (newProto === 'tcp') {
    form.value.tuning.proxy.udp_proxy_requests = null
    form.value.tuning.proxy.udp_proxy_responses = null
    return
  }
  form.value.relay_chain = []
})

const LB_TAG_MAP = { round_robin: 'RR', least_conn: 'LC', random: 'RND', hash: 'HASH' }
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

function startDrag(index) {
  draggingIndex.value = index
  const handleMouseUp = () => {
    draggingIndex.value = -1
    document.removeEventListener('mouseup', handleMouseUp)
    document.removeEventListener('mouseleave', handleMouseUp)
  }
  document.addEventListener('mouseup', handleMouseUp)
  document.addEventListener('mouseleave', handleMouseUp)
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
      weight: Number(b.weight) || 1,
      resolve: !!b.resolve,
      backup: !!b.backup,
      max_conns: Number(b.max_conns) || 0,
    }))

  const payload = {
    protocol: form.value.protocol,
    listen_host: form.value.listen_host.trim(),
    listen_port: listenPort,
    upstream_host: validBackends[0]?.host || '',
    upstream_port: validBackends[0]?.port || 0,
    backends: validBackends,
    load_balancing: {
      strategy: form.value.load_balancing.strategy,
      ...(form.value.load_balancing.strategy === 'hash'
        ? { hash_key: form.value.load_balancing.hash_key || '$binary_remote_addr' }
        : {}),
      zone_size: form.value.load_balancing.zone_size || '128k',
    },
    enabled: form.value.enabled,
    tags: [...sysTags, ...userTags],
    relay_chain: form.value.protocol === 'tcp' ? [...form.value.relay_chain] : [],
  }

  // Only send tuning if advanced panel has non-default values or editing existing rule with tuning
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
        decode: t.proxy_protocol.decode,
        send: t.proxy_protocol.send,
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
  if (validBackends.length === 0) {
    error.value = '至少需要一个有效的后端服务器'
    return
  }

  const payload = buildPayload()
  try {
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
  padding: var(--space-3);
  background: var(--color-bg-subtle);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  transition: all var(--duration-fast);
}

.backend-item:hover {
  border-color: var(--color-border-strong);
}

.backend-item--dragging {
  opacity: 0.5;
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

.backend-weight-wrapper {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  flex-shrink: 0;
}

.backend-weight-label {
  font-size: var(--text-xs);
  color: var(--color-text-muted);
  white-space: nowrap;
}

.backend-weight-input {
  width: 56px;
  text-align: center;
  padding: var(--space-2) var(--space-1);
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

/* Tab Bar */
.form-tabs {
  display: flex;
  border-bottom: 1px solid var(--color-border-default);
  gap: 0;
  margin-bottom: var(--space-4);
}

.form-tabs__btn {
  padding: var(--space-3) var(--space-4);
  border: none;
  background: transparent;
  cursor: pointer;
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  color: var(--color-text-muted);
  border-bottom: 2px solid transparent;
  transition: all var(--duration-fast);
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.form-tabs__btn:hover { color: var(--color-text-secondary); background: var(--color-bg-hover); }
.form-tabs__btn--active { color: var(--color-primary); border-bottom-color: var(--color-primary); }

.form-tabs__badge {
  font-size: 9px;
  font-weight: var(--font-bold);
  padding: 1px 6px;
  background: var(--color-primary-subtle);
  color: var(--color-primary);
  border-radius: var(--radius-sm);
}

/* Tab Panel */
.form-tab-panel {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  margin-bottom: var(--space-4);
}

.advanced-group {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.advanced-group__title {
  font-size: var(--text-xs);
  font-weight: var(--font-semibold);
  color: var(--color-text-muted);
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.advanced-checks {
  display: flex;
  gap: var(--space-4);
  flex-wrap: wrap;
}

.advanced-backends-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.advanced-backend-row {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-2) var(--space-3);
  background: var(--color-bg-subtle);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-md);
}

.advanced-backend-label {
  flex: 1;
  min-width: 0;
  font-size: var(--text-xs);
  font-family: var(--font-mono);
  color: var(--color-text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
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
  background: var(--gradient-primary);
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
  border-radius: var(--radius-md);
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
  background: var(--gradient-primary);
}

.toggle__input:checked + .toggle__slider::after {
  transform: translateX(20px);
}

.toggle__label {
  font-size: var(--text-sm);
  color: var(--color-text-secondary);
}
</style>

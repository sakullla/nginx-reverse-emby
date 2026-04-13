<template>
  <form @submit.prevent="handleSubmit" class="rule-form">
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
        :class="{ 'form-tabs__btn--active': activeTab === 'headers' }"
        @click="activeTab = 'headers'"
      >
        高级配置
        <span v-if="hasRequestHeaderConfig" class="form-tabs__badge">已配置</span>
      </button>
      <button
        type="button"
        class="form-tabs__btn"
        :class="{ 'form-tabs__btn--active': activeTab === 'relay' }"
        @click="activeTab = 'relay'"
      >
        Relay 配置
        <span v-if="hasRelayConfig" class="form-tabs__badge">已配置</span>
      </button>
    </div>

    <div v-if="activeTab === 'basic'" class="form-tab-panel">
      <!-- 地址配置卡片 -->
      <div class="settings-card">
        <div class="section-header">
          <div>
            <h3 class="section-title">地址配置</h3>
            <p class="section-description">配置用户访问入口和代理目标服务</p>
          </div>
        </div>

        <!-- 前端地址 -->
        <div class="form-group form-group--block">
          <label for="frontend-url" class="form-label form-label--required">前端访问地址</label>
          <div class="input-wrapper">
            <span class="input-wrapper__icon">
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <circle cx="12" cy="12" r="10"/>
                <line x1="2" y1="12" x2="22" y2="12"/>
                <path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/>
              </svg>
            </span>
            <input
              id="frontend-url"
              v-model="form.frontend_url"
              type="text"
              class="input"
              :class="{ 'input--error': errors.frontend_url }"
              placeholder="例如：https://emby.yourdomain.com"
              @input="handleFrontendUrlInput"
            >
          </div>
          <p v-if="errors.frontend_url" class="form-error">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <circle cx="12" cy="12" r="10"/>
              <line x1="12" y1="8" x2="12" y2="12"/>
              <line x1="12" y1="16" x2="12.01" y2="16"/>
            </svg>
            {{ errors.frontend_url }}
          </p>
        </div>

        <div class="form-group">
          <label class="form-label">负载均衡策略</label>
          <div class="select-wrapper">
            <select v-model="form.load_balancing.strategy" class="input">
              <option value="round_robin">轮询 (Round Robin)</option>
              <option value="random">随机 (Random)</option>
            </select>
          </div>
        </div>

        <div class="form-group form-group--block">
          <div class="backends-header">
            <label class="form-label form-label--required">后端服务器</label>
            <button type="button" class="btn btn--sm btn--secondary" @click="addBackend">
              添加后端
            </button>
          </div>
          <div class="backends-list">
            <div
              v-for="(backend, index) in form.backends"
              :key="backend.id"
              class="backend-item"
            >
              <div class="input-wrapper backend-item__input">
                <span class="input-wrapper__icon">
                  <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <rect x="2" y="2" width="20" height="8" rx="2" ry="2"/>
                    <rect x="2" y="14" width="20" height="8" rx="2" ry="2"/>
                    <line x1="6" y1="6" x2="6.01" y2="6"/>
                    <line x1="6" y1="18" x2="6.01" y2="18"/>
                  </svg>
                </span>
                <input
                  :id="index === 0 ? 'backend-url' : undefined"
                  v-model="backend.url"
                  type="text"
                  class="input"
                  :class="{ 'input--error': errors.backend_url }"
                  placeholder="例如：http://192.168.1.100:8096"
                  @input="handleBackendUrlInput"
                >
              </div>
              <button
                v-if="form.backends.length > 1"
                type="button"
                class="btn btn--icon btn--danger-ghost"
                @click="removeBackend(index)"
              >
                删除
              </button>
            </div>
          </div>
          <p v-if="errors.backend_url" class="form-error">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <circle cx="12" cy="12" r="10"/>
              <line x1="12" y1="8" x2="12" y2="12"/>
              <line x1="12" y1="16" x2="12.01" y2="16"/>
            </svg>
            {{ errors.backend_url }}
          </p>
        </div>

        <!-- 使用说明 -->
        <div class="address-help">
          <div class="address-help__title">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <circle cx="12" cy="12" r="10"/>
              <line x1="12" y1="16" x2="12" y2="12"/>
              <line x1="12" y1="8" x2="12.01" y2="8"/>
            </svg>
            使用说明
          </div>
          <ul class="address-help__list">
            <li><strong>前端访问地址</strong>：用户访问的公开地址（VPS 地址），需指向当前服务器的公网 IP 或域名</li>
            <li><strong>后端服务器</strong>：要代理的实际服务地址（如 Emby），支持配置多个后端并按策略分发</li>
          </ul>
        </div>
      </div>

      <!-- 标签配置 -->
      <div class="settings-card">
        <div class="section-header">
          <div>
            <h3 class="section-title">分类标签</h3>
            <p class="section-description">为规则添加标签，便于分类管理和搜索</p>
          </div>
        </div>

        <div class="form-group form-group--block">
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
                id="tag-input"
                v-model="tagInput"
                type="text"
                class="tag-input__field"
                placeholder="输入标签按回车添加..."
                @keydown.enter.prevent="addTag"
              >
            </div>
          </div>
        </div>
      </div>

      <!-- 规则状态 -->
      <div class="settings-card">
        <div class="section-header">
          <div>
            <h3 class="section-title">规则状态</h3>
            <p class="section-description">控制规则的启用状态和行为选项</p>
          </div>
        </div>

        <div class="toggle-list toggle-list--simple">
          <label class="toggle toggle--simple" :class="{ 'toggle--active': form.enabled }">
            <input
              v-model="form.enabled"
              type="checkbox"
              class="toggle__input"
            >
            <span class="toggle__slider"></span>
            <span class="toggle__content">
              <span class="toggle__label">启用此规则</span>
              <span class="toggle__desc">启用后，该代理规则将生效并处理匹配的请求</span>
            </span>
          </label>
        </div>
      </div>
    </div>

    <div v-else-if="activeTab === 'headers'" class="form-tab-panel">
      <!-- 代理行为配置 -->
      <div class="settings-card">
        <div class="section-header">
          <div>
            <h3 class="section-title">代理行为</h3>
            <p class="section-description">控制代理过程中的重定向和客户端 IP 透传行为</p>
          </div>
        </div>

        <div class="toggle-group">
          <label class="toggle toggle--card" :class="{ 'toggle--active': form.proxy_redirect }">
            <input
              v-model="form.proxy_redirect"
              type="checkbox"
              class="toggle__input"
            >
            <span class="toggle__slider"></span>
            <span class="toggle__content">
              <span class="toggle__label">代理 302/307 重定向</span>
              <span class="toggle__desc">开启时，后端返回的重定向地址会被重写为前端地址；关闭时直接透传给客户端</span>
            </span>
          </label>

          <label class="toggle toggle--card" :class="{ 'toggle--active': form.pass_proxy_headers, 'toggle--disabled': proxyHeadersGloballyDisabled }">
            <input
              v-model="form.pass_proxy_headers"
              type="checkbox"
              class="toggle__input"
              :disabled="proxyHeadersGloballyDisabled"
            >
            <span class="toggle__slider"></span>
            <span class="toggle__content">
              <span class="toggle__label">透传客户端 IP</span>
              <span class="toggle__desc">传递 X-Real-IP、X-Forwarded-Host、X-Forwarded-Port、X-Forwarded-For、X-Forwarded-Proto</span>
            </span>
          </label>
        </div>

        <div v-if="proxyHeadersGloballyDisabled" class="global-disabled-notice">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <circle cx="12" cy="12" r="10"/>
            <line x1="12" y1="8" x2="12" y2="12"/>
            <line x1="12" y1="16" x2="12.01" y2="16"/>
          </svg>
          <span>当前全局配置已禁用透传客户端 IP，此开关仅展示规则保存值，不会生效</span>
        </div>
      </div>

      <!-- User-Agent -->
      <div class="settings-card">
        <div class="section-header">
          <div>
            <h3 class="section-title">User-Agent</h3>
            <p class="section-description">覆盖请求中的 User-Agent 头，用于模拟特定客户端</p>
          </div>
        </div>

        <div class="form-row">
          <div class="form-group">
            <label for="ua-preset" class="form-label">预设选择</label>
            <div class="select-wrapper">
              <select id="ua-preset" v-model="selectedUserAgentPreset" class="input">
                <option v-for="preset in UA_PRESETS" :key="preset.id" :value="preset.id">
                  {{ preset.label }}
                </option>
              </select>
            </div>
          </div>

          <div class="form-group">
            <label for="user-agent" class="form-label">自定义值</label>
            <input
              id="user-agent"
              v-model="form.user_agent"
              type="text"
              class="input"
              placeholder="留空表示不覆盖 User-Agent"
              @input="errors.submit = ''"
            >
          </div>
        </div>
      </div>

      <!-- 自定义请求头 -->
      <div class="settings-card">
        <div class="section-header section-header--split">
          <div>
            <h3 class="section-title">自定义请求头</h3>
            <p class="section-description">添加额外的请求头，用于认证、标识等场景</p>
          </div>

          <button type="button" class="btn btn--secondary btn--sm" @click="addCustomHeader">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <line x1="12" y1="5" x2="12" y2="19"/>
              <line x1="5" y1="12" x2="19" y2="12"/>
            </svg>
            添加 Header
          </button>
        </div>

        <div v-if="form.custom_headers.length" class="headers-table">
          <div class="headers-table__head">
            <span class="headers-table__th">Header 名称</span>
            <span class="headers-table__th">Header 值</span>
            <span class="headers-table__th--action"></span>
          </div>
          <div class="headers-table__body">
            <div
              v-for="(header, index) in form.custom_headers"
              :key="`header-${index}`"
              class="headers-table__row"
            >
              <div class="headers-table__cell">
                <input
                  v-model="header.name"
                  type="text"
                  class="input input--compact"
                  :class="{ 'input--error': headerErrors[index]?.name }"
                  placeholder="例如 X-Custom-Header"
                  @input="handleCustomHeaderNameInput(index)"
                >
                <p v-if="headerErrors[index]?.name" class="field-error">{{ headerErrors[index].name }}</p>
              </div>
              <div class="headers-table__cell">
                <input
                  v-model="header.value"
                  type="text"
                  class="input input--compact"
                  :class="{ 'input--error': headerErrors[index]?.value }"
                  placeholder="例如 custom-value"
                  @input="clearHeaderFieldError(index, 'value')"
                >
                <p v-if="headerErrors[index]?.value" class="field-error">{{ headerErrors[index].value }}</p>
              </div>
              <div class="headers-table__cell--action">
                <button
                  type="button"
                  class="btn btn--icon btn--danger-ghost"
                  title="删除 Header"
                  @click="removeCustomHeader(index)"
                >
                  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <polyline points="3 6 5 6 21 6"/>
                    <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
                  </svg>
                </button>
              </div>
            </div>
          </div>
        </div>

        <div v-else class="empty-state">
          <p class="empty-state__title">尚未配置自定义请求头</p>
          <p class="empty-state__desc">点击右上角按钮添加</p>
        </div>
      </div>
    </div>

    <div v-else-if="activeTab === 'relay'" class="form-tab-panel">
      <!-- 提示信息 -->
      <div v-if="!relayListeners.length" class="relay-alert relay-alert--warning">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/>
          <line x1="12" y1="9" x2="12" y2="13"/>
          <line x1="12" y1="17" x2="12.01" y2="17"/>
        </svg>
        <span>当前没有可用的 Relay 监听器，请先创建监听器后再配置链路</span>
      </div>

      <div v-else-if="!form.relay_chain.length" class="relay-alert relay-alert--info">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <circle cx="12" cy="12" r="10"/>
          <line x1="12" y1="16" x2="12" y2="12"/>
          <line x1="12" y1="8" x2="12.01" y2="8"/>
        </svg>
        <span>当前为直连模式，流量将直接转发到后端服务，不经过 Relay 中转</span>
      </div>

      <!-- Relay 链路配置 -->
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
          v-model="form.relay_chain"
          :listeners="relayListeners"
        />
      </div>

      <div class="settings-card">
        <div class="section-header">
          <div>
            <h3 class="section-title">隐私增强</h3>
            <p class="section-description">仅当首跳 Relay 使用 TLS/TCP 时可启用，用于隐藏内层 ss/TLS 握手特征</p>
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
            <span class="toggle__desc">首跳为 TLS/TCP 时生效；首跳 QUIC 链路会自动关闭</span>
          </span>
        </label>
        <p v-if="relayObfsDisabled" class="form-help-text">{{ relayObfsUnsupportedReason }}</p>
      </div>

      <!-- 使用说明 -->
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
          <li>Relay 链路按顺序转发，客户端 → 中继节点 1 → 中继节点 2 → ... → 后端服务</li>
          <li>每个中继节点需要配置对应的 Relay 监听器</li>
          <li>可通过拖拽或上下按钮调整链路顺序</li>
          <li>链路越长延迟越高，建议根据网络拓扑合理规划</li>
        </ul>
      </div>
    </div>

    <p v-if="errors.submit" class="form-error">
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
        <circle cx="12" cy="12" r="10"/>
        <line x1="12" y1="8" x2="12" y2="12"/>
        <line x1="12" y1="16" x2="12.01" y2="16"/>
      </svg>
      {{ errors.submit }}
    </p>

    <button
      type="submit"
      class="btn btn--primary btn--full"
      :disabled="isLoading"
    >
      <span v-if="isLoading" class="spinner spinner--sm"></span>
      <span v-else>{{ isEdit ? '保存修改' : '创建规则' }}</span>
    </button>
  </form>
</template>

<script setup>
import { computed, ref, watch } from 'vue'
import { useCreateRule, useUpdateRule } from '../hooks/useRules'
import { useAllRelayListeners } from '../hooks/useRelayListeners'
import { useAgent } from '../context/AgentContext'
import RelayChainInput from './RelayChainInput.vue'

const UA_PRESETS = [
  { id: 'custom', label: '自定义', value: '' },
  { id: 'chrome', label: 'Chrome', value: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36' },
  { id: 'rodel', label: '小幻影视', value: 'RodelPlayer' },
  { id: 'hills', label: 'Hills', value: 'Hills' },
  { id: 'senplayer', label: 'SenPlayer', value: 'SenPlayer' }
]

const HEADER_NAME_PATTERN = /^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/

const props = defineProps({
  initialData: { type: Object, default: null },
  agentId: { type: [String, Object], required: true }
})

const emit = defineEmits(['success'])

const { systemInfo } = useAgent()

const createRule = useCreateRule(props.agentId)
const updateRule = useUpdateRule(props.agentId)
const { data: relayListenersData } = useAllRelayListeners()
const isEdit = computed(() => !!props.initialData?.id)
const isLoading = computed(() => createRule.isPending.value || updateRule.isPending.value)
const proxyHeadersGloballyDisabled = computed(() => systemInfo.value?.proxy_headers_globally_disabled === true)
const relayListeners = computed(() => relayListenersData.value ?? [])
const SUPPORTED_HTTP_STRATEGIES = new Set(['round_robin', 'random'])
let backendIdCounter = 0

const activeTab = ref('basic')
const form = ref(createDefaultForm())
const tagInput = ref('')
const headerErrors = ref([])
const shouldValidateCustomHeaders = ref(false)
const errors = ref({
  frontend_url: '',
  backend_url: '',
  submit: ''
})

const hasRequestHeaderConfig = computed(() => {
  const hasCustomHeaderConfig = form.value.custom_headers.some((item) => {
    const name = String(item?.name || '').trim()
    const value = item?.value == null ? '' : String(item.value).trim()
    return Boolean(name || value)
  })

  return Boolean(
    form.value.user_agent.trim()
    || hasCustomHeaderConfig
    || form.value.pass_proxy_headers === true
    || form.value.proxy_redirect === false
  )
})

const hasRelayConfig = computed(() => {
  return Array.isArray(form.value.relay_chain) && form.value.relay_chain.length > 0
})
const selectedRelayListeners = computed(() => {
  const listenerMap = new Map(relayListeners.value.map((listener) => [Number(listener.id), listener]))
  return (Array.isArray(form.value.relay_chain) ? form.value.relay_chain : [])
    .map((id) => listenerMap.get(Number(id)) || null)
})
const firstRelayListener = computed(() => selectedRelayListeners.value[0] ?? null)
const relayObfsUnsupportedReason = computed(() => {
  if (!Array.isArray(form.value.relay_chain) || form.value.relay_chain.length === 0) {
    return '当前为直连模式，此选项不会生效'
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

const selectedUserAgentPreset = computed({
  get() {
    const matchedPreset = UA_PRESETS.find((preset) => {
      return preset.id !== 'custom' && preset.value === form.value.user_agent
    })

    return matchedPreset?.id || 'custom'
  },
  set(presetId) {
    const preset = UA_PRESETS.find((item) => item.id === presetId)
    if (!preset) return
    form.value.user_agent = preset.value
    errors.value.submit = ''
  }
})

watch(
  () => props.initialData,
  (value) => {
    form.value = createFormState(value)
    tagInput.value = ''
    headerErrors.value = form.value.custom_headers.map(() => ({ name: '', value: '' }))
    shouldValidateCustomHeaders.value = false
    errors.value.frontend_url = ''
    errors.value.backend_url = ''
    errors.value.submit = ''
    activeTab.value = 'basic'
  },
  { immediate: true }
)

watch([() => form.value.relay_chain, firstRelayListener], ([relayChain]) => {
  if (
    !Array.isArray(relayChain)
    || relayChain.length === 0
    || firstRelayListener.value?.transport_mode !== 'tls_tcp'
  ) {
    form.value.relay_obfs = false
  }
})

function createDefaultForm() {
  return {
    frontend_url: '',
    backends: [createBackend()],
    load_balancing: { strategy: 'round_robin' },
    tags: [],
    enabled: true,
    proxy_redirect: true,
    pass_proxy_headers: false,
    user_agent: '',
    custom_headers: [],
    relay_chain: [],
    relay_obfs: false
  }
}

function createBackend(data = {}) {
  return {
    id: `http-backend-${Date.now()}-${backendIdCounter++}`,
    url: String(data?.url || '').trim()
  }
}

function normalizeHttpStrategy(value) {
  const strategy = String(value || '').trim().toLowerCase()
  return SUPPORTED_HTTP_STRATEGIES.has(strategy) ? strategy : 'round_robin'
}

function normalizeHttpBackends(initialData) {
  if (Array.isArray(initialData?.backends) && initialData.backends.length > 0) {
    const backends = initialData.backends
      .map((backend) => createBackend(backend))
      .filter((backend) => backend.url)
    if (backends.length > 0) return backends
  }

  if (initialData?.backend_url) {
    return [createBackend({ url: initialData.backend_url })]
  }

  return [createBackend()]
}

function createFormState(initialData) {
  if (!initialData) {
    return createDefaultForm()
  }

  return {
    frontend_url: initialData.frontend_url || '',
    backends: normalizeHttpBackends(initialData),
    load_balancing: {
      strategy: normalizeHttpStrategy(initialData.load_balancing?.strategy)
    },
    tags: Array.isArray(initialData.tags) ? [...initialData.tags] : [],
    enabled: initialData.enabled !== false,
    proxy_redirect: initialData.proxy_redirect !== false,
    pass_proxy_headers: initialData.pass_proxy_headers !== false,
    user_agent: String(initialData.user_agent || ''),
    custom_headers: normalizeCustomHeaders(initialData.custom_headers),
    relay_chain: Array.isArray(initialData.relay_chain) ? [...initialData.relay_chain] : [],
    relay_obfs: initialData.relay_obfs === true
  }
}

function normalizeCustomHeaders(value) {
  if (!Array.isArray(value)) return []

  return value.map((item) => ({
    name: String(item?.name || ''),
    value: item?.value == null ? '' : String(item.value)
  }))
}

function handleFrontendUrlInput() {
  errors.value.frontend_url = ''
  errors.value.submit = ''
  updateAutoTags()
}

function handleBackendUrlInput() {
  errors.value.backend_url = ''
  errors.value.submit = ''
}

function addBackend() {
  form.value.backends.push(createBackend())
}

function removeBackend(index) {
  if (form.value.backends.length > 1) {
    form.value.backends.splice(index, 1)
  }
  handleBackendUrlInput()
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

function addCustomHeader() {
  form.value.custom_headers.push({ name: '', value: '' })
  headerErrors.value.push({ name: '', value: '' })
  errors.value.submit = ''
}

function removeCustomHeader(index) {
  form.value.custom_headers.splice(index, 1)
  headerErrors.value.splice(index, 1)

  if (shouldValidateCustomHeaders.value) {
    validateCustomHeaderRows()
  }
}

function clearHeaderFieldError(index, field) {
  errors.value.submit = ''
  if (!headerErrors.value[index]) return
  headerErrors.value[index][field] = ''
}

function handleCustomHeaderNameInput(index) {
  if (shouldValidateCustomHeaders.value) {
    validateCustomHeaderRows()
    errors.value.submit = ''
    return
  }

  clearHeaderFieldError(index, 'name')
}

function isHttpAutoTag(tag) {
  return tag === 'HTTP' || tag === 'HTTPS' || /^:\d+$/.test(tag)
}

function updateAutoTags() {
  if (isEdit.value) return
  const autoTags = computeHttpAutoTags(form.value.frontend_url)
  const userTags = form.value.tags.filter((tag) => !isHttpAutoTag(tag))
  form.value.tags = [...autoTags, ...userTags]
}

function computeHttpAutoTags(urlStr) {
  try {
    const url = new URL(urlStr)
    const protocolTag = url.protocol === 'https:' ? 'HTTPS' : 'HTTP'
    const port = url.port ? parseInt(url.port, 10) : (url.protocol === 'https:' ? 443 : 80)
    return [protocolTag, `:${port}`]
  } catch {
    return []
  }
}

function validateBasicFields() {
  errors.value.frontend_url = ''
  errors.value.backend_url = ''

  if (!form.value.frontend_url.trim()) {
    errors.value.frontend_url = '请输入前端访问地址'
  }

  const validBackends = form.value.backends
    .map((backend) => ({ url: String(backend?.url || '').trim() }))
    .filter((backend) => backend.url)
  if (validBackends.length === 0) {
    errors.value.backend_url = '至少需要一个后端服务器'
  }

  return !errors.value.frontend_url && !errors.value.backend_url
}

function validateCustomHeaderRows() {
  const nextErrors = form.value.custom_headers.map(() => ({ name: '', value: '' }))
  const seenHeaders = new Map()

  form.value.custom_headers.forEach((item, index) => {
    const name = String(item?.name || '').trim()
    const value = item?.value == null ? '' : String(item.value)

    if (!name) {
      nextErrors[index].name = '请输入 Header 名称'
      return
    }

    if (!HEADER_NAME_PATTERN.test(name)) {
      nextErrors[index].name = 'Header 名称格式无效'
      return
    }

    if (name.toLowerCase() === 'user-agent') {
      nextErrors[index].name = 'User-Agent 请使用上方独立字段'
      return
    }

    if (/[\u0000-\u001F\u007F]/.test(value)) {
      nextErrors[index].value = 'Header 值不能包含控制字符'
      return
    }

    const loweredName = name.toLowerCase()
    if (seenHeaders.has(loweredName)) {
      nextErrors[index].name = 'Header 名称重复'
      const firstIndex = seenHeaders.get(loweredName)
      if (!nextErrors[firstIndex].name) {
        nextErrors[firstIndex].name = 'Header 名称重复'
      }
      return
    }

    seenHeaders.set(loweredName, index)
  })

  headerErrors.value = nextErrors
  return nextErrors.every((item) => !item.name && !item.value)
}

function validate() {
  errors.value.submit = ''
  shouldValidateCustomHeaders.value = true

  const basicValid = validateBasicFields()
  const headersValid = validateCustomHeaderRows()

  if (!basicValid) {
    activeTab.value = 'basic'
  } else if (!headersValid) {
    activeTab.value = 'headers'
  }

  return basicValid && headersValid
}

async function handleSubmit() {
  if (!validate()) return

  try {
    const validBackends = form.value.backends
      .map((backend) => ({ url: String(backend?.url || '').trim() }))
      .filter((backend) => backend.url)
    const payload = {
      frontend_url: form.value.frontend_url.trim(),
      backend_url: validBackends[0]?.url || '',
      backends: validBackends,
      load_balancing: {
        strategy: normalizeHttpStrategy(form.value.load_balancing.strategy)
      },
      tags: [...form.value.tags],
      enabled: form.value.enabled,
      proxy_redirect: form.value.proxy_redirect,
      pass_proxy_headers: form.value.pass_proxy_headers,
      user_agent: form.value.user_agent.trim(),
      custom_headers: form.value.custom_headers.map((item) => ({
        name: String(item.name || '').trim(),
        value: item.value ?? ''
      })),
      relay_chain: Array.isArray(form.value.relay_chain) ? [...form.value.relay_chain] : [],
      relay_obfs: firstRelayListener.value?.transport_mode === 'tls_tcp'
        && Array.isArray(form.value.relay_chain)
        && form.value.relay_chain.length > 0
        && form.value.relay_obfs === true
    }

    if (isEdit.value) {
      await updateRule.mutateAsync({ id: props.initialData.id, ...payload })
    } else {
      await createRule.mutateAsync(payload)
    }

    emit('success')
  } catch (err) {
    errors.value.submit = err?.message || '操作失败'
  }
}
</script>

<style scoped>
.rule-form {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

/* 1080p 屏幕优化 */
@media (min-height: 900px) and (min-width: 1200px) {
  .rule-form {
    gap: var(--space-2);
  }
  .form-tabs {
    margin-bottom: 0;
  }
  .form-tabs__btn {
    padding: 6px var(--space-4);
  }
  .settings-card {
    padding: var(--space-3);
    gap: var(--space-2);
  }
  .form-group--block + .form-group--block {
    margin-top: var(--space-2);
  }
  .form-tab-panel > .settings-card {
    padding: var(--space-3);
    gap: var(--space-2);
  }
  .form-tab-panel .toggle--card {
    padding: 10px var(--space-3);
  }
  .address-help,
  .relay-help {
    padding: var(--space-2) var(--space-3);
    margin-top: 0;
  }
  .address-help__list,
  .relay-help__list {
    line-height: 1.5;
  }
  .empty-state {
    padding: var(--space-3);
  }
}

.form-tabs {
  display: flex;
  border-bottom: 1px solid var(--color-border-default);
  gap: 0;
  margin-bottom: 0;
  flex-shrink: 0;
}

.form-tabs__btn {
  padding: var(--space-2) var(--space-4);
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

.form-tabs__btn:hover {
  color: var(--color-text-secondary);
  background: var(--color-bg-hover);
}

.form-tabs__btn--active {
  color: var(--color-primary);
  border-bottom-color: var(--color-primary);
}

.form-tabs__badge {
  font-size: 10px;
  font-weight: var(--font-bold);
  padding: 1px 6px;
  background: var(--color-primary-subtle);
  color: var(--color-primary);
  border-radius: var(--radius-sm);
}

.form-tab-panel {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.form-row {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-2);
}

.form-group {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}

.form-label {
  font-size: 13px;
  font-weight: var(--font-medium);
  color: var(--color-text-secondary);
  line-height: 1.4;
}

.form-label--required::after {
  content: ' *';
  color: var(--color-danger);
}

.form-hint {
  font-size: var(--text-xs);
  color: var(--color-text-muted);
}

.form-help-text {
  margin: var(--space-2) 0 0 0;
  font-size: var(--text-sm);
  color: var(--color-text-secondary);
  line-height: 1.5;
}

.form-group--block {
  display: block;
  width: 100%;
}

.form-group--block + .form-group--block {
  margin-top: var(--space-2);
}

.form-label__hint {
  display: block;
  margin-top: var(--space-1);
  font-size: var(--text-xs);
  font-weight: var(--font-normal);
  color: var(--color-text-muted);
}

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

.settings-card {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-3);
  background: var(--color-bg-subtle);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-lg);
}

/* 高级配置中的卡片更紧凑 */
.form-tab-panel > .settings-card {
  gap: var(--space-2);
  padding: var(--space-3);
}

.form-tab-panel > .settings-card .section-header {
  margin-bottom: 0;
}

.form-tab-panel > .settings-card .section-title {
  font-size: var(--text-sm);
  font-weight: var(--font-semibold);
}

.form-tab-panel > .settings-card .section-description {
  font-size: var(--text-xs);
  color: var(--color-text-muted);
}

/* 高级配置中的 toggle 更紧凑 */
.toggle-group {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.form-tab-panel .toggle--card {
  padding: 10px var(--space-3);
  background: var(--color-bg-surface);
  border-radius: var(--radius-md);
  border: 1px solid var(--color-border-subtle);
}

.form-tab-panel .toggle--card .toggle__label {
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  color: var(--color-text-primary);
}

.form-tab-panel .toggle--card .toggle__desc {
  font-size: var(--text-xs);
  color: var(--color-text-secondary);
  line-height: 1.5;
  margin-top: var(--space-1);
}

/* 全局禁用状态的卡片 */
.settings-card--disabled {
  opacity: 0.75;
}

.global-disabled-notice {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  padding: 6px 10px;
  background: var(--color-warning-50);
  border: 1px solid var(--color-warning);
  border-radius: var(--radius-md);
  font-size: 12px;
  color: var(--color-warning);
}

.form-error,
.form-warning {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-md);
  font-size: var(--text-sm);
}

.form-error {
  background: var(--color-danger-50);
  color: var(--color-danger);
}

.form-warning {
  background: var(--color-warning-50);
  color: var(--color-warning);
}

.field-error {
  margin: 0;
  font-size: var(--text-xs);
  color: var(--color-danger);
}

.input {
  width: 100%;
  min-width: 0;
  padding: 6px 10px;
  font-size: 14px;
  color: var(--color-text-primary);
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  transition: all var(--duration-fast) var(--ease-default);
  font-family: inherit;
  box-sizing: border-box;
  height: 34px;
}

.input:focus {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.input::placeholder {
  color: var(--color-text-muted);
}

.input--error {
  border-color: var(--color-danger);
}

.input-wrapper {
  position: relative;
  overflow: hidden;
}

.input-wrapper__icon {
  position: absolute;
  left: var(--space-4);
  top: 50%;
  transform: translateY(-50%);
  color: var(--color-text-muted);
  pointer-events: none;
  display: flex;
  align-items: center;
}

.input-wrapper .input {
  padding-left: var(--space-10);
}

.backends-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  margin-bottom: var(--space-2);
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
  padding: var(--space-2);
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-md);
}

.backend-item__input {
  flex: 1;
  min-width: 0;
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
  gap: 4px;
  padding: 4px 6px;
  align-items: center;
  min-height: 32px;
}

.tag-input__field {
  flex: 1;
  min-width: 80px;
  border: none;
  background: transparent;
  padding: var(--space-1);
  font-size: var(--text-sm);
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

.toggle-row {
  padding: var(--space-2) 0;
  border-bottom: 1px solid var(--color-border-subtle);
}

.toggle-row:last-child {
  border-bottom: none;
}

.toggle {
  display: flex;
  align-items: flex-start;
  gap: var(--space-3);
  cursor: pointer;
}

.toggle--disabled {
  cursor: not-allowed;
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
  margin-top: 2px;
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
  transition: transform var(--duration-fast) var(--ease-bounce);
  box-shadow: var(--shadow-sm);
}

.toggle__input:checked + .toggle__slider {
  background: var(--gradient-primary);
}

.toggle__input:checked + .toggle__slider::after {
  transform: translateX(20px);
}

.toggle__input:focus-visible + .toggle__slider {
  box-shadow: var(--shadow-focus);
}

.toggle__input:disabled + .toggle__slider {
  opacity: 0.75;
}

.toggle__label {
  font-size: var(--text-sm);
  color: var(--color-text-primary);
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

/* 简化版 Toggle - 用于规则状态 */
.toggle-list--simple {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.toggle--simple {
  display: flex;
  align-items: flex-start;
  gap: var(--space-3);
  padding: var(--space-2) 0;
  border-bottom: 1px solid var(--color-border-subtle);
}

.toggle--simple:last-child {
  border-bottom: none;
}

.toggle--simple .toggle__content {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.toggle--simple .toggle__label {
  font-weight: var(--font-medium);
}

.toggle--simple .toggle__desc {
  font-size: var(--text-xs);
  color: var(--color-text-muted);
  line-height: 1.5;
}

.headers-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.header-row {
  display: flex;
  gap: var(--space-3);
  align-items: flex-start;
  padding: var(--space-3);
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
}

.header-row__fields {
  flex: 1;
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-3);
  min-width: 0;
}

/* 表格样式请求头列表 - 简化设计 */
.headers-table {
  display: flex;
  flex-direction: column;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-md);
  overflow: hidden;
  background: transparent;
}

.headers-table__head {
  display: grid;
  grid-template-columns: 1fr 1fr auto;
  gap: var(--space-3);
  padding: var(--space-2) var(--space-3);
  background: transparent;
  border-bottom: 1px solid var(--color-border-subtle);
  font-size: var(--text-xs);
  font-weight: var(--font-medium);
  color: var(--color-text-muted);
}

.headers-table__th {
  padding-left: var(--space-2);
}

.headers-table__th--action {
  width: 36px;
  text-align: center;
}

.headers-table__body {
  display: flex;
  flex-direction: column;
}

.headers-table__row {
  display: grid;
  grid-template-columns: 1fr 1fr auto;
  gap: var(--space-3);
  align-items: center;
  padding: var(--space-2) var(--space-3);
  background: var(--color-bg-surface);
  border-bottom: 1px solid var(--color-border-subtle);
}

.headers-table__row:last-child {
  border-bottom: none;
}

.headers-table__cell {
  min-width: 0;
}

.headers-table__cell .input {
  border-color: transparent;
  background: var(--color-bg-subtle);
  border-radius: var(--radius-md);
}

.headers-table__cell .input:focus {
  border-color: var(--color-primary);
  background: var(--color-bg-surface);
}

.headers-table__cell--action {
  width: 36px;
  display: flex;
  justify-content: center;
}

.input--compact {
  padding: var(--space-2) var(--space-3);
  font-size: var(--text-sm);
}

.empty-state {
  padding: var(--space-3);
  border: 1px dashed var(--color-border-default);
  border-radius: var(--radius-md);
  text-align: center;
  font-size: 13px;
  color: var(--color-text-muted);
  background: var(--color-bg-surface);
}

.empty-state__title {
  margin: var(--space-1) 0 0;
  font-size: 13px;
  font-weight: var(--font-medium);
  color: var(--color-text-primary);
}

.empty-state__desc {
  margin: 2px 0 0;
  font-size: 12px;
  color: var(--color-text-muted);
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
  transition: all var(--duration-fast) var(--ease-default);
  font-family: inherit;
}

.btn--sm {
  padding: 4px 10px;
  font-size: 12px;
}

.btn--icon {
  padding: var(--space-2);
  border-radius: var(--radius-md);
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
}

.btn--danger-ghost:hover {
  color: var(--color-danger);
  background: var(--color-danger-50);
}

.btn--full {
  width: 100%;
}

.btn:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

/* 请求头配置样式 */

.relay-intro {
  display: flex;
  align-items: center;
  gap: var(--space-4);
  padding: var(--space-5);
  background: linear-gradient(135deg, var(--color-primary-subtle) 0%, var(--color-bg-surface) 100%);
  border: 1px solid var(--color-primary-subtle);
  border-radius: var(--radius-xl);
}

.relay-intro__icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 48px;
  height: 48px;
  background: var(--gradient-primary);
  border-radius: var(--radius-lg);
  color: white;
  flex-shrink: 0;
}

.relay-intro__content {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.relay-intro__title {
  margin: 0;
  font-size: var(--text-lg);
  font-weight: var(--font-semibold);
  color: var(--color-text-primary);
}

.relay-intro__desc {
  margin: 0;
  font-size: var(--text-sm);
  color: var(--color-text-secondary);
}

.relay-alert {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
  border-radius: var(--radius-lg);
  font-size: var(--text-sm);
}

.relay-alert--warning {
  background: var(--color-warning-50);
  border: 1px solid var(--color-warning);
  color: var(--color-warning);
}

.relay-alert--info {
  background: var(--color-primary-subtle);
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

.address-help {
  margin-top: var(--space-2);
  padding: var(--space-2) var(--space-3);
  background: var(--color-bg-subtle);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-md);
}

.address-help__title {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  margin-bottom: var(--space-2);
  font-size: 13px;
  font-weight: var(--font-medium);
  color: var(--color-text-primary);
  line-height: 1.4;
}

.address-help__title svg {
  color: var(--color-primary);
}

.address-help__list {
  margin: 0;
  padding-left: var(--space-4);
  font-size: 13px;
  color: var(--color-text-secondary);
  line-height: 1.5;
}

.address-help__list li {
  margin-bottom: var(--space-1);
}

.relay-help {
  padding: var(--space-2) var(--space-3);
  background: var(--color-bg-subtle);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-md);
}

.relay-help__title {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  margin-bottom: var(--space-2);
  font-size: 13px;
  font-weight: var(--font-medium);
  color: var(--color-text-primary);
  line-height: 1.4;
}

.relay-help__title svg {
  color: var(--color-primary);
}

.relay-help__list {
  margin: 0;
  padding-left: var(--space-4);
  font-size: 13px;
  color: var(--color-text-secondary);
  line-height: 1.5;
}

.relay-help__list li {
  margin-bottom: var(--space-1);
}

@media (max-width: 720px) {
  .form-row,
  .header-row__fields {
    grid-template-columns: 1fr;
  }

  .section-header--split,
  .header-row,
  .backend-item,
  .backends-header {
    flex-direction: column;
  }

  .header-row .btn--icon,
  .backend-item .btn--icon {
    align-self: flex-end;
  }

  .form-tab-panel {
    gap: var(--space-3);
  }

  .settings-card {
    padding: var(--space-3);
  }

  .section-header {
    margin-bottom: var(--space-2);
  }

  .address-help,
  .relay-help {
    padding: var(--space-3);
  }

  .empty-state {
    padding: var(--space-4) var(--space-3);
  }

  .toggle--card {
    padding: var(--space-3);
  }

  .toggle--simple {
    padding: var(--space-2) 0;
  }

  .headers-list {
    gap: var(--space-2);
  }

  .header-row {
    padding: var(--space-2);
  }

  .headers-table__head {
    display: none;
  }

  .headers-table__row {
    grid-template-columns: 1fr 1fr auto;
    gap: var(--space-2);
    padding: var(--space-2) var(--space-3);
  }

  .headers-table__cell .input {
    background: var(--color-bg-subtle);
  }
}

/* iPhone 优化 */
@media (max-width: 414px) {
  .rule-form {
    gap: var(--space-2);
  }

  .form-tabs__btn {
    padding: var(--space-2) var(--space-3);
    font-size: var(--text-xs);
  }

  .form-tabs__badge {
    font-size: 8px;
    padding: 0 4px;
  }

  .settings-card {
    padding: var(--space-3);
    gap: var(--space-2);
    border-radius: var(--radius-lg);
  }

  .section-title {
    font-size: 14px;
  }

  .section-description {
    font-size: 13px;
  }

  .input {
    padding: var(--space-2) var(--space-3);
    font-size: 14px;
  }

  .form-group--block + .form-group--block {
    margin-top: var(--space-3);
  }

  .form-tab-panel > .settings-card {
    padding: var(--space-3);
    gap: var(--space-2);
  }

  .form-tab-panel .toggle--card {
    padding: var(--space-3);
  }

  .address-help,
  .relay-help {
    padding: var(--space-3);
  }

  .address-help__list,
  .relay-help__list {
    font-size: 13px;
    line-height: 1.5;
    padding-left: var(--space-4);
  }

  .empty-state {
    padding: var(--space-4) var(--space-3);
  }

  .empty-state__title {
    font-size: var(--text-sm);
  }

  .empty-state__desc {
    font-size: var(--text-xs);
  }

  .headers-table__head {
    display: none;
  }

  .headers-table__row {
    grid-template-columns: 1fr 1fr auto;
    gap: var(--space-2);
    padding: var(--space-2) var(--space-3);
  }

  .btn--full {
    padding: var(--space-3);
    font-size: var(--text-sm);
  }
}

/* iPhone SE 等小屏幕 */
@media (max-width: 375px) and (max-height: 812px) {
  .form-tabs__btn {
    padding: var(--space-2) var(--space-2);
    font-size: 11px;
  }

  .settings-card {
    padding: var(--space-2);
  }

  .section-header {
    gap: var(--space-1);
  }

  .section-title {
    font-size: 13px;
  }

  .section-description {
    font-size: 12px;
  }

  .input-wrapper__icon {
    left: var(--space-3);
  }

  .input-wrapper .input {
    padding-left: var(--space-8);
  }
}
</style>

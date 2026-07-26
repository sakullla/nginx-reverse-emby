<template>
  <form @submit.prevent="handleSubmit" class="rule-form">
    <div class="form-tabs rule-form__tabs" role="tablist" aria-label="规则配置分区">
      <button
        type="button"
        role="tab"
        class="form-tabs__btn"
        :class="{ 'form-tabs__btn--active': activeTab === 'basic' }"
        :aria-selected="activeTab === 'basic' ? 'true' : 'false'"
        @click="activeTab = 'basic'"
      >
        基础配置
      </button>
      <button
        type="button"
        role="tab"
        class="form-tabs__btn"
        :class="{ 'form-tabs__btn--active': activeTab === 'headers' }"
        :aria-selected="activeTab === 'headers' ? 'true' : 'false'"
        @click="activeTab = 'headers'"
      >
        高级配置
        <span v-if="hasRequestHeaderConfig" class="form-tabs__dot" title="已配置"></span>
      </button>
      <button
        type="button"
        role="tab"
        class="form-tabs__btn"
        :class="{ 'form-tabs__btn--active': activeTab === 'relay' }"
        :aria-selected="activeTab === 'relay' ? 'true' : 'false'"
        @click="activeTab = 'relay'"
      >
        Relay 配置
        <span v-if="hasRelayConfig" class="form-tabs__dot" title="已配置"></span>
      </button>
    </div>

    <div class="rule-form__body">
    <div v-if="activeTab === 'basic'" class="form-tab-panel" role="tabpanel">
      <!-- 访问地址 -->
      <div class="settings-card">
        <div class="section-header">
          <div>
            <h3 class="section-title">访问地址</h3>
            <p class="section-description">用户从哪访问，流量转到哪台服务</p>
          </div>
        </div>

        <!-- 前端地址 -->
        <div class="form-group form-group--block">
          <label for="frontend-url" class="form-label form-label--required">前端访问地址</label>
          <div class="protocol-input-group">
            <select
              v-model="frontendProtocol"
              class="input input--protocol"
            >
              <option value="https://">https://</option>
              <option value="http://">http://</option>
            </select>
            <input
              id="frontend-url"
              :value="getUrlHost(form.frontend_url)"
              type="text"
              class="input protocol-input-group__host"
              :class="{ 'input--error': errors.frontend_url }"
              placeholder="例如：emby.yourdomain.com"
              @input="handleFrontendHostInput($event.target.value)"
              @paste="handleFrontendPaste"
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

        <div class="form-group form-group--block">
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
              <div class="protocol-input-group backend-item__input">
                <select
                  :value="backend._protocol"
                  class="input input--protocol"
                  @change="handleBackendProtocolChange(backend, $event.target.value)"
                >
                  <option value="https://">https://</option>
                  <option value="http://">http://</option>
                </select>
                <input
                  :id="index === 0 ? 'backend-url' : undefined"
                  :value="getUrlHost(backend.url)"
                  type="text"
                  class="input protocol-input-group__host"
                  :class="{ 'input--error': errors.backend }"
                  placeholder="例如：192.168.1.100:8096"
                  @input="handleBackendHostInput(index, $event.target.value)"
                  @paste="handleBackendPaste(index, $event)"
                >
              </div>
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
          <p v-if="errors.backend" class="form-error">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <circle cx="12" cy="12" r="10"/>
              <line x1="12" y1="8" x2="12" y2="12"/>
              <line x1="12" y1="16" x2="12.01" y2="16"/>
            </svg>
            {{ errors.backend }}
          </p>
          <p class="field-hint">可填多台后端，多后端时按负载策略分发</p>
        </div>
      </div>

      <!-- 标签 -->
      <div class="settings-card settings-card--compact">
        <div class="section-header">
          <div>
            <h3 class="section-title">标签</h3>
            <p class="section-description">可选，回车添加；用来筛选和分组</p>
          </div>
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
              id="tag-input"
              v-model="tagInput"
              type="text"
              class="tag-input__field"
              placeholder="例如 media / jellyfin"
              @keydown.enter.prevent="addTag"
            >
          </div>
        </div>
      </div>

      <!-- 启用 -->
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

    <div v-else-if="activeTab === 'headers'" class="form-tab-panel" role="tabpanel">
      <!-- 代理行为配置 -->
      <div class="settings-card">
        <div class="section-header">
          <div>
            <h3 class="section-title">代理行为</h3>
            <p class="section-description">重定向怎么处理、要不要带上真实 IP、多后端怎么选</p>
          </div>
        </div>

        <div class="option-list">
          <label class="option-row" :class="{ 'option-row--active': form.proxy_redirect }">
            <input
              v-model="form.proxy_redirect"
              type="checkbox"
              class="toggle__input"
            >
            <span class="toggle__slider"></span>
            <span class="option-row__content">
              <span class="option-row__label">代理 302/307 重定向</span>
              <span class="option-row__desc">开启时重写后端重定向地址为前端地址；关闭则透传</span>
            </span>
          </label>

          <label class="option-row" :class="{ 'option-row--active': form.pass_proxy_headers, 'option-row--disabled': proxyHeadersGloballyDisabled }">
            <input
              v-model="form.pass_proxy_headers"
              type="checkbox"
              class="toggle__input"
              :disabled="proxyHeadersGloballyDisabled"
            >
            <span class="toggle__slider"></span>
            <span class="option-row__content">
              <span class="option-row__label">透传客户端 IP</span>
              <span class="option-row__desc">X-Real-IP / X-Forwarded-*</span>
            </span>
          </label>
        </div>

        <div v-if="proxyHeadersGloballyDisabled" class="global-disabled-notice">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <circle cx="12" cy="12" r="10"/>
            <line x1="12" y1="8" x2="12" y2="12"/>
            <line x1="12" y1="16" x2="12.01" y2="16"/>
          </svg>
          <span>全局已禁用透传客户端 IP，此开关仅展示保存值</span>
        </div>

        <div class="form-row">
          <div class="form-group">
            <label class="form-label">负载均衡策略</label>
            <div class="select-wrapper">
              <select v-model="form.load_balancing.strategy" class="input">
                <option value="adaptive">自适应 (Adaptive)</option>
                <option value="round_robin">轮询 (Round Robin)</option>
                <option value="random">随机 (Random)</option>
              </select>
            </div>
            <p class="field-hint">多后端时生效</p>
          </div>

          <div class="form-group">
            <label class="form-label">出口 Profile</label>
            <div class="select-wrapper">
              <select
                v-model.number="form.egress_profile_id"
                name="egress-profile"
                class="input"
                @change="errors.submit = ''"
              >
                <option :value="0">Direct</option>
                <option v-for="profile in enabledEgressProfiles" :key="profile.id" :value="Number(profile.id)">
                  {{ profile.name || profile.id }} ({{ profile.type }})
                </option>
              </select>
            </div>
            <p class="field-hint">只影响这台 Agent 去连后端时怎么出站，不影响用户入口</p>
          </div>
        </div>
      </div>

      <!-- User-Agent -->
      <div class="settings-card settings-card--compact">
        <div class="section-header">
          <div>
            <h3 class="section-title">User-Agent</h3>
            <p class="section-description">需要时再改写请求 UA，留空不改</p>
          </div>
        </div>

        <div class="form-row">
          <div class="form-group">
            <label for="ua-preset" class="form-label">预设</label>
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
              placeholder="留空表示不覆盖"
              @input="errors.submit = ''"
            >
          </div>
        </div>
      </div>

      <!-- 更多：出口 Profile / 自定义请求头，默认折叠 -->
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
          <div class="more-panel__section">
            <div class="section-header section-header--split">
              <div>
                <h3 class="section-title">自定义请求头</h3>
                <p class="section-description">认证或业务标识用的额外 Header</p>
              </div>

              <button type="button" class="btn btn--secondary btn--sm" @click="addCustomHeader">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <line x1="12" y1="5" x2="12" y2="19"/>
                  <line x1="5" y1="12" x2="19" y2="12"/>
                </svg>
                添加
              </button>
            </div>

            <div v-if="form.custom_headers.length" class="headers-table">
              <div class="headers-table__head">
                <span class="headers-table__th">名称</span>
                <span class="headers-table__th">值</span>
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
                      placeholder="X-Custom-Header"
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
                      placeholder="value"
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

            <div v-else class="empty-state empty-state--inline">
              <p class="empty-state__desc">暂无自定义 Header</p>
            </div>
          </div>
        </div>
      </div>
    </div>

    <div v-else-if="activeTab === 'relay'" class="form-tab-panel" role="tabpanel">
      <!-- 提示信息 -->
      <div v-if="!relayListeners.length" class="relay-alert relay-alert--warning">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/>
          <line x1="12" y1="9" x2="12" y2="13"/>
          <line x1="12" y1="17" x2="12.01" y2="17"/>
        </svg>
        <span>还没有可用的 Relay 监听器，先去创建后再配链路</span>
      </div>

      <div v-else-if="!hasRelayConfig" class="relay-alert relay-alert--info">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <circle cx="12" cy="12" r="10"/>
          <line x1="12" y1="16" x2="12" y2="12"/>
          <line x1="12" y1="8" x2="12.01" y2="8"/>
        </svg>
        <span>现在是直连，流量不经过 Relay，直接到后端</span>
      </div>

      <!-- Relay 链路配置 -->
      <div class="settings-card">
        <div class="section-header section-header--split">
          <div>
            <h3 class="section-title">转发链路</h3>
            <p class="section-description">按顺序加监听器，流量一层层往下走</p>
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
            <p class="section-description">仅首跳 TLS/TCP 可用，用来弱化握手特征</p>
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
        <p v-else class="field-hint">链路越长延迟通常越高</p>
      </div>
    </div>

    </div>

    <div class="rule-form__footer">
      <p v-if="errors.submit" class="form-error rule-form__submit-error">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <circle cx="12" cy="12" r="10"/>
          <line x1="12" y1="8" x2="12" y2="12"/>
          <line x1="12" y1="16" x2="12.01" y2="16"/>
        </svg>
        {{ errors.submit }}
      </p>
      <button
        type="submit"
        class="btn btn--primary rule-form__submit"
        :disabled="isLoading"
      >
        <span v-if="isLoading" class="spinner spinner--sm"></span>
        <span v-else>{{ isEdit ? '保存修改' : '创建规则' }}</span>
      </button>
    </div>
  </form>
</template>

<script setup>
import { computed, ref, watch } from 'vue'
import { useCreateRule, useUpdateRule } from '../hooks/useRules'
import { useAllRelayListeners } from '../hooks/useRelayListeners'
import { useEgressProfiles } from '../hooks/useEgressProfiles'
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
const { data: egressProfilesData } = useEgressProfiles()
const isEdit = computed(() => !!props.initialData?.id)
const isLoading = computed(() => createRule.isPending.value || updateRule.isPending.value)
const proxyHeadersGloballyDisabled = computed(() => systemInfo.value?.proxy_headers_globally_disabled === true)
const relayListeners = computed(() => relayListenersData.value ?? [])
const egressProfiles = computed(() => egressProfilesData.value ?? [])
const enabledEgressProfiles = computed(() => egressProfiles.value.filter((profile) => {
  const id = Number(profile.id)
  return Number.isInteger(id) && id > 0 && profile.enabled !== false
}))
const selectedEgressProfileID = computed(() => {
  const id = Number(form.value.egress_profile_id)
  if (!Number.isInteger(id) || id <= 0) return null
  return enabledEgressProfiles.value.some((profile) => Number(profile.id) === id) ? id : null
})
const SUPPORTED_HTTP_STRATEGIES = new Set(['adaptive', 'round_robin', 'random'])
let backendIdCounter = 0

const activeTab = ref('basic')
const form = ref(createDefaultForm())
const tagInput = ref('')
const headerErrors = ref([])
const shouldValidateCustomHeaders = ref(false)
const advancedMoreOpen = ref(false)
const errors = ref({
  frontend_url: '',
  backend: '',
  submit: ''
})
const dragState = ref({ from: -1, to: -1 })
const frontendProtocol = ref('https://')

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
    || (Number(form.value.egress_profile_id) || 0) > 0
  )
})

const configuredCustomHeaderCount = computed(() => {
  return form.value.custom_headers.reduce((count, item) => {
    const name = String(item?.name || '').trim()
    const value = item?.value == null ? '' : String(item.value).trim()
    return count + (name || value ? 1 : 0)
  }, 0)
})

const advancedMoreSummary = computed(() => {
  if (configuredCustomHeaderCount.value > 0) {
    return `自定义 Header ${configuredCustomHeaderCount.value} 项`
  }
  return '自定义请求头'
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
  const layers = getRelayLayers(form.value)
  if (!layers.length || !layers[0]?.length) return null
  const listenerMap = new Map(relayListeners.value.map((listener) => [Number(listener.id), listener]))
  return listenerMap.get(Number(layers[0][0])) || null
})
const relayObfsUnsupportedReason = computed(() => {
  const layers = getRelayLayers(form.value)
  if (!layers.length || !layers[0]?.length) {
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
    const hasConfiguredHeaders = form.value.custom_headers.some((item) => {
      const name = String(item?.name || '').trim()
      const value = item?.value == null ? '' : String(item.value).trim()
      return Boolean(name || value)
    })
    advancedMoreOpen.value = hasConfiguredHeaders || (Number(form.value.egress_profile_id) || 0) > 0
    errors.value.frontend_url = ''
    errors.value.backend = ''
    errors.value.submit = ''
    activeTab.value = 'basic'
    const parsed = parseUrl(form.value.frontend_url)
    frontendProtocol.value = parsed.protocol
  },
  { immediate: true }
)

watch(frontendProtocol, (protocol) => {
  form.value.frontend_url = buildUrl(protocol, getUrlHost(form.value.frontend_url))
  updateAutoTags()
})

watch([() => form.value.relay_layers, firstRelayListener], ([relayLayers]) => {
  if (
    !Array.isArray(relayLayers)
    || relayLayers.length === 0
    || firstRelayListener.value?.transport_mode !== 'tls_tcp'
  ) {
    form.value.relay_obfs = false
  }
})

function createDefaultForm() {
  return {
    frontend_url: '',
    backends: [createBackend()],
    load_balancing: { strategy: 'adaptive' },
    tags: [],
    enabled: true,
    proxy_redirect: true,
    pass_proxy_headers: false,
    user_agent: '',
    custom_headers: [],
    egress_profile_id: 0,
    relay_layers: [],
    relay_obfs: false
  }
}

function createBackend(data = {}) {
  const url = String(data?.url || '').trim()
  const parsed = parseUrl(url)
  const hasProtocol = /^https?:\/\//i.test(url)
  return {
    id: `http-backend-${Date.now()}-${backendIdCounter++}`,
    url,
    _protocol: hasProtocol ? parsed.protocol : 'https://'
  }
}

function normalizeHttpStrategy(value) {
  const strategy = String(value || '').trim().toLowerCase()
  return SUPPORTED_HTTP_STRATEGIES.has(strategy) ? strategy : 'adaptive'
}

function normalizeHttpBackends(initialData) {
  if (Array.isArray(initialData?.backends) && initialData.backends.length > 0) {
    const backends = initialData.backends
      .map((backend) => createBackend(backend))
      .filter((backend) => backend.url)
    if (backends.length > 0) return backends
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
    egress_profile_id: initialData.egress_profile_id == null ? 0 : Number(initialData.egress_profile_id),
    relay_layers: getRelayLayers(initialData),
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

function handleFrontendHostInput(host) {
  const h = String(host || '').trim()
  if (!h) {
    form.value.frontend_url = ''
  } else if (/^https?:\/\/.+/i.test(h)) {
    const parsed = parseUrl(h)
    frontendProtocol.value = parsed.protocol
    form.value.frontend_url = h
  } else {
    form.value.frontend_url = buildUrl(frontendProtocol.value, h)
  }
  errors.value.frontend_url = ''
  errors.value.submit = ''
  updateAutoTags()
}

function handleBackendHostInput(index, host) {
  const backend = form.value.backends[index]
  const h = String(host || '').trim()
  if (!h) {
    backend.url = ''
  } else if (/^https?:\/\/.+/i.test(h)) {
    const parsed = parseUrl(h)
    backend._protocol = parsed.protocol
    backend.url = h
  } else {
    backend.url = buildUrl(backend._protocol, h)
  }
  errors.value.backend = ''
  errors.value.submit = ''
}

function handleBackendProtocolChange(backend, protocol) {
  backend._protocol = protocol
  backend.url = buildUrl(protocol, getUrlHost(backend.url))
}

function handleFrontendPaste(event) {
  const pasted = (event.clipboardData || window.clipboardData).getData('text').trim()
  const parsed = parseUrl(pasted)
  if (parsed.protocol !== 'https://' || pasted.startsWith('https://')) {
    event.preventDefault()
    frontendProtocol.value = parsed.protocol
    form.value.frontend_url = pasted
    errors.value.frontend_url = ''
    errors.value.submit = ''
    updateAutoTags()
  }
}

function handleBackendPaste(index, event) {
  const pasted = (event.clipboardData || window.clipboardData).getData('text').trim()
  const parsed = parseUrl(pasted)
  if (parsed.protocol !== 'https://' || pasted.startsWith('https://')) {
    event.preventDefault()
    const backend = form.value.backends[index]
    backend._protocol = parsed.protocol
    backend.url = pasted
    errors.value.backend = ''
    errors.value.submit = ''
  }
}

// URL 工具函数
function parseUrl(url, defaultProtocol = 'https://') {
  const s = String(url || '').trim()
  const lower = s.toLowerCase()
  if (lower.startsWith('https://')) {
    return { protocol: 'https://', host: s.slice(8) }
  }
  if (lower.startsWith('http://')) {
    return { protocol: 'http://', host: s.slice(7) }
  }
  return { protocol: defaultProtocol, host: s }
}

function getUrlProtocol(url, defaultProtocol = 'https://') {
  return parseUrl(url, defaultProtocol).protocol
}

function getUrlHost(url) {
  return parseUrl(url).host
}

function setUrlProtocol(url, protocol, defaultProtocol = 'https://') {
  const host = getUrlHost(url)
  return buildUrl(protocol, host)
}

function setUrlHost(url, host, defaultProtocol = 'https://') {
  const protocol = getUrlProtocol(url, defaultProtocol)
  return buildUrl(protocol, host)
}

function buildUrl(protocol, host) {
  const h = String(host || '').trim()
  return h ? protocol + h : ''
}

function addBackend() {
  form.value.backends.push(createBackend())
}

function removeBackend(index) {
  if (form.value.backends.length > 1) {
    form.value.backends.splice(index, 1)
  }
  errors.value.backend = ''
  errors.value.submit = ''
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
  advancedMoreOpen.value = true
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
  errors.value.backend = ''

  if (!form.value.frontend_url.trim()) {
    errors.value.frontend_url = '请输入前端访问地址'
  }

  const validBackends = form.value.backends
    .map((backend) => ({ url: String(backend?.url || '').trim() }))
    .filter((backend) => backend.url)
  if (validBackends.length === 0) {
    errors.value.backend = '至少需要一个后端服务器'
  }

  return !errors.value.frontend_url && !errors.value.backend
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
    advancedMoreOpen.value = true
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
      relay_layers: Array.isArray(form.value.relay_layers) ? form.value.relay_layers.map((l) => [...l]) : [],
      relay_obfs: firstRelayListener.value?.transport_mode === 'tls_tcp'
        && Array.isArray(form.value.relay_layers)
        && form.value.relay_layers.length > 0
        && form.value.relay_obfs === true
    }
    if (selectedEgressProfileID.value != null) {
      payload.egress_profile_id = selectedEgressProfileID.value
    } else if (isEdit.value && Number(form.value.egress_profile_id) === 0) {
      payload.egress_profile_id = 0
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
  gap: 0;
  min-height: 0;
  flex: 1 1 auto;
  width: 100%;
  max-height: 100%;
  margin: -0.1rem 0 0;
}

.rule-form__tabs.form-tabs,
.form-tabs {
  display: flex;
  gap: 0.2rem;
  margin: 0;
  flex: 0 0 auto;
  padding: 0.25rem;
  background:
    linear-gradient(
      180deg,
      color-mix(in srgb, var(--color-bg-subtle) 88%, var(--color-primary-subtle)) 0%,
      var(--color-bg-subtle) 100%
    );
  border: 1px solid color-mix(in srgb, var(--color-border-default) 92%, var(--color-primary) 8%);
  border-radius: var(--radius-full);
  z-index: 2;
  box-shadow: 0 1px 0 color-mix(in srgb, var(--color-bg-surface-raised) 70%, transparent);
}

.form-tabs__btn {
  padding: 0.48rem 0.8rem;
  border: 1px solid transparent;
  background: transparent;
  cursor: pointer;
  font-size: 0.8125rem;
  font-weight: 600;
  color: var(--color-text-muted);
  border-radius: var(--radius-full);
  transition:
    color var(--duration-fast) var(--ease-default),
    background var(--duration-fast) var(--ease-default),
    border-color var(--duration-fast) var(--ease-default),
    box-shadow var(--duration-fast) var(--ease-default);
  display: flex;
  align-items: center;
  gap: 0.35rem;
  flex: 1;
  justify-content: center;
  white-space: nowrap;
  line-height: 1.3;
  min-height: 2.15rem;
}

.form-tabs__btn:hover {
  color: var(--color-text-secondary);
  background: color-mix(in srgb, var(--color-bg-hover) 70%, transparent);
}

.form-tabs__btn:focus-visible {
  outline: none;
  box-shadow: var(--shadow-focus);
}

.form-tabs__btn--active {
  color: var(--color-primary);
  background: var(--color-bg-surface-raised);
  border-color: color-mix(in srgb, var(--color-primary) 16%, transparent);
  font-weight: 700;
  box-shadow: 0 1px 2px rgb(15 23 42 / 0.06);
}

.form-tabs__dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--color-success);
  flex-shrink: 0;
  box-shadow: 0 0 0 2px color-mix(in srgb, var(--color-success) 18%, transparent);
}

.rule-form__body {
  display: flex;
  flex-direction: column;
  gap: 0.65rem;
  flex: 1 1 auto;
  min-height: 0;
  overflow-x: hidden;
  overflow-y: auto;
  overscroll-behavior: contain;
  padding: 0.55rem 0.05rem 0.15rem;
}

.form-tab-panel {
  display: flex;
  flex-direction: column;
  gap: 0.65rem;
  padding-top: 0;
  min-width: 0;
}

.rule-form__footer {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-end;
  gap: 0.65rem 0.85rem;
  flex: 0 0 auto;
  margin-top: 0.35rem;
  padding: 0.75rem 0.05rem 0.05rem;
  border-top: 1px solid color-mix(in srgb, var(--color-border-default) 88%, transparent);
  background: var(--color-bg-surface-raised);
  z-index: 2;
}

.rule-form__submit-error {
  margin: 0;
  margin-right: auto;
  max-width: min(100%, 28rem);
}

.rule-form__submit {
  min-width: 8.5rem;
  min-height: 2.35rem;
  padding: 0.55rem 1.15rem;
  border-radius: var(--radius-lg);
  font-weight: 700;
  letter-spacing: -0.01em;
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

.form-hint,
.field-hint {
  margin: 0.3rem 0 0;
  font-size: 0.6875rem;
  color: var(--color-text-muted);
  line-height: 1.4;
}

.form-help-text {
  margin: 0.25rem 0 0 0;
  font-size: 0.6875rem;
  color: var(--color-text-tertiary);
  line-height: 1.4;
}

.form-group--block {
  display: block;
  width: 100%;
}

.form-group--block + .form-group--block {
  margin-top: 0.6rem;
}

.form-label__hint {
  display: block;
  margin-top: 0.15rem;
  font-size: 0.6875rem;
  font-weight: var(--font-normal);
  color: var(--color-text-muted);
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
  gap: 0.6rem;
  padding: 0.95rem 1.05rem;
  background: var(--color-bg-surface);
  border: 1px solid color-mix(in srgb, var(--color-border-default) 78%, transparent);
  border-radius: var(--radius-xl);
  box-shadow: none;
}

.settings-card--compact {
  gap: 0.4rem;
  padding: 0.7rem 0.85rem;
  justify-content: flex-start;
  background: color-mix(in srgb, var(--color-bg-surface) 92%, var(--color-bg-subtle));
  border-color: color-mix(in srgb, var(--color-border-default) 62%, transparent);
}

.settings-card--status {
  min-height: 0;
  justify-content: center;
  padding-block: 0.7rem;
  border-style: dashed;
  border-color: color-mix(in srgb, var(--color-border-default) 55%, transparent);
}

.settings-card--status .toggle--inline {
  width: 100%;
  align-items: center;
}

.form-tab-panel > .settings-card {
  gap: 0.55rem;
}

.form-tab-panel > .settings-card .section-header {
  margin-bottom: 0;
}

.form-tab-panel > .settings-card .section-title {
  font-size: 0.8125rem;
  font-weight: 650;
}

.form-tab-panel > .settings-card .section-description {
  font-size: 0.6875rem;
  color: var(--color-text-muted);
}

.option-list {
  display: flex;
  flex-direction: column;
  border: 1px solid color-mix(in srgb, var(--color-border-subtle) 55%, transparent);
  border-radius: var(--radius-md);
  overflow: hidden;
  background: color-mix(in srgb, var(--color-bg-subtle) 28%, var(--color-bg-surface));
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

.option-row--disabled {
  cursor: not-allowed;
  opacity: 0.72;
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

.behavior-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0.45rem;
}

.behavior-item {
  display: flex;
  flex-direction: column;
  gap: 0.3rem;
  min-width: 0;
}

.behavior-item__label {
  font-size: 0.75rem;
  font-weight: 600;
  color: var(--color-text-secondary);
}

.behavior-item__help {
  margin: 0;
  font-size: 0.6875rem;
  color: var(--color-text-tertiary);
  line-height: 1.35;
}

.toggle--inline {
  display: flex;
  align-items: center;
  gap: 0.55rem;
  min-width: 0;
  white-space: nowrap;
}

.toggle--inline .toggle__content {
  display: inline-flex;
  flex-direction: row;
  align-items: center;
  flex-wrap: nowrap;
  gap: 0.45rem;
  min-width: 0;
}

.toggle--inline .toggle__label {
  font-size: 0.8125rem;
  font-weight: 600;
  line-height: 1.3;
  color: var(--color-text-primary);
  white-space: nowrap;
}

.toggle--inline .toggle__desc {
  font-size: 0.6875rem;
  color: var(--color-text-muted);
  line-height: 1.3;
  white-space: nowrap;
}

.toggle--inline .toggle__desc::before {
  content: '·';
  margin-right: 0.45rem;
  color: var(--color-text-tertiary);
}

.toggle--inline .toggle__slider {
  margin-top: 0;
  flex-shrink: 0;
}

.settings-card--status .toggle--inline .toggle__content {
  overflow: hidden;
}

.settings-card--status .toggle--inline .toggle__desc {
  overflow: hidden;
  text-overflow: ellipsis;
}

/* 全局禁用状态的卡片 */
.settings-card--disabled {
  opacity: 0.75;
}

.global-disabled-notice {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  padding: 0.4rem 0.55rem;
  background: var(--color-bg-surface);
  border: 1px solid var(--color-warning);
  border-radius: var(--radius-md);
  font-size: 0.6875rem;
  color: var(--color-warning);
  line-height: 1.35;
}

.form-error,
.form-warning {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  padding: 0.4rem 0.55rem;
  border-radius: var(--radius-md);
  font-size: 0.75rem;
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
  font-size: 0.6875rem;
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
  border-radius: var(--radius-lg);
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

.input--error {
  border-color: var(--color-danger);
}

/* 协议前缀 + 地址输入：一体控件 */
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
  min-width: 5.25rem;
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

.protocol-input-group__host.input--error {
  box-shadow: inset 0 0 0 1px var(--color-danger);
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

.toggle-row {
  padding: 0.35rem 0;
  border-bottom: 1px solid var(--color-border-subtle);
}

.toggle-row:last-child {
  border-bottom: none;
}

.toggle {
  display: flex;
  align-items: flex-start;
  gap: 0.55rem;
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
  display: flex;
  flex-direction: column;
  gap: 0.1rem;
}

/* 简化版 Toggle - 用于规则状态 */
.toggle-list--simple {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
}

.toggle--simple {
  display: flex;
  align-items: flex-start;
  gap: 0.55rem;
  padding: 0.15rem 0;
  border-bottom: none;
}

.toggle--simple:last-child {
  border-bottom: none;
}

.toggle--simple .toggle__content {
  display: flex;
  flex-direction: column;
  gap: 0.1rem;
}

.toggle--simple .toggle__label {
  font-weight: 600;
  font-size: 0.8125rem;
}

.toggle--simple .toggle__desc {
  font-size: 0.6875rem;
  color: var(--color-text-muted);
  line-height: 1.35;
}

.headers-list {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}

.header-row {
  display: flex;
  gap: 0.5rem;
  align-items: flex-start;
  padding: 0.55rem;
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
}

.header-row__fields {
  flex: 1;
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0.5rem;
  min-width: 0;
}

/* 表格样式请求头列表 - 简化设计 */
.headers-table {
  display: flex;
  flex-direction: column;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  overflow: hidden;
  background: var(--color-bg-surface);
}

.headers-table__head {
  display: grid;
  grid-template-columns: 1fr 1fr auto;
  gap: 0.5rem;
  padding: 0.4rem 0.55rem;
  background: color-mix(in srgb, var(--color-bg-subtle) 70%, transparent);
  border-bottom: 1px solid var(--color-border-subtle);
  font-size: 0.6875rem;
  font-weight: 650;
  letter-spacing: 0.02em;
  color: var(--color-text-muted);
}

.headers-table__th {
  padding-left: 0.25rem;
}

.headers-table__th--action {
  width: 32px;
  text-align: center;
}

.headers-table__body {
  display: flex;
  flex-direction: column;
}

.headers-table__row {
  display: grid;
  grid-template-columns: 1fr 1fr auto;
  gap: 0.5rem;
  align-items: center;
  padding: 0.35rem 0.55rem;
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
  width: 32px;
  display: flex;
  justify-content: center;
}

.input--compact {
  padding: 0.3rem 0.5rem;
  font-size: 0.8125rem;
}

.empty-state {
  padding: 0.7rem 0.8rem;
  border: 1px dashed var(--color-border-default);
  border-radius: var(--radius-md);
  text-align: center;
  font-size: 0.75rem;
  color: var(--color-text-muted);
  background: color-mix(in srgb, var(--color-bg-subtle) 45%, transparent);
}

.empty-state--inline {
  display: block;
  min-height: 0;
  padding: 0.1rem 0 0.15rem;
  border: none;
  border-radius: 0;
  text-align: left;
  background: transparent;
  color: var(--color-text-muted);
}

.empty-state__title {
  margin: 0;
  font-size: 0.75rem;
  font-weight: 600;
  color: var(--color-text-primary);
}

.empty-state__desc {
  margin: 0.15rem 0 0;
  font-size: 0.6875rem;
  color: var(--color-text-muted);
}

.empty-state--inline .empty-state__desc {
  margin: 0;
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
  gap: 0.9rem;
  margin-top: 0.7rem;
  padding-top: 0.7rem;
  border-top: 1px solid var(--color-border-subtle);
}

.more-panel__section {
  display: flex;
  flex-direction: column;
  gap: 0.55rem;
  padding-top: 0.75rem;
  border-top: 1px solid color-mix(in srgb, var(--color-border-subtle) 80%, transparent);
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

.rule-form__submit.btn--primary {
  box-shadow: 0 8px 18px -12px color-mix(in srgb, var(--color-primary) 70%, transparent);
}

.rule-form__submit.btn--primary:hover:not(:disabled) {
  opacity: 1;
  filter: brightness(1.02);
}

.btn:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

/* 请求头配置样式 */

.relay-intro {
  display: flex;
  align-items: center;
  gap: 0.65rem;
  padding: 0.7rem 0.8rem;
  background: color-mix(in srgb, var(--color-primary-subtle) 55%, var(--color-bg-surface));
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
}

.relay-intro__icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  background: var(--color-primary);
  border-radius: var(--radius-md);
  color: white;
  flex-shrink: 0;
}

.relay-intro__content {
  display: flex;
  flex-direction: column;
  gap: 0.1rem;
}

.relay-intro__title {
  margin: 0;
  font-size: 0.8125rem;
  font-weight: 650;
  color: var(--color-text-primary);
}

.relay-intro__desc {
  margin: 0;
  font-size: 0.75rem;
  color: var(--color-text-secondary);
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
  .header-row__fields,
  .behavior-grid {
    grid-template-columns: 1fr;
  }

  .section-header--split,
  .header-row,
  .backend-item:not(.backend-item--flat),
  .backends-header {
    flex-direction: column;
  }

  .header-row .btn--icon,
  .backend-item .btn--icon {
    align-self: flex-end;
  }

  .form-tab-panel {
    gap: 0.55rem;
  }

  .settings-card {
    padding: 0.65rem 0.7rem;
  }

  .empty-state:not(.empty-state--inline) {
    padding: 0.65rem 0.7rem;
  }

  .headers-table__head {
    display: none;
  }

  .headers-table__row {
    grid-template-columns: 1fr 1fr auto;
    gap: 0.4rem;
    padding: 0.35rem 0.5rem;
  }

  .input--protocol {
    min-width: 4.75rem;
    font-size: 0.75rem;
  }
}

/* iPhone 优化 */
@media (max-width: 414px) {
  .rule-form {
    gap: 0.5rem;
  }

  .form-tabs__btn {
    padding: 0.35rem 0.45rem;
    font-size: 0.75rem;
  }

  .settings-card {
    padding: 0.6rem 0.65rem;
    gap: 0.45rem;
  }

  .section-title {
    font-size: 0.8125rem;
  }

  .section-description {
    font-size: 0.6875rem;
  }

  .input {
    font-size: 0.8125rem;
  }

  .form-group--block + .form-group--block {
    margin-top: 0.5rem;
  }

  .form-tab-panel > .settings-card {
    padding: 0.6rem 0.65rem;
    gap: 0.45rem;
  }

  .empty-state:not(.empty-state--inline) {
    padding: 0.6rem 0.65rem;
  }

  .headers-table__head {
    display: none;
  }

  .headers-table__row {
    grid-template-columns: 1fr 1fr auto;
    gap: 0.35rem;
    padding: 0.3rem 0.45rem;
  }

  .btn--full {
    height: 2.15rem;
    font-size: 0.8125rem;
  }

  .input--protocol {
    min-width: 4.5rem;
    font-size: 0.75rem;
    padding-left: 0.4rem;
    padding-right: 0.4rem;
  }
}

/* iPhone SE 等小屏幕 */
@media (max-width: 375px) and (max-height: 812px) {
  .form-tabs__btn {
    padding: 0.3rem 0.35rem;
    font-size: 0.6875rem;
  }

  .settings-card {
    padding: 0.5rem 0.55rem;
  }

  .section-header {
    gap: 0.1rem;
  }

  .section-title {
    font-size: 0.75rem;
  }

  .section-description {
    font-size: 0.6875rem;
  }
}

</style>

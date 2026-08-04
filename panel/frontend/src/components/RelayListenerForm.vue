<template>
  <form class="relay-listener-form" @submit.prevent="handleSubmit">
    <div class="relay-listener-form__body">
      <!-- 基础信息 -->
      <section class="settings-card">
        <div class="section-header">
          <div>
            <h3 class="section-title">基础信息</h3>
            <p class="section-description">命名监听器，便于在节点与规则中识别</p>
          </div>
        </div>

        <div class="form-group">
          <label class="form-label form-label--required">名称</label>
          <input
            v-model="form.name"
            class="input"
            :class="{ 'input--error': errors.name }"
            placeholder="例如 hk-edge-1"
            autocomplete="off"
          >
          <p v-if="errors.name" class="form-error">{{ errors.name }}</p>
        </div>

        <div class="form-group">
          <div class="section-header section-header--inline">
            <label class="form-label">标签</label>
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
                placeholder="例如 edge / hk"
                @keydown.enter.prevent="addTag"
              >
            </div>
          </div>
        </div>
      </section>

      <!-- 监听入口 -->
      <section class="settings-card">
        <div class="section-header">
          <div>
            <h3 class="section-title">监听入口</h3>
            <p class="section-description">节点本地绑定地址与对外可达入口</p>
          </div>
        </div>

        <div class="listen-grid">
          <div class="form-group listen-grid__hosts">
            <label class="form-label form-label--required">绑定地址</label>
            <textarea
              v-model="form.bind_hosts_text"
              class="input textarea textarea--hosts"
              :class="{ 'input--error': errors.bind_hosts }"
              placeholder="0.0.0.0"
              rows="2"
              spellcheck="false"
            ></textarea>
            <p v-if="errors.bind_hosts" class="form-error">{{ errors.bind_hosts }}</p>
            <p v-else class="field-hint">每行一个地址；多地址绑定可继续换行填写</p>
          </div>

          <div class="form-group listen-grid__port">
            <label class="form-label form-label--required">监听端口</label>
            <input
              v-model.number="form.listen_port"
              class="input"
              type="number"
              min="1"
              max="65535"
              :class="{ 'input--error': errors.listen_port }"
              placeholder="7443"
            >
            <p v-if="errors.listen_port" class="form-error">{{ errors.listen_port }}</p>
          </div>
        </div>

        <div class="form-group">
          <label class="form-label">{{ publicEndpointLabel }}</label>
          <input
            v-model="form.public_endpoint"
            class="input"
            :class="{ 'input--error': errors.public_endpoint }"
            :placeholder="publicEndpointPlaceholder"
            spellcheck="false"
          >
          <p v-if="errors.public_endpoint" class="form-error">{{ errors.public_endpoint }}</p>
          <p v-else class="field-hint">{{ publicEndpointHint }}</p>
        </div>
      </section>

      <!-- 传输与证书 -->
      <section class="settings-card">
        <div class="section-header">
          <div>
            <h3 class="section-title">传输与证书</h3>
            <p class="section-description">默认自动签发证书并启用 Relay CA + Pin</p>
          </div>
        </div>

        <div class="form-row">
          <div class="form-group">
            <label class="form-label">Relay Transport</label>
            <select v-model="form.transport_mode" class="input">
              <option value="tls_tcp">TLS/TCP</option>
              <option value="quic">QUIC</option>
            </select>
          </div>

          <div v-if="form.transport_mode === 'tls_tcp'" class="form-group">
            <label class="form-label">TLS 隐匿策略</label>
            <select v-model="form.obfs_mode" class="input">
              <option value="off">关闭</option>
              <option value="early_window_v2">early_window_v2</option>
            </select>
          </div>

          <div v-else class="form-group">
            <label class="form-label">QUIC 回退</label>
            <label class="option-row option-row--compact" :class="{ 'option-row--active': form.allow_transport_fallback }">
              <input
                v-model="form.allow_transport_fallback"
                type="checkbox"
                class="toggle__input"
              >
              <span class="toggle__slider"></span>
              <span class="option-row__content">
                <span class="option-row__label">失败时回退 TLS/TCP</span>
              </span>
            </label>
          </div>
        </div>

        <div class="form-row">
          <div class="form-group">
            <label class="form-label">监听证书来源</label>
            <select v-model="form.certificate_source" class="input">
              <option value="auto_relay_ca">自动签发（Relay CA）</option>
              <option value="existing_certificate">绑定已有证书</option>
            </select>
          </div>

          <div class="form-group">
            <label class="form-label">信任策略</label>
            <select v-model="form.trust_mode_source" class="input">
              <option value="auto">自动（Relay CA + Pin）</option>
              <option value="custom">高级自定义</option>
            </select>
          </div>
        </div>

        <div
          v-if="form.certificate_source === 'existing_certificate'"
          class="form-group"
        >
          <label class="form-label" :class="{ 'form-label--required': form.enabled }">绑定监听证书</label>
          <select
            v-model="form.certificate_id"
            class="input"
            :class="{ 'input--error': errors.certificate_id }"
          >
            <option :value="null">请选择证书</option>
            <option v-for="cert in certificates" :key="cert.id" :value="cert.id">
              #{{ cert.id }} {{ cert.domain }}
            </option>
          </select>
          <p v-if="errors.certificate_id" class="form-error">{{ errors.certificate_id }}</p>
        </div>

        <div
          v-else-if="form.certificate_source === 'auto_relay_ca' && form.trust_mode_source === 'auto'"
          class="path-chip"
        >
          <span class="path-chip__dot"></span>
          <span>默认路径：自动签发证书 + 自动信任，无需维护 Pin / CA</span>
        </div>
      </section>

      <section class="settings-card settings-card--compact">
        <label class="option-row option-row--compact" :class="{ 'option-row--active': form.enabled }">
          <input v-model="form.enabled" type="checkbox" class="toggle__input">
          <span class="toggle__slider"></span>
          <span class="option-row__content">
            <span class="option-row__label">启用监听器</span>
            <span class="option-row__desc">创建后立即参与同步与接入</span>
          </span>
        </label>
      </section>

      <!-- 高级设置 -->
      <section class="settings-card settings-card--compact">
        <button type="button" class="advanced-toggle" @click="showAdvanced = !showAdvanced">
          <svg
            class="advanced-toggle__arrow"
            :class="{ 'advanced-toggle__arrow--open': showAdvanced }"
            width="14"
            height="14"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
          >
            <polyline points="6 9 12 15 18 9"/>
          </svg>
          高级设置
          <span v-if="form.trust_mode_source === 'custom'" class="advanced-toggle__badge">自定义信任</span>
        </button>

        <div v-if="showAdvanced" class="advanced-panel">
          <p class="field-hint advanced-panel__hint">
            {{ form.trust_mode_source === 'auto'
              ? '自动模式下由系统派生 Relay CA + Pin；切到「高级自定义」后以下字段才会提交。'
              : '自定义模式将直接提交 TLS 模式、Pin Set 与可信 CA。' }}
          </p>

          <label
            class="option-row option-row--compact"
            :class="{
              'option-row--active': form.allow_self_signed,
              'option-row--disabled': form.trust_mode_source === 'auto'
            }"
          >
            <input
              v-model="form.allow_self_signed"
              type="checkbox"
              class="toggle__input"
              :disabled="form.trust_mode_source === 'auto'"
            >
            <span class="toggle__slider"></span>
            <span class="option-row__content">
              <span class="option-row__label">允许上游使用自签名证书</span>
              <span v-if="form.trust_mode_source === 'auto'" class="option-row__desc">自动信任模式下固定开启</span>
            </span>
          </label>

          <div class="form-group">
            <label class="form-label">TLS 模式</label>
            <select
              v-model="form.tls_mode"
              class="input"
              :disabled="form.trust_mode_source === 'auto'"
            >
              <option value="pin_and_ca">Pin + CA</option>
              <option value="pin_only">仅证书 Pin</option>
              <option value="ca_only">仅 CA 信任链</option>
              <option value="pin_or_ca">证书 Pin 或 CA</option>
            </select>
          </div>

          <div class="form-group">
            <label class="form-label">Pin Set（每行一个，格式 type:value）</label>
            <textarea
              v-model="pinSetText"
              class="input textarea"
              placeholder="spki_sha256:abc123"
              :disabled="form.trust_mode_source === 'auto'"
              rows="3"
              spellcheck="false"
            ></textarea>
          </div>

          <div class="form-group">
            <label class="form-label">可信 CA 证书</label>
            <div class="checkbox-list" :class="{ 'checkbox-list--disabled': form.trust_mode_source === 'auto' }">
              <p v-if="!certificates.length" class="checkbox-list__empty">暂无可用证书</p>
              <label
                v-for="cert in certificates"
                :key="`ca-${cert.id}`"
                class="checkbox-item"
              >
                <input
                  :checked="trustedCaSet.has(Number(cert.id))"
                  type="checkbox"
                  :disabled="form.trust_mode_source === 'auto'"
                  @change="toggleTrustedCa(cert.id)"
                >
                <span>#{{ cert.id }} {{ cert.domain }}</span>
              </label>
            </div>
          </div>
        </div>
      </section>
    </div>

    <div class="relay-listener-form__footer">
      <p v-if="errors.trust_material" class="form-error form-error--block relay-listener-form__submit-error">
        {{ errors.trust_material }}
      </p>
      <p v-if="errors.submit" class="form-error form-error--block relay-listener-form__submit-error">
        {{ errors.submit }}
      </p>
      <button
        type="submit"
        class="btn btn--primary relay-listener-form__submit"
        :disabled="isLoading"
      >
        {{ isEdit ? '保存修改' : '创建监听器' }}
      </button>
    </div>
  </form>
</template>

<script setup>
import { computed, ref, watch } from 'vue'
import { useCreateRelayListener, useUpdateRelayListener } from '../hooks/useRelayListeners'
import { useCertificates } from '../hooks/useCertificates'
import {
  parsePublicEndpoint,
  buildPublicEndpoint,
  normalizeBindHosts,
  buildBindHostsText
} from './relay/endpointState.mjs'

const props = defineProps({
  initialData: { type: Object, default: null },
  agentId: { type: [String, Object], required: true }
})

const emit = defineEmits(['success'])

const createRelayListener = useCreateRelayListener(props.agentId)
const updateRelayListener = useUpdateRelayListener(props.agentId)
const { data: certificatesData } = useCertificates(props.agentId)

const certificates = computed(() => certificatesData.value ?? [])
const isEdit = computed(() => !!props.initialData?.id)
const isLoading = computed(() => createRelayListener.isPending.value || updateRelayListener.isPending.value)
const publicEndpointLabel = computed(() => '公网入口')
const publicEndpointPlaceholder = computed(() => 'relay.example.com:7443')
const publicEndpointHint = computed(() => '使用通配绑定地址时必填；具体绑定地址可直接作为证书端点')

const form = ref(createDefaultForm())
const showAdvanced = ref(false)
const tagInput = ref('')
const pinSetText = ref('')
const trustedCaSet = ref(new Set())
const errors = ref(createEmptyErrors())

watch(
  () => props.initialData,
  (value) => {
    form.value = createFormState(value)
    showAdvanced.value = form.value.trust_mode_source === 'custom'
    tagInput.value = ''
    pinSetText.value = (form.value.pin_set || [])
      .map((item) => `${item.type}:${item.value}`)
      .join('\n')
    trustedCaSet.value = new Set((form.value.trusted_ca_certificate_ids || []).map((id) => Number(id)))
    resetErrors()
  },
  { immediate: true }
)

watch(
  certificates,
  (items) => {
    if (!items.length) return
    if (form.value.certificate_source === 'existing_certificate' && form.value.certificate_id == null) {
      form.value.certificate_id = Number(items[0].id)
    }
  },
  { immediate: true }
)

watch(
  () => form.value.certificate_source,
  (value, oldValue) => {
    if (value === 'auto_relay_ca') {
      form.value.certificate_id = null
      return
    }
    if (
      value === 'existing_certificate'
      && form.value.certificate_id == null
      && certificates.value.length
      && oldValue !== undefined
    ) {
      form.value.certificate_id = Number(certificates.value[0].id)
    }
  }
)

watch(
  () => form.value.trust_mode_source,
  (value, oldValue) => {
    if (value === 'auto') {
      form.value.tls_mode = 'pin_and_ca'
      form.value.allow_self_signed = true
      if (oldValue && oldValue !== 'auto') {
        pinSetText.value = ''
        trustedCaSet.value = new Set()
      }
      return
    }
    if (value === 'custom' && oldValue === 'auto') {
      showAdvanced.value = true
    }
  }
)

watch(
  () => form.value.transport_mode,
  (value) => {
    if (value === 'quic') {
      form.value.obfs_mode = 'off'
    } else {
      form.value.obfs_mode = normalizeObfsMode(form.value.obfs_mode, value)
      form.value.allow_transport_fallback = true
    }
  }
)

function createEmptyErrors() {
  return {
    name: '',
    bind_hosts: '',
    public_endpoint: '',
    listen_port: '',
    certificate_id: '',
    trust_material: '',
    submit: ''
  }
}

function createDefaultForm() {
  return {
    name: '',
    bind_hosts_text: '0.0.0.0',
    public_endpoint: '',
    listen_port: null,
    transport_mode: 'tls_tcp',
    allow_transport_fallback: true,
    obfs_mode: 'off',
    enabled: true,
    certificate_id: null,
    certificate_source: 'auto_relay_ca',
    trust_mode_source: 'auto',
    tls_mode: 'pin_and_ca',
    pin_set: [],
    trusted_ca_certificate_ids: [],
    allow_self_signed: true,
    tags: []
  }
}

function inferCertificateSource(initialData) {
  if (initialData?.certificate_source === 'auto_relay_ca' || initialData?.certificate_source === 'existing_certificate') {
    return initialData.certificate_source
  }
  return initialData ? 'existing_certificate' : 'auto_relay_ca'
}

function inferTrustModeSource(initialData) {
  if (initialData?.trust_mode_source === 'auto' || initialData?.trust_mode_source === 'custom') {
    return initialData.trust_mode_source
  }
  if (!initialData) return 'auto'
  return 'custom'
}

function normalizeTransportMode(value) {
  return value === 'quic' ? 'quic' : 'tls_tcp'
}

function normalizeObfsMode(value, transportMode) {
  if (transportMode !== 'tls_tcp') return 'off'
  return value === 'early_window_v2' ? 'early_window_v2' : 'off'
}

function normalizeListenPort(value) {
  if (value === '' || value == null) return null
  const port = Number(value)
  if (!Number.isInteger(port) || port <= 0) return null
  return port
}

function createFormState(initialData) {
  if (!initialData) return createDefaultForm()
  const transportMode = normalizeTransportMode(initialData.transport_mode)
  return {
    name: initialData.name || '',
    bind_hosts_text: buildBindHostsText(
      Array.isArray(initialData.bind_hosts) && initialData.bind_hosts.length
        ? initialData.bind_hosts
        : [initialData.listen_host || '0.0.0.0']
    ),
    public_endpoint: buildPublicEndpoint(initialData),
    listen_port: normalizeListenPort(initialData.listen_port),
    transport_mode: transportMode,
    allow_transport_fallback: initialData.allow_transport_fallback !== false,
    obfs_mode: normalizeObfsMode(initialData.obfs_mode, transportMode),
    enabled: initialData.enabled !== false,
    certificate_id: initialData.certificate_id == null ? null : Number(initialData.certificate_id),
    certificate_source: inferCertificateSource(initialData),
    trust_mode_source: inferTrustModeSource(initialData),
    tls_mode: initialData.tls_mode || 'pin_and_ca',
    pin_set: Array.isArray(initialData.pin_set)
      ? initialData.pin_set
        .map((item) => ({
          type: String(item?.type || '').trim(),
          value: String(item?.value || '').trim()
        }))
        .filter((item) => item.type && item.value)
      : [],
    trusted_ca_certificate_ids: Array.isArray(initialData.trusted_ca_certificate_ids)
      ? initialData.trusted_ca_certificate_ids.map((id) => Number(id)).filter((id) => Number.isInteger(id))
      : [],
    allow_self_signed: initialData.allow_self_signed === true,
    tags: Array.isArray(initialData.tags) ? [...initialData.tags] : []
  }
}

function resetErrors() {
  errors.value = createEmptyErrors()
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

function toggleTrustedCa(certId) {
  if (form.value.trust_mode_source === 'auto') return
  const value = Number(certId)
  const next = new Set(trustedCaSet.value)
  if (next.has(value)) next.delete(value)
  else next.add(value)
  trustedCaSet.value = next
}

function parsePinSetRows() {
  return pinSetText.value
    .split(/\r?\n/)
    .map((item) => item.trim())
    .filter(Boolean)
    .map((row) => {
      const separator = row.indexOf(':')
      if (separator === -1) {
        return { type: 'spki_sha256', value: row }
      }
      return {
        type: row.slice(0, separator).trim(),
        value: row.slice(separator + 1).trim()
      }
    })
    .filter((item) => item.type && item.value)
}

function validateCustomTrustMaterial(pinSet, trustedCaIds) {
  if (form.value.tls_mode === 'pin_only' && pinSet.length === 0) {
    return '仅 Pin 模式至少需要一个 Pin Set'
  }
  if (form.value.tls_mode === 'ca_only' && trustedCaIds.length === 0) {
    return '仅 CA 模式至少需要一个可信 CA'
  }
  if (form.value.tls_mode === 'pin_and_ca' && (pinSet.length === 0 || trustedCaIds.length === 0)) {
    return 'Pin + CA 模式需要同时提供 Pin Set 和可信 CA'
  }
  if (form.value.tls_mode === 'pin_or_ca' && pinSet.length === 0 && trustedCaIds.length === 0) {
    return '证书 Pin 或 CA 模式至少需要提供一项信任材料'
  }
  return ''
}

function validate() {
  resetErrors()
  const publicEndpoint = parsePublicEndpoint(form.value.public_endpoint)
  const bindHosts = normalizeBindHosts(form.value.bind_hosts_text)
  const listenPort = normalizeListenPort(form.value.listen_port)

  if (!form.value.name.trim()) {
    errors.value.name = '请输入监听器名称'
  }
  if (!bindHosts.length) {
    errors.value.bind_hosts = '请至少填写一个绑定地址'
  }
  if (!publicEndpoint.isValid) {
    errors.value.public_endpoint = '公网入口仅支持空值、host 或 host:port'
  } else if (publicEndpoint.publicHost && !isConcreteCertificateHost(publicEndpoint.publicHost)) {
    errors.value.public_endpoint = '公网入口必须是具体的 DNS 名称或 IP 地址'
  } else if (!publicEndpoint.publicHost && !bindHosts.some(isConcreteCertificateHost)) {
    errors.value.public_endpoint = '使用通配绑定地址时必须填写公网入口'
  }
  if (listenPort == null || listenPort < 1 || listenPort > 65535) {
    errors.value.listen_port = '监听端口必须在 1-65535 之间'
  }
  if (form.value.enabled && form.value.certificate_source === 'existing_certificate' && form.value.certificate_id == null) {
    errors.value.certificate_id = '启用监听器时必须绑定监听证书'
  }
  const pinSet = parsePinSetRows()
  const trustedCaIds = [...trustedCaSet.value]
  if (form.value.trust_mode_source === 'custom') {
    errors.value.trust_material = validateCustomTrustMaterial(pinSet, trustedCaIds)
  }

  return !errors.value.name
    && !errors.value.bind_hosts
    && !errors.value.public_endpoint
    && !errors.value.listen_port
    && !errors.value.certificate_id
    && !errors.value.trust_material
}

function isConcreteCertificateHost(value) {
  const host = String(value || '').trim().replace(/^\[|\]$/g, '')
  const ipv4 = parseIPv4CertificateHost(host)
  if (ipv4) return ipv4 !== '0.0.0.0'
  if (host.includes(':')) {
    try {
      const parsed = new URL(`http://[${host}]/`)
      return parsed.hostname !== '[::]'
    } catch {
      return false
    }
  }
  const dnsHost = host.toLowerCase().replace(/\.$/, '')
  if (!dnsHost || dnsHost.length > 253) return false
  return dnsHost.split('.').every((label) => (
    label.length > 0
    && label.length <= 63
    && /^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/.test(label)
  ))
}

function parseIPv4CertificateHost(host) {
  const labels = host.split('.')
  if (labels.length !== 4 || labels.some((label) => !/^\d{1,3}$/.test(label))) return ''
  const octets = labels.map(Number)
  if (octets.some((octet) => octet > 255)) return ''
  return octets.join('.')
}

async function handleSubmit() {
  if (!validate()) return

  const publicEndpoint = parsePublicEndpoint(form.value.public_endpoint)
  const bindHosts = normalizeBindHosts(form.value.bind_hosts_text)
  const listenPort = normalizeListenPort(form.value.listen_port)
  const pinSet = form.value.trust_mode_source === 'auto' ? [] : parsePinSetRows()
  const trustedCaIds = form.value.trust_mode_source === 'auto'
    ? []
    : [...trustedCaSet.value].map((id) => Number(id))
  const payload = {
    name: form.value.name.trim(),
    listen_port: listenPort,
    transport_mode: form.value.transport_mode,
    allow_transport_fallback: form.value.transport_mode === 'quic'
      ? form.value.allow_transport_fallback === true
      : true,
    obfs_mode: form.value.transport_mode === 'tls_tcp'
      ? form.value.obfs_mode
      : 'off',
    enabled: form.value.enabled,
    certificate_id: form.value.certificate_source === 'existing_certificate'
      ? (form.value.certificate_id == null ? null : Number(form.value.certificate_id))
      : null,
    certificate_source: form.value.certificate_source,
    trust_mode_source: form.value.trust_mode_source,
    tls_mode: form.value.trust_mode_source === 'auto' ? 'pin_and_ca' : form.value.tls_mode,
    pin_set: pinSet,
    trusted_ca_certificate_ids: trustedCaIds,
    allow_self_signed: form.value.trust_mode_source === 'auto' ? true : form.value.allow_self_signed,
    tags: [...form.value.tags]
  }
  payload.bind_hosts = bindHosts
  if (publicEndpoint.publicHost) {
    payload.public_host = publicEndpoint.publicHost
  }
  if (publicEndpoint.publicPort != null) {
    payload.public_port = publicEndpoint.publicPort
  }

  try {
    if (isEdit.value) {
      await updateRelayListener.mutateAsync({ id: props.initialData.id, ...payload })
    } else {
      await createRelayListener.mutateAsync(payload)
    }
    emit('success')
  } catch (err) {
    errors.value.submit = err?.message || '操作失败'
  }
}
</script>

<style scoped>
.relay-listener-form {
  display: flex;
  flex-direction: column;
  gap: 0;
  min-height: 0;
  flex: 1 1 auto;
  width: 100%;
  max-height: 100%;
  margin: -0.1rem 0 0;
}

.relay-listener-form__body {
  display: flex;
  flex-direction: column;
  gap: 0.7rem;
  flex: 1 1 auto;
  min-height: 0;
  overflow-x: hidden;
  overflow-y: auto;
  overscroll-behavior: contain;
  padding: 0.15rem 0.05rem 0.15rem;
}

.relay-listener-form__footer {
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

.relay-listener-form__submit-error {
  margin: 0;
  margin-right: auto;
  max-width: min(100%, 28rem);
}

.relay-listener-form__submit {
  min-width: 8.5rem;
  min-height: 2.35rem;
  padding: 0.55rem 1.15rem;
  border-radius: var(--radius-lg);
  font-weight: 700;
  letter-spacing: -0.01em;
  box-shadow: 0 8px 18px -12px color-mix(in srgb, var(--color-primary) 70%, transparent);
}

.relay-listener-form__submit:hover:not(:disabled) {
  filter: brightness(1.02);
}

.settings-card {
  display: flex;
  flex-direction: column;
  gap: 0.55rem;
  padding: 0.8rem 0.9rem;
  background:
    linear-gradient(
      180deg,
      color-mix(in srgb, var(--color-bg-surface) 92%, var(--color-primary-subtle)) 0%,
      var(--color-bg-surface) 42%
    );
  border: 1px solid color-mix(in srgb, var(--color-border-default) 94%, var(--color-primary) 6%);
  border-radius: calc(var(--radius-lg) + 2px);
  box-shadow: 0 1px 0 color-mix(in srgb, var(--color-bg-surface-raised) 65%, transparent);
}

.settings-card--compact {
  gap: 0.4rem;
  padding: 0.65rem 0.75rem;
  justify-content: flex-start;
}

.section-header {
  display: flex;
  flex-direction: column;
  gap: 0.1rem;
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

.form-row {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0.65rem;
  align-items: start;
}

.listen-grid {
  display: grid;
  grid-template-columns: minmax(0, 1.55fr) minmax(7.5rem, 0.55fr);
  gap: 0.65rem;
  align-items: start;
}

.form-group {
  display: flex;
  flex-direction: column;
  gap: 0.3rem;
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

.field-hint {
  margin: 0;
  font-size: 0.6875rem;
  color: var(--color-text-muted);
  line-height: 1.4;
}

.form-error {
  margin: 0;
  font-size: var(--text-xs);
  color: var(--color-danger);
}

.form-error--block {
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-md);
  background: var(--color-danger-50);
}

.input {
  width: 100%;
  min-width: 0;
  min-height: 2.35rem;
  padding: 0.5rem 0.75rem;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-surface);
  color: var(--color-text-primary);
  font-size: var(--text-sm);
  box-sizing: border-box;
  font-family: inherit;
  line-height: 1.35;
}

.input::placeholder {
  color: var(--color-text-muted);
  opacity: 1;
}

.input:focus {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.input:disabled {
  opacity: 0.65;
  cursor: not-allowed;
  background: var(--color-bg-subtle);
}

.input--error {
  border-color: var(--color-danger);
}

.textarea {
  min-height: 4.5rem;
  resize: vertical;
  line-height: 1.45;
}

.textarea--hosts {
  min-height: 2.75rem;
  height: 2.75rem;
  resize: vertical;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 0.8125rem;
}

.path-chip {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.55rem 0.7rem;
  border-radius: var(--radius-md);
  border: 1px solid color-mix(in srgb, var(--color-primary) 14%, var(--color-border-default));
  background: color-mix(in srgb, var(--color-primary-subtle) 55%, var(--color-bg-surface));
  font-size: 0.75rem;
  color: var(--color-text-secondary);
  line-height: 1.4;
}

.path-chip__dot {
  width: 0.45rem;
  height: 0.45rem;
  border-radius: 999px;
  background: var(--color-primary);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--color-primary) 16%, transparent);
  flex-shrink: 0;
}

.option-row {
  display: flex;
  align-items: center;
  gap: 0.65rem;
  min-height: 2.35rem;
  padding: 0.55rem 0.7rem;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-surface);
  cursor: pointer;
  transition:
    border-color var(--duration-fast) var(--ease-default),
    background var(--duration-fast) var(--ease-default);
}

.option-row--compact {
  min-height: 2.35rem;
  white-space: nowrap;
}

.option-row--active {
  border-color: color-mix(in srgb, var(--color-primary) 28%, var(--color-border-default));
  background: color-mix(in srgb, var(--color-primary-subtle) 42%, var(--color-bg-surface));
}

.option-row--disabled {
  opacity: 0.72;
  cursor: not-allowed;
}

.option-row__content {
  display: inline-flex;
  flex-direction: row;
  align-items: center;
  flex-wrap: nowrap;
  gap: 0.45rem;
  min-width: 0;
}

.option-row__label {
  font-size: 0.8125rem;
  font-weight: 600;
  color: var(--color-text-primary);
  line-height: 1.35;
  white-space: nowrap;
}

.option-row__desc {
  font-size: 0.6875rem;
  color: var(--color-text-tertiary);
  line-height: 1.35;
  white-space: nowrap;
}

.option-row__desc::before {
  content: '·';
  margin-right: 0.45rem;
  color: var(--color-text-tertiary);
}

.checkbox-list {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
  gap: var(--space-2);
  padding: var(--space-2);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-surface);
  min-height: 2.75rem;
}

.checkbox-list--disabled {
  opacity: 0.7;
  background: var(--color-bg-subtle);
}

.checkbox-list__empty {
  margin: 0;
  grid-column: 1 / -1;
  font-size: var(--text-xs);
  color: var(--color-text-muted);
  padding: 0.25rem 0.15rem;
}

.checkbox-item {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  font-size: var(--text-xs);
  color: var(--color-text-primary);
}

.toggle__input {
  position: absolute;
  opacity: 0;
  width: 0;
  height: 0;
}

.toggle__slider {
  position: relative;
  width: 42px;
  height: 24px;
  background: var(--color-border-strong);
  border-radius: var(--radius-full);
  flex-shrink: 0;
  transition: background var(--duration-fast) var(--ease-default);
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
  box-shadow: 0 1px 2px rgb(15 23 42 / 0.16);
}

.toggle__input:checked + .toggle__slider {
  background: var(--color-primary);
}

.toggle__input:checked + .toggle__slider::after {
  transform: translateX(18px);
}

.toggle__input:disabled + .toggle__slider {
  opacity: 0.7;
}

.advanced-toggle {
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
  align-self: flex-start;
  border: none;
  background: none;
  padding: 0.15rem 0;
  font-size: 0.8125rem;
  font-weight: 600;
  color: var(--color-text-secondary);
  cursor: pointer;
  transition: color var(--duration-fast) var(--ease-default);
}

.advanced-toggle:hover {
  color: var(--color-text-primary);
}

.advanced-toggle__arrow {
  transition: transform var(--duration-fast) var(--ease-default);
  flex-shrink: 0;
}

.advanced-toggle__arrow--open {
  transform: rotate(180deg);
}

.advanced-toggle__badge {
  display: inline-flex;
  align-items: center;
  padding: 0.1rem 0.45rem;
  border-radius: var(--radius-full);
  background: color-mix(in srgb, var(--color-primary-subtle) 70%, transparent);
  color: var(--color-primary);
  font-size: 0.6875rem;
  font-weight: 650;
  line-height: 1.3;
}

.advanced-panel {
  display: flex;
  flex-direction: column;
  gap: 0.65rem;
  padding-top: 0.35rem;
}

.advanced-panel__hint {
  margin-top: 0;
}

.tag-input {
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  transition: all var(--duration-fast) var(--ease-default);
  max-width: 100%;
  overflow: hidden;
  min-height: 2.35rem;
}

.tag-input:focus-within {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.tag-input__container {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  padding: 0.3rem 0.5rem;
  align-items: center;
  min-height: 2.35rem;
}

.tag-input__field {
  flex: 1;
  min-width: 72px;
  border: none;
  background: transparent;
  padding: 0.2rem;
  font-size: var(--text-sm);
  color: var(--color-text-primary);
  outline: none;
  font-family: inherit;
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

@media (max-width: 720px) {
  .form-row,
  .listen-grid {
    grid-template-columns: 1fr;
  }

  .textarea--hosts {
    height: auto;
    min-height: 3.5rem;
  }
}
</style>

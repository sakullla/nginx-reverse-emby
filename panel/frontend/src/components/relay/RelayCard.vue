<template>
  <BaseListCard
    :status="listener.enabled ? 'success' : 'neutral'"
    :disabled="!listener.enabled"
    :title="listener.name"
    @click="$emit('edit', listener)"
  >
    <template #header-left>
      <BaseBadge tone="neutral" subtone="secondary" mono>#{{ listener.id }}</BaseBadge>
      <BaseBadge :tone="listener.enabled ? 'success' : 'neutral'" dot>
        {{ listener.enabled ? '启用' : '已禁用' }}
      </BaseBadge>
    </template>

    <template #header-right>
      <BaseIconButton
        tone="warning"
        :title="listener.enabled ? '停用' : '启用'"
        @click="$emit('toggle', listener)"
      >
        <svg v-if="listener.enabled" width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
          <rect x="6" y="4" width="4" height="16" rx="1"/>
          <rect x="14" y="4" width="4" height="16" rx="1"/>
        </svg>
        <svg v-else width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
          <polygon points="5 3 19 12 5 21 5 3"/>
        </svg>
      </BaseIconButton>
      <BaseIconButton title="编辑" @click="$emit('edit', listener)">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
          <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
        </svg>
      </BaseIconButton>
      <BaseIconButton tone="danger" title="删除" @click="$emit('delete', listener)">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <polyline points="3 6 5 6 21 6"/>
          <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
        </svg>
      </BaseIconButton>
    </template>

    <div class="relay-card__mapping">
      <div class="relay-card__endpoint">
        <span class="relay-card__url-icon">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>
            <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>
          </svg>
        </span>
        <code class="relay-card__addr">{{ publicEndpoint }}</code>
      </div>
      <div class="relay-card__endpoint">
        <span class="relay-card__url-icon">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M5 12h14"/>
            <path d="M12 5l7 7-7 7"/>
          </svg>
        </span>
        <code class="relay-card__addr">{{ bindEndpoint }}</code>
      </div>
    </div>

    <div class="relay-card__meta">
      <BaseBadge tone="neutral" subtone="secondary" size="sm">
        {{ listener.certificate_id ? `证书 #${listener.certificate_id}` : '未绑定证书' }}
      </BaseBadge>
      <BaseBadge tone="neutral" subtone="secondary" size="sm">{{ transportLabel }}</BaseBadge>
      <BaseBadge tone="neutral" subtone="secondary" size="sm">{{ obfsLabel }}</BaseBadge>
      <BaseBadge tone="neutral" subtone="secondary" size="sm">{{ trustLabel }}</BaseBadge>
      <BaseBadge v-if="isQuic" tone="neutral" subtone="secondary" size="sm">{{ fallbackLabel }}</BaseBadge>
      <BaseBadge v-if="listener.allow_self_signed" tone="warning" size="sm">允许自签</BaseBadge>
    </div>

    <div class="traffic-line">
      <span>↓ {{ formatBytes(normalizedTraffic.rx_bytes) }}</span>
      <span>↑ {{ formatBytes(normalizedTraffic.tx_bytes) }}</span>
    </div>

    <template v-if="hasTags" #footer>
      <BaseBadge v-for="tag in listener.tags" :key="tag" tone="primary">{{ tag }}</BaseBadge>
    </template>
  </BaseListCard>
</template>

<script setup>
import { computed } from 'vue'
import BaseListCard from '../base/BaseListCard.vue'
import BaseBadge from '../base/BaseBadge.vue'
import BaseIconButton from '../base/BaseIconButton.vue'
import { formatBytes, normalizeTrafficBucket } from '../../utils/trafficStats.js'

const props = defineProps({
  listener: { type: Object, required: true },
  traffic: { type: Object, default: () => ({ rx_bytes: 0, tx_bytes: 0 }) },
})

defineEmits(['edit', 'delete', 'toggle'])

function normalizePort(port) {
  const value = Number(port)
  return Number.isInteger(value) && value > 0 ? value : null
}

function resolveBindHosts(listener) {
  if (Array.isArray(listener?.bind_hosts) && listener.bind_hosts.length) {
    return listener.bind_hosts
      .map((item) => String(item || '').trim())
      .filter(Boolean)
  }
  const legacyHost = String(listener?.listen_host || '').trim()
  return legacyHost ? [legacyHost] : []
}

const publicEndpoint = computed(() => {
  const publicHost = String(props.listener?.public_host || '').trim()
  const bindHosts = resolveBindHosts(props.listener)
  const host = publicHost || bindHosts[0] || '-'
  const port = normalizePort(props.listener?.public_port) ?? normalizePort(props.listener?.listen_port)
  return port ? `${host}:${port}` : host
})

const bindEndpoint = computed(() => {
  const bindHosts = resolveBindHosts(props.listener)
  const bindLabel = bindHosts.length ? bindHosts.join(', ') : '-'
  const listenPort = normalizePort(props.listener?.listen_port)
  return listenPort ? `${bindLabel}:${listenPort}` : bindLabel
})

const isQuic = computed(() => props.listener?.transport_mode === 'quic')

const transportLabel = computed(() => (isQuic.value ? 'QUIC' : 'TLS/TCP'))

const obfsLabel = computed(() => {
  const isTlsTcp = !isQuic.value
  const obfs = isTlsTcp && props.listener?.obfs_mode === 'early_window_v2'
    ? '隐匿 early_window_v2'
    : '隐匿关闭'
  return obfs
})

const trustLabel = computed(() => {
  if (props.listener?.trust_mode_source === 'auto') return '自动 Relay CA + Pin'
  if (props.listener?.tls_mode === 'pin_and_ca') return 'Pin + CA'
  if (props.listener?.tls_mode === 'pin_only') return '仅 Pin'
  if (props.listener?.tls_mode === 'ca_only') return '仅 CA'
  return '兼容模式'
})

const fallbackLabel = computed(() => {
  if (props.listener?.allow_transport_fallback === false) return '禁止回退'
  return '允许回退 TLS/TCP'
})

const normalizedTraffic = computed(() => normalizeTrafficBucket(props.traffic))
const hasTags = computed(() => Array.isArray(props.listener.tags) && props.listener.tags.length > 0)
</script>

<style scoped>
.relay-card__mapping {
  display: flex;
  flex-direction: column;
  gap: 0.375rem;
}
.relay-card__endpoint {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  min-width: 0;
}
.relay-card__addr {
  font-family: var(--font-mono);
  font-size: 0.875rem;
  font-weight: 500;
  color: var(--color-text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.relay-card__url-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--color-text-tertiary);
  flex-shrink: 0;
}
.relay-card__meta {
  display: flex;
  gap: 0.25rem;
  flex-wrap: wrap;
}
.traffic-line {
  display: flex;
  gap: 0.75rem;
  color: var(--color-text-tertiary);
  font-size: 0.8125rem;
  font-variant-numeric: tabular-nums;
}

@media (max-width: 640px) {
  :deep(.base-icon-button) {
    width: 36px;
    height: 36px;
  }
}
</style>

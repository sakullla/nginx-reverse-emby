<template>
  <BaseListCard
    :status="statusTone"
    :disabled="!listener.enabled"
  >
    <template #header-left>
      <BaseBadge tone="neutral" subtone="secondary" mono>#{{ listener.id }}</BaseBadge>
      <span
        v-if="listener.name"
        class="relay-card__name"
        :title="listener.name"
      >{{ listener.name }}</span>
      <BaseBadge
        class="relay-card__transport"
        :tone="transportTone"
        shape="square"
        mono
      >
        {{ transportLabel }}
      </BaseBadge>
      <BaseBadge :tone="statusTone" dot>
        {{ statusLabel }}
      </BaseBadge>
      <!-- 已按节点筛选时，节点徽章重复；仅全部节点视图展示 -->
      <AgentBadge v-if="showAgentBadge" :item="listener" :agent="agent" />
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
      <BaseActionMenu :items="moreItems" @select="onMoreSelect" />
    </template>

    <div class="relay-card__mapping">
      <div class="relay-card__endpoint">
        <span class="relay-card__endpoint-label">{{ publicEndpointLabel }}</span>
        <code class="relay-card__addr" :title="publicEndpoint">{{ publicEndpoint }}</code>
      </div>
    </div>

    <div v-if="metaChips.length" class="relay-card__meta">
      <BaseBadge
        v-for="chip in metaChips"
        :key="chip.key"
        :tone="chip.tone"
        :subtone="chip.subtone"
        size="sm"
        :shape="chip.shape || 'pill'"
        :mono="!!chip.mono"
        :title="chip.title || undefined"
      >
        {{ chip.label }}
      </BaseBadge>
    </div>

    <TrafficBar
      v-if="hasTraffic"
      :accounted="normalizedTraffic.accounted_bytes"
      :rx="normalizedTraffic.rx_bytes"
      :tx="normalizedTraffic.tx_bytes"
      :node-total="agentNodeTotal"
      @click="$emit('traffic-click', listener)"
    />

    <template v-if="hasTags" #footer>
      <BaseBadge v-for="tag in listener.tags" :key="tag" tone="primary">{{ tag }}</BaseBadge>
    </template>
  </BaseListCard>
</template>

<script setup>
import { computed } from 'vue'
import BaseListCard from '../base/BaseListCard.vue'
import BaseBadge from '../base/BaseBadge.vue'
import BaseActionMenu from '../base/BaseActionMenu.vue'
import AgentBadge from '../common/AgentBadge.vue'
import BaseIconButton from '../base/BaseIconButton.vue'
import TrafficBar from '../traffic/TrafficBar.vue'
import { normalizeTrafficSummaryBucket } from '../../utils/trafficStats.js'
import { enabledStatusLabel, enabledStatusTone } from '../../utils/resourceCardStatus.js'

const props = defineProps({
  listener: { type: Object, required: true },
  agent: { type: Object, default: null },
  traffic: { type: Object, default: null },
  agentNodeTotal: { type: Number, default: 0 },
})

const emit = defineEmits(['edit', 'delete', 'toggle', 'traffic-click'])

const statusTone = computed(() => enabledStatusTone(!!props.listener.enabled))
const statusLabel = computed(() => enabledStatusLabel(!!props.listener.enabled))
// agent prop is the page-selected node; when set, every card would repeat the same badge.
const showAgentBadge = computed(() => !props.agent)

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

const isQuic = computed(() => props.listener?.transport_mode === 'quic')
const publicEndpointLabel = computed(() => '公网入口')

const transportLabel = computed(() => (isQuic.value ? 'QUIC' : 'TLS/TCP'))
const transportTone = computed(() => (isQuic.value ? 'primary' : 'warning'))

const certChip = computed(() => {
  if (props.listener?.certificate_id) {
    return {
      key: 'cert',
      label: `证书 #${props.listener.certificate_id}`,
      tone: 'neutral',
      subtone: 'secondary',
      shape: 'square',
      mono: true,
      title: `绑定证书 #${props.listener.certificate_id}`
    }
  }
  return {
    key: 'cert-missing',
    label: '未绑证书',
    tone: 'warning',
    subtone: undefined,
    shape: 'square',
    mono: true,
    title: '未绑定监听证书'
  }
})

const obfsChip = computed(() => {
  if (isQuic.value) return null
  if (props.listener?.obfs_mode === 'early_window_v2') {
    return {
      key: 'obfs-on',
      label: '隐匿',
      tone: 'primary',
      subtone: 'secondary',
      shape: 'square',
      mono: true,
      title: 'TLS 隐匿 early_window_v2'
    }
  }
  return null
})

const trustChip = computed(() => {
  if (props.listener?.trust_mode_source === 'auto') {
    return {
      key: 'trust-auto',
      label: '自动信任',
      tone: 'neutral',
      subtone: 'secondary',
      shape: 'square',
      mono: true,
      title: '自动 Relay CA + Pin'
    }
  }
  if (props.listener?.tls_mode === 'pin_and_ca') {
    return {
      key: 'trust-pin-ca',
      label: 'Pin+CA',
      tone: 'neutral',
      subtone: 'secondary',
      shape: 'square',
      mono: true,
      title: '自定义 Pin + CA'
    }
  }
  if (props.listener?.tls_mode === 'pin_only') {
    return {
      key: 'trust-pin',
      label: '仅 Pin',
      tone: 'neutral',
      subtone: 'secondary',
      shape: 'square',
      mono: true,
      title: '自定义仅 Pin'
    }
  }
  if (props.listener?.tls_mode === 'ca_only') {
    return {
      key: 'trust-ca',
      label: '仅 CA',
      tone: 'neutral',
      subtone: 'secondary',
      shape: 'square',
      mono: true,
      title: '自定义仅 CA'
    }
  }
  return {
    key: 'trust-compat',
    label: '兼容信任',
    tone: 'neutral',
    subtone: 'secondary',
    shape: 'square',
    mono: true,
    title: '兼容模式'
  }
})

const fallbackChip = computed(() => {
  if (!isQuic.value) return null
  if (props.listener?.allow_transport_fallback === false) {
    return {
      key: 'fallback-off',
      label: '禁回退',
      tone: 'warning',
      subtone: undefined,
      shape: 'square',
      mono: true,
      title: '禁止回退 TLS/TCP'
    }
  }
  return {
    key: 'fallback-on',
    label: '可回退',
    tone: 'neutral',
    subtone: 'secondary',
    shape: 'square',
    mono: true,
    title: '允许回退 TLS/TCP'
  }
})

const selfSignedChip = computed(() => {
  if (!props.listener?.allow_self_signed) return null
  return {
    key: 'self-signed',
    label: '允许自签',
    tone: 'warning',
    subtone: undefined,
    shape: undefined,
    mono: false,
    title: '允许上游使用自签名证书'
  }
})

const metaChips = computed(() => (
  [certChip.value, obfsChip.value, trustChip.value, fallbackChip.value, selfSignedChip.value]
    .filter(Boolean)
))

const hasTraffic = computed(() => props.traffic != null)
const normalizedTraffic = computed(() => normalizeTrafficSummaryBucket(props.traffic))
const hasTags = computed(() => Array.isArray(props.listener.tags) && props.listener.tags.length > 0)

const moreItems = computed(() => [
  { id: 'delete', label: '删除', tone: 'danger' },
])

function onMoreSelect(item) {
  if (item.id === 'delete') emit('delete', props.listener)
}
</script>

<style scoped>
.relay-card__name {
  min-width: 0;
  max-width: min(18rem, 100%);
  margin-right: 0.1rem;
  font-family: var(--font-mono);
  font-size: 0.9375rem;
  font-weight: 700;
  color: var(--color-text-primary);
  line-height: 1.2;
  letter-spacing: -0.025em;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.relay-card__transport {
  flex-shrink: 0;
  letter-spacing: 0.02em;
}

.relay-card__mapping {
  display: flex;
  flex-direction: column;
  gap: 0.15rem;
  min-width: 0;
  padding: 0.05rem 0 0;
}

.relay-card__endpoint {
  display: flex;
  align-items: baseline;
  gap: 0.4rem;
  min-width: 0;
}

.relay-card__addr {
  font-family: var(--font-mono);
  font-size: 0.75rem;
  font-weight: 500;
  color: var(--color-text-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
  flex: 1;
  line-height: 1.35;
}

.relay-card__endpoint-label {
  flex-shrink: 0;
  font-size: 0.6875rem;
  font-weight: 600;
  letter-spacing: 0.02em;
  color: var(--color-text-muted);
  line-height: 1.3;
}

.relay-card__meta {
  display: flex;
  gap: 0.28rem;
  flex-wrap: wrap;
  align-items: center;
  min-width: 0;
}

@media (max-width: 640px) {
  :deep(.base-icon-button) {
    width: 36px;
    height: 36px;
  }

  .relay-card__name {
    font-size: 0.875rem;
  }
}
</style>

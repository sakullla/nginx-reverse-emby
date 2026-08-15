<template>
  <BaseListCard
    :status="statusTone"
    :disabled="!rule.enabled"
    :title="rule.frontend_url || ''"
  >
    <template #header-left>
      <BaseBadge tone="neutral" subtone="secondary" mono>#{{ rule.id }}</BaseBadge>
      <BaseBadge :tone="isHttps ? 'success' : 'primary'" shape="square" mono>
        {{ isHttps ? 'HTTPS' : 'HTTP' }}
      </BaseBadge>
      <BaseBadge :tone="statusTone" dot>{{ statusLabel }}</BaseBadge>
      <!-- 已按节点筛选时，节点徽章重复；仅全部节点视图展示 -->
      <AgentBadge v-if="showAgentBadge" :item="rule" :agent="agent" />
    </template>
    <template #header-right>
      <BaseIconButton tone="warning" :title="rule.enabled ? '停用' : '启用'" @click="$emit('toggle', rule)">
        <svg v-if="rule.enabled" width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
          <rect x="6" y="4" width="4" height="16" rx="1"/>
          <rect x="14" y="4" width="4" height="16" rx="1"/>
        </svg>
        <svg v-else width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
          <polygon points="5 3 19 12 5 21 5 3"/>
        </svg>
      </BaseIconButton>
      <BaseIconButton title="编辑" @click="$emit('edit', rule)">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
          <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
        </svg>
      </BaseIconButton>
      <BaseIconButton title="复制" @click="$emit('copy', rule)">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <rect x="9" y="9" width="13" height="13" rx="2"/>
          <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/>
        </svg>
      </BaseIconButton>
      <BaseIconButton title="诊断" @click="$emit('diagnose', rule)">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/>
        </svg>
      </BaseIconButton>
      <BaseIconButton tone="danger" title="删除" @click="$emit('delete', rule)">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <polyline points="3 6 5 6 21 6"/>
          <path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/>
        </svg>
      </BaseIconButton>
    </template>

    <div class="rule-card__mapping">
      <div class="rule-card__url-row">
        <span class="rule-card__url-label">后端</span>
        <code class="rule-card__backend" :title="backendsTooltip">{{ backendPrimary }}</code>
        <span v-if="backendExtraCount > 0" class="rule-card__backend-more">+{{ backendExtraCount }}</span>
      </div>
      <div v-if="providerStatus" class="rule-card__provider-status" :class="`rule-card__provider-status--${providerState}`">
        <span class="rule-card__provider-dot" aria-hidden="true"></span>
        {{ providerStatus }}
      </div>
    </div>

    <TrafficBar
      v-if="hasTraffic"
      :accounted="normalizedTraffic.accounted_bytes"
      :rx="normalizedTraffic.rx_bytes"
      :tx="normalizedTraffic.tx_bytes"
      :node-total="agentNodeTotal"
      @click="$emit('traffic-click', rule)"
    />

    <template v-if="hasTags" #footer>
      <BaseBadge v-for="tag in rule.tags" :key="tag" tone="primary">{{ tag }}</BaseBadge>
    </template>
  </BaseListCard>
</template>

<script setup>
import { computed } from 'vue'
import BaseListCard from '../base/BaseListCard.vue'
import BaseBadge from '../base/BaseBadge.vue'
import AgentBadge from '../common/AgentBadge.vue'
import BaseIconButton from '../base/BaseIconButton.vue'
import { getRuleEffectiveStatus } from '../../utils/syncStatus.js'
import { syncStatusLabel, syncStatusTone } from '../../utils/resourceCardStatus.js'
import TrafficBar from '../traffic/TrafficBar.vue'
import { normalizeTrafficSummaryBucket } from '../../utils/trafficStats.js'
import { describeHTTPBackends } from '../../utils/httpBackend.js'

const props = defineProps({
  rule: { type: Object, required: true },
  agent: { type: Object, default: null },
  traffic: { type: Object, default: null },
  agentNodeTotal: { type: Number, default: 0 },
  providerCatalog: { type: Array, default: () => [] },
  providerCatalogStatus: { type: String, default: 'ready' },
})

defineEmits(['edit', 'toggle', 'copy', 'diagnose', 'delete', 'traffic-click'])

const status = computed(() => getRuleEffectiveStatus(props.rule, props.agent))
const statusTone = computed(() => syncStatusTone(status.value))
const statusLabel = computed(() => syncStatusLabel(status.value))

const isHttps = computed(() => String(props.rule.frontend_url || '').startsWith('https'))

const backendDescriptions = computed(() => describeHTTPBackends(props.rule, props.providerCatalog, props.providerCatalogStatus))
const backends = computed(() => {
  return backendDescriptions.value.map((backend) => (
    backend.kind === 'provider' ? `${backend.label} · ${backend.detail}` : backend.label
  ))
})

const backendPrimary = computed(() => backends.value[0] || '-')
const backendExtraCount = computed(() => Math.max(0, backends.value.length - 1))
const backendsTooltip = computed(() => backends.value.join('\n'))
const providerStatus = computed(() => {
  const provider = backendDescriptions.value.find((backend) => backend.kind === 'provider')
  if (!provider) return ''
  if (provider.state === 'active') {
    return provider.generation ? `插件已就绪 · ${provider.generation}` : '插件已就绪'
  }
  return provider.state === 'unknown' ? '插件状态待确认' : '插件当前不可用'
})
const providerState = computed(() => backendDescriptions.value.find((backend) => backend.kind === 'provider')?.state || '')
// agent prop is the page-selected node; when set, every card would repeat the same badge.
const showAgentBadge = computed(() => !props.agent)

const hasTraffic = computed(() => props.traffic != null)
const normalizedTraffic = computed(() => normalizeTrafficSummaryBucket(props.traffic))
const hasTags = computed(() => Array.isArray(props.rule.tags) && props.rule.tags.length > 0)

</script>

<style scoped>
.rule-card__mapping {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
  min-width: 0;
}

.rule-card__url-row {
  display: flex;
  align-items: baseline;
  gap: 0.4rem;
  min-width: 0;
}

.rule-card__url-label {
  flex-shrink: 0;
  color: var(--color-text-muted);
  font-size: 0.6875rem;
  font-weight: 650;
  letter-spacing: 0.03em;
  line-height: 1.3;
}

.rule-card__backend {
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

.rule-card__backend-more {
  flex-shrink: 0;
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  font-size: 0.6875rem;
  font-weight: 650;
  line-height: 1.3;
}

.rule-card__provider-status {
  display: flex;
  align-items: center;
  gap: 0.35rem;
  color: var(--color-success);
  font-size: 0.6875rem;
  font-weight: 600;
  line-height: 1.3;
}

.rule-card__provider-status--unknown {
  color: var(--color-text-muted);
}

.rule-card__provider-status--unavailable,
.rule-card__provider-status--inactive {
  color: var(--color-warning);
}

.rule-card__provider-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
}
</style>

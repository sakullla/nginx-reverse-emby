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
      <BaseActionMenu :items="moreItems" @select="onMoreSelect" />
    </template>

    <div class="rule-card__mapping">
      <div class="rule-card__url-row">
        <span class="rule-card__url-label">后端</span>
        <code class="rule-card__backend" :title="backendsTooltip">{{ backendPrimary }}</code>
        <span v-if="backendExtraCount > 0" class="rule-card__backend-more">+{{ backendExtraCount }}</span>
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
import BaseActionMenu from '../base/BaseActionMenu.vue'
import AgentBadge from '../common/AgentBadge.vue'
import BaseIconButton from '../base/BaseIconButton.vue'
import { getRuleEffectiveStatus } from '../../utils/syncStatus.js'
import { syncStatusLabel, syncStatusTone } from '../../utils/resourceCardStatus.js'
import TrafficBar from '../traffic/TrafficBar.vue'
import { normalizeTrafficSummaryBucket } from '../../utils/trafficStats.js'

const props = defineProps({
  rule: { type: Object, required: true },
  agent: { type: Object, default: null },
  traffic: { type: Object, default: null },
  agentNodeTotal: { type: Number, default: 0 },
})

const emit = defineEmits(['edit', 'toggle', 'copy', 'diagnose', 'delete', 'traffic-click'])

const status = computed(() => getRuleEffectiveStatus(props.rule, props.agent))
const statusTone = computed(() => syncStatusTone(status.value))
const statusLabel = computed(() => syncStatusLabel(status.value))

const isHttps = computed(() => String(props.rule.frontend_url || '').startsWith('https'))

const backends = computed(() => {
  if (Array.isArray(props.rule.backends) && props.rule.backends.length > 0) {
    return props.rule.backends
      .map((b) => String(b?.url || '').trim())
      .filter(Boolean)
  }
  return []
})

const backendPrimary = computed(() => backends.value[0] || '-')
const backendExtraCount = computed(() => Math.max(0, backends.value.length - 1))
const backendsTooltip = computed(() => backends.value.join('\n'))
// agent prop is the page-selected node; when set, every card would repeat the same badge.
const showAgentBadge = computed(() => !props.agent)

const hasTraffic = computed(() => props.traffic != null)
const normalizedTraffic = computed(() => normalizeTrafficSummaryBucket(props.traffic))
const hasTags = computed(() => Array.isArray(props.rule.tags) && props.rule.tags.length > 0)

const moreItems = computed(() => [
  { id: 'copy', label: '复制' },
  { id: 'diagnose', label: '诊断' },
  { id: 'delete', label: '删除', tone: 'danger' },
])

function onMoreSelect(item) {
  if (item.id === 'copy') emit('copy', props.rule)
  else if (item.id === 'diagnose') emit('diagnose', props.rule)
  else if (item.id === 'delete') emit('delete', props.rule)
}
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
</style>

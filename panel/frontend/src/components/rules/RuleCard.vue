<template>
  <BaseListCard
    :status="statusTone"
    :disabled="!rule.enabled"
    :class="{ 'rule-card--selectable': selectable, 'rule-card--selected': selectable && selected }"
    :aria-selected="selectable ? (selected ? 'true' : 'false') : null"
    @click="onCardClick"
  >
    <template #header-left>
      <input
        v-if="selectable"
        type="checkbox"
        class="rule-card__select"
        :checked="selected"
        :aria-label="`选择规则 #${rule.id}`"
        @click.stop="$emit('select', rule)"
        @keydown.enter.stop
        @keydown.space.stop
      />
      <BaseBadge tone="neutral" subtone="secondary" mono>#{{ rule.id }}</BaseBadge>
      <BaseBadge :tone="isHttps ? 'success' : 'primary'" shape="square" mono>
        {{ isHttps ? 'HTTPS' : 'HTTP' }}
      </BaseBadge>
      <BaseBadge :tone="statusTone" dot>{{ statusLabel }}</BaseBadge>
      <!-- 已按节点筛选时，节点徽章重复；仅全部节点视图展示 -->
      <AgentBadge v-if="showAgentBadge" :item="rule" :agent="agent" />
    </template>
    <template #header-right>
      <BaseIconButton
        tone="warning"
        :title="rule.enabled ? '停用' : '启用'"
        :disabled="selectable"
        data-action="toggle"
        @click="$emit('toggle', rule)"
      >
        <svg v-if="rule.enabled" width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
          <rect x="6" y="4" width="4" height="16" rx="1"/>
          <rect x="14" y="4" width="4" height="16" rx="1"/>
        </svg>
        <svg v-else width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
          <polygon points="5 3 19 12 5 21 5 3"/>
        </svg>
      </BaseIconButton>
      <BaseIconButton title="编辑" :disabled="selectable" data-action="edit" @click="$emit('edit', rule)">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
          <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
        </svg>
      </BaseIconButton>
      <BaseActionMenu v-if="!selectable" :items="menuItems" @select="onMenuSelect" />
    </template>

    <div v-if="frontendUrl" class="rule-card__title-row">
      <div class="rule-card__title" :class="{ 'rule-card__title--disabled': !rule.enabled }" :title="frontendUrl">
        <template v-for="(part, index) in frontendUrlParts" :key="index">{{ part }}<wbr v-if="index < frontendUrlParts.length - 1" /></template>
      </div>
      <CopyButton
        v-if="!selectable"
        :value="frontendUrl"
        label="复制入口 URL"
        success-message="已复制入口 URL"
        size="xs"
        data-copy-target="frontend"
      />
    </div>

    <div class="rule-card__mapping">
      <div class="rule-card__url-row">
        <span class="rule-card__url-label">后端</span>
        <code class="rule-card__backend" :title="backendsTooltip">{{ backendPrimary }}</code>
        <span v-if="backendExtraCount > 0" class="rule-card__backend-more">+{{ backendExtraCount }}</span>
        <CopyButton
          v-if="!selectable"
          :value="backendPrimaryCopyValue"
          label="复制后端地址"
          success-message="已复制后端地址"
          size="xs"
          data-copy-target="backend"
        />
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
      @click="onTrafficClick"
    />

    <template v-if="hasTags || serviceLink" #footer>
      <BaseBadge v-for="tag in rule.tags || []" :key="tag" tone="neutral" subtone="secondary" class="tag-badge">{{ tag }}</BaseBadge>
      <RouterLink v-if="serviceLink" :to="serviceLink.to" class="rule-card__service-link" :title="`服务 ${serviceLink.id}`">服务接入</RouterLink>
    </template>
  </BaseListCard>
</template>

<script setup>
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import BaseListCard from '../base/BaseListCard.vue'
import BaseBadge from '../base/BaseBadge.vue'
import AgentBadge from '../common/AgentBadge.vue'
import BaseIconButton from '../base/BaseIconButton.vue'
import BaseActionMenu from '../base/BaseActionMenu.vue'
import CopyButton from '../common/CopyButton.vue'
import { getRuleEffectiveStatus } from '../../utils/syncStatus.js'
import { syncStatusLabel, syncStatusTone } from '../../utils/resourceCardStatus.js'
import TrafficBar from '../traffic/TrafficBar.vue'
import { normalizeTrafficSummaryBucket } from '../../utils/trafficStats.js'
import { describeHTTPBackends } from '../../utils/httpBackend.js'
import { ruleServiceLink } from '../../utils/accessService.js'
import { breakableUrl } from '../../utils/breakableUrl.js'

const props = defineProps({
  rule: { type: Object, required: true },
  agent: { type: Object, default: null },
  traffic: { type: Object, default: null },
  agentNodeTotal: { type: Number, default: 0 },
  providerCatalog: { type: Array, default: () => [] },
  providerCatalogStatus: { type: String, default: 'ready' },
  /** Bulk selection mode: card body toggles selection instead of opening the editor. */
  selectable: { type: Boolean, default: false },
  selected: { type: Boolean, default: false },
})

const emit = defineEmits(['edit', 'toggle', 'copy', 'diagnose', 'delete', 'traffic-click', 'select'])

const menuItems = [
  { id: 'copy', label: '复制' },
  { id: 'diagnose', label: '诊断' },
  { id: 'delete', label: '删除', tone: 'danger' },
]

function onMenuSelect(item) {
  if (item?.id === 'copy') emit('copy', props.rule)
  else if (item?.id === 'diagnose') emit('diagnose', props.rule)
  else if (item?.id === 'delete') emit('delete', props.rule)
}

function onCardClick() {
  if (props.selectable) emit('select', props.rule)
  else emit('edit', props.rule)
}

function onTrafficClick() {
  if (props.selectable) emit('select', props.rule)
  else emit('traffic-click', props.rule)
}

const status = computed(() => getRuleEffectiveStatus(props.rule, props.agent))
const statusTone = computed(() => syncStatusTone(status.value))
const statusLabel = computed(() => syncStatusLabel(status.value))

const frontendUrl = computed(() => String(props.rule.frontend_url || '').trim())
const frontendUrlParts = computed(() => breakableUrl(frontendUrl.value))
const isHttps = computed(() => frontendUrl.value.startsWith('https'))

const backendDescriptions = computed(() => describeHTTPBackends(props.rule, props.providerCatalog, props.providerCatalogStatus))
const backends = computed(() => {
  return backendDescriptions.value.map((backend) => (
    backend.kind === 'provider' ? `${backend.label} · ${backend.detail}` : backend.label
  ))
})

const backendPrimary = computed(() => backends.value[0] || '-')
const backendPrimaryCopyValue = computed(() => {
  const first = backendDescriptions.value[0]
  return first?.kind === 'url' ? first.label : ''
})
const backendExtraCount = computed(() => Math.max(0, backends.value.length - 1))
const backendsTooltip = computed(() => backends.value.join('\n'))
const providerStatus = computed(() => {
  const provider = backendDescriptions.value.find((backend) => backend.kind === 'provider')
  if (!provider) return ''
  if (provider.state === 'active') {
    return '插件已就绪'
  }
  return provider.state === 'unknown' ? '插件状态待确认' : '插件当前不可用'
})
const providerState = computed(() => backendDescriptions.value.find((backend) => backend.kind === 'provider')?.state || '')
// agent prop is the page-selected node; when set, every card would repeat the same badge.
const showAgentBadge = computed(() => !props.agent)

const hasTraffic = computed(() => props.traffic != null)
const normalizedTraffic = computed(() => normalizeTrafficSummaryBucket(props.traffic))
const hasTags = computed(() => Array.isArray(props.rule.tags) && props.rule.tags.length > 0)
const serviceLink = computed(() => ruleServiceLink(props.rule))

</script>

<style scoped>
.rule-card__select {
  width: 16px;
  height: 16px;
  margin: 0 0.15rem 0 0;
  flex-shrink: 0;
  accent-color: var(--color-primary);
  cursor: pointer;
}

.rule-card--selected {
  border-color: var(--color-primary);
  box-shadow: 0 0 0 1px var(--color-primary);
}

.rule-card__title-row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-1-5);
  min-width: 0;
}

.rule-card__title {
  flex: 1;
  min-width: 0;
  font-family: var(--font-sans);
  font-size: var(--text-base);
  font-weight: var(--font-semibold);
  color: var(--color-text-primary);
  line-height: 1.4;
  letter-spacing: -0.015em;
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  overflow: hidden;
  overflow-wrap: anywhere;
  word-break: normal;
}

.rule-card__title--disabled {
  color: var(--color-text-tertiary);
}

.rule-card__mapping {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
  min-width: 0;
}

.rule-card__url-row {
  display: flex;
  align-items: center;
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

.rule-card__service-link {
  color: var(--color-primary);
  font-size: 0.75rem;
  font-weight: 650;
  text-decoration: none;
}
</style>

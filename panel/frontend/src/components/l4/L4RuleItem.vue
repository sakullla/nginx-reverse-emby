<template>
  <BaseListCard
    :status="statusTone"
    :disabled="!rule.enabled"
    :class="{ 'l4-card--selectable': selectable, 'l4-card--selected': selectable && selected }"
    :aria-selected="selectable ? (selected ? 'true' : 'false') : undefined"
    @click="onCardClick"
  >
    <template #header-left>
      <label v-if="selectable" class="l4-card__select" @click.stop>
        <input
          type="checkbox"
          class="l4-card__checkbox"
          :checked="selected"
          :aria-label="`选择规则 ${listenTitle || '#' + rule.id}`"
          @change="$emit('select', rule)"
        >
      </label>
      <BaseBadge tone="neutral" subtone="secondary" mono>#{{ rule.id }}</BaseBadge>
      <BaseBadge :tone="protoTone" shape="square" mono>{{ rule.protocol?.toUpperCase() }}</BaseBadge>
      <BaseBadge v-if="listenModeLabel" :tone="listenModeTone" shape="square" mono>{{ listenModeLabel }}</BaseBadge>
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
        <svg v-if="rule.enabled" width="14" height="14" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
          <rect x="6" y="4" width="4" height="16" rx="1"/>
          <rect x="14" y="4" width="4" height="16" rx="1"/>
        </svg>
        <svg v-else width="14" height="14" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
          <polygon points="5 3 19 12 5 21 5 3"/>
        </svg>
      </BaseIconButton>
      <BaseIconButton title="编辑" :disabled="selectable" data-action="edit" @click="$emit('edit', rule)">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
          <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
        </svg>
      </BaseIconButton>
      <BaseActionMenu v-if="!selectable" :items="menuItems" @select="onMenuSelect" />
    </template>

    <div v-if="listenTitle" class="l4-card__title-row">
      <div class="l4-card__title" :class="{ 'l4-card__title--disabled': !rule.enabled }" :title="listenTitle">
        <template v-for="(part, index) in listenTitleParts" :key="index">{{ part }}<wbr v-if="index < listenTitleParts.length - 1" /></template>
      </div>
      <CopyButton
        v-if="!selectable"
        :value="listenTitle"
        label="复制监听地址"
        success-message="已复制监听地址"
        size="xs"
        data-copy-target="listen"
      />
    </div>

    <div class="l4-card__mapping">
      <div class="l4-card__endpoint">
        <span class="l4-card__url-label">后端</span>
        <code class="l4-card__addr" :title="backendsTooltip">{{ primaryBackend }}</code>
        <span v-if="backendExtraCount > 0" class="l4-card__more">+{{ backendExtraCount }}</span>
        <CopyButton
          v-if="!selectable"
          :value="primaryBackendCopyValue"
          label="复制后端地址"
          success-message="已复制后端地址"
          size="xs"
          data-copy-target="backend"
        />
        <BaseBadge v-if="hasRelay" tone="warning" shape="square" mono>Relay</BaseBadge>
        <BaseBadge tone="primary" shape="square" :title="lbTitle">{{ lbLabel }}</BaseBadge>
      </div>
    </div>

    <div v-if="tuningTags.length" class="l4-card__tuning">
      <BaseBadge v-for="tag in tuningTags" :key="tag" tone="neutral" subtone="secondary" shape="square" mono>
        {{ tag }}
      </BaseBadge>
    </div>

    <TrafficBar
      v-if="hasTraffic"
      :accounted="normalizedTraffic.accounted_bytes"
      :rx="normalizedTraffic.rx_bytes"
      :tx="normalizedTraffic.tx_bytes"
      :node-total="agentNodeTotal"
      @click="onTrafficClick"
    />

    <template v-if="hasTags" #footer>
      <BaseBadge v-for="tag in rule.tags" :key="tag" tone="neutral" subtone="secondary" class="tag-badge">{{ tag }}</BaseBadge>
    </template>
  </BaseListCard>
</template>

<script setup>
import { computed } from 'vue'
import BaseListCard from '../base/BaseListCard.vue'
import BaseBadge from '../base/BaseBadge.vue'
import AgentBadge from '../common/AgentBadge.vue'
import BaseIconButton from '../base/BaseIconButton.vue'
import BaseActionMenu from '../base/BaseActionMenu.vue'
import CopyButton from '../common/CopyButton.vue'
import { getRuleEffectiveStatus } from '../../utils/syncStatus'
import { syncStatusLabel, syncStatusTone } from '../../utils/resourceCardStatus.js'
import TrafficBar from '../traffic/TrafficBar.vue'
import { normalizeTrafficSummaryBucket } from '../../utils/trafficStats.js'
import { breakableUrl } from '../../utils/breakableUrl.js'

const props = defineProps({
  rule: { type: Object, required: true },
  agent: { type: Object, default: null },
  traffic: { type: Object, default: null },
  agentNodeTotal: { type: Number, default: 0 },
  /** Bulk selection mode: card click toggles selection instead of opening the editor. */
  selectable: { type: Boolean, default: false },
  selected: { type: Boolean, default: false },
})
const emit = defineEmits(['edit', 'delete', 'copy', 'toggle', 'diagnose', 'traffic-click', 'select'])

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

const listenTitle = computed(() => {
  const host = props.rule?.listen_host ?? ''
  const port = props.rule?.listen_port
  if (host === '' && (port === undefined || port === null || port === '')) return ''
  return `${host}:${port}`
})
const listenTitleParts = computed(() => breakableUrl(listenTitle.value))

const protoTone = computed(() => {
  const proto = String(props.rule?.protocol || '').toLowerCase()
  if (proto === 'udp') return 'primary'
  if (proto === 'tcp') return 'warning'
  return 'neutral'
})

const listenModeLabel = computed(() => {
  const mode = String(props.rule?.listen_mode || '').toLowerCase()
  if (mode === 'proxy') return '代理'
  return ''
})

const listenModeTone = computed(() => {
  const mode = String(props.rule?.listen_mode || '').toLowerCase()
  if (mode === 'proxy') return 'primary'
  return 'neutral'
})

const hasRelay = computed(() => {
  return Array.isArray(props.rule?.relay_layers) && props.rule.relay_layers.length > 0
})

const canDiagnose = computed(() => String(props.rule?.protocol || '').toLowerCase() === 'tcp')

const menuItems = computed(() => [
  { id: 'copy', label: '复制' },
  ...(canDiagnose.value ? [{ id: 'diagnose', label: '诊断' }] : []),
  { id: 'delete', label: '删除', tone: 'danger' },
])

function onMenuSelect(item) {
  if (item?.id === 'copy') emit('copy', props.rule)
  else if (item?.id === 'diagnose') emit('diagnose', props.rule)
  else if (item?.id === 'delete') emit('delete', props.rule)
}

const backends = computed(() => {
  if (Array.isArray(props.rule.backends) && props.rule.backends.length > 0) return props.rule.backends
  return []
})
const primaryBackend = computed(() => {
  const b = backends.value[0]
  return b ? `${b.host}:${b.port}` : '-'
})
const primaryBackendCopyValue = computed(() => {
  const b = backends.value[0]
  if (!b) return ''
  const host = String(b.host ?? '').trim()
  const port = b.port
  if (!host && (port === undefined || port === null || port === '')) return ''
  return `${host}:${port}`
})
const backendExtraCount = computed(() => Math.max(0, backends.value.length - 1))
const backendsTooltip = computed(() => backends.value.map((b, i) => {
  let s = `${i + 1}. ${b.host}:${b.port}`
  if (b.weight > 1) s += ` (权重${b.weight})`
  if (b.backup) s += ' [备用]'
  return s
}).join('\n'))
// agent prop is the page-selected node; when set, every card would repeat the same badge.
const showAgentBadge = computed(() => !props.agent)

const LB_MAP = { adaptive: 'ADP', round_robin: 'RR', random: 'RND' }
const LB_TITLES = { adaptive: '自适应 (Adaptive)', round_robin: '轮询 (Round Robin)', random: '随机 (Random)' }
const lbLabel = computed(() => LB_MAP[props.rule.load_balancing?.strategy] || 'ADP')
const lbTitle = computed(() => LB_TITLES[props.rule.load_balancing?.strategy] || '自适应 (Adaptive)')

const tuningTags = computed(() => {
  const t = props.rule.tuning
  if (!t) return []
  const tags = []
  const isUdp = props.rule.protocol === 'udp'
  const defaultIdle = isUdp ? '20s' : '10m'
  if (t.proxy?.idle_timeout && t.proxy.idle_timeout !== defaultIdle) tags.push(`超时:${t.proxy.idle_timeout}`)
  if (t.proxy?.connect_timeout && t.proxy.connect_timeout !== '10s') tags.push(`连接:${t.proxy.connect_timeout}`)
  if (t.limit_conn?.count && Number(t.limit_conn.count) > 0) tags.push(`限连:${t.limit_conn.count}`)
  const mf = t.upstream?.max_fails
  const ft = t.upstream?.fail_timeout
  if ((mf !== undefined && mf !== 3) || (ft && ft !== '30s')) tags.push(`健检:${mf ?? 3}/${ft || '30s'}`)
  if (t.listen?.reuseport === true && !isUdp) tags.push('reuseport')
  if (t.listen?.so_keepalive === true) tags.push('keepalive')
  if (t.proxy?.buffer_size && t.proxy.buffer_size !== '16k') tags.push(`buf:${t.proxy.buffer_size}`)
  if (t.proxy_protocol?.decode) tags.push('PP接收')
  if (t.proxy_protocol?.send) tags.push('PP发送')
  return tags
})

const hasTraffic = computed(() => props.traffic != null)
const normalizedTraffic = computed(() => normalizeTrafficSummaryBucket(props.traffic))
const hasTags = computed(() => Array.isArray(props.rule.tags) && props.rule.tags.length > 0)

</script>

<style scoped>
.l4-card--selected {
  border-color: var(--color-primary);
  box-shadow: 0 0 0 1px var(--color-primary);
}

.l4-card__select {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  cursor: pointer;
}

.l4-card__checkbox {
  width: 16px;
  height: 16px;
  margin: 0;
  cursor: pointer;
  accent-color: var(--color-primary);
}

.l4-card__title-row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-1-5);
  min-width: 0;
}

.l4-card__title {
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

.l4-card__title--disabled {
  color: var(--color-text-tertiary);
}

.l4-card__mapping {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
  min-width: 0;
}

.l4-card__endpoint {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  min-width: 0;
}

.l4-card__url-label {
  flex-shrink: 0;
  color: var(--color-text-muted);
  font-size: 0.6875rem;
  font-weight: 650;
  letter-spacing: 0.03em;
  line-height: 1.3;
}

.l4-card__addr {
  font-family: var(--font-mono);
  font-size: 0.75rem;
  font-weight: 500;
  color: var(--color-text-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  flex: 1;
  min-width: 0;
  line-height: 1.35;
}

.l4-card__more {
  flex-shrink: 0;
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  font-size: 0.6875rem;
  font-weight: 650;
  line-height: 1.3;
}

.l4-card__tuning {
  display: flex;
  gap: 0.25rem;
  flex-wrap: wrap;
}
</style>

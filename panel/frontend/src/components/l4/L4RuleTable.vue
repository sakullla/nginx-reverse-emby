<template>
  <div ref="rootRef" class="rule-table">
    <table class="rules-table" :class="{ 'rules-table--selectable': selectable, 'rules-table--with-agent': showAgentColumn }">
      <thead>
        <tr>
          <th v-if="selectable" class="rules-table__col-select">
            <input
              ref="selectAllRef"
              type="checkbox"
              class="rules-table__checkbox"
              aria-label="全选当前页"
              :checked="allSelected"
              :disabled="!rules.length"
              @change="$emit('select-all')"
            >
          </th>
          <th class="rules-table__col-toggle"></th>
          <th class="rules-table__col-status">状态</th>
          <th class="rules-table__col-listen">监听地址</th>
          <th class="rules-table__col-backend">后端地址</th>
          <th class="rules-table__col-lb">均衡</th>
          <th v-if="showAgentColumn" class="rules-table__col-agent">节点</th>
          <th class="rules-table__col-tags">标签</th>
          <th v-if="showTraffic" class="rules-table__col-traffic">流量</th>
          <th class="rules-table__col-actions">操作</th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="rule in rules"
          :key="ruleKey(rule)"
          class="rules-table__row"
          :class="{
            'rules-table__row--disabled': !rule.enabled,
            'rules-table__row--selectable': selectable,
            'rules-table__row--selected': selectable && isRowSelected(rule)
          }"
          :aria-selected="selectable ? (isRowSelected(rule) ? 'true' : 'false') : undefined"
          @click="onRowClick(rule)"
        >
          <td v-if="selectable" class="rules-table__cell-select" @click.stop>
            <input
              type="checkbox"
              class="rules-table__checkbox"
              :checked="isRowSelected(rule)"
              :aria-label="`选择规则 ${listenTitle(rule)}`"
              @change="$emit('select', rule)"
            >
          </td>
          <td @click.stop>
            <button
              type="button"
              class="toggle"
              :class="{ 'toggle--on': rule.enabled }"
              :aria-label="rule.enabled ? '停用规则' : '启用规则'"
              :title="rule.enabled ? '停用' : '启用'"
              :disabled="selectable"
              @click="$emit('toggle', rule)"
            >
              <span class="toggle__knob"></span>
            </button>
          </td>
          <td class="rules-table__dim">
            <div class="rules-table__status">
              <BaseBadge shape="square" mono :tone="getProtocolBadge(rule.protocol).tone">
                {{ getProtocolBadge(rule.protocol).label }}
              </BaseBadge>
              <BaseBadge :tone="getStatusBadge(getStatus(rule)).tone" dot>
                {{ getStatusBadge(getStatus(rule)).label }}
              </BaseBadge>
            </div>
          </td>
          <td class="rules-table__dim">
            <div class="rules-table__url-cell">
              <span class="rules-table__id">#{{ rule.id }}</span>
              <span class="rules-table__url" :title="listenTitle(rule)">{{ listenTitle(rule) }}</span>
            </div>
          </td>
          <td class="rules-table__dim">
            <span class="rules-table__url rules-table__url--backend" :title="backendTooltip(rule)">
              {{ formatBackend(rule) }}
            </span>
          </td>
          <td class="rules-table__dim">
            <BaseBadge shape="square" mono :tone="getLBLabel(rule.load_balancing?.strategy).tone">
              {{ getLBLabel(rule.load_balancing?.strategy).label }}
            </BaseBadge>
          </td>
          <td v-if="showAgentColumn" class="rules-table__dim">
            <AgentBadge :item="rule" :agent="agent" />
          </td>
          <td class="rules-table__dim">
            <div :ref="tagsRefFor(rule)" class="rules-table__tags" :title="tagsTitle(rule)">
              <span
                v-for="(tag, index) in ruleTags(rule)"
                :key="`${index}:${tag}`"
                class="tag"
                :class="{ 'tag--overflow': index >= visibleTagCount(rule) }"
                :aria-hidden="index >= visibleTagCount(rule) ? 'true' : undefined"
                data-tag
              >{{ tag }}</span>
              <span v-if="hiddenTagCount(rule) > 0" class="tag tag--more" data-testid="l4-tags-more">+{{ hiddenTagCount(rule) }}</span>
              <span v-if="!ruleTags(rule).length" class="rules-table__empty-cell">—</span>
            </div>
          </td>
          <td v-if="showTraffic" class="rules-table__dim" @click.stop>
            <TrafficBar
              v-if="trafficOf(rule)"
              compact
              :accounted="trafficOf(rule).accounted_bytes"
              :rx="trafficOf(rule).rx_bytes"
              :tx="trafficOf(rule).tx_bytes"
              :node-total="nodeTotalOf(rule)"
              @click="$emit('traffic-click', rule)"
            />
            <span v-else class="rules-table__empty-cell">—</span>
          </td>
          <td @click.stop>
            <div class="rules-table__actions">
              <button
                type="button"
                class="btn-icon"
                title="编辑"
                aria-label="编辑"
                :disabled="selectable"
                @click="$emit('edit', rule)"
              >
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                  <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
                  <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
                </svg>
              </button>
              <BaseActionMenu v-if="!selectable" :items="menuItemsFor(rule)" @select="onMenuSelect(rule, $event)" />
            </div>
          </td>
        </tr>
        <tr v-if="!rules.length" class="empty-state-row">
          <td :colspan="columnCount" class="empty-state">暂无数据</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<script>
/**
 * Number of whole tags that fit in `available` px. When not all fit, room for
 * a trailing "+N" badge (`moreWidth`) is reserved after the visible tags.
 */
export function fitTagCount(widths, available, gap, moreWidth) {
  const total = widths.length
  if (!total) return 0
  const EPSILON = 0.5
  let used = 0
  widths.forEach((width, index) => { used += (index ? gap : 0) + width })
  if (used <= available + EPSILON) return total
  used = 0
  let count = 0
  for (let index = 0; index < total; index += 1) {
    const next = used + (index ? gap : 0) + widths[index]
    if (next + gap + moreWidth > available + EPSILON) break
    used = next
    count += 1
  }
  return count
}
</script>

<script setup>
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch, watchEffect } from 'vue'
import { getRuleEffectiveStatus } from '../../utils/syncStatus.js'
import { getStatusBadge, getProtocolBadge, getLBLabel } from '../../utils/enumLabels.js'
import { normalizeTrafficSummaryBucket } from '../../utils/trafficStats.js'
import BaseBadge from '../base/BaseBadge.vue'
import BaseActionMenu from '../base/BaseActionMenu.vue'
import AgentBadge from '../common/AgentBadge.vue'
import TrafficBar from '../traffic/TrafficBar.vue'

// Reserve room for a "+N" badge that is not rendered yet (all tags fit).
const MORE_BADGE_FALLBACK_WIDTH = 28

const props = defineProps({
  rules: { type: Array, default: () => [] },
  agent: { type: Object, default: null },
  /** Bulk selection mode: checkbox column, row click toggles selection. */
  selectable: { type: Boolean, default: false },
  /** Selected keys in `${agent_id}:${id}` form. */
  selectedKeys: { type: Array, default: () => [] },
  allSelected: { type: Boolean, default: false },
  indeterminate: { type: Boolean, default: false },
  /** (rule) => traffic summary bucket | null */
  trafficFor: { type: Function, default: null },
  /** (rule) => node total bytes */
  nodeTotalFor: { type: Function, default: null },
  showTraffic: { type: Boolean, default: true }
})
const emit = defineEmits(['toggle', 'edit', 'delete', 'copy', 'diagnose', 'traffic-click', 'select', 'select-all'])

const selectAllRef = ref(null)
watchEffect(() => {
  if (selectAllRef.value) selectAllRef.value.indeterminate = props.indeterminate
})

// Page-selected agent means every row would repeat the same node badge.
const showAgentColumn = computed(() => !props.agent)
const selectedKeySet = computed(() => new Set(props.selectedKeys))
const columnCount = computed(() => 7 + (showAgentColumn.value ? 1 : 0) + (props.showTraffic ? 1 : 0) + (props.selectable ? 1 : 0))

function ruleKey(rule) {
  return `${rule?.agent_id || ''}:${rule?.id}`
}

function isRowSelected(rule) {
  return selectedKeySet.value.has(ruleKey(rule))
}

function onRowClick(rule) {
  if (props.selectable) emit('select', rule)
}

function getStatus(rule) {
  return getRuleEffectiveStatus(rule, props.agent)
}

function ruleTags(rule) {
  return Array.isArray(rule?.tags) ? rule.tags : []
}

// Tags never truncate mid-word: each row shows as many whole tags as its cell
// width allows and folds the rest into "+N". Folded tags stay in the DOM
// (out of flow, invisible) so their widths can be re-measured on resize.
const rootRef = ref(null)
const tagsEls = new Map()
const tagFit = ref({})

function tagsRefFor(rule) {
  const key = ruleKey(rule)
  return (el) => {
    if (el) tagsEls.set(key, el)
    else tagsEls.delete(key)
  }
}

function visibleTagCount(rule) {
  const fit = tagFit.value[ruleKey(rule)]
  return fit == null ? ruleTags(rule).length : Math.min(fit, ruleTags(rule).length)
}

function hiddenTagCount(rule) {
  return Math.max(0, ruleTags(rule).length - visibleTagCount(rule))
}

// "+N" width each row's fit reserved; a wider real badge (e.g. "+12") refits.
const reservedMoreWidth = new Map()

function moreBadgeOutgrown() {
  for (const [key, el] of tagsEls) {
    const more = el.querySelector('.tag--more')
    if (more && more.getBoundingClientRect().width > (reservedMoreWidth.get(key) ?? 0) + 0.5) return true
  }
  return false
}

function measureTags(pass = 0) {
  const next = {}
  for (const [key, el] of tagsEls) {
    // Hidden/unlaid-out tables report 0; keep the previous fit until a resize.
    if (!el.getBoundingClientRect().width) {
      if (key in tagFit.value) next[key] = tagFit.value[key]
      continue
    }
    const widths = [...el.querySelectorAll('[data-tag]')].map((tag) => tag.getBoundingClientRect().width)
    const gap = parseFloat(getComputedStyle(el).columnGap) || 0
    const more = el.querySelector('.tag--more')
    const moreWidth = Math.max(more ? more.getBoundingClientRect().width : 0, MORE_BADGE_FALLBACK_WIDTH)
    reservedMoreWidth.set(key, moreWidth)
    next[key] = fitTagCount(widths, el.getBoundingClientRect().width, gap, moreWidth)
  }
  tagFit.value = next
  // The badge text changes with the new fit; refit (bounded) if it outgrew its reserve.
  if (pass < 2) {
    nextTick(() => { if (moreBadgeOutgrown()) measureTags(pass + 1) })
  }
}

function measureAll() {
  measureTags(0)
}

let resizeObserver = null
onMounted(() => {
  nextTick(measureAll)
  if (typeof ResizeObserver === 'function' && rootRef.value) {
    resizeObserver = new ResizeObserver(measureAll)
    resizeObserver.observe(rootRef.value)
  }
  // Late web fonts change tag widths without resizing the table.
  document.fonts?.ready?.then(measureAll)
  document.fonts?.addEventListener?.('loadingdone', measureAll)
})
onBeforeUnmount(() => {
  resizeObserver?.disconnect()
  document.fonts?.removeEventListener?.('loadingdone', measureAll)
})
watch(
  () => [props.rules, props.selectable, props.showTraffic, showAgentColumn.value],
  () => nextTick(measureAll),
  { deep: true }
)

function tagsTitle(rule) {
  return ruleTags(rule).join(', ') || undefined
}

function trafficOf(rule) {
  const raw = props.trafficFor ? props.trafficFor(rule) : null
  return raw == null ? null : normalizeTrafficSummaryBucket(raw)
}

function nodeTotalOf(rule) {
  return props.nodeTotalFor ? Number(props.nodeTotalFor(rule)) || 0 : 0
}

function canDiagnose(rule) {
  return String(rule?.protocol || '').toLowerCase() === 'tcp'
}

function menuItemsFor(rule) {
  return [
    { id: 'copy', label: '复制' },
    ...(canDiagnose(rule) ? [{ id: 'diagnose', label: '诊断' }] : []),
    { id: 'delete', label: '删除', tone: 'danger' }
  ]
}

function onMenuSelect(rule, item) {
  if (item?.id === 'copy') emit('copy', rule)
  else if (item?.id === 'diagnose') emit('diagnose', rule)
  else if (item?.id === 'delete') emit('delete', rule)
}

function listenTitle(rule) {
  const host = rule?.listen_host ?? ''
  const port = rule?.listen_port
  if (host === '' && (port === undefined || port === null || port === '')) return '-'
  return `${host}:${port}`
}

function l4Backends(rule) {
  return Array.isArray(rule?.backends) ? rule.backends : []
}

function formatBackend(rule) {
  const backends = l4Backends(rule)
  if (backends.length === 0) return '-'
  const first = backends[0]
  const addr = `${first.host}:${first.port}`
  if (backends.length === 1) return addr
  return `${addr} +${backends.length - 1}`
}

function backendTooltip(rule) {
  return l4Backends(rule).map((b, i) => {
    let s = `${i + 1}. ${b.host}:${b.port}`
    if (b.weight > 1) s += ` (权重${b.weight})`
    if (b.backup) s += ' [备用]'
    return s
  }).join('\n')
}
</script>

<style scoped>
.rule-table {
  overflow-x: auto;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-2xl);
  background: var(--color-bg-surface);
  box-shadow: var(--shadow-xs);
}

.rules-table {
  width: 100%;
  border-collapse: collapse;
  /* Fixed layout gives the tags cell a definite width to fold tags against. */
  table-layout: fixed;
  /* Sum of the fixed columns below (border-box: width + 1.5rem padding) plus
     ~100px for the remainder tags column, so listen/backend keep their widths.
     All variants fit the ~1040px table at 1360 without horizontal scroll. */
  min-width: 880px;
}

.rules-table--selectable { min-width: 908px; }
.rules-table--with-agent { min-width: 1008px; }
.rules-table--with-agent.rules-table--selectable { min-width: 1036px; }

.rules-table th {
  text-align: left;
  padding: 0.7rem 0.75rem;
  font-size: 0.6875rem;
  font-weight: 650;
  letter-spacing: 0.04em;
  color: var(--color-text-muted);
  border-bottom: 1px solid var(--color-border-subtle);
  white-space: nowrap;
}

/* Fixed layout: widths are content-box; the tags column takes the remainder.
   Listen fits "0.0.0.0:65535" / "[::]:65535", backend "192.168.1.20:25565 +1";
   both are wider than in the pre-T9 auto layout (longer values ellipsize with
   a title). Measured at 1360 with node + select columns: tags keep ~100px. */
.rules-table__col-select { width: 1rem; }
.rules-table__col-toggle { width: 2.25rem; }
.rules-table__col-status { width: 5rem; }
.rules-table__col-listen { width: 7.5rem; }
.rules-table__col-backend { width: 11rem; }
.rules-table__col-lb { width: 3.5rem; }
.rules-table__col-agent { width: 6.25rem; }
.rules-table__col-traffic { width: 5rem; }
.rules-table__col-actions { width: 4.25rem; }

.rules-table th.rules-table__col-select,
.rules-table__cell-select {
  padding-right: 0;
}

.rules-table__checkbox {
  width: 16px;
  height: 16px;
  margin: 0;
  cursor: pointer;
  accent-color: var(--color-primary);
}

.rules-table__row--selectable { cursor: pointer; }

.rules-table__row--selected,
.rules-table__row--selected:hover {
  background: color-mix(in srgb, var(--color-primary) 8%, transparent);
}

.rules-table__row {
  border-bottom: 1px solid var(--color-border-subtle);
  transition: background-color var(--duration-fast) var(--ease-default);
}

.rules-table__row:last-child {
  border-bottom: none;
}

.rules-table__row:hover {
  background: var(--color-bg-hover);
}

/* Dim content cells only; the toggle and actions keep full contrast. */
.rules-table__row--disabled .rules-table__dim {
  opacity: 0.6;
}

.rules-table td {
  padding: 0.75rem;
  vertical-align: middle;
  overflow: hidden;
}

.rules-table__status {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 0.3rem;
}

.rules-table__url-cell {
  display: flex;
  flex-direction: column;
  gap: 0.15rem;
  min-width: 0;
}

.rules-table__id {
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  font-size: 0.6875rem;
  font-weight: 650;
  line-height: 1.2;
}

.rules-table__url {
  display: block;
  font-family: var(--font-mono);
  font-size: 0.8125rem;
  font-weight: 500;
  color: var(--color-text-primary);
  line-height: 1.35;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.rules-table__url--backend {
  color: var(--color-text-secondary);
}

.rules-table__tags {
  position: relative;
  display: flex;
  gap: 0.25rem;
  flex-wrap: nowrap;
  align-items: center;
  min-width: 0;
  overflow: hidden;
}

/* Whole tags only; whatever does not fit is folded into "+N" by measureTags(). */
.rules-table__tags .tag {
  flex-shrink: 0;
}

.rules-table__tags .tag--overflow {
  position: absolute;
  top: 0;
  left: 0;
  visibility: hidden;
  pointer-events: none;
}

.tag--more {
  color: var(--color-text-tertiary);
  font-family: var(--font-mono);
}

.rules-table__empty-cell {
  color: var(--color-text-muted);
  font-size: 0.8125rem;
}

.rules-table__actions {
  display: flex;
  align-items: center;
  gap: 0.2rem;
}

.rules-table__actions .btn-icon:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.rules-table__actions .btn-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 30px;
  height: 30px;
  border-radius: var(--radius-sm);
  border: none;
  background: transparent;
  color: var(--color-text-tertiary);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-default);
}

.rules-table__actions .btn-icon:hover {
  background: var(--color-bg-hover);
  color: var(--color-primary);
}

.rules-table__actions .btn-icon--danger:hover {
  background: var(--color-danger-50);
  color: var(--color-danger);
}

.toggle {
  width: 36px;
  height: 20px;
  border-radius: var(--radius-full);
  border: 1px solid var(--color-border-strong);
  background: var(--color-bg-active);
  cursor: pointer;
  position: relative;
  box-sizing: border-box;
  transition: background var(--duration-normal) var(--ease-default);
  padding: 0;
}

.toggle--on {
  background: var(--color-primary);
  border-color: var(--color-primary);
}

.toggle:disabled {
  cursor: not-allowed;
  opacity: 0.5;
}

.toggle__knob {
  position: absolute;
  top: 1px;
  left: 1px;
  width: 16px;
  height: 16px;
  border-radius: 50%;
  /* Fixed white knob: --color-text-inverse is dark in night themes and the
     off-state knob would vanish into the dark track. */
  background: #ffffff;
  transition: transform var(--duration-normal) var(--ease-default);
  box-shadow: 0 0 0 1px rgb(0 0 0 / 0.08), 0 1px 2px rgb(0 0 0 / 0.25);
}

.toggle--on .toggle__knob { transform: translateX(16px); }

.tag {
  display: inline-flex;
  align-items: center;
  height: 1.25rem;
  font-size: var(--text-2xs);
  padding: 0 var(--space-1-5);
  background: var(--color-bg-subtle);
  border: 1px solid var(--color-border-default);
  color: var(--color-text-secondary);
  border-radius: var(--radius-xs);
  font-weight: var(--font-medium);
  white-space: nowrap;
  box-sizing: border-box;
}

tbody tr.empty-state-row:hover { background: transparent; }

.empty-state {
  text-align: center;
  padding: 2rem 1rem;
  color: var(--color-text-tertiary);
  font-size: 0.875rem;
}
</style>

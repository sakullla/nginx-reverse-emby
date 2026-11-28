<template>
  <div ref="rootRef" class="rule-table">
    <table class="rules-table" :class="{ 'rules-table--selectable': selectable, 'rules-table--with-agent': showAgentColumn }">
      <thead>
        <tr>
          <th v-if="selectable" class="rules-table__col-select">
            <input
              type="checkbox"
              class="rules-table__checkbox"
              aria-label="全选本页"
              :checked="allSelected"
              :indeterminate.prop="indeterminate"
              :disabled="!rules.length"
              data-testid="rule-table-select-all"
              @change="$emit('select-all')"
            />
          </th>
          <th class="rules-table__col-toggle"></th>
          <th class="rules-table__col-status">状态</th>
          <th class="rules-table__col-frontend">前端地址</th>
          <th class="rules-table__col-backend">后端地址</th>
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
            'rules-table__row--selected': selectable && isSelected(rule)
          }"
          @click="onRowClick(rule)"
        >
          <td v-if="selectable" class="rules-table__cell-select" @click.stop>
            <input
              type="checkbox"
              class="rules-table__checkbox"
              :aria-label="`选择规则 #${rule.id}`"
              :checked="isSelected(rule)"
              @change="$emit('select', rule)"
            />
          </td>
          <td class="rules-table__cell-toggle" @click.stop>
            <button
              type="button"
              class="toggle"
              :class="{ 'toggle--on': rule.enabled }"
              role="switch"
              :aria-checked="rule.enabled ? 'true' : 'false'"
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
              <BaseBadge :tone="isHttps(rule) ? 'success' : 'primary'" shape="square" mono>
                {{ isHttps(rule) ? 'HTTPS' : 'HTTP' }}
              </BaseBadge>
              <BaseBadge :tone="getStatusBadge(getStatus(rule)).tone" dot>
                {{ getStatusBadge(getStatus(rule)).label }}
              </BaseBadge>
            </div>
          </td>
          <td class="rules-table__dim">
            <div class="rules-table__url-cell">
              <span class="rules-table__id">#{{ rule.id }}</span>
              <span class="rules-table__url" :title="rule.frontend_url">{{ rule.frontend_url }}</span>
              <CopyButton
                v-if="!selectable"
                class="rules-table__copy-btn"
                :value="rule.frontend_url"
                label="复制入口 URL"
                success-message="已复制入口 URL"
                size="xs"
                data-copy-target="frontend"
              />
            </div>
          </td>
          <td class="rules-table__dim">
            <div class="rules-table__backend-cell">
              <span class="rules-table__url rules-table__url--backend" :title="backendTooltip(rule)">
                {{ formatBackend(rule) }}
              </span>
              <CopyButton
                v-if="!selectable"
                class="rules-table__copy-btn"
                :value="primaryBackendCopyValue(rule)"
                label="复制后端地址"
                success-message="已复制后端地址"
                size="xs"
                data-copy-target="backend"
              />
            </div>
          </td>
          <td v-if="showAgentColumn" class="rules-table__dim rules-table__cell-agent">
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
              <span v-if="hiddenTagCount(rule) > 0" class="tag tag--more" data-testid="rule-table-tags-more">+{{ hiddenTagCount(rule) }}</span>
              <RouterLink v-if="serviceLink(rule)" :to="serviceLink(rule).to" class="rules-table__service-link" :title="`服务 ${serviceLink(rule).id}`" @click.stop>服务接入</RouterLink>
              <span v-if="!(rule.tags || []).length && !serviceLink(rule)" class="rules-table__empty-cell">—</span>
            </div>
          </td>
          <td v-if="showTraffic" class="rules-table__dim rules-table__cell-traffic">
            <TrafficBar
              v-if="trafficOf(rule)"
              compact
              :accounted="trafficOf(rule).accounted_bytes"
              :rx="trafficOf(rule).rx_bytes"
              :tx="trafficOf(rule).tx_bytes"
              :node-total="nodeTotalOf(rule)"
              @click="onTrafficClick(rule)"
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
                data-action="edit"
                @click="$emit('edit', rule)"
              >
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                  <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
                  <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
                </svg>
              </button>
              <BaseActionMenu v-if="!selectable" :items="menuItems" @select="onMenuSelect(rule, $event)" />
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
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { getRuleEffectiveStatus } from '../../utils/syncStatus.js'
import { getStatusBadge } from '../../utils/enumLabels.js'
import BaseBadge from '../base/BaseBadge.vue'
import BaseActionMenu from '../base/BaseActionMenu.vue'
import CopyButton from '../common/CopyButton.vue'
import TrafficBar from '../traffic/TrafficBar.vue'
import { normalizeTrafficSummaryBucket } from '../../utils/trafficStats.js'
import AgentBadge from '../common/AgentBadge.vue'
import { describeHTTPBackends } from '../../utils/httpBackend.js'
import { ruleServiceLink } from '../../utils/accessService.js'

const props = defineProps({
  rules: { type: Array, default: () => [] },
  agent: { type: Object, default: null },
  providerCatalog: { type: Array, default: () => [] },
  providerCatalogStatus: { type: String, default: 'ready' },
  /** Bulk selection mode: adds a checkbox column; row click toggles selection. */
  selectable: { type: Boolean, default: false },
  selectedKeys: { type: Array, default: () => [] },
  allSelected: { type: Boolean, default: false },
  indeterminate: { type: Boolean, default: false },
  showTraffic: { type: Boolean, default: true },
  trafficFor: { type: Function, default: null },
  nodeTotalFor: { type: Function, default: null }
})
const emit = defineEmits(['toggle', 'edit', 'copy', 'diagnose', 'delete', 'traffic-click', 'select', 'select-all'])

// Reserve room for a "+N" badge that is not rendered yet (all tags fit).
const MORE_BADGE_FALLBACK_WIDTH = 28

const menuItems = [
  { id: 'copy', label: '复制' },
  { id: 'diagnose', label: '诊断' },
  { id: 'delete', label: '删除', tone: 'danger' }
]

function onMenuSelect(rule, item) {
  if (item?.id === 'copy') emit('copy', rule)
  else if (item?.id === 'diagnose') emit('diagnose', rule)
  else if (item?.id === 'delete') emit('delete', rule)
}

function ruleKey(rule) {
  return `${rule?.agent_id ?? ''}:${rule?.id}`
}

const selectedKeySet = computed(() => new Set(props.selectedKeys.map(String)))

function isSelected(rule) {
  return selectedKeySet.value.has(ruleKey(rule))
}

function onRowClick(rule) {
  if (props.selectable) emit('select', rule)
}

function onTrafficClick(rule) {
  if (props.selectable) emit('select', rule)
  else emit('traffic-click', rule)
}

const columnCount = computed(() => (
  6 + (props.selectable ? 1 : 0) + (showAgentColumn.value ? 1 : 0) + (props.showTraffic ? 1 : 0)
))

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
    let available = el.getBoundingClientRect().width
    const link = el.querySelector('.rules-table__service-link')
    if (link) available -= link.getBoundingClientRect().width + gap
    const more = el.querySelector('.tag--more')
    const moreWidth = Math.max(more ? more.getBoundingClientRect().width : 0, MORE_BADGE_FALLBACK_WIDTH)
    reservedMoreWidth.set(key, moreWidth)
    next[key] = fitTagCount(widths, available, gap, moreWidth)
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

function tagsTitle(rule) {
  return ruleTags(rule).join(', ') || null
}

function trafficOf(rule) {
  if (typeof props.trafficFor !== 'function') return null
  const raw = props.trafficFor(rule)
  return raw == null ? null : normalizeTrafficSummaryBucket(raw)
}

function nodeTotalOf(rule) {
  return typeof props.nodeTotalFor === 'function' ? (Number(props.nodeTotalFor(rule)) || 0) : 0
}

function serviceLink(rule) {
  return ruleServiceLink(rule)
}

// Page-selected agent means every row would repeat the same node badge.
const showAgentColumn = computed(() => !props.agent)

function getStatus(rule) {
  return getRuleEffectiveStatus(rule, props.agent)
}

function isHttps(rule) {
  return String(rule?.frontend_url || '').startsWith('https')
}

function httpBackends(rule) {
  return describeHTTPBackends(rule, props.providerCatalog, props.providerCatalogStatus).map((backend) => (
    backend.kind === 'provider' ? `${backend.label} · ${backend.detail}` : backend.label
  ))
}

function formatBackend(rule) {
  const backends = httpBackends(rule)
  if (backends.length === 0) return '-'
  if (backends.length === 1) return backends[0]
  return `${backends[0]} +${backends.length - 1}`
}

function primaryBackendCopyValue(rule) {
  const first = describeHTTPBackends(rule, props.providerCatalog, props.providerCatalogStatus)[0]
  return first?.kind === 'url' ? first.label : ''
}

function backendTooltip(rule) {
  return describeHTTPBackends(rule, props.providerCatalog, props.providerCatalogStatus).map((backend) => {
    if (backend.kind !== 'provider') return backend.label
    const status = backend.state === 'active'
      ? '插件已就绪'
      : (backend.state === 'unknown' ? '插件状态待确认' : '插件状态不可用')
    return `${backend.label} · ${backend.detail}\n${status}`
  }).join('\n')
}

watch(
  () => [props.rules, props.selectable, props.showTraffic, showAgentColumn.value],
  () => nextTick(measureAll),
  { deep: true }
)
</script>

<style scoped>
.rule-table {
  overflow-x: auto;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  background: var(--color-bg-surface);
  box-shadow: var(--shadow-xs);
}

.rules-table {
  width: 100%;
  border-collapse: collapse;
  table-layout: fixed;
  min-width: 980px;
}

/* Extra fixed columns would otherwise squeeze the auto-width frontend column to nothing.
   The node-column variants are condensed (see below) so they still fit the
   ~1040px table at 1360 without horizontal scroll. */
.rules-table--selectable { min-width: 1020px; }
.rules-table--with-agent { min-width: 940px; }
.rules-table--with-agent.rules-table--selectable { min-width: 970px; }

.rules-table th {
  text-align: left;
  padding: 0 var(--space-3);
  height: 2.5rem;
  font-size: var(--text-xs);
  font-weight: var(--font-medium);
  color: var(--color-text-tertiary);
  background: var(--color-bg-subtle);
  border-bottom: 1px solid var(--color-border-default);
  white-space: nowrap;
}

/* Fixed layout: widths are content-box. At 1360 (~1040px table) the backend
   column fits a full "http://192.168.1.12:32400", the tags column 2-3 short
   tags (rest fold into +N) and the auto frontend column keeps ~230px. Status
   fits "HTTPS" + "同步失败" on one line so rows do not grow. */
.rules-table__col-select { width: 1.75rem; }
.rules-table__col-toggle { width: 2.5rem; }
.rules-table__col-status { width: 7.75rem; }
.rules-table__col-backend { width: 18%; }
.rules-table__col-agent { width: 8rem; }
.rules-table__col-tags { width: 14%; }
.rules-table__col-traffic { width: 5rem; }
.rules-table__col-actions { width: 4rem; }

/* 全部节点 mode adds the node column: tighter cell padding and narrower
   toggle/node/tags columns keep 流量 and 操作 in view at 1360 (table ~1040px)
   while the backend column still fits "http://192.168.1.12:32400" in full and
   the frontend column keeps ~200px, in both normal and batch mode. */
.rules-table.rules-table--with-agent th,
.rules-table.rules-table--with-agent td {
  padding-left: var(--space-2);
  padding-right: var(--space-2);
}
.rules-table--with-agent .rules-table__col-select { width: 1.25rem; }
.rules-table--with-agent .rules-table__col-toggle { width: 2.25rem; }
.rules-table--with-agent .rules-table__col-agent { width: 5.5rem; }
.rules-table--with-agent .rules-table__col-tags { width: 12.5%; }

/* Long node names ellipsize (full name stays in the badge title). */
.rules-table__cell-agent :deep(.agent-badge) {
  display: inline-block;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  vertical-align: middle;
}

.rules-table__row {
  border-bottom: 1px solid var(--color-border-subtle);
  transition: background-color var(--duration-fast) var(--ease-default);
}

.rules-table__row:last-child {
  border-bottom: none;
}

.rules-table__row:hover {
  background: var(--color-bg-subtle);
}

/* Only text cells fade; the switch, checkbox and actions keep full contrast. */
.rules-table__row--disabled .rules-table__dim {
  opacity: 0.6;
}

.rules-table--selectable .rules-table__row {
  cursor: pointer;
}

.rules-table__row--selected,
.rules-table__row--selected:hover {
  background: color-mix(in srgb, var(--color-primary) 8%, var(--color-bg-surface));
}

.rules-table__checkbox {
  width: 16px;
  height: 16px;
  margin: 0;
  vertical-align: middle;
  accent-color: var(--color-primary);
  cursor: pointer;
}

.rules-table th.rules-table__col-select,
.rules-table__cell-select {
  padding-right: 0;
}

.rules-table__cell-traffic :deep(.traffic-bar--compact) {
  min-width: 0;
}

.rules-table td {
  padding: var(--space-3);
  vertical-align: middle;
  overflow: hidden;
}

.rules-table__status {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 0.3rem;
}

.rules-table__url-cell {
  display: flex;
  align-items: center;
  gap: var(--space-1-5);
  min-width: 0;
}

.rules-table__backend-cell {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  min-width: 0;
}

.rules-table__copy-btn {
  opacity: 0.6;
}

.rules-table__row:hover .rules-table__copy-btn,
.rules-table__copy-btn:hover,
.rules-table__copy-btn:focus-visible {
  opacity: 1;
}

@media (pointer: coarse) {
  .rules-table__copy-btn {
    opacity: 1;
  }
}

.rules-table__id {
  flex-shrink: 0;
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  font-size: var(--text-2xs);
  font-variant-numeric: tabular-nums;
}

.rules-table__url {
  display: block;
  flex: 0 1 auto;
  min-width: 0;
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  color: var(--color-text-primary);
  line-height: 1.4;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.rules-table__url--backend {
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  font-weight: var(--font-normal);
  color: var(--color-text-secondary);
}

.rules-table__tags {
  position: relative;
  display: flex;
  align-items: center;
  gap: 0.25rem;
  flex-wrap: nowrap;
  min-width: 0;
  overflow: hidden;
}

/* Whole tags only; whatever does not fit is folded into "+N" by measureTags(). */
.rules-table__tags .tag {
  flex-shrink: 0;
  white-space: nowrap;
}

.rules-table__tags .tag--overflow {
  position: absolute;
  top: 0;
  left: 0;
  visibility: hidden;
  pointer-events: none;
}

.rules-table__tags .tag--more {
  color: var(--color-text-tertiary);
  font-variant-numeric: tabular-nums;
}

.rules-table__tags .rules-table__service-link {
  flex-shrink: 0;
  white-space: nowrap;
}

.rules-table__empty-cell {
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.rules-table__service-link {
  color: var(--color-primary);
  font-size: var(--text-xs);
  font-weight: var(--font-medium);
  text-decoration: none;
}

.rules-table__actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 0.2rem;
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
  transition: background var(--duration-fast) var(--ease-default), color var(--duration-fast) var(--ease-default);
}

.rules-table__actions .btn-icon:hover:not(:disabled) {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}

.rules-table__actions .btn-icon:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.rules-table__actions .btn-icon--danger:hover:not(:disabled) {
  background: var(--color-danger-50);
  color: var(--color-danger);
}

.toggle {
  width: 34px;
  height: 20px;
  border-radius: var(--radius-full);
  border: none;
  background: var(--color-bg-active);
  cursor: pointer;
  position: relative;
  flex-shrink: 0;
  transition: background var(--duration-normal) var(--ease-default);
  padding: 0;
  /* Off state needs a visible track edge in both themes. */
  box-shadow: inset 0 0 0 1px var(--color-border-strong);
}

.toggle:disabled {
  cursor: not-allowed;
  opacity: 0.5;
}

.toggle:focus-visible {
  outline: none;
  box-shadow: inset 0 0 0 1px var(--color-border-strong), var(--shadow-focus);
}

.toggle--on {
  background: var(--color-primary);
  box-shadow: none;
}

.toggle__knob {
  position: absolute;
  top: 2px;
  left: 2px;
  width: 16px;
  height: 16px;
  border-radius: 50%;
  background: #ffffff;
  transition: transform var(--duration-normal) var(--ease-spring);
  box-shadow: 0 0 0 1px rgb(0 0 0 / 0.08), 0 1px 2px rgb(0 0 0 / 0.25);
}

.toggle--on .toggle__knob { transform: translateX(14px); }

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
  box-sizing: border-box;
}

tbody tr.empty-state-row:hover { background: transparent; }

.empty-state {
  text-align: center;
  padding: 2rem 1rem;
  color: var(--color-text-tertiary);
  font-size: var(--text-sm);
}
</style>

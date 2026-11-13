<template>
  <div class="rule-table">
    <table class="rules-table">
      <thead>
        <tr>
          <th class="rules-table__col-toggle"></th>
          <th class="rules-table__col-status">状态</th>
          <th class="rules-table__col-frontend">前端地址</th>
          <th class="rules-table__col-backend">后端地址</th>
          <th v-if="showAgentColumn" class="rules-table__col-agent">节点</th>
          <th class="rules-table__col-tags">标签</th>
          <th class="rules-table__col-actions">操作</th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="rule in rules"
          :key="`${rule.agent_id || ''}:${rule.id}`"
          class="rules-table__row"
          :class="{ 'rules-table__row--disabled': !rule.enabled }"
        >
          <td>
            <button
              type="button"
              class="toggle"
              :class="{ 'toggle--on': rule.enabled }"
              :aria-label="rule.enabled ? '停用规则' : '启用规则'"
              :title="rule.enabled ? '停用' : '启用'"
              @click="$emit('toggle', rule)"
            >
              <span class="toggle__knob"></span>
            </button>
          </td>
          <td>
            <div class="rules-table__status">
              <BaseBadge :tone="isHttps(rule) ? 'success' : 'primary'" shape="square" mono>
                {{ isHttps(rule) ? 'HTTPS' : 'HTTP' }}
              </BaseBadge>
              <BaseBadge :tone="getStatusBadge(getStatus(rule)).tone" dot>
                {{ getStatusBadge(getStatus(rule)).label }}
              </BaseBadge>
            </div>
          </td>
          <td>
            <div class="rules-table__url-cell">
              <span class="rules-table__id">#{{ rule.id }}</span>
              <span class="rules-table__url" :title="rule.frontend_url">{{ rule.frontend_url }}</span>
            </div>
          </td>
          <td>
            <span class="rules-table__url rules-table__url--backend" :title="backendTooltip(rule)">
              {{ formatBackend(rule) }}
            </span>
          </td>
          <td v-if="showAgentColumn">
            <AgentBadge :item="rule" :agent="agent" />
          </td>
          <td>
            <div class="rules-table__tags">
              <span v-for="tag in (rule.tags || [])" :key="tag" class="tag">{{ tag }}</span>
              <RouterLink v-if="serviceLink(rule)" :to="serviceLink(rule).to" class="rules-table__service-link" :title="`服务 ${serviceLink(rule).id}`">服务接入</RouterLink>
              <span v-if="!(rule.tags || []).length && !serviceLink(rule)" class="rules-table__empty-cell">—</span>
            </div>
          </td>
          <td>
            <div class="rules-table__actions">
              <button type="button" class="btn-icon" title="编辑" @click="$emit('edit', rule)">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
                  <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
                </svg>
              </button>
              <button type="button" class="btn-icon btn-icon--danger" title="删除" @click="$emit('delete', rule)">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <polyline points="3 6 5 6 21 6"/>
                  <path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/>
                </svg>
              </button>
            </div>
          </td>
        </tr>
        <tr v-if="!rules.length" class="empty-state-row">
          <td :colspan="showAgentColumn ? 7 : 6" class="empty-state">暂无数据</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { getRuleEffectiveStatus } from '../../utils/syncStatus.js'
import { getStatusBadge } from '../../utils/enumLabels.js'
import BaseBadge from '../base/BaseBadge.vue'
import AgentBadge from '../common/AgentBadge.vue'
import { describeHTTPBackends } from '../../utils/httpBackend.js'
import { ruleServiceLink } from '../../utils/accessService.js'

const props = defineProps({
  rules: { type: Array, default: () => [] },
  agent: { type: Object, default: null },
  providerCatalog: { type: Array, default: () => [] },
  providerCatalogStatus: { type: String, default: 'ready' }
})
defineEmits(['toggle', 'edit', 'delete'])

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

function backendTooltip(rule) {
  return describeHTTPBackends(rule, props.providerCatalog, props.providerCatalogStatus).map((backend) => {
    if (backend.kind !== 'provider') return backend.label
    const status = backend.state === 'active'
      ? '插件已就绪'
      : (backend.state === 'unknown' ? '插件状态待确认' : '插件状态不可用')
    return `${backend.label} · ${backend.detail}\n${status}`
  }).join('\n')
}
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
  min-width: 760px;
}

.rules-table th {
  text-align: left;
  padding: 0 var(--space-4);
  height: 2.5rem;
  font-size: var(--text-xs);
  font-weight: var(--font-medium);
  color: var(--color-text-tertiary);
  background: var(--color-bg-subtle);
  border-bottom: 1px solid var(--color-border-default);
  white-space: nowrap;
}

.rules-table__col-toggle { width: 4rem; }
.rules-table__col-status { width: 10.5rem; }
.rules-table__col-backend { width: 24%; }
.rules-table__col-agent { width: 9rem; }
.rules-table__col-tags { width: 18%; }
.rules-table__col-actions { width: 6rem; }

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

.rules-table__row--disabled td:not(:first-child) {
  opacity: 0.55;
}

.rules-table td {
  padding: var(--space-3) var(--space-4);
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
  align-items: baseline;
  gap: var(--space-2);
  min-width: 0;
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
  display: flex;
  gap: 0.25rem;
  flex-wrap: wrap;
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

.rules-table__actions .btn-icon:hover {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}

.rules-table__actions .btn-icon--danger:hover {
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
  transition: background var(--duration-normal) var(--ease-default);
  padding: 0;
  box-shadow: inset 0 0 0 1px var(--color-border-default);
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
  box-shadow: 0 1px 2px rgb(0 0 0 / 0.2);
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

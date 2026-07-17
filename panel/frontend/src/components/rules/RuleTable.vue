<template>
  <div class="rule-table">
    <table class="rules-table">
      <thead>
        <tr>
          <th class="rules-table__col-toggle"></th>
          <th class="rules-table__col-status">状态</th>
          <th>前端地址</th>
          <th>后端地址</th>
          <th v-if="showAgentColumn">节点</th>
          <th>标签</th>
          <th class="rules-table__col-actions">操作</th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="rule in rules"
          :key="rule.id"
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
              <span v-if="!(rule.tags || []).length" class="rules-table__empty-cell">—</span>
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
import { getRuleEffectiveStatus } from '../../utils/syncStatus.js'
import { getStatusBadge } from '../../utils/enumLabels.js'
import BaseBadge from '../base/BaseBadge.vue'
import AgentBadge from '../common/AgentBadge.vue'

const props = defineProps({
  rules: { type: Array, default: () => [] },
  agent: { type: Object, default: null }
})
defineEmits(['toggle', 'edit', 'delete'])

// Page-selected agent means every row would repeat the same node badge.
const showAgentColumn = computed(() => !props.agent)

function getStatus(rule) {
  return getRuleEffectiveStatus(rule, props.agent)
}

function isHttps(rule) {
  return String(rule?.frontend_url || '').startsWith('https')
}

function httpBackends(rule) {
  if (Array.isArray(rule?.backends) && rule.backends.length > 0) {
    return rule.backends
      .map((backend) => String(backend?.url || '').trim())
      .filter(Boolean)
  }
  return []
}

function formatBackend(rule) {
  const backends = httpBackends(rule)
  if (backends.length === 0) return '-'
  if (backends.length === 1) return backends[0]
  return `${backends[0]} +${backends.length - 1}`
}

function backendTooltip(rule) {
  return httpBackends(rule).join('\n')
}
</script>

<style scoped>
.rule-table {
  overflow-x: auto;
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  background: var(--color-bg-surface);
}

.rules-table {
  width: 100%;
  border-collapse: collapse;
  min-width: 720px;
}

.rules-table th {
  text-align: left;
  padding: 0.55rem 0.8rem;
  font-size: 0.6875rem;
  font-weight: 650;
  letter-spacing: 0.02em;
  color: var(--color-text-tertiary);
  border-bottom: 1px solid var(--color-border-subtle);
  background: color-mix(in srgb, var(--color-bg-subtle) 70%, transparent);
  white-space: nowrap;
}

.rules-table__col-toggle { width: 3rem; }
.rules-table__col-status { width: 8rem; }
.rules-table__col-actions { width: 5.25rem; }

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

.rules-table__row--disabled {
  opacity: 0.62;
}

.rules-table td {
  padding: 0.6rem 0.8rem;
  vertical-align: middle;
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
  max-width: 28rem;
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
  max-width: 18rem;
  color: var(--color-text-secondary);
}

.rules-table__tags {
  display: flex;
  gap: 0.25rem;
  flex-wrap: wrap;
  max-width: 12rem;
}

.rules-table__empty-cell {
  color: var(--color-text-muted);
  font-size: 0.8125rem;
}

.rules-table__actions {
  display: flex;
  gap: 0.2rem;
}

.rules-table__actions .btn-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: var(--radius-md);
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
  border-radius: 999px;
  border: none;
  background: var(--color-bg-subtle);
  cursor: pointer;
  position: relative;
  transition: background var(--duration-normal) var(--ease-default);
  padding: 0;
}

.toggle--on { background: var(--color-primary); }

.toggle__knob {
  position: absolute;
  top: 2px;
  left: 2px;
  width: 16px;
  height: 16px;
  border-radius: 50%;
  background: var(--color-text-inverse);
  transition: transform var(--duration-normal) var(--ease-default);
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.12);
}

.toggle--on .toggle__knob { transform: translateX(16px); }

.tag {
  font-size: 0.6875rem;
  padding: 2px 7px;
  background: var(--color-primary-subtle);
  color: var(--color-primary);
  border-radius: var(--radius-full);
  font-weight: 600;
  font-family: var(--font-mono);
  line-height: 1.3;
}

tbody tr.empty-state-row:hover { background: transparent; }

.empty-state {
  text-align: center;
  padding: 2rem 1rem;
  color: var(--color-text-tertiary);
  font-size: 0.875rem;
}
</style>

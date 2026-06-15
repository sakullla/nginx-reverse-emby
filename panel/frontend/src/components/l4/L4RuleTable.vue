<template>
  <div class="rule-table">
    <table class="rules-table">
      <thead>
        <tr>
          <th>状态</th>
          <th>协议</th>
          <th>监听地址</th>
          <th>后端地址</th>
          <th>负载均衡</th>
          <th>标签</th>
          <th style="width: 80px">操作</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="rule in rules" :key="rule.id" class="rules-table__row">
          <td>
            <span class="status-badge" :class="`status-badge--${getStatus(rule)}`">
              {{ getLabel(getStatus(rule)) }}
            </span>
          </td>
          <td>
            <span class="proto-badge" :class="`proto-badge--${(rule.protocol || '').toLowerCase()}`">
              {{ (rule.protocol || 'tcp').toUpperCase() }}
            </span>
          </td>
          <td class="rules-table__mono">{{ rule.listen_host }}:{{ rule.listen_port }}</td>
          <td class="rules-table__mono">{{ formatBackend(rule) }}</td>
          <td>{{ getLBLabel(rule) }}</td>
          <td>
            <div class="rules-table__tags">
              <span v-for="tag in (rule.tags || [])" :key="tag" class="tag">{{ tag }}</span>
            </div>
          </td>
          <td>
            <div class="rules-table__actions">
              <button class="btn-icon" title="编辑" @click="$emit('edit', rule)">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
                  <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
                </svg>
              </button>
              <button class="btn-icon btn-icon--danger" title="删除" @click="$emit('delete', rule)">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <polyline points="3 6 5 6 21 6"/>
                  <path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/>
                </svg>
              </button>
            </div>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<script setup>
import { getRuleEffectiveStatus } from '../../utils/syncStatus.js'

const STATUS_LABEL = {
  active: '生效中',
  pending: '待同步',
  failed: '同步失败',
  disabled: '已禁用',
}
const LB_MAP = { adaptive: 'ADP', round_robin: 'RR', random: 'RND' }

function getStatus(rule) {
  return getRuleEffectiveStatus(rule, props.agent)
}

function getLabel(status) {
  return STATUS_LABEL[status] || '未知'
}

function getLBLabel(rule) {
  return LB_MAP[rule.load_balancing?.strategy] || 'ADP'
}

function formatBackend(rule) {
  const backends = Array.isArray(rule.backends) ? rule.backends : []
  if (backends.length === 0) return '-'
  const first = backends[0]
  const addr = `${first.host}:${first.port}`
  if (backends.length === 1) return addr
  return `${addr} +${backends.length - 1}`
}

const props = defineProps({
  rules: { type: Array, default: () => [] },
  agent: { type: Object, default: null }
})
defineEmits(['toggle', 'edit', 'delete'])
</script>

<style scoped>
.rule-table { overflow-x: auto; }
.rules-table { width: 100%; border-collapse: collapse; }
.rules-table th { text-align: left; padding: 0.75rem 1rem; font-size: 0.75rem; font-weight: 600; color: var(--color-text-tertiary); border-bottom: 1px solid var(--color-border-default); }
.rules-table__row { border-bottom: 1px solid var(--color-border-subtle); }
.rules-table__row:hover { background: var(--color-bg-hover); }
.rules-table td { padding: 0.875rem 1rem; vertical-align: middle; }
.rules-table__mono { font-family: var(--font-mono); font-size: 0.8125rem; color: var(--color-text-primary); }
.rules-table__tags { display: flex; gap: 0.25rem; flex-wrap: wrap; }
.rules-table__actions { display: flex; gap: 0.25rem; }
.rules-table__actions .btn-icon { display: flex; align-items: center; justify-content: center; width: 28px; height: 28px; border-radius: var(--radius-md); border: none; background: transparent; color: var(--color-text-tertiary); cursor: pointer; transition: all 0.15s; }
.rules-table__actions .btn-icon:hover { background: var(--color-bg-hover); color: var(--color-primary); }
.rules-table__actions .btn-icon--danger:hover { background: var(--color-danger-50); color: var(--color-danger); }
.tag { font-size: 0.75rem; padding: 2px 8px; background: var(--color-primary-subtle); color: var(--color-primary); border-radius: var(--radius-full); font-weight: 500; }
.status-badge { font-size: 0.75rem; padding: 2px 8px; border-radius: var(--radius-full); font-weight: 500; white-space: nowrap; }
.status-badge--active { background: rgba(var(--color-success-rgb, 34, 197, 94), 0.1); color: var(--color-success); }
.status-badge--pending { background: rgba(var(--color-warning-rgb, 245, 158, 11), 0.1); color: var(--color-warning); }
.status-badge--failed { background: rgba(var(--color-danger-rgb, 239, 68, 68), 0.1); color: var(--color-danger); }
.status-badge--disabled { background: var(--color-bg-subtle); color: var(--color-text-tertiary); }
.proto-badge { font-size: 0.75rem; padding: 2px 8px; border-radius: var(--radius-sm); font-weight: 600; font-family: var(--font-mono); }
.proto-badge--tcp { background: rgba(var(--color-primary-rgb, 99, 102, 241), 0.1); color: var(--color-primary); }
.proto-badge--udp { background: rgba(var(--color-warning-rgb, 245, 158, 11), 0.1); color: var(--color-warning); }
</style>

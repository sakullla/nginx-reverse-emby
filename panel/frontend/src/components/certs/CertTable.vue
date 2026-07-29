<template>
  <div class="rule-table">
    <table class="rules-table">
      <thead>
        <tr>
          <th class="rules-table__col-status">状态</th>
          <th>域名</th>
          <th>用途</th>
          <th>类型</th>
          <th>签发时间</th>
          <th>到期时间</th>
          <th v-if="showAgentColumn">节点</th>
          <th>标签</th>
          <th class="rules-table__col-actions">操作</th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="cert in certificates"
          :key="cert.id"
          class="rules-table__row"
          :class="{ 'rules-table__row--disabled': !cert.enabled }"
        >
          <td>
            <div class="rules-table__status-cell">
              <BaseBadge :tone="certStatusBadge(cert).tone" dot>
                {{ certStatusBadge(cert).label }}
              </BaseBadge>
              <div v-if="cert.last_error || nextRetryLabel(cert)" class="rules-table__status-detail">
                <span v-if="cert.last_error" class="rules-table__error">{{ cert.last_error }}</span>
                <span v-if="nextRetryLabel(cert)" class="rules-table__retry">{{ nextRetryLabel(cert) }}</span>
              </div>
            </div>
          </td>
          <td>
            <div class="rules-table__url-cell">
              <span class="rules-table__id">#{{ cert.id }}</span>
              <span class="rules-table__url" :title="cert.domain">{{ cert.domain }}</span>
            </div>
          </td>
          <td>
            <BaseBadge shape="square" mono tone="primary">{{ getCertificateUsageLabel(cert.usage) }}</BaseBadge>
          </td>
          <td>
            <BaseBadge shape="square" mono tone="neutral">{{ getCertificateSourceLabel(cert.certificate_type) }}</BaseBadge>
          </td>
          <td class="rules-table__mono">{{ formatDate(cert.last_issue_at) }}</td>
          <td>
            <div v-if="expiryInfoFor(cert)" class="rules-table__expiry-cell">
              <span class="rules-table__mono">{{ expiryInfoFor(cert).dateLabel }}</span>
              <BaseBadge :tone="expiryInfoFor(cert).tone" size="sm">
                {{ expiryInfoFor(cert).remainingLabel }}
              </BaseBadge>
            </div>
            <span v-else class="rules-table__empty-cell">—</span>
          </td>
          <td v-if="showAgentColumn">
            <AgentBadge :item="cert" :agent="agent" />
          </td>
          <td>
            <div class="rules-table__tags">
              <span v-for="tag in (cert.tags || [])" :key="tag" class="tag">{{ tag }}</span>
              <span v-if="!(cert.tags || []).length" class="rules-table__empty-cell">—</span>
            </div>
          </td>
          <td>
            <div class="rules-table__actions">
              <button type="button" class="btn-icon" title="编辑" @click="$emit('edit', cert)">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
                  <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
                </svg>
              </button>
              <button type="button" class="btn-icon btn-icon--danger" title="删除" @click="$emit('delete', cert)">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <polyline points="3 6 5 6 21 6"/>
                  <path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/>
                </svg>
              </button>
            </div>
          </td>
        </tr>
        <tr v-if="!certificates.length" class="empty-state-row">
          <td :colspan="showAgentColumn ? 9 : 8" class="empty-state">暂无数据</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import {
  getCertificateSourceLabel,
  getCertificateUsageLabel,
} from '../../utils/certificateTemplates'
import { certExpiryInfo } from '../../utils/certExpiry.js'
import BaseBadge from '../base/BaseBadge.vue'
import AgentBadge from '../common/AgentBadge.vue'

const CERT_STATUS = {
  active: { label: '生效中', tone: 'success' },
  pending: { label: '待签发', tone: 'warning' },
  issuing: { label: '签发中', tone: 'primary' },
  error: { label: '签发失败', tone: 'danger' },
}

const props = defineProps({
  certificates: { type: Array, default: () => [] },
  agent: { type: Object, default: null },
})
defineEmits(['edit', 'delete'])

// Page-selected agent means every row would repeat the same node badge.
const showAgentColumn = computed(() => !props.agent)

function certStatusBadge(cert) {
  if (!cert.enabled) return { label: '已禁用', tone: 'neutral' }
  return CERT_STATUS[cert.status] || { label: '未知', tone: 'neutral' }
}

function formatDate(val) {
  if (!val) return '—'
  try { return new Date(val).toLocaleDateString('zh-CN') } catch { return val }
}

function expiryInfoFor(cert) {
  return certExpiryInfo(cert?.not_after)
}

function nextRetryLabel(cert) {
  const ts = Number(cert?.next_retry_at_unix)
  if (!ts || ts <= 0) return ''
  let formatted
  try {
    formatted = new Date(ts * 1000).toLocaleString('zh-CN', {
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
    })
  } catch {
    return ''
  }
  const retryCount = Number(cert?.retry_count) || 0
  const countPart = retryCount > 0 ? `（第 ${retryCount} 次）` : ''
  return `下次重试 ${formatted}${countPart}`
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
  min-width: 720px;
}

.rules-table th {
  text-align: left;
  padding: 0.7rem 1rem;
  font-size: 0.6875rem;
  font-weight: 650;
  letter-spacing: 0.04em;
  color: var(--color-text-muted);
  border-bottom: 1px solid var(--color-border-subtle);
  white-space: nowrap;
}

.rules-table__col-status { width: 7.5rem; }
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
  padding: 0.75rem 1rem;
  vertical-align: middle;
}

.rules-table__url-cell {
  display: flex;
  flex-direction: column;
  gap: 0.15rem;
  min-width: 0;
  max-width: 22rem;
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

.rules-table__mono {
  font-family: var(--font-mono);
  font-size: 0.8125rem;
  color: var(--color-text-secondary);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
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

.rules-table__status-cell {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
}

.rules-table__expiry-cell {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  white-space: nowrap;
}

.rules-table__status-detail {
  display: flex;
  flex-direction: column;
  gap: 0.125rem;
  max-width: 220px;
}

.rules-table__error {
  font-size: 0.6875rem;
  color: var(--color-danger);
  line-height: 1.4;
  word-break: break-all;
}

.rules-table__retry {
  font-size: 0.6875rem;
  color: var(--color-text-tertiary);
  line-height: 1.4;
}

.rules-table__actions {
  display: flex;
  gap: 0.2rem;
}

.rules-table__actions .btn-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 30px;
  height: 30px;
  border-radius: var(--radius-full);
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

<template>
  <div class="import-report">
    <div class="result-summary">
      <div class="result-card">
        <span class="result-card__label">已导入</span>
        <span class="result-card__value result-card__value--ok">{{ summaryTotal(importResult.summary?.imported) }}</span>
      </div>
      <div class="result-card">
        <span class="result-card__label">冲突跳过</span>
        <span class="result-card__value">{{ summaryTotal(importResult.summary?.skipped_conflict) }}</span>
      </div>
      <div class="result-card">
        <span class="result-card__label">无效跳过</span>
        <span class="result-card__value">{{ summaryTotal(importResult.summary?.skipped_invalid) }}</span>
      </div>
      <div class="result-card">
        <span class="result-card__label">缺少证书跳过</span>
        <span class="result-card__value">{{ summaryTotal(importResult.summary?.skipped_missing_material) }}</span>
      </div>
    </div>
    <div class="report-meta">
      <div class="info-row">
        <span class="info-label">来源架构</span>
        <span class="info-value">{{ importResult.manifest?.source_architecture || '—' }}</span>
      </div>
      <div class="info-row">
        <span class="info-label">导出时间</span>
        <span class="info-value">{{ formatTimestamp(importResult.manifest?.exported_at) }}</span>
      </div>
    </div>
    <details class="report-details">
      <summary class="report-details__summary">查看导入报告</summary>
      <div class="report-group">
        <div v-for="section in reportSections" :key="section.key" class="report-block">
          <div class="report-block__header">
            <span>{{ section.label }}</span>
            <span class="report-block__count">{{ section.items.length }}</span>
          </div>
          <ul v-if="section.items.length" class="report-list">
            <li
              v-for="item in section.items"
              :key="`${section.key}-${item.kind}-${item.key}`"
              class="report-list__item"
            >
              <span class="report-list__kind">{{ item.kind }}</span>
              <span class="report-list__key">{{ item.key }}</span>
              <span v-if="item.reason" class="report-list__reason">{{ item.reason }}</span>
            </li>
          </ul>
          <div v-else class="report-empty">无</div>
        </div>
      </div>
    </details>
  </div>
</template>

<script setup>
import { computed } from 'vue'

const props = defineProps({
  importResult: { type: Object, required: true }
})

const reportSections = computed(() => {
  const report = props.importResult?.report || {}
  return [
    { key: 'imported', label: '已导入', items: report.imported || [] },
    { key: 'skipped_conflict', label: '冲突跳过', items: report.skipped_conflict || [] },
    { key: 'skipped_invalid', label: '无效跳过', items: report.skipped_invalid || [] },
    { key: 'skipped_missing_material', label: '缺少证书材料跳过', items: report.skipped_missing_material || [] }
  ]
})

function summaryTotal(group = {}) {
  return Object.values(group || {}).reduce((sum, v) => sum + Number(v || 0), 0)
}

function formatTimestamp(value) {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}
</script>

<style scoped>
.import-report {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.result-summary {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: var(--space-3);
}

.result-card {
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  background: var(--color-bg-subtle);
  padding: var(--space-4);
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-1-5);
}

.result-card__label {
  font-size: var(--text-sm);
  color: var(--color-text-secondary);
}

.result-card__value {
  font-size: var(--text-2xl);
  font-weight: var(--font-bold);
  color: var(--color-text-primary);
}

.result-card__value--ok {
  color: var(--color-success);
}

.report-meta {
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-xl);
  padding: 0 var(--space-4);
  background: var(--color-bg-surface);
}

.info-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-2) 0;
  border-bottom: 1px solid var(--color-border-subtle);
}

.info-row:last-child {
  border-bottom: none;
}

.info-label {
  font-size: var(--text-sm);
  color: var(--color-text-secondary);
}

.info-value {
  font-size: var(--text-sm);
  color: var(--color-text-primary);
  font-weight: var(--font-medium);
}

.report-details {
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-lg);
  overflow: hidden;
  background: var(--color-bg-surface);
}

.report-details__summary {
  padding: var(--space-4);
  font-size: var(--text-sm);
  font-weight: var(--font-semibold);
  cursor: pointer;
  background: var(--color-bg-subtle);
  color: var(--color-text-primary);
  list-style: none;
}

.report-details__summary::-webkit-details-marker {
  display: none;
}

.report-details__summary::before {
  content: '▸';
  margin-right: var(--space-2);
  display: inline-block;
  transition: transform var(--duration-fast);
}

.report-details[open] .report-details__summary::before {
  transform: rotate(90deg);
}

.report-group {
  padding: 0 var(--space-4) var(--space-4);
}

.report-block + .report-block {
  border-top: 1px solid var(--color-border-subtle);
}

.report-block__header {
  display: flex;
  justify-content: space-between;
  gap: var(--space-4);
  padding: var(--space-3) var(--space-4);
  font-size: var(--text-sm);
  font-weight: var(--font-semibold);
}

.report-block__count {
  color: var(--color-text-secondary);
}

.report-list {
  list-style: none;
  margin: 0;
  padding: 0 var(--space-4) var(--space-4);
  display: flex;
  flex-direction: column;
  gap: var(--space-2-5);
}

.report-list__item {
  display: grid;
  grid-template-columns: 120px 1fr;
  gap: var(--space-2) var(--space-3);
  align-items: start;
}

.report-list__kind {
  font-size: var(--text-xs);
  color: var(--color-text-secondary);
  text-transform: uppercase;
  letter-spacing: 0.04em;
}

.report-list__key {
  color: var(--color-text-primary);
  word-break: break-all;
}

.report-list__reason {
  grid-column: 2;
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
}

.report-empty {
  padding: 0 var(--space-4) var(--space-4);
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
}

@media (max-width: 480px) {
  .result-summary {
    grid-template-columns: 1fr;
  }
}
</style>

<template>
  <div class="import-preview">
    <div class="preview-meta">
      <div class="preview-meta__item">
        <span class="info-label">来源架构</span>
        <span class="info-value">{{ previewResult.manifest?.source_architecture || '—' }}</span>
      </div>
      <div class="preview-meta__item">
        <span class="info-label">导出时间</span>
        <span class="info-value">{{ formatTimestamp(previewResult.manifest?.exported_at) }}</span>
      </div>
    </div>
    <div class="preview-list">
      <div
        v-for="section in previewSections"
        :key="section.key"
        class="preview-list__item"
        :class="{ 'preview-list__item--ok': section.imported > 0 }"
      >
        <div class="preview-list__main">
          <span class="preview-list__icon">{{ section.icon }}</span>
          <div class="preview-list__info">
            <span class="preview-list__name">{{ section.label }}</span>
            <span class="preview-list__count">共 {{ section.total }} 项</span>
          </div>
        </div>
        <span :class="section.statusClass">{{ section.statusText }}</span>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'

const props = defineProps({
  previewResult: { type: Object, required: true }
})

const TYPES = [
  { key: 'agents', label: '节点', importKey: 'agents', icon: '🖥️' },
  { key: 'http_rules', label: 'HTTP 规则', importKey: 'http_rules', icon: '🌐' },
  { key: 'l4_rules', label: 'L4 规则', importKey: 'l4_rules', icon: '🔌' },
  { key: 'relay_listeners', label: '中继监听', importKey: 'relay_listeners', icon: '📡' },
  { key: 'certificates', label: '证书', importKey: 'certificates', icon: '🔒' },
  { key: 'version_policies', label: '版本策略', importKey: 'version_policies', icon: '📋' }
]

const previewSections = computed(() => {
  if (!props.previewResult) return []
  const s = props.previewResult.summary || {}
  return TYPES.map(t => {
    const imported = s.imported?.[t.importKey] || 0
    const conflict = s.skipped_conflict?.[t.importKey] || 0
    const invalid = s.skipped_invalid?.[t.importKey] || 0
    const missing = s.skipped_missing_material?.[t.importKey] || 0
    const total = imported + conflict + invalid + missing
    const parts = []
    if (imported) parts.push(`新增 ${imported}`)
    if (conflict) parts.push(`跳过 ${conflict}`)
    if (invalid) parts.push(`跳过 ${invalid}`)
    if (missing) parts.push(`跳过 ${missing}`)
    return {
      ...t,
      imported,
      total,
      statusText: total === 0 ? '无' : parts.join(' / '),
      statusClass: imported > 0 ? 'preview-status--ok' : 'preview-status--skip'
    }
  })
})

function formatTimestamp(value) {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}
</script>

<style scoped>
.import-preview {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.preview-meta {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: var(--space-3);
}

.preview-meta__item {
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  padding: var(--space-3) var(--space-4);
  background: var(--color-bg-surface);
}

.info-label {
  display: block;
  font-size: var(--text-xs);
  color: var(--color-text-secondary);
  margin-bottom: var(--space-1);
}

.info-value {
  font-size: var(--text-sm);
  color: var(--color-text-primary);
  font-weight: var(--font-medium);
}

.preview-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.preview-list__item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-3) var(--space-4);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  background: var(--color-bg-surface);
  transition: border-color var(--duration-fast) var(--ease-default),
              background-color var(--duration-fast) var(--ease-default);
}

.preview-list__item--ok {
  border-color: var(--color-primary);
  background: var(--color-primary-subtle);
}

.preview-list__main {
  display: flex;
  align-items: center;
  gap: var(--space-3);
}

.preview-list__icon {
  font-size: var(--text-lg);
  line-height: 1;
}

.preview-list__info {
  display: flex;
  flex-direction: column;
  gap: 0.1rem;
}

.preview-list__name {
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  color: var(--color-text-primary);
}

.preview-list__count {
  font-size: var(--text-xs);
  color: var(--color-text-tertiary);
}

.preview-status--ok {
  color: var(--color-success);
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
}

.preview-status--skip {
  color: var(--color-text-tertiary);
  font-size: var(--text-sm);
}

@media (max-width: 640px) {
  .preview-meta {
    grid-template-columns: 1fr;
  }
}
</style>

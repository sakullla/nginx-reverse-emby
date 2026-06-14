<template>
  <section class="export-panel">
    <div class="export-panel__header">
      <div class="export-panel__title-wrap">
        <span class="export-panel__icon">💾</span>
        <div class="export-panel__text">
          <h2 class="export-panel__title">备份</h2>
          <p class="export-panel__desc">选择要备份的资源类型</p>
        </div>
      </div>
      <button
        class="btn btn--primary"
        :disabled="exporting || !hasAnySelection"
        @click="handleExport"
      >
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/>
          <polyline points="7 10 12 15 17 10"/>
          <line x1="12" y1="15" x2="12" y2="3"/>
        </svg>
        {{ exportButtonText }}
      </button>
    </div>

    <div class="export-toolbar">
      <div class="export-toolbar__actions">
        <button class="text-button" @click="selectAll">全选</button>
        <button class="text-button" @click="deselectAll">取消全选</button>
      </div>
      <span class="export-toolbar__count">已选 {{ selectedCount }} / 共 {{ exportItems.length }} 项</span>
    </div>

    <div class="resource-list">
      <label
        v-for="item in exportItems"
        :key="item.key"
        class="resource-list__item"
        :class="{ active: exportSelection[item.key] }"
      >
        <input
          v-model="exportSelection[item.key]"
          type="checkbox"
          class="resource-list__checkbox"
        >
        <span class="resource-list__icon">{{ item.icon }}</span>
        <span class="resource-list__name">{{ item.label }}</span>
        <span class="resource-list__count">{{ counts?.[item.key] ?? 0 }}</span>
      </label>
    </div>

    <p v-if="!hasAnySelection" class="export-hint">请至少选择一项资源</p>
  </section>
</template>

<script setup>
import { computed, ref } from 'vue'
import { exportBackup, exportBackupSelective } from '../../../api'
import { messageStore } from '../../../stores/messages'

defineProps({
  counts: { type: Object, default: () => ({}) }
})

const exportSelection = ref({
  agents: true,
  http_rules: true,
  l4_rules: true,
  relay_listeners: true,
  certificates: true,
  version_policies: true
})
const exporting = ref(false)

const exportItems = [
  { key: 'agents', label: '节点', icon: '🖥️' },
  { key: 'http_rules', label: 'HTTP 规则', icon: '🌐' },
  { key: 'l4_rules', label: 'L4 规则', icon: '🔌' },
  { key: 'relay_listeners', label: '中继监听', icon: '📡' },
  { key: 'certificates', label: '证书', icon: '🔒' },
  { key: 'version_policies', label: '版本策略', icon: '📋' }
]

const hasAnySelection = computed(() => Object.values(exportSelection.value).some(Boolean))
const selectedCount = computed(() => Object.values(exportSelection.value).filter(Boolean).length)
const allSelected = computed(() => exportItems.every(item => exportSelection.value[item.key]))
const exportButtonText = computed(() => {
  if (exporting.value) return '备份中...'
  if (!hasAnySelection.value) return '备份'
  return allSelected.value ? '一键备份全部' : '备份选中项'
})

function selectAll() {
  exportItems.forEach(item => { exportSelection.value[item.key] = true })
}
function deselectAll() {
  exportItems.forEach(item => { exportSelection.value[item.key] = false })
}

function downloadBlob(blob, filename) {
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  document.body.appendChild(link)
  link.click()
  link.remove()
  URL.revokeObjectURL(url)
}

async function handleExport() {
  const selected = Object.entries(exportSelection.value).filter(([, v]) => v).map(([k]) => k)
  const allSelected = selected.length === exportItems.length
  exporting.value = true
  try {
    const result = allSelected ? await exportBackup() : await exportBackupSelective(selected)
    downloadBlob(result.blob, result.filename)
    messageStore.success('备份已导出')
  } catch (error) {
    messageStore.error(error, '导出备份失败')
  } finally {
    exporting.value = false
  }
}
</script>

<style scoped>
.export-panel {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.export-panel__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-4);
  flex-wrap: wrap;
}

.export-panel__title-wrap {
  display: flex;
  align-items: center;
  gap: var(--space-3);
}

.export-panel__icon {
  font-size: var(--text-xl);
  line-height: 1;
}

.export-panel__text {
  display: flex;
  flex-direction: column;
  gap: var(--space-0-5);
}

.export-panel__title {
  font-size: var(--text-base);
  font-weight: var(--font-semibold);
  color: var(--color-text-primary);
  margin: 0;
}

.export-panel__desc {
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
  margin: 0;
}

.export-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-2) 0;
}

.export-toolbar__actions {
  display: flex;
  gap: var(--space-3);
}

.text-button {
  background: none;
  border: none;
  color: var(--color-primary);
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  cursor: pointer;
  padding: 0;
  font-family: inherit;
}
.text-button:hover {
  text-decoration: underline;
}

.export-toolbar__count {
  font-size: var(--text-sm);
  color: var(--color-text-secondary);
}

.resource-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.resource-list__item {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  background: var(--color-bg-surface);
  cursor: pointer;
  transition:
    border-color var(--duration-fast) var(--ease-default),
    background-color var(--duration-fast) var(--ease-default);
}

.resource-list__item:hover {
  border-color: var(--color-primary);
}

.resource-list__item.active {
  border-color: var(--color-primary);
  background: var(--color-primary-subtle);
}

.resource-list__checkbox {
  width: 16px;
  height: 16px;
  accent-color: var(--color-primary);
  cursor: pointer;
  flex-shrink: 0;
}

.resource-list__icon {
  font-size: var(--text-lg);
  line-height: 1;
  flex-shrink: 0;
}

.resource-list__name {
  flex: 1;
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  color: var(--color-text-primary);
}

.resource-list__count {
  font-size: var(--text-xs);
  color: var(--color-text-tertiary);
  background: var(--color-bg-subtle);
  padding: 0.15rem 0.5rem;
  border-radius: 10px;
  flex-shrink: 0;
}

.resource-list__item.active .resource-list__count {
  background: color-mix(in srgb, var(--color-primary) 15%, transparent);
  color: var(--color-primary);
}

.export-hint {
  font-size: var(--text-xs);
  color: var(--color-text-tertiary);
  text-align: right;
  margin: 0;
}

@media (max-width: 480px) {
  .export-panel__header {
    flex-direction: column;
    align-items: flex-start;
  }
}
</style>

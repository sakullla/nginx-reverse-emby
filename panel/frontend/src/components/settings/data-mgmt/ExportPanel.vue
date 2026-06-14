<template>
  <section class="settings-section">
    <div class="settings-section__header">
      <h2 class="settings-section__title">导出备份</h2>
      <p class="settings-section__desc">选择要导出的资源类型</p>
    </div>
    <div class="settings-section__body">
      <div class="export-toolbar">
        <div class="export-toolbar__actions">
          <button class="text-button" @click="selectAll">全选</button>
          <button class="text-button" @click="deselectAll">取消全选</button>
        </div>
        <span class="export-toolbar__count">已选 {{ selectedCount }} / 共 {{ exportItems.length }} 项</span>
      </div>
      <div class="resource-grid">
        <div
          v-for="item in exportItems"
          :key="item.key"
          class="resource-card"
          :class="{ active: exportSelection[item.key] }"
          @click="exportSelection[item.key] = !exportSelection[item.key]"
        >
          <div class="resource-card__check" v-if="exportSelection[item.key]">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="white" stroke-width="3"><polyline points="20 6 9 17 4 12"/></svg>
          </div>
          <span class="resource-card__icon">{{ item.icon }}</span>
          <span class="resource-card__name">{{ item.label }}</span>
          <span class="resource-card__count">{{ counts?.[item.key] ?? 0 }}</span>
        </div>
      </div>
      <div class="export-actions">
        <button class="btn btn--primary" :disabled="exporting || !hasAnySelection" @click="handleExport">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/></svg>
          {{ exporting ? '导出中...' : '导出选中备份' }}
        </button>
      </div>
      <p v-if="!hasAnySelection" class="export-hint">请至少选择一项资源</p>
    </div>
  </section>
</template>

<script setup>
import { computed, ref } from 'vue'
import { exportBackup, exportBackupSelective } from '../../../api'
import { messageStore } from '../../../stores/messages'

defineProps({
  counts: { type: Object, default: () => ({}) }
})

const exportSelection = ref({ agents: true, http_rules: true, l4_rules: true, relay_listeners: true, certificates: true, version_policies: true })
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

function selectAll() { exportItems.forEach(item => { exportSelection.value[item.key] = true }) }
function deselectAll() { exportItems.forEach(item => { exportSelection.value[item.key] = false }) }

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
.export-toolbar { display: flex; align-items: center; justify-content: space-between; padding: var(--space-2) 0; }
.export-toolbar__actions { display: flex; gap: var(--space-3); }
.text-button { background: none; border: none; color: var(--color-primary); font-size: var(--text-sm); font-weight: var(--font-medium); cursor: pointer; padding: 0; font-family: inherit; }
.text-button:hover { text-decoration: underline; }
.export-toolbar__count { font-size: var(--text-sm); color: var(--color-text-secondary); }

.resource-grid { display: grid; grid-template-columns: repeat(2, 1fr); gap: var(--space-3); }
@media (max-width: 640px) { .resource-grid { grid-template-columns: 1fr; } }
.resource-card {
  position: relative; display: flex; flex-direction: column; align-items: center; gap: var(--space-1-5);
  padding: var(--space-4) var(--space-3); border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-lg); background: var(--color-bg-surface); cursor: pointer;
  transition: border-color var(--duration-fast) var(--ease-default),
              background-color var(--duration-fast) var(--ease-default),
              transform var(--duration-fast) var(--ease-default),
              box-shadow var(--duration-fast) var(--ease-default);
}
.resource-card:hover { border-color: var(--color-primary); transform: translateY(-2px); box-shadow: 0 2px 8px color-mix(in srgb, var(--color-border-default) 40%, transparent); }
.resource-card.active { border-color: var(--color-primary); background: var(--color-primary-subtle); }
.resource-card__check {
  position: absolute; top: var(--space-2); right: var(--space-2); width: 20px; height: 20px;
  border-radius: var(--radius-full); background: var(--color-primary); display: flex; align-items: center; justify-content: center;
}
.resource-card__icon { font-size: var(--text-xl); }
.resource-card__name { font-size: var(--text-sm); font-weight: var(--font-medium); color: var(--color-text-primary); }
.resource-card__count { font-size: var(--text-xs); color: var(--color-text-tertiary); background: var(--color-bg-subtle); padding: 0.15rem 0.5rem; border-radius: 10px; }
.resource-card.active .resource-card__count { background: color-mix(in srgb, var(--color-primary) 15%, transparent); color: var(--color-primary); }

.export-actions { display: flex; justify-content: flex-end; }
.export-hint { font-size: var(--text-xs); color: var(--color-text-tertiary); text-align: right; margin: 0; }
</style>

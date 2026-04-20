<template>
  <div class="settings-data-mgmt">
    <section class="settings-section">
      <div class="settings-section__header">
        <h2 class="settings-section__title">导出备份</h2>
        <p class="settings-section__desc">选择要导出的资源类型</p>
      </div>
      <div class="settings-section__body">
        <div class="export-checklist">
          <label
            v-for="item in exportItems"
            :key="item.key"
            class="export-checklist__item"
          >
            <input type="checkbox" v-model="exportSelection[item.key]" class="export-checklist__input">
            <span class="export-checklist__label">{{ item.label }}</span>
            <span class="export-checklist__count">{{ counts[item.key] ?? 0 }} 项</span>
          </label>
        </div>
        <button class="action-button" :disabled="exporting || !hasAnySelection" @click="handleExport">
          {{ exporting ? '导出中...' : '导出备份' }}
        </button>
      </div>
    </section>

    <section class="settings-section">
      <div class="settings-section__header">
        <h2 class="settings-section__title">导入备份</h2>
        <p class="settings-section__desc">从备份文件恢复配置</p>
      </div>
      <div class="settings-section__body">
        <div class="import-steps">
          <span class="import-step" :class="{ active: importStep >= 1, done: importStep > 1 }">1. 选择文件</span>
          <span class="import-step" :class="{ active: importStep >= 2, done: importStep > 2 }">2. 预览确认</span>
          <span class="import-step" :class="{ active: importStep >= 3 }">3. 导入结果</span>
        </div>

        <template v-if="importStep === 1">
          <input ref="fileInputRef" type="file" accept=".tar.gz,.tgz,.gz,application/gzip" class="backup-file-input" @change="handleFileChange">
          <div class="backup-hint">
            <span class="info-label">当前文件</span>
            <span class="info-value">{{ selectedFileName || '未选择备份文件' }}</span>
          </div>
          <div class="import-actions">
            <button class="action-button action-button--secondary" @click="fileInputRef?.click()">选择备份文件</button>
            <button v-if="selectedFileName" class="action-button" :disabled="previewing" @click="handlePreview">
              {{ previewing ? '分析中...' : '预览导入' }}
            </button>
          </div>
        </template>

        <template v-if="importStep === 2 && previewResult">
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
          <div class="preview-table">
            <div class="preview-table__header">
              <span>资源</span>
              <span>操作</span>
            </div>
            <div v-for="section in previewSections" :key="section.key" class="preview-table__row">
              <span>{{ section.label }} ({{ section.total }})</span>
              <span :class="section.statusClass">{{ section.statusText }}</span>
            </div>
          </div>
          <div class="import-actions">
            <button class="action-button action-button--secondary" @click="resetImport">取消</button>
            <button class="action-button" :disabled="importing" @click="handleConfirmImport">
              {{ importing ? '导入中...' : '确认导入' }}
            </button>
          </div>
        </template>

        <template v-if="importStep === 3 && importResult">
          <div class="backup-report__summary">
            <div class="summary-card">
              <span class="summary-card__label">已导入</span>
              <span class="summary-card__value">{{ summaryTotal(importResult.summary?.imported) }}</span>
            </div>
            <div class="summary-card">
              <span class="summary-card__label">冲突跳过</span>
              <span class="summary-card__value">{{ summaryTotal(importResult.summary?.skipped_conflict) }}</span>
            </div>
            <div class="summary-card">
              <span class="summary-card__label">无效跳过</span>
              <span class="summary-card__value">{{ summaryTotal(importResult.summary?.skipped_invalid) }}</span>
            </div>
            <div class="summary-card">
              <span class="summary-card__label">缺少证书跳过</span>
              <span class="summary-card__value">{{ summaryTotal(importResult.summary?.skipped_missing_material) }}</span>
            </div>
          </div>
          <div class="backup-report__meta">
            <div class="info-row">
              <span class="info-label">来源架构</span>
              <span class="info-value">{{ importResult.manifest?.source_architecture || '—' }}</span>
            </div>
            <div class="info-row">
              <span class="info-label">导出时间</span>
              <span class="info-value">{{ formatTimestamp(importResult.manifest?.exported_at) }}</span>
            </div>
          </div>
          <div class="report-group">
            <h3 class="report-group__title">导入报告</h3>
            <div v-for="section in reportSections" :key="section.key" class="report-block">
              <div class="report-block__header">
                <span>{{ section.label }}</span>
                <span class="report-block__count">{{ section.items.length }}</span>
              </div>
              <ul v-if="section.items.length" class="report-list">
                <li v-for="item in section.items" :key="`${section.key}-${item.kind}-${item.key}`" class="report-list__item">
                  <span class="report-list__kind">{{ item.kind }}</span>
                  <span class="report-list__key">{{ item.key }}</span>
                  <span v-if="item.reason" class="report-list__reason">{{ item.reason }}</span>
                </li>
              </ul>
              <div v-else class="report-empty">无</div>
            </div>
          </div>
          <button class="action-button action-button--secondary" @click="resetImport">完成</button>
        </template>
      </div>
    </section>
  </div>
</template>

<script setup>
import { computed, ref, onMounted } from 'vue'
import { exportBackup, exportBackupSelective, importBackup, importBackupPreview, fetchBackupResourceCounts } from '../../api'
import { messageStore } from '../../stores/messages'

const counts = ref({ agents: 0, http_rules: 0, l4_rules: 0, relay_listeners: 0, certificates: 0, version_policies: 0 })
const exportSelection = ref({ agents: true, http_rules: true, l4_rules: true, relay_listeners: true, certificates: true, version_policies: true })
const exporting = ref(false)

const importStep = ref(1)
const previewing = ref(false)
const importing = ref(false)
const previewResult = ref(null)
const importResult = ref(null)
const selectedFileName = ref('')
const fileInputRef = ref(null)
let selectedFile = null

const exportItems = [
  { key: 'agents', label: '节点 (Agents)' },
  { key: 'http_rules', label: 'HTTP 规则' },
  { key: 'l4_rules', label: 'L4 规则' },
  { key: 'relay_listeners', label: '中继监听' },
  { key: 'certificates', label: '证书' },
  { key: 'version_policies', label: '版本策略' }
]

const hasAnySelection = computed(() => Object.values(exportSelection.value).some(Boolean))

const previewSections = computed(() => {
  if (!previewResult.value) return []
  const s = previewResult.value.summary || {}
  const types = [
    { key: 'agents', label: '节点', importKey: 'agents' },
    { key: 'http_rules', label: 'HTTP 规则', importKey: 'http_rules' },
    { key: 'l4_rules', label: 'L4 规则', importKey: 'l4_rules' },
    { key: 'relay_listeners', label: '中继监听', importKey: 'relay_listeners' },
    { key: 'certificates', label: '证书', importKey: 'certificates' },
    { key: 'version_policies', label: '版本策略', importKey: 'version_policies' }
  ]
  return types.map(t => {
    const imported = s.imported?.[t.importKey] || 0
    const conflict = s.skipped_conflict?.[t.importKey] || 0
    const invalid = s.skipped_invalid?.[t.importKey] || 0
    const missing = s.skipped_missing_material?.[t.importKey] || 0
    const total = imported + conflict + invalid + missing
    const parts = []
    if (imported) parts.push(`新增 ${imported}`)
    if (conflict) parts.push(`跳过 ${conflict} (冲突)`)
    if (invalid) parts.push(`跳过 ${invalid} (无效)`)
    if (missing) parts.push(`跳过 ${missing} (缺证书)`)
    return {
      ...t,
      total,
      statusText: total === 0 ? '无' : parts.join(' / '),
      statusClass: imported > 0 ? 'preview-status--ok' : 'preview-status--skip'
    }
  })
})

const reportSections = computed(() => {
  const report = importResult.value?.report || {}
  return [
    { key: 'imported', label: '已导入', items: report.imported || [] },
    { key: 'skipped_conflict', label: '冲突跳过', items: report.skipped_conflict || [] },
    { key: 'skipped_invalid', label: '无效跳过', items: report.skipped_invalid || [] },
    { key: 'skipped_missing_material', label: '缺少证书材料跳过', items: report.skipped_missing_material || [] }
  ]
})

onMounted(() => {
  fetchBackupResourceCounts()
    .then(d => { counts.value = d.counts })
    .catch(() => {})
})

function summaryTotal(group = {}) {
  return Object.values(group || {}).reduce((sum, v) => sum + Number(v || 0), 0)
}

function formatTimestamp(value) {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
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
  const selected = Object.entries(exportSelection.value)
    .filter(([, v]) => v)
    .map(([k]) => k)
  const allSelected = selected.length === exportItems.length
  exporting.value = true
  try {
    const result = allSelected
      ? await exportBackup()
      : await exportBackupSelective(selected)
    downloadBlob(result.blob, result.filename)
    messageStore.success('备份已导出')
  } catch (error) {
    messageStore.error(error, '导出备份失败')
  } finally {
    exporting.value = false
  }
}

function handleFileChange(event) {
  const file = event.target.files?.[0]
  if (!file) return
  selectedFile = file
  selectedFileName.value = file.name
  importStep.value = 1
}

async function handlePreview() {
  if (!selectedFile) return
  previewing.value = true
  try {
    previewResult.value = await importBackupPreview(selectedFile)
    importStep.value = 2
  } catch (error) {
    messageStore.error(error, '预览失败')
  } finally {
    previewing.value = false
  }
}

async function handleConfirmImport() {
  if (!selectedFile) return
  importing.value = true
  try {
    importResult.value = await importBackup(selectedFile)
    messageStore.success('备份导入完成')
    importStep.value = 3
  } catch (error) {
    importResult.value = null
    messageStore.error(error, '导入备份失败')
  } finally {
    importing.value = false
  }
}

function resetImport() {
  importStep.value = 1
  previewResult.value = null
  importResult.value = null
  selectedFile = null
  selectedFileName.value = ''
  if (fileInputRef.value) fileInputRef.value.value = ''
}
</script>

<style scoped>
.settings-data-mgmt { display: flex; flex-direction: column; gap: 1.25rem; }
.settings-section {
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-2xl);
  overflow: hidden;
}
.settings-section__header { padding: 1rem 1.25rem; border-bottom: 1px solid var(--color-border-subtle); }
.settings-section__title { font-size: 1rem; font-weight: 600; margin: 0 0 0.25rem; color: var(--color-text-primary); }
.settings-section__desc { font-size: 0.875rem; color: var(--color-text-tertiary); margin: 0; }
.settings-section__body { padding: 1.25rem; display: flex; flex-direction: column; gap: 1rem; }

.export-checklist {
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  overflow: hidden;
}
.export-checklist__item {
  display: flex;
  align-items: center;
  gap: 0.6rem;
  padding: 0.65rem 1rem;
  cursor: pointer;
  transition: background 0.1s;
}
.export-checklist__item:not(:last-child) { border-bottom: 1px solid var(--color-border-subtle); }
.export-checklist__item:hover { background: var(--color-bg-subtle); }
.export-checklist__input { width: 16px; height: 16px; cursor: pointer; }
.export-checklist__label { font-size: 0.9rem; flex: 1; }
.export-checklist__count { font-size: 0.8rem; color: var(--color-text-tertiary); }

.import-steps { display: flex; gap: 0.5rem; flex-wrap: wrap; }
.import-step {
  padding: 0.3rem 0.8rem;
  border-radius: 20px;
  font-size: 0.75rem;
  font-weight: 500;
  background: var(--color-bg-subtle);
  color: var(--color-text-tertiary);
}
.import-step.active { background: var(--color-primary); color: #fff; font-weight: 600; }
.import-step.done { background: var(--color-primary-subtle); color: var(--color-primary); }

.backup-file-input { display: none; }

.backup-hint {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.75rem 0.9rem;
  border: 1px dashed var(--color-border-default);
  border-radius: var(--radius-lg);
  background: var(--color-bg-subtle);
}

.import-actions { display: flex; gap: 0.75rem; flex-wrap: wrap; }

.preview-meta { display: grid; grid-template-columns: 1fr 1fr; gap: 0.6rem; }
.preview-meta__item {
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  padding: 0.6rem 0.9rem;
}
.preview-table {
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  overflow: hidden;
}
.preview-table__header {
  display: flex;
  justify-content: space-between;
  padding: 0.5rem 1rem;
  background: var(--color-bg-subtle);
  border-bottom: 1px solid var(--color-border-default);
  font-size: 0.85rem;
  font-weight: 600;
}
.preview-table__row {
  display: flex;
  justify-content: space-between;
  padding: 0.45rem 1rem;
  font-size: 0.85rem;
}
.preview-table__row:not(:last-child) { border-bottom: 1px solid var(--color-border-subtle); }
.preview-status--ok { color: #16a34a; font-size: 0.8rem; }
.preview-status--skip { color: var(--color-text-tertiary); font-size: 0.8rem; }

.backup-report__summary {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(120px, 1fr));
  gap: 0.75rem;
}
.summary-card {
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  background: var(--color-bg-subtle);
  padding: 0.85rem 1rem;
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
}
.summary-card__label { font-size: 0.8rem; color: var(--color-text-secondary); }
.summary-card__value { font-size: 1.35rem; font-weight: 700; color: var(--color-text-primary); }

.backup-report__meta {
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-xl);
  padding: 0.25rem 1rem;
}

.report-group {
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-xl);
  overflow: hidden;
}
.report-group__title {
  margin: 0; padding: 0.9rem 1rem;
  font-size: 0.95rem; font-weight: 600;
  border-bottom: 1px solid var(--color-border-subtle);
  background: var(--color-bg-subtle);
}
.report-block + .report-block { border-top: 1px solid var(--color-border-subtle); }
.report-block__header {
  display: flex; justify-content: space-between; gap: 1rem;
  padding: 0.8rem 1rem; font-size: 0.875rem; font-weight: 600;
}
.report-block__count { color: var(--color-text-secondary); }
.report-list {
  list-style: none; margin: 0; padding: 0 1rem 1rem;
  display: flex; flex-direction: column; gap: 0.6rem;
}
.report-list__item {
  display: grid; grid-template-columns: 120px 1fr;
  gap: 0.5rem 0.75rem; align-items: start;
}
.report-list__kind { font-size: 0.75rem; color: var(--color-text-secondary); text-transform: uppercase; letter-spacing: 0.04em; }
.report-list__key { color: var(--color-text-primary); word-break: break-all; }
.report-list__reason { grid-column: 2; font-size: 0.8rem; color: var(--color-text-tertiary); }
.report-empty { padding: 0 1rem 1rem; font-size: 0.85rem; color: var(--color-text-tertiary); }

.action-button {
  border: 1.5px solid var(--color-primary);
  background: var(--color-primary);
  color: white;
  border-radius: var(--radius-lg);
  padding: 0.7rem 1rem;
  font-size: 0.95rem;
  font-weight: 600;
  cursor: pointer;
  transition: transform 0.2s var(--ease-default), opacity 0.2s var(--ease-default);
}
.action-button:hover:not(:disabled) { transform: translateY(-1px); }
.action-button:disabled { opacity: 0.6; cursor: not-allowed; }
.action-button--secondary {
  background: var(--color-bg-subtle);
  color: var(--color-text-primary);
  border-color: var(--color-border-default);
}

.info-row { display: flex; align-items: center; justify-content: space-between; padding: 0.5rem 0; border-bottom: 1px solid var(--color-border-subtle); }
.info-row:last-child { border-bottom: none; }
.info-label { font-size: 0.875rem; color: var(--color-text-secondary); }
.info-value { font-size: 0.875rem; color: var(--color-text-primary); font-weight: 500; }

@media (max-width: 640px) {
  .preview-meta { grid-template-columns: 1fr; }
  .import-actions { flex-direction: column; }
  .backup-hint { flex-direction: column; align-items: flex-start; }
}
</style>

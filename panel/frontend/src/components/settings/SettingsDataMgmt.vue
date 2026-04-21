<template>
  <div class="settings-data-mgmt">
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
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="white" stroke-width="3">
                <polyline points="20 6 9 17 4 12"/>
              </svg>
            </div>
            <span class="resource-card__icon">{{ item.icon }}</span>
            <span class="resource-card__name">{{ item.label }}</span>
            <span class="resource-card__count">{{ counts[item.key] ?? 0 }}</span>
          </div>
        </div>
        <div class="export-actions">
          <button class="action-button" :disabled="exporting || !hasAnySelection" @click="handleExport">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/>
            </svg>
            {{ exporting ? '导出中...' : '导出选中备份' }}
          </button>
        </div>
        <p v-if="!hasAnySelection" class="export-hint">请至少选择一项资源</p>
      </div>
    </section>

    <section class="settings-section">
      <div class="settings-section__header">
        <h2 class="settings-section__title">导入备份</h2>
        <p class="settings-section__desc">从备份文件恢复配置</p>
      </div>
      <div class="settings-section__body">
        <div class="stepper">
          <div
            v-for="(step, index) in stepLabels"
            :key="index"
            class="stepper__item"
            :class="{ active: importStep === index + 1, done: importStep > index + 1 }"
          >
            <div class="stepper__circle">
              <span v-if="importStep > index + 1">
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3">
                  <polyline points="20 6 9 17 4 12"/>
                </svg>
              </span>
              <span v-else>{{ index + 1 }}</span>
            </div>
            <span class="stepper__label">{{ step }}</span>
          </div>
        </div>

        <template v-if="importStep === 1">
          <div
            class="dropzone"
            :class="{ active: isDragging }"
            @click="fileInputRef?.click()"
            @dragover.prevent="isDragging = true"
            @dragleave="isDragging = false"
            @drop.prevent="handleDrop"
          >
            <input ref="fileInputRef" type="file" accept=".tar.gz,.tgz,.gz,application/gzip" class="backup-file-input" @change="handleFileChange">
            <div v-if="!selectedFileName" class="dropzone__placeholder">
              <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="var(--color-text-tertiary)" stroke-width="1.5">
                <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="17 8 12 3 7 8"/><line x1="12" y1="3" x2="12" y2="15"/>
              </svg>
              <p class="dropzone__text">点击或拖拽备份文件到此处</p>
              <p class="dropzone__hint">支持 .tar.gz, .tgz 格式</p>
            </div>
            <div v-else class="dropzone__file">
              <span class="dropzone__filename">{{ selectedFileName }}</span>
              <button class="text-button" @click.stop="clearFile">删除</button>
            </div>
          </div>
          <div class="import-actions">
            <button class="action-button" :disabled="previewing || !selectedFileName" @click="handlePreview">
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
          <div class="preview-cards">
            <div v-for="section in previewSections" :key="section.key" class="preview-card">
              <div class="preview-card__main">
                <span class="preview-card__icon">{{ section.icon }}</span>
                <div class="preview-card__info">
                  <span class="preview-card__name">{{ section.label }}</span>
                  <span class="preview-card__count">共 {{ section.total }} 项</span>
                </div>
              </div>
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
          <details class="report-details">
            <summary class="report-details__summary">查看导入报告</summary>
            <div class="report-group">
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
          </details>
          <div class="import-actions import-actions--center">
            <button class="action-button action-button--secondary" @click="resetImport">完成</button>
          </div>
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
const isDragging = ref(false)
let selectedFile = null

const stepLabels = ['选择文件', '预览确认', '导入结果']

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

const previewSections = computed(() => {
  if (!previewResult.value) return []
  const s = previewResult.value.summary || {}
  const types = [
    { key: 'agents', label: '节点', importKey: 'agents', icon: '🖥️' },
    { key: 'http_rules', label: 'HTTP 规则', importKey: 'http_rules', icon: '🌐' },
    { key: 'l4_rules', label: 'L4 规则', importKey: 'l4_rules', icon: '🔌' },
    { key: 'relay_listeners', label: '中继监听', importKey: 'relay_listeners', icon: '📡' },
    { key: 'certificates', label: '证书', importKey: 'certificates', icon: '🔒' },
    { key: 'version_policies', label: '版本策略', importKey: 'version_policies', icon: '📋' }
  ]
  return types.map(t => {
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

function selectAll() {
  exportItems.forEach(item => { exportSelection.value[item.key] = true })
}
function deselectAll() {
  exportItems.forEach(item => { exportSelection.value[item.key] = false })
}

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

function handleDrop(event) {
  isDragging.value = false
  const file = event.dataTransfer?.files?.[0]
  if (file) {
    selectedFile = file
    selectedFileName.value = file.name
    importStep.value = 1
  }
}

function clearFile() {
  selectedFile = null
  selectedFileName.value = ''
  if (fileInputRef.value) fileInputRef.value.value = ''
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
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-xl);
  overflow: hidden;
  transition: box-shadow 0.2s var(--ease-default);
}
.settings-section:hover {
  box-shadow: 0 1px 4px color-mix(in srgb, var(--color-border-default) 30%, transparent);
}
.settings-section__header { padding: 1rem 1.25rem; border-bottom: 1px solid var(--color-border-subtle); }
.settings-section__title { font-size: 1rem; font-weight: 600; margin: 0 0 0.25rem; color: var(--color-text-primary); }
.settings-section__desc { font-size: 0.875rem; color: var(--color-text-tertiary); margin: 0; }
.settings-section__body { padding: 1.25rem; display: flex; flex-direction: column; gap: 1rem; }

.export-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.5rem 0;
}
.export-toolbar__actions { display: flex; gap: 0.75rem; }
.text-button {
  background: none;
  border: none;
  color: var(--color-primary);
  font-size: 0.85rem;
  font-weight: 500;
  cursor: pointer;
  padding: 0;
}
.text-button:hover { text-decoration: underline; }
.export-toolbar__count { font-size: 0.85rem; color: var(--color-text-secondary); }

.resource-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 0.75rem;
}
@media (max-width: 640px) {
  .resource-grid { grid-template-columns: 1fr; }
}
.resource-card {
  position: relative;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 0.35rem;
  padding: 1rem 0.75rem;
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  background: var(--color-bg-surface);
  cursor: pointer;
  transition: all 0.2s var(--ease-default);
}
.resource-card:hover {
  border-color: var(--color-primary);
  transform: translateY(-2px);
  box-shadow: 0 2px 8px color-mix(in srgb, var(--color-border-default) 40%, transparent);
}
.resource-card.active {
  border-color: var(--color-primary);
  background: var(--color-primary-subtle);
}
.resource-card__check {
  position: absolute;
  top: 0.5rem;
  right: 0.5rem;
  width: 20px;
  height: 20px;
  border-radius: 50%;
  background: var(--color-primary);
  display: flex;
  align-items: center;
  justify-content: center;
}
.resource-card__icon { font-size: 1.4rem; }
.resource-card__name { font-size: 0.85rem; font-weight: 500; color: var(--color-text-primary); }
.resource-card__count {
  font-size: 0.75rem;
  color: var(--color-text-tertiary);
  background: var(--color-bg-subtle);
  padding: 0.15rem 0.5rem;
  border-radius: 10px;
}
.resource-card.active .resource-card__count {
  background: color-mix(in srgb, var(--color-primary) 15%, transparent);
  color: var(--color-primary);
}

.export-actions { display: flex; justify-content: flex-end; }
.export-hint { font-size: 0.8rem; color: var(--color-text-tertiary); text-align: right; margin: 0; }

.stepper {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 1.5rem;
}
.stepper__item {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 0.4rem;
  flex: 1;
  position: relative;
}
.stepper__item:not(:last-child)::after {
  content: '';
  position: absolute;
  top: 12px;
  right: -50%;
  width: 100%;
  height: 2px;
  background: var(--color-border-default);
  z-index: 0;
}
.stepper__item.done:not(:last-child)::after {
  background: var(--color-primary);
}
.stepper__circle {
  width: 26px;
  height: 26px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 0.75rem;
  font-weight: 600;
  border: 2px solid var(--color-border-default);
  color: var(--color-text-tertiary);
  background: var(--color-bg-surface);
  z-index: 1;
  transition: all 0.2s var(--ease-default);
}
.stepper__item.active .stepper__circle {
  border-color: var(--color-primary);
  background: var(--color-primary);
  color: white;
}
.stepper__item.done .stepper__circle {
  border-color: var(--color-primary);
  background: var(--color-primary-subtle);
  color: var(--color-primary);
}
.stepper__label {
  font-size: 0.75rem;
  color: var(--color-text-tertiary);
  font-weight: 500;
}
.stepper__item.active .stepper__label {
  color: var(--color-text-primary);
  font-weight: 600;
}
.stepper__item.done .stepper__label {
  color: var(--color-primary);
}

.dropzone {
  border: 2px dashed var(--color-border-default);
  border-radius: var(--radius-xl);
  padding: 2rem;
  text-align: center;
  cursor: pointer;
  transition: all 0.2s var(--ease-default);
  background: var(--color-bg-subtle);
}
.dropzone:hover, .dropzone.active {
  border-color: var(--color-primary);
  border-style: solid;
  background: var(--color-primary-subtle);
}
.dropzone__placeholder { display: flex; flex-direction: column; align-items: center; gap: 0.5rem; }
.dropzone__text { font-size: 0.9rem; color: var(--color-text-primary); font-weight: 500; margin: 0; }
.dropzone__hint { font-size: 0.75rem; color: var(--color-text-tertiary); margin: 0; }
.dropzone__file {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
}
.dropzone__filename { font-size: 0.9rem; color: var(--color-text-primary); font-weight: 500; }
.backup-file-input { display: none; }

.preview-meta { display: grid; grid-template-columns: 1fr 1fr; gap: 0.6rem; }
.preview-meta__item {
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  padding: 0.6rem 0.9rem;
}

.preview-cards { display: flex; flex-direction: column; gap: 0.5rem; }
.preview-card {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.75rem 1rem;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  background: var(--color-bg-surface);
}
.preview-card__main { display: flex; align-items: center; gap: 0.6rem; }
.preview-card__icon { font-size: 1.1rem; }
.preview-card__info { display: flex; flex-direction: column; gap: 0.1rem; }
.preview-card__name { font-size: 0.85rem; font-weight: 500; color: var(--color-text-primary); }
.preview-card__count { font-size: 0.75rem; color: var(--color-text-tertiary); }
.preview-status--ok { color: #16a34a; font-size: 0.8rem; font-weight: 500; }
.preview-status--skip { color: var(--color-text-tertiary); font-size: 0.8rem; }

.result-summary {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 0.75rem;
}
@media (max-width: 480px) {
  .result-summary { grid-template-columns: 1fr; }
}
.result-card {
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  background: var(--color-bg-subtle);
  padding: 1rem;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 0.35rem;
}
.result-card__label { font-size: 0.8rem; color: var(--color-text-secondary); }
.result-card__value { font-size: 1.6rem; font-weight: 700; color: var(--color-text-primary); }
.result-card__value--ok { color: #16a34a; }

.backup-report__meta {
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-xl);
  padding: 0.25rem 1rem;
}

.report-details { border: 1px solid var(--color-border-subtle); border-radius: var(--radius-lg); overflow: hidden; }
.report-details__summary {
  padding: 0.9rem 1rem;
  font-size: 0.9rem;
  font-weight: 600;
  cursor: pointer;
  background: var(--color-bg-subtle);
  color: var(--color-text-primary);
  list-style: none;
}
.report-details__summary::-webkit-details-marker { display: none; }
.report-details__summary::before {
  content: '▸';
  margin-right: 0.4rem;
  display: inline-block;
  transition: transform 0.2s;
}
.report-details[open] .report-details__summary::before { transform: rotate(90deg); }
.report-group { padding: 0 1rem 1rem; }
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
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
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
.action-button--secondary:hover:not(:disabled) {
  border-color: var(--color-primary);
}

.import-actions { display: flex; gap: 0.75rem; flex-wrap: wrap; }
.import-actions--center { justify-content: center; }

.info-row { display: flex; align-items: center; justify-content: space-between; padding: 0.5rem 0; border-bottom: 1px solid var(--color-border-subtle); }
.info-row:last-child { border-bottom: none; }
.info-label { font-size: 0.875rem; color: var(--color-text-secondary); }
.info-value { font-size: 0.875rem; color: var(--color-text-primary); font-weight: 500; }

@media (max-width: 640px) {
  .preview-meta { grid-template-columns: 1fr; }
  .import-actions { flex-direction: column; }
}
</style>

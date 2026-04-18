<template>
  <div class="settings-page">
    <div class="settings-page__header">
      <h1 class="settings-page__title">系统设置</h1>
    </div>

    <!-- Theme -->
    <section class="settings-section">
      <div class="settings-section__header">
        <h2 class="settings-section__title">外观主题</h2>
        <p class="settings-section__desc">选择面板的外观风格</p>
      </div>
      <div class="settings-section__body">
        <div class="theme-grid">
          <button
            v-for="theme in themes"
            :key="theme.id"
            class="theme-option"
            :class="{ active: currentTheme === theme.id }"
            @click="setTheme(theme.id)"
          >
            <span class="theme-option__label">{{ theme.label }}</span>
            <svg v-if="currentTheme === theme.id" class="theme-option__check" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3">
              <polyline points="20 6 9 17 4 12"/>
            </svg>
          </button>
        </div>
      </div>
    </section>

    <!-- Deploy Mode -->
    <section class="settings-section">
      <div class="settings-section__header">
        <h2 class="settings-section__title">部署模式</h2>
        <p class="settings-section__desc">当前面板的运行模式</p>
      </div>
      <div class="settings-section__body">
        <div class="info-row">
          <span class="info-label">角色</span>
          <span class="info-value">{{ systemInfo?.role || '—' }}</span>
        </div>
        <div class="info-row">
          <span class="info-label">本地 Agent</span>
          <span class="info-value">{{ systemInfo?.local_agent_enabled ? '已启用' : '未启用' }}</span>
        </div>
      </div>
    </section>

    <section class="settings-section">
      <div class="settings-section__header">
        <h2 class="settings-section__title">数据管理</h2>
        <p class="settings-section__desc">导出可跨版本迁移的备份包，或导入旧版本和当前版本的备份</p>
      </div>
      <div class="settings-section__body">
        <div class="backup-actions">
          <button class="action-button" :disabled="exporting" @click="handleExportBackup">
            {{ exporting ? '导出中...' : '导出备份' }}
          </button>
          <input
            ref="fileInputRef"
            type="file"
            accept=".tar.gz,.tgz,.gz,application/gzip"
            class="backup-file-input"
            @change="handleFileChange"
          >
          <button class="action-button action-button--secondary" :disabled="importing" @click="triggerImportSelect">
            {{ importing ? '导入中...' : '导入备份' }}
          </button>
        </div>

        <div class="backup-hint">
          <span class="info-label">当前文件</span>
          <span class="info-value">{{ selectedFileName || '未选择备份文件' }}</span>
        </div>

        <div v-if="importResult" class="backup-report">
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
            <div
              v-for="section in reportSections"
              :key="section.key"
              class="report-block"
            >
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
        </div>
      </div>
    </section>

    <!-- About -->
    <section class="settings-section">
      <div class="settings-section__header">
        <h2 class="settings-section__title">关于</h2>
      </div>
      <div class="settings-section__body">
        <div class="info-row">
          <span class="info-label">版本</span>
          <span class="info-value">1.0.0</span>
        </div>
        <div class="info-row">
          <span class="info-label">项目</span>
          <span class="info-value">nginx-reverse-emby</span>
        </div>
      </div>
    </section>
  </div>
</template>

<script setup>
import { computed, ref, onMounted } from 'vue'
import { useTheme } from '../context/ThemeContext'
import { exportBackup, fetchSystemInfo, importBackup } from '../api'
import { messageStore } from '../stores/messages'

const { currentThemeId: currentTheme, setTheme, themes } = useTheme()

const systemInfo = ref(null)
const exporting = ref(false)
const importing = ref(false)
const importResult = ref(null)
const selectedFileName = ref('')
const fileInputRef = ref(null)

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
  fetchSystemInfo().then(i => { systemInfo.value = i }).catch(() => {})
})

function summaryTotal(group = {}) {
  return Object.values(group || {}).reduce((sum, value) => sum + Number(value || 0), 0)
}

function formatTimestamp(value) {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

function triggerImportSelect() {
  fileInputRef.value?.click()
}

function downloadBlob(blob, filename) {
  const objectUrl = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = objectUrl
  link.download = filename
  document.body.appendChild(link)
  link.click()
  link.remove()
  URL.revokeObjectURL(objectUrl)
}

async function handleExportBackup() {
  exporting.value = true
  try {
    const { blob, filename } = await exportBackup()
    downloadBlob(blob, filename)
    messageStore.success('备份已导出')
  } catch (error) {
    messageStore.error(error, '导出备份失败')
  } finally {
    exporting.value = false
  }
}

async function handleFileChange(event) {
  const file = event.target.files?.[0]
  if (!file) return
  selectedFileName.value = file.name
  importing.value = true
  try {
    importResult.value = await importBackup(file)
    messageStore.success('备份导入完成')
  } catch (error) {
    importResult.value = null
    messageStore.error(error, '导入备份失败')
  } finally {
    importing.value = false
    event.target.value = ''
  }
}
</script>

<style scoped>
.settings-page { max-width: 700px; margin: 0 auto; }
.settings-page__header { margin-bottom: 2rem; }
.settings-page__title { font-size: 1.5rem; font-weight: 700; margin: 0; color: var(--color-text-primary); }
.settings-section { background: var(--color-bg-surface); border: 1.5px solid var(--color-border-default); border-radius: var(--radius-2xl); margin-bottom: 1.25rem; overflow: hidden; }
.settings-section__header { padding: 1rem 1.25rem; border-bottom: 1px solid var(--color-border-subtle); }
.settings-section__title { font-size: 1rem; font-weight: 600; margin: 0 0 0.25rem; color: var(--color-text-primary); }
.settings-section__desc { font-size: 0.875rem; color: var(--color-text-tertiary); margin: 0; }
.settings-section__body { padding: 1.25rem; display: flex; flex-direction: column; gap: 1rem; }
.backup-actions { display: flex; gap: 0.75rem; flex-wrap: wrap; }
.backup-file-input { display: none; }
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
.backup-hint {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.75rem 0.9rem;
  border: 1px dashed var(--color-border-default);
  border-radius: var(--radius-lg);
  background: var(--color-bg-subtle);
}
.backup-report { display: flex; flex-direction: column; gap: 1rem; }
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
  margin: 0;
  padding: 0.9rem 1rem;
  font-size: 0.95rem;
  font-weight: 600;
  border-bottom: 1px solid var(--color-border-subtle);
  background: var(--color-bg-subtle);
}
.report-block + .report-block { border-top: 1px solid var(--color-border-subtle); }
.report-block__header {
  display: flex;
  justify-content: space-between;
  gap: 1rem;
  padding: 0.8rem 1rem;
  font-size: 0.875rem;
  font-weight: 600;
}
.report-block__count { color: var(--color-text-secondary); }
.report-list {
  list-style: none;
  margin: 0;
  padding: 0 1rem 1rem;
  display: flex;
  flex-direction: column;
  gap: 0.6rem;
}
.report-list__item {
  display: grid;
  grid-template-columns: 120px 1fr;
  gap: 0.5rem 0.75rem;
  align-items: start;
}
.report-list__kind {
  font-size: 0.75rem;
  color: var(--color-text-secondary);
  text-transform: uppercase;
  letter-spacing: 0.04em;
}
.report-list__key { color: var(--color-text-primary); word-break: break-all; }
.report-list__reason {
  grid-column: 2;
  font-size: 0.8rem;
  color: var(--color-text-tertiary);
}
.report-empty {
  padding: 0 1rem 1rem;
  font-size: 0.85rem;
  color: var(--color-text-tertiary);
}

/* Theme Grid */
.theme-grid { display: flex; gap: 0.5rem; flex-wrap: wrap; }
.theme-option {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.5rem 0.75rem;
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  background: var(--color-bg-subtle);
  cursor: pointer;
  transition: all 0.2s var(--ease-default);
  position: relative;
}
.theme-option:hover { border-color: var(--color-primary); transform: translateY(-1px); }
.theme-option.active {
  border-color: var(--color-primary);
  background: var(--color-primary-subtle);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--color-primary) 20%, transparent);
  transform: translateY(-1px);
}
.theme-option__label { font-size: 0.875rem; font-weight: 500; color: var(--color-text-primary); }
.theme-option__check {
  color: var(--color-primary);
  animation: checkPop 0.3s var(--ease-bounce);
}
@keyframes checkPop {
  0% { transform: scale(0); opacity: 0; }
  100% { transform: scale(1); opacity: 1; }
}

/* 4K adaptation */
@media (min-width: 2560px) {
  .theme-option { padding: 0.625rem 1rem; }
  .theme-option__label { font-size: 1rem; }
  .settings-page { max-width: 900px; }
  .settings-page__title { font-size: 1.75rem; }
  .settings-section__title { font-size: 1.125rem; }
}

/* Info rows */
.info-row { display: flex; align-items: center; justify-content: space-between; padding: 0.5rem 0; border-bottom: 1px solid var(--color-border-subtle); }
.info-row:last-child { border-bottom: none; }
.info-label { font-size: 0.875rem; color: var(--color-text-secondary); }
.info-value { font-size: 0.875rem; color: var(--color-text-primary); font-weight: 500; }

@media (max-width: 640px) {
  .backup-hint,
  .report-list__item,
  .info-row {
    grid-template-columns: 1fr;
    flex-direction: column;
    align-items: flex-start;
  }
  .backup-actions { flex-direction: column; }
}
</style>

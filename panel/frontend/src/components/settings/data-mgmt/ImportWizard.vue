<template>
  <div class="import-wizard">
    <div class="import-wizard__header">
      <span class="import-wizard__icon">📥</span>
      <div class="import-wizard__text">
        <h2 class="import-wizard__title">导入配置</h2>
        <p class="import-wizard__desc">从配置文件导入面板配置</p>
      </div>
    </div>

    <div class="stepper">
      <div
        v-for="(step, index) in stepLabels"
        :key="index"
        class="stepper__item"
        :class="{ active: importStep === index + 1, done: importStep > index + 1 }"
      >
        <div class="stepper__circle">
          <span v-if="importStep > index + 1">
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3"><polyline points="20 6 9 17 4 12"/></svg>
          </span>
          <span v-else>{{ index + 1 }}</span>
        </div>
        <span class="stepper__label">{{ step }}</span>
      </div>
    </div>

    <div class="import-wizard__body">
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
            <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="var(--color-text-tertiary)" stroke-width="1.5"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="17 8 12 3 7 8"/><line x1="12" y1="3" x2="12" y2="15"/></svg>
            <p class="dropzone__text">点击或拖拽配置文件到此处</p>
            <p class="dropzone__hint">支持 .tar.gz, .tgz 格式</p>
          </div>
          <div v-else class="dropzone__file">
            <span class="dropzone__filename">{{ selectedFileName }}</span>
            <button class="text-button" @click.stop="clearFile">删除</button>
          </div>
        </div>
        <div class="import-actions">
          <button class="btn btn--primary" :disabled="previewing || !selectedFileName" @click="handlePreview">
            {{ previewing ? '分析中...' : '预览导入' }}
          </button>
        </div>
      </template>

      <template v-if="importStep === 2 && previewResult">
        <ImportPreview :preview-result="previewResult" />
        <div class="import-actions">
          <button class="btn btn--secondary" @click="resetImport">取消</button>
          <button class="btn btn--primary" :disabled="importing" @click="handleConfirmImport">
            {{ importing ? '导入中...' : '确认导入' }}
          </button>
        </div>
      </template>

      <template v-if="importStep === 3 && importResult">
        <ImportReport :import-result="importResult" />
        <div class="import-actions import-actions--center">
          <button class="btn btn--secondary" @click="resetImport">完成</button>
        </div>
      </template>
    </div>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import { importBackup, importBackupPreview } from '../../../api'
import { messageStore } from '../../../stores/messages'
import ImportPreview from './ImportPreview.vue'
import ImportReport from './ImportReport.vue'

const importStep = ref(1)
const previewing = ref(false)
const importing = ref(false)
const previewResult = ref(null)
const importResult = ref(null)
const selectedFileName = ref('')
const fileInputRef = ref(null)
const isDragging = ref(false)
let selectedFile = null

const stepLabels = ['选择配置文件', '预览确认', '导入结果']

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
    messageStore.success('配置导入完成')
    importStep.value = 3
  } catch (error) {
    importResult.value = null
    messageStore.error(error, '导入配置失败')
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
.import-wizard {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.import-wizard__header {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding-bottom: var(--space-3);
  border-bottom: 1px solid var(--color-border-subtle);
}

.import-wizard__icon {
  font-size: var(--text-xl);
  line-height: 1;
}

.import-wizard__text {
  display: flex;
  flex-direction: column;
  gap: var(--space-0-5);
}

.import-wizard__title {
  font-size: var(--text-base);
  font-weight: var(--font-semibold);
  color: var(--color-text-primary);
  margin: 0;
}

.import-wizard__desc {
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
  margin: 0;
}

.import-wizard__body {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.stepper {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: var(--space-2);
}
.stepper__item {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-1-5);
  flex: 1;
  position: relative;
}
.stepper__item:not(:last-child)::after {
  content: '';
  position: absolute;
  top: 14px;
  right: -50%;
  width: 100%;
  height: 2px;
  background: var(--color-border-default);
  z-index: 0;
}
.stepper__item.done:not(:last-child)::after { background: var(--color-primary); }
.stepper__circle {
  width: 30px;
  height: 30px;
  border-radius: var(--radius-full);
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: var(--text-xs);
  font-weight: var(--font-semibold);
  border: 2px solid var(--color-border-default);
  color: var(--color-text-tertiary);
  background: var(--color-bg-surface);
  z-index: 1;
  transition: all var(--duration-fast) var(--ease-default);
}
.stepper__item.active .stepper__circle { border-color: var(--color-primary); background: var(--color-primary); color: white; }
.stepper__item.done .stepper__circle { border-color: var(--color-primary); background: var(--color-primary-subtle); color: var(--color-primary); }
.stepper__label { font-size: var(--text-xs); color: var(--color-text-tertiary); font-weight: var(--font-medium); }
.stepper__item.active .stepper__label { color: var(--color-text-primary); font-weight: var(--font-semibold); }
.stepper__item.done .stepper__label { color: var(--color-primary); }

.dropzone {
  border: 2px dashed var(--color-border-default);
  border-radius: var(--radius-xl);
  padding: var(--space-8);
  text-align: center;
  cursor: pointer;
  transition: border-color var(--duration-fast) var(--ease-default), background-color var(--duration-fast) var(--ease-default);
  background: var(--color-bg-subtle);
}
.dropzone:hover, .dropzone.active { border-color: var(--color-primary); background: var(--color-primary-subtle); }
.dropzone__placeholder { display: flex; flex-direction: column; align-items: center; gap: var(--space-2); }
.dropzone__text { font-size: var(--text-sm); color: var(--color-text-primary); font-weight: var(--font-medium); margin: 0; }
.dropzone__hint { font-size: var(--text-xs); color: var(--color-text-tertiary); margin: 0; }
.dropzone__file { display: flex; align-items: center; justify-content: space-between; gap: var(--space-4); }
.dropzone__filename { font-size: var(--text-sm); color: var(--color-text-primary); font-weight: var(--font-medium); }
.backup-file-input { display: none; }

.text-button { background: none; border: none; color: var(--color-primary); font-size: var(--text-sm); font-weight: var(--font-medium); cursor: pointer; padding: 0; font-family: inherit; }
.text-button:hover { text-decoration: underline; }

.import-actions { display: flex; gap: var(--space-3); flex-wrap: wrap; justify-content: flex-end; }
.import-actions--center { justify-content: center; }

@media (max-width: 480px) {
  .stepper__circle {
    width: 26px;
    height: 26px;
  }
  .stepper__item:not(:last-child)::after {
    top: 12px;
  }
}
</style>

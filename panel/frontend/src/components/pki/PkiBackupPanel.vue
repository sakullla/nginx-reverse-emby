<template>
  <div class="backup-panel">
    <div v-if="!hideHeader" class="backup-panel__head">
      <div class="backup-panel__eyebrow">灾难恢复</div>
      <h2>受保护备份</h2>
      <p>口令只驻留在本次表单与 request body；成功或失败后立即清空。口令丢失无法恢复。</p>
    </div>

    <div class="backup-panel__cards">
      <form class="backup-card" @submit.prevent="$emit('export')">
        <div class="backup-card__head">
          <div class="backup-card__icon backup-card__icon--export" aria-hidden="true">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/>
              <polyline points="7 10 12 15 17 10"/>
              <line x1="12" y1="15" x2="12" y2="3"/>
            </svg>
          </div>
          <div class="backup-card__titles">
            <strong>导出加密备份</strong>
            <span>生成可下载的受保护 archive，请离线保存口令</span>
          </div>
        </div>

        <div class="backup-card__body">
          <label class="field">
            <span class="field__label">导出口令</span>
            <input
              :value="exportPassphrase"
              data-test="export-passphrase"
              class="field__input"
              type="password"
              autocomplete="new-password"
              placeholder="设置强口令"
              required
              @input="$emit('update:exportPassphrase', $event.target.value)"
            >
          </label>
          <label class="field">
            <span class="field__label">再次输入口令</span>
            <input
              :value="exportPassphraseConfirm"
              class="field__input"
              type="password"
              autocomplete="new-password"
              placeholder="再次确认口令"
              required
              @input="$emit('update:exportPassphraseConfirm', $event.target.value)"
            >
          </label>
        </div>

        <div class="backup-card__footer">
          <BaseButton type="submit" variant="primary" :disabled="busy" :loading="busy">生成加密备份</BaseButton>
          <BaseButton v-if="hasArchive" type="button" variant="secondary" @click="$emit('download')">下载后清除</BaseButton>
        </div>
      </form>

      <form class="backup-card backup-card--import" @submit.prevent="$emit('import')">
        <div class="backup-card__head">
          <div class="backup-card__icon backup-card__icon--import" aria-hidden="true">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/>
              <polyline points="17 8 12 3 7 8"/>
              <line x1="12" y1="3" x2="12" y2="15"/>
            </svg>
          </div>
          <div class="backup-card__titles">
            <strong>导入受保护备份</strong>
            <span>会替换当前 SQLite 目标状态，请先建立回滚点</span>
          </div>
        </div>

        <div class="backup-card__body backup-card__body--import">
          <div class="field field--full">
            <span class="field__label">加密备份文件</span>
            <div class="file-picker" :class="{ 'file-picker--filled': Boolean(fileName) }">
              <input
                ref="fileInput"
                data-test="import-archive"
                class="file-picker__native"
                type="file"
                accept=".nre-pki,.bin,application/octet-stream"
                required
                @change="onFileChange"
              >
              <button type="button" class="file-picker__trigger" @click="openFilePicker">选择文件</button>
              <span class="file-picker__name" :class="{ 'file-picker__name--empty': !fileName }">
                {{ fileName || '未选择任何文件 · 支持 .nre-pki' }}
              </span>
              <button
                v-if="fileName"
                type="button"
                class="file-picker__clear"
                @click="clearSelectedFile"
              >清除</button>
            </div>
          </div>

          <label class="field">
            <span class="field__label">导入口令</span>
            <input
              :value="importPassphrase"
              data-test="import-passphrase"
              class="field__input"
              type="password"
              autocomplete="new-password"
              placeholder="备份导出口令"
              required
              @input="$emit('update:importPassphrase', $event.target.value)"
            >
          </label>

          <label class="field field--full">
            <span class="field__label">操作原因</span>
            <input
              :value="importReason"
              data-test="import-reason"
              class="field__input"
              required
              placeholder="例如：计划迁移恢复"
              @input="$emit('update:importReason', $event.target.value)"
            >
          </label>
        </div>

        <div class="backup-card__footer">
          <BaseButton
            type="submit"
            variant="danger"
            :disabled="busy || !fileName"
            :loading="busy"
          >导入受保护备份</BaseButton>
        </div>
      </form>
    </div>

    <p
      v-if="message"
      class="backup-message"
      :class="messageKind === 'error' ? 'danger-text' : 'success-text'"
      role="status"
    >{{ message }}</p>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import BaseButton from '../base/BaseButton.vue'

defineProps({
  busy: { type: Boolean, default: false },
  exportPassphrase: { type: String, default: '' },
  exportPassphraseConfirm: { type: String, default: '' },
  importPassphrase: { type: String, default: '' },
  importReason: { type: String, default: '' },
  hasArchive: { type: Boolean, default: false },
  message: { type: String, default: '' },
  messageKind: { type: String, default: 'success' },
  hideHeader: { type: Boolean, default: false },
})

const emit = defineEmits([
  'export',
  'import',
  'download',
  'select-file',
  'update:exportPassphrase',
  'update:exportPassphraseConfirm',
  'update:importPassphrase',
  'update:importReason',
])

const fileInput = ref(null)
const fileName = ref('')

function openFilePicker() {
  fileInput.value?.click()
}

function onFileChange(event) {
  const file = event.target.files?.[0] || null
  fileName.value = file?.name || ''
  emit('select-file', event)
}

function clearSelectedFile() {
  fileName.value = ''
  if (fileInput.value) fileInput.value.value = ''
  emit('select-file', { target: { files: [] } })
}

defineExpose({
  clearFile() {
    fileName.value = ''
    if (fileInput.value) fileInput.value.value = ''
  },
})
</script>

<style scoped>
.backup-panel {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  min-width: 0;
}

.backup-panel__eyebrow {
  color: var(--color-primary);
  font-size: 0.7rem;
  font-weight: 700;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  margin-bottom: 0.2rem;
}

.backup-panel__head h2 {
  margin: 0 0 0.25rem;
  color: var(--color-text-primary);
  font-size: var(--text-lg);
}

.backup-panel__head p {
  margin: 0;
  color: var(--color-text-tertiary);
  font-size: var(--text-sm);
  line-height: 1.5;
  max-width: 56rem;
}

.backup-panel__cards {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-4);
  align-items: start;
}

.backup-card {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  min-width: 0;
  padding: var(--space-5);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-xl);
  background: var(--color-bg-surface);
  box-shadow: var(--shadow-xs);
}

.backup-card--import {
  border-color: color-mix(in srgb, var(--color-danger) 18%, var(--color-border-subtle));
  background:
    linear-gradient(180deg, color-mix(in srgb, var(--color-danger) 4%, var(--color-bg-surface)), var(--color-bg-surface));
}

.backup-card__head {
  display: flex;
  align-items: flex-start;
  gap: var(--space-3);
}

.backup-card__titles {
  min-width: 0;
}

.backup-card__titles strong {
  display: block;
  color: var(--color-text-primary);
  margin-bottom: 0.2rem;
  font-size: var(--text-base);
}

.backup-card__titles span {
  display: block;
  color: var(--color-text-tertiary);
  font-size: var(--text-xs);
  line-height: 1.45;
}

.backup-card__icon {
  width: 2.25rem;
  height: 2.25rem;
  border-radius: var(--radius-md);
  display: grid;
  place-items: center;
  flex-shrink: 0;
}

.backup-card__icon--export {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
}

.backup-card__icon--import {
  background: color-mix(in srgb, var(--color-danger) 10%, var(--color-bg-subtle));
  color: var(--color-danger);
}

.backup-card__body {
  display: grid;
  grid-template-columns: 1fr;
  gap: var(--space-3);
}

.backup-card__body--import {
  grid-template-columns: 1fr 1fr;
}

.field {
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
  min-width: 0;
}

.field--full {
  grid-column: 1 / -1;
}

.field__label {
  color: var(--color-text-secondary);
  font-size: var(--text-xs);
  font-weight: 600;
}

.field__input {
  width: 100%;
  box-sizing: border-box;
  min-height: 2.5rem;
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-surface);
  color: var(--color-text-primary);
  padding: 0.55rem 0.75rem;
  font: inherit;
  font-size: var(--text-sm);
  transition: border-color var(--duration-fast) var(--ease-default),
              box-shadow var(--duration-fast) var(--ease-default);
}

.field__input:focus {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.field__input::placeholder {
  color: var(--color-text-muted);
}

.file-picker {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-height: 2.5rem;
  padding: 0.3rem;
  border: 1.5px dashed var(--color-border-default);
  border-radius: var(--radius-md);
  background: color-mix(in srgb, var(--color-bg-subtle) 55%, var(--color-bg-surface));
}

.file-picker--filled {
  border-style: solid;
  border-color: color-mix(in srgb, var(--color-primary) 28%, var(--color-border-default));
  background: var(--color-bg-surface);
}

.file-picker__native {
  position: absolute;
  width: 1px;
  height: 1px;
  opacity: 0;
  pointer-events: none;
}

.file-picker__trigger {
  flex-shrink: 0;
  min-height: 2rem;
  padding: 0.35rem 0.75rem;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-surface);
  color: var(--color-text-primary);
  font: inherit;
  font-size: var(--text-xs);
  font-weight: 600;
  cursor: pointer;
}

.file-picker__trigger:hover {
  border-color: var(--color-primary);
  color: var(--color-primary);
  background: var(--color-primary-subtle);
}

.file-picker__name {
  min-width: 0;
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
}

.file-picker__name--empty {
  color: var(--color-text-muted);
}

.file-picker__clear {
  flex-shrink: 0;
  border: 0;
  background: transparent;
  color: var(--color-text-tertiary);
  font: inherit;
  font-size: var(--text-xs);
  cursor: pointer;
  padding: 0.25rem 0.45rem;
  border-radius: var(--radius-sm);
}

.file-picker__clear:hover {
  color: var(--color-danger);
  background: color-mix(in srgb, var(--color-danger) 8%, transparent);
}

.backup-card__footer {
  display: flex;
  gap: var(--space-2);
  flex-wrap: wrap;
  margin-top: auto;
  padding-top: var(--space-1);
}

.backup-message {
  margin: 0;
  font-size: var(--text-sm);
}

.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}

.danger-text { color: var(--color-danger) !important; }
.success-text { color: var(--color-success) !important; }

@media (min-width: 1920px) {
  .backup-panel__cards {
    gap: var(--space-5);
  }

  .backup-card {
    padding: var(--space-6);
  }
}

@media (max-width: 1100px) {
  .backup-panel__cards {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 680px) {
  .backup-card {
    padding: var(--space-4);
  }

  .backup-card__body--import {
    grid-template-columns: 1fr;
  }

  .backup-card__footer {
    width: 100%;
  }

  .backup-card__footer :deep(.btn),
  .backup-card__footer .btn {
    width: 100%;
    min-height: 2.4rem;
    justify-content: center;
  }

  .file-picker {
    flex-wrap: wrap;
  }

  .file-picker__name {
    flex: 1 1 100%;
    order: 3;
    padding: 0 0.15rem 0.1rem;
  }
}
</style>

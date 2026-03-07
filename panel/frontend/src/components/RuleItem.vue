<template>
  <tr class="rule-row" :class="{ 'is-editing': isEditing }">
    <td class="col-id" data-label="ID">{{ rule.id }}</td>

    <!-- 前端 URL 列 -->
    <td class="col-url" data-label="前端 URL">
      <div class="url-display" v-if="!isEditing">
        <span class="url-text">{{ rule.frontend_url }}</span>
      </div>
      <div class="url-edit" v-else>
        <input
          v-model="editForm.frontend_url"
          type="text"
          class="inline-input"
          placeholder="前端 URL"
        />
      </div>
    </td>

    <!-- 后端 URL 列 -->
    <td class="col-url" data-label="后端 URL">
      <div class="url-display" v-if="!isEditing">
        <span class="url-text">{{ rule.backend_url }}</span>
      </div>
      <div class="url-edit" v-else>
        <input
          v-model="editForm.backend_url"
          type="text"
          class="inline-input"
          placeholder="后端 URL"
        />
      </div>
    </td>

    <!-- 操作列 -->
    <td class="col-actions">
      <div class="action-buttons">
        <!-- 正常模式：编辑 & 删除 -->
        <template v-if="!isEditing">
          <button @click="startEdit" class="btn-icon primary" title="编辑规则">
            <svg viewBox="0 0 24 24"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/></svg>
          </button>
          <button @click="handleDelete" class="btn-icon danger" title="删除规则">
            <svg viewBox="0 0 24 24"><path d="M3 6h18m-2 0v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6m3 0V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2"/></svg>
          </button>
        </template>

        <!-- 编辑模式：保存 & 取消 -->
        <template v-else>
          <button @click="handleSave" class="btn-icon success" title="保存修改" :disabled="loading">
            <svg v-if="!loading" viewBox="0 0 24 24"><polyline points="20 6 9 17 4 12"/></svg>
            <span v-else class="loading-mini"></span>
          </button>
          <button @click="cancelEdit" class="btn-icon secondary" title="取消" :disabled="loading">
            <svg viewBox="0 0 24 24"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
          </button>
        </template>
      </div>
    </td>
  </tr>

  <!-- 删除确认弹窗 -->
  <Teleport to="body">
    <BaseModal
      v-model="showDeleteModal"
      title="确认删除"
      confirm-text="确认删除"
      confirm-variant="danger"
      :loading="isDeletingRule"
      @confirm="confirmDelete"
    >
      <div class="delete-confirm-content">
        <div class="warning-icon">
          <svg viewBox="0 0 24 24"><path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/><line x1="12" y1="9" x2="12" y2="13"/><line x1="12" y1="17" x2="12.01" y2="17"/></svg>
        </div>
        <div class="confirm-text">
          <p class="confirm-title">确定要删除规则 <strong>#{{ rule.id }}</strong> 吗？</p>
          <div class="rule-details-preview">
            <div class="preview-item">
              <span class="label">前端:</span>
              <span class="value">{{ rule.frontend_url }}</span>
            </div>
            <div class="preview-item">
              <span class="label">后端:</span>
              <span class="value">{{ rule.backend_url }}</span>
            </div>
          </div>
          <p class="warning-note">此操作不可撤销，且会立即触发 Nginx 配置应用。</p>
        </div>
      </div>
    </BaseModal>
  </Teleport>
</template>

<script setup>
import { ref, reactive } from 'vue'
import { useRuleStore } from '../stores/rules'
import BaseModal from './base/BaseModal.vue'

const props = defineProps({
  rule: {
    type: Object,
    required: true
  }
})

const ruleStore = useRuleStore()
const isEditing = ref(false)
const loading = ref(false)
const showDeleteModal = ref(false)
const isDeletingRule = ref(false)

const editForm = reactive({
  frontend_url: '',
  backend_url: ''
})

const startEdit = () => {
  editForm.frontend_url = props.rule.frontend_url
  editForm.backend_url = props.rule.backend_url
  isEditing.value = true
}

const cancelEdit = () => {
  isEditing.value = false
}

const handleSave = async () => {
  if (!editForm.frontend_url.trim() || !editForm.backend_url.trim()) return

  loading.value = true
  try {
    await ruleStore.modifyRule(props.rule.id, editForm.frontend_url, editForm.backend_url)
    isEditing.value = false
  } catch (err) {
    // 错误已由 store 处理
  } finally {
    loading.value = false
  }
}

const handleDelete = () => {
  showDeleteModal.value = true
}

const confirmDelete = async () => {
  isDeletingRule.value = true
  try {
    await ruleStore.removeRule(props.rule.id)
    showDeleteModal.value = false
  } catch (err) {
    // 错误已由 store 处理
  } finally {
    isDeletingRule.value = false
  }
}
</script>

<style scoped>
.url-text {
  font-family: var(--font-family-mono);
  font-size: 0.9rem;
  word-break: break-all;
}

/* 删除弹窗样式 */
.delete-confirm-content {
  display: flex;
  flex-direction: column;
  align-items: center;
  text-align: center;
  padding: 0 var(--spacing-sm);
}

.warning-icon {
  width: 56px;
  height: 56px;
  background: var(--color-danger-bg);
  color: var(--color-danger);
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  margin-bottom: var(--spacing-md);
  box-shadow: 0 0 0 6px rgba(244, 63, 94, 0.1);
}

.warning-icon svg {
  width: 28px;
  height: 28px;
  stroke: currentColor;
  stroke-width: 2;
  fill: none;
}

.confirm-text {
  width: 100%;
}

.confirm-title {
  margin: 0 0 var(--spacing-md) 0;
  font-size: 1.1rem;
  color: var(--color-heading);
}

.rule-details-preview {
  background: var(--color-bg-secondary);
  padding: var(--spacing-md);
  border-radius: var(--radius-md);
  margin: var(--spacing-md) 0;
  font-size: 0.85rem;
  font-family: var(--font-family-mono);
  text-align: left;
  border: 1px solid var(--color-border-light);
  width: 100%;
  box-sizing: border-box;
}

.preview-item {
  display: flex;
  gap: var(--spacing-sm);
  line-height: 1.6;
}

.preview-item .label {
  color: var(--color-text-muted);
  width: 45px;
  flex-shrink: 0;
}

.preview-item .value {
  color: var(--color-text-primary);
  word-break: break-all;
}

.warning-note {
  font-size: 0.85rem;
  color: var(--color-danger);
  margin: var(--spacing-sm) 0 0 0;
}

.inline-input {
  height: 32px !important;
  padding: 4px 8px !important;
  font-size: 0.85rem !important;
  font-family: var(--font-family-mono);
  background: var(--color-bg-primary) !important;
  border-radius: var(--radius-sm) !important;
}

.action-buttons {
  display: flex;
  gap: 8px;
}

.btn-icon {
  width: 34px;
  height: 34px;
  padding: 0;
  border-radius: var(--radius-md);
  background: var(--color-bg-secondary);
  color: var(--color-text-secondary);
  border: 1px solid var(--color-border);
  transition: all var(--transition-fast);
}

.btn-icon svg {
  width: 16px;
  height: 16px;
  stroke: currentColor;
  stroke-width: 2;
  fill: none;
}

.btn-icon:hover:not(:disabled) {
  background: var(--color-bg-primary);
  transform: translateY(-1px);
}

.btn-icon.primary:hover { color: var(--color-primary); border-color: var(--color-primary); }
.btn-icon.danger:hover { color: var(--color-danger); border-color: var(--color-danger); }
.btn-icon.success:hover { color: var(--color-success); border-color: var(--color-success); }

.loading-mini {
  width: 14px;
  height: 14px;
  border: 2px solid var(--color-border);
  border-top-color: var(--color-success);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

/* 编辑模式行高亮 */
.rule-row.is-editing {
  background: var(--color-primary-bg) !important;
}

/* Responsive Mobile Card Style */
@media (max-width: 768px) {
  .rule-row {
    display: block;
    background: var(--color-bg-card);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-md);
    margin-bottom: var(--spacing-md);
    padding: var(--spacing-md);
    box-shadow: var(--shadow-sm);
  }

  .rule-row.is-editing {
    border-color: var(--color-primary);
    box-shadow: 0 0 0 1px var(--color-primary);
  }

  td {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: var(--spacing-xs) 0 !important;
    border: none !important;
    text-align: right;
  }

  td::before {
    content: attr(data-label);
    font-weight: var(--font-weight-bold);
    color: var(--color-text-muted);
    font-size: 0.8rem;
    text-align: left;
  }

  .url-display, .url-edit {
    max-width: 70%;
    width: 100%;
  }

  .inline-input {
    width: 100%;
    text-align: right;
  }

  .col-actions {
    margin-top: var(--spacing-sm);
    padding-top: var(--spacing-sm) !important;
    border-top: 1px solid var(--color-border-light) !important;
  }

  .action-buttons {
    justify-content: flex-end;
    width: 100%;
  }
}
</style>

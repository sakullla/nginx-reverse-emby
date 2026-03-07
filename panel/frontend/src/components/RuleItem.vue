<template>
  <div class="rule-card" :class="{ 'is-editing': isEditing }">
    <div class="card-header">
      <div class="rule-badge">#{{ rule.id }}</div>
      <div class="card-actions">
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
    </div>

    <div class="card-body">
      <div class="url-group">
        <div class="url-label">
          <svg class="icon-sm" viewBox="0 0 24 24"><circle cx="12" cy="12" r="10"/><line x1="2" y1="12" x2="22" y2="12"/><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/></svg>
          <span>前端 URL</span>
        </div>
        <div class="url-content">
          <span v-if="!isEditing" class="url-text">{{ rule.frontend_url }}</span>
          <input
            v-else
            v-model="editForm.frontend_url"
            type="text"
            class="card-input"
            placeholder="前端 URL"
          />
        </div>
      </div>

      <div class="url-divider">
        <svg viewBox="0 0 24 24"><polyline points="7 13 12 18 17 13"/><polyline points="7 6 12 11 17 6"/></svg>
      </div>

      <div class="url-group">
        <div class="url-label">
          <svg class="icon-sm" viewBox="0 0 24 24"><rect x="2" y="2" width="20" height="8" rx="2" ry="2"/><rect x="2" y="14" width="20" height="8" rx="2" ry="2"/><line x1="6" y1="6" x2="6.01" y2="6"/><line x1="6" y1="18" x2="6.01" y2="18"/></svg>
          <span>后端 URL</span>
        </div>
        <div class="url-content">
          <span v-if="!isEditing" class="url-text">{{ rule.backend_url }}</span>
          <input
            v-else
            v-model="editForm.backend_url"
            type="text"
            class="card-input"
            placeholder="后端 URL"
          />
        </div>
      </div>
    </div>

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
  </div>
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
.rule-card {
  background: var(--color-bg-card);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-xl);
  padding: var(--spacing-lg);
  display: flex;
  flex-direction: column;
  gap: var(--spacing-md);
  transition: all var(--transition-base);
  box-shadow: var(--shadow-sm);
  position: relative;
  overflow: hidden;
}

.rule-card:hover {
  transform: translateY(-2px);
  box-shadow: var(--shadow-md);
  border-color: var(--color-primary-light);
}

.rule-card.is-editing {
  border-color: var(--color-primary);
  box-shadow: 0 0 0 1px var(--color-primary);
  background: var(--color-primary-bg);
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.rule-badge {
  background: var(--color-bg-tertiary);
  color: var(--color-text-secondary);
  padding: 2px 10px;
  border-radius: var(--radius-full);
  font-size: var(--font-size-xs);
  font-weight: var(--font-weight-bold);
  font-family: var(--font-family-mono);
}

.card-actions {
  display: flex;
  gap: 8px;
}

.btn-icon {
  width: 32px;
  height: 32px;
  padding: 0;
  border-radius: var(--radius-md);
  background: var(--color-bg-secondary);
  color: var(--color-text-secondary);
  border: 1px solid var(--color-border);
  transition: all var(--transition-fast);
  display: flex;
  align-items: center;
  justify-content: center;
}

.btn-icon svg {
  width: 14px;
  height: 14px;
  stroke: currentColor;
  stroke-width: 2.5;
  fill: none;
}

.btn-icon:hover:not(:disabled) {
  background: var(--color-bg-primary);
  transform: translateY(-1px);
}

.btn-icon.primary:hover { color: var(--color-primary); border-color: var(--color-primary); }
.btn-icon.danger:hover { color: var(--color-danger); border-color: var(--color-danger); }
.btn-icon.success:hover { color: var(--color-success); border-color: var(--color-success); }

.card-body {
  display: flex;
  flex-direction: column;
  gap: var(--spacing-xs);
}

.url-group {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.url-label {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: var(--font-size-xs);
  color: var(--color-text-muted);
  font-weight: var(--font-weight-semibold);
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.icon-sm {
  width: 12px;
  height: 12px;
  stroke: currentColor;
  stroke-width: 3;
  fill: none;
}

.url-content {
  min-height: 24px;
  display: flex;
  align-items: center;
}

.url-text {
  font-family: var(--font-family-mono);
  font-size: 0.9rem;
  word-break: break-all;
  color: var(--color-text-primary);
}

.card-input {
  width: 100%;
  height: 36px !important;
  padding: 4px 10px !important;
  font-size: 0.85rem !important;
  font-family: var(--font-family-mono);
  background: var(--color-bg-primary) !important;
  border-radius: var(--radius-md) !important;
  border: 1px solid var(--color-border);
}

.url-divider {
  display: flex;
  justify-content: center;
  color: var(--color-text-muted);
  opacity: 0.2;
  margin: 2px 0;
}

.url-divider svg {
  width: 16px;
  height: 16px;
  stroke: currentColor;
  stroke-width: 3;
  fill: none;
}

.loading-mini {
  width: 14px;
  height: 14px;
  border: 2px solid var(--color-border);
  border-top-color: var(--color-success);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
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
</style>

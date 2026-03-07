<template>
  <div class="rule-card">
    <div class="card-header">
      <div class="rule-info">
        <div class="rule-badge">
          <span class="badge-hash">#</span>
          <span class="badge-id">{{ rule.id }}</span>
        </div>
        <div class="rule-status">
          <span class="status-dot"></span>
          <span class="status-text">运行中</span>
        </div>
      </div>
      <div class="card-actions">
        <button @click="showEditModal = true" class="btn-action edit" title="编辑规则">
          <svg viewBox="0 0 24 24"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/></svg>
        </button>
        <button @click="handleDelete" class="btn-action delete" title="删除规则">
          <svg viewBox="0 0 24 24"><path d="M3 6h18m-2 0v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6m3 0V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2"/></svg>
        </button>
      </div>
    </div>

    <div class="card-body">
      <div class="url-section frontend">
        <div class="url-header">
          <div class="url-icon-wrapper">
            <svg class="url-icon" viewBox="0 0 24 24">
              <circle cx="12" cy="12" r="10"/>
              <line x1="2" y1="12" x2="22" y2="12"/>
              <path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/>
            </svg>
          </div>
          <span class="url-label">前端地址</span>
        </div>
        <div class="url-content">
          <span class="url-value">{{ rule.frontend_url }}</span>
        </div>
      </div>

      <div class="flow-divider">
        <div class="flow-line"></div>
        <div class="flow-icon">
          <svg viewBox="0 0 24 24">
            <polyline points="7 13 12 18 17 13"/>
            <polyline points="7 6 12 11 17 6"/>
          </svg>
        </div>
      </div>

      <div class="url-section backend">
        <div class="url-header">
          <div class="url-icon-wrapper">
            <svg class="url-icon" viewBox="0 0 24 24">
              <rect x="2" y="2" width="20" height="8" rx="2" ry="2"/>
              <rect x="2" y="14" width="20" height="8" rx="2" ry="2"/>
              <line x1="6" y1="6" x2="6.01" y2="6"/>
              <line x1="6" y1="18" x2="6.01" y2="18"/>
            </svg>
          </div>
          <span class="url-label">后端地址</span>
        </div>
        <div class="url-content">
          <span class="url-value">{{ rule.backend_url }}</span>
        </div>
      </div>
    </div>

    <!-- 编辑弹窗 -->
    <Teleport to="body">
      <BaseModal
        v-model="showEditModal"
        title="修改代理规则"
        subtitle="更新您的前端转发规则或后端目标地址"
        :show-default-footer="false"
      >
        <RuleForm :initial-data="rule" @success="showEditModal = false" />
      </BaseModal>
    </Teleport>

    <!-- 删除确认弹窗 -->
    <Teleport to="body">
      <BaseModal
        v-model="showDeleteModal"
        title="确认删除规则"
        subtitle="该操作将立即停止相关的流量转发"
        confirm-text="确认删除"
        confirm-variant="danger"
        :loading="isDeletingRule"
        @confirm="confirmDelete"
      >
        <div class="delete-confirm-content">
          <div class="warning-banner">
            <div class="warning-icon-wrapper">
              <svg viewBox="0 0 24 24"><path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/><line x1="12" y1="9" x2="12" y2="13"/><line x1="12" y1="17" x2="12.01" y2="17"/></svg>
            </div>
            <div class="warning-text">
              <h3 class="confirm-msg-title">确定要移除该规则吗？</h3>
              <p class="confirm-msg-desc">此操作不可撤销，前端请求将不再转发。</p>
            </div>
          </div>

          <div class="rule-preview-card">
            <div class="preview-header">
              <span class="preview-badge">规则 #{{ rule.id }}</span>
            </div>
            <div class="preview-body">
              <div class="preview-row">
                <span class="p-label">前端 URL</span>
                <span class="p-value">{{ rule.frontend_url }}</span>
              </div>
              <div class="preview-divider"></div>
              <div class="preview-row">
                <span class="p-label">后端 URL</span>
                <span class="p-value">{{ rule.backend_url }}</span>
              </div>
            </div>
          </div>
        </div>
      </BaseModal>
    </Teleport>
    </div>
    </template>
<script setup>
import { ref } from 'vue'
import { useRuleStore } from '../stores/rules'
import BaseModal from './base/BaseModal.vue'
import RuleForm from './RuleForm.vue'

const props = defineProps({
  rule: {
    type: Object,
    required: true
  }
})

const ruleStore = useRuleStore()
const showEditModal = ref(false)
const showDeleteModal = ref(false)
const isDeletingRule = ref(false)

const handleDelete = () => {
  showDeleteModal.value = true
}

const confirmDelete = async () => {
  isDeletingRule.value = true
  try {
    await ruleStore.removeRule(props.rule.id)
    showDeleteModal.value = false
  } catch (err) {
    // 错误已处理
  } finally {
    isDeletingRule.value = false
  }
}
</script>

<style scoped>
.rule-card {
  background: var(--color-bg-card);
  border: 1.5px solid var(--color-border);
  border-radius: var(--radius-2xl);
  padding: 0;
  display: flex;
  flex-direction: column;
  transition: all 0.3s cubic-bezier(0.4, 0, 0.2, 1);
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.05);
  position: relative;
  overflow: hidden;
}

.rule-card::before {
  content: '';
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  height: 3px;
  background: var(--gradient-primary);
  opacity: 0;
  transition: opacity 0.3s ease;
}

.rule-card:hover {
  transform: translateY(-2px);
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.08);
  border-color: var(--color-primary-lighter);
}

.rule-card:hover::before {
  opacity: 1;
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: var(--spacing-lg);
  background: var(--color-bg-secondary);
  border-bottom: 1px solid var(--color-border-light);
}

.rule-info {
  display: flex;
  align-items: center;
  gap: var(--spacing-md);
}

.rule-badge {
  display: flex;
  align-items: center;
  gap: 2px;
  background: linear-gradient(135deg, var(--color-primary-bg) 0%, var(--color-primary-lighter) 100%);
  padding: 6px 14px;
  border-radius: var(--radius-full);
  font-family: var(--font-family-mono);
  box-shadow: inset 0 0 0 1px var(--color-primary-light);
}

.badge-hash {
  font-size: 0.7rem;
  font-weight: 600;
  color: var(--color-primary);
  opacity: 0.7;
}

.badge-id {
  font-size: 0.85rem;
  font-weight: 800;
  color: var(--color-primary);
}

.rule-status {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 4px 10px;
  background: var(--color-success-bg);
  border-radius: var(--radius-full);
  border: 1px solid var(--color-success-light);
}

.status-dot {
  width: 6px;
  height: 6px;
  background: var(--color-success);
  border-radius: 50%;
  animation: pulse 2s cubic-bezier(0.4, 0, 0.6, 1) infinite;
}

@keyframes pulse {
  0%, 100% {
    opacity: 1;
    transform: scale(1);
  }
  50% {
    opacity: 0.6;
    transform: scale(1.1);
  }
}

.status-text {
  font-size: 0.7rem;
  font-weight: 700;
  color: var(--color-success);
  text-transform: uppercase;
  letter-spacing: 0.03em;
}

.card-actions {
  display: flex;
  gap: 6px;
}

.btn-action {
  width: 34px;
  height: 34px;
  padding: 0;
  border-radius: var(--radius-lg);
  background: var(--color-bg-primary);
  border: 1px solid var(--color-border);
  transition: all 0.2s ease;
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
}

.btn-action svg {
  width: 15px;
  height: 15px;
  stroke: var(--color-text-tertiary);
  stroke-width: 2.2;
  fill: none;
  transition: all 0.2s ease;
}

.btn-action:hover {
  transform: translateY(-1px);
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.08);
}

.btn-action.edit:hover {
  background: var(--color-primary-bg);
  border-color: var(--color-primary-light);
}

.btn-action.edit:hover svg {
  stroke: var(--color-primary);
}

.btn-action.delete:hover {
  background: var(--color-danger-bg);
  border-color: var(--color-danger-light);
}

.btn-action.delete:hover svg {
  stroke: var(--color-danger);
}

.card-body {
  padding: var(--spacing-lg);
  display: flex;
  flex-direction: column;
  gap: var(--spacing-sm);
}

.url-section {
  background: var(--color-bg-secondary);
  border: 1px solid var(--color-border-light);
  border-radius: var(--radius-xl);
  padding: var(--spacing-md);
  transition: all 0.2s ease;
}

.rule-card:hover .url-section {
  background: var(--color-bg-primary);
  border-color: var(--color-border);
}

.url-section.frontend {
  border-left: 3px solid var(--color-primary);
}

.url-section.backend {
  border-left: 3px solid var(--color-secondary);
}

.url-header {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 10px;
}

.url-icon-wrapper {
  width: 26px;
  height: 26px;
  border-radius: var(--radius-md);
  display: flex;
  align-items: center;
  justify-content: center;
  transition: all 0.2s ease;
}

.frontend .url-icon-wrapper {
  background: var(--color-primary-bg);
  color: var(--color-primary);
}

.backend .url-icon-wrapper {
  background: var(--color-secondary-bg);
  color: var(--color-secondary);
}

.url-icon {
  width: 13px;
  height: 13px;
  stroke: currentColor;
  stroke-width: 2.5;
  fill: none;
}

.url-label {
  font-size: 0.7rem;
  font-weight: 800;
  color: var(--color-text-muted);
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.url-content {
  padding-left: 34px;
}

.url-value {
  font-family: var(--font-family-mono);
  font-size: 0.9rem;
  font-weight: 500;
  color: var(--color-heading);
  word-break: break-all;
  line-height: 1.6;
  display: block;
}

.flow-divider {
  position: relative;
  height: 32px;
  display: flex;
  align-items: center;
  justify-content: center;
  margin: -4px 0;
}

.flow-line {
  position: absolute;
  left: 0;
  right: 0;
  height: 1px;
  background: repeating-linear-gradient(
    to right,
    var(--color-border) 0px,
    var(--color-border) 4px,
    transparent 4px,
    transparent 8px
  );
  opacity: 0.5;
}

.flow-icon {
  position: relative;
  width: 32px;
  height: 32px;
  background: var(--color-bg-card);
  border: 1.5px solid var(--color-border);
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--color-text-muted);
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.04);
  transition: all 0.3s ease;
}

.rule-card:hover .flow-icon {
  border-color: var(--color-primary-light);
  color: var(--color-primary);
  transform: scale(1.1);
}

.flow-icon svg {
  width: 16px;
  height: 16px;
  stroke: currentColor;
  stroke-width: 2.5;
  fill: none;
}

/* Delete Modal Refinement */
.delete-confirm-content {
  display: flex;
  flex-direction: column;
  gap: var(--spacing-xl);
  padding: var(--spacing-sm) 0;
}

.warning-banner {
  display: flex;
  align-items: center;
  gap: var(--spacing-md);
  background: var(--color-danger-bg);
  padding: var(--spacing-md) var(--spacing-lg);
  border-radius: var(--radius-xl);
  border: 1px solid var(--color-danger-light);
}

.warning-icon-wrapper {
  width: 48px;
  height: 48px;
  background: white;
  color: var(--color-danger);
  border-radius: var(--radius-lg);
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  box-shadow: 0 4px 12px rgba(244, 63, 94, 0.1);
}

.warning-icon-wrapper svg {
  width: 24px;
  height: 24px;
  stroke: currentColor;
  stroke-width: 2.2;
  fill: none;
}

.confirm-msg-title {
  font-size: 1.15rem;
  font-weight: 800;
  color: var(--color-danger-dark);
  margin: 0;
  letter-spacing: -0.01em;
}

.confirm-msg-desc {
  color: var(--color-danger);
  font-size: 0.85rem;
  font-weight: 500;
  margin: 4px 0 0;
  opacity: 0.85;
}

.rule-preview-card {
  background: var(--color-bg-secondary);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-xl);
  overflow: hidden;
}

.preview-header {
  padding: var(--spacing-sm) var(--spacing-md);
  background: var(--color-bg-tertiary);
  border-bottom: 1px solid var(--color-border-light);
}

.preview-badge {
  font-size: 0.75rem;
  font-weight: 800;
  color: var(--color-text-tertiary);
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.preview-body {
  padding: var(--spacing-md);
  display: flex;
  flex-direction: column;
  gap: var(--spacing-md);
}

.preview-row {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.preview-divider {
  height: 1px;
  background: var(--color-border-light);
  margin: 2px 0;
}

.p-label {
  font-size: 0.7rem;
  font-weight: 700;
  color: var(--color-text-muted);
  text-transform: uppercase;
  letter-spacing: 0.02em;
}

.p-value {
  font-size: 0.9rem;
  font-family: var(--font-family-mono);
  color: var(--color-heading);
  word-break: break-all;
  line-height: 1.4;
  font-weight: 600;
}

@media (max-width: 768px) {
  .rule-card {
    border-radius: var(--radius-xl);
  }

  .card-header {
    padding: var(--spacing-md);
  }

  .card-body {
    padding: var(--spacing-md);
  }

  .rule-badge {
    padding: 5px 12px;
  }

  .badge-id {
    font-size: 0.8rem;
  }

  .rule-status {
    padding: 3px 8px;
  }

  .status-text {
    font-size: 0.65rem;
  }

  .btn-action {
    width: 32px;
    height: 32px;
  }

  .btn-action svg {
    width: 14px;
    height: 14px;
  }

  .url-section {
    padding: var(--spacing-sm);
  }

  .url-header {
    margin-bottom: 8px;
  }

  .url-content {
    padding-left: 30px;
  }

  .url-value {
    font-size: 0.85rem;
  }

  .warning-banner {
    flex-direction: column;
    text-align: center;
    padding: var(--spacing-lg);
  }

  .warning-icon-wrapper {
    width: 44px;
    height: 44px;
  }

  .confirm-msg-title {
    font-size: 1.05rem;
  }
}
</style>

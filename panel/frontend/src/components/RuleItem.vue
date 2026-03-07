<template>
  <div class="rule-card">
    <div class="card-header">
      <div class="rule-info">
        <div class="rule-badge">#{{ rule.id }}</div>
        <span class="rule-status-dot"></span>
      </div>
      <div class="card-actions">
        <button @click="showEditModal = true" class="btn-action primary" title="编辑规则">
          <svg viewBox="0 0 24 24"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/></svg>
        </button>
        <button @click="handleDelete" class="btn-action danger" title="删除规则">
          <svg viewBox="0 0 24 24"><path d="M3 6h18m-2 0v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6m3 0V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2"/></svg>
        </button>
      </div>
    </div>

    <div class="card-body">
      <div class="url-row">
        <div class="url-meta">
          <div class="url-icon-bg primary">
            <svg class="url-icon" viewBox="0 0 24 24"><circle cx="12" cy="12" r="10"/><line x1="2" y1="12" x2="22" y2="12"/><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/></svg>
          </div>
          <span class="url-label">前端 URL</span>
        </div>
        <div class="url-value-container">
          <span class="url-value">{{ rule.frontend_url }}</span>
        </div>
      </div>

      <div class="url-flow-indicator">
        <div class="flow-line"></div>
        <div class="flow-arrow">
          <svg viewBox="0 0 24 24"><polyline points="7 13 12 18 17 13"/><polyline points="7 6 12 11 17 6"/></svg>
        </div>
      </div>

      <div class="url-row">
        <div class="url-meta">
          <div class="url-icon-bg secondary">
            <svg class="url-icon" viewBox="0 0 24 24"><rect x="2" y="2" width="20" height="8" rx="2" ry="2"/><rect x="2" y="14" width="20" height="8" rx="2" ry="2"/><line x1="6" y1="6" x2="6.01" y2="6"/><line x1="6" y1="18" x2="6.01" y2="18"/></svg>
          </div>
          <span class="url-label">后端 URL</span>
        </div>
        <div class="url-value-container">
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
          <div class="warning-icon-wrapper">
            <svg viewBox="0 0 24 24"><path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/><line x1="12" y1="9" x2="12" y2="13"/><line x1="12" y1="17" x2="12.01" y2="17"/></svg>
          </div>
          <h3 class="confirm-msg-title">确定要移除该规则吗？</h3>
          <p class="confirm-msg-desc">此操作将立即从 Nginx 配置中移除，前端请求将不再转发。该操作不可撤销。</p>

          <div class="rule-preview-box">
            <div class="preview-row">
              <span class="p-label">ID:</span>
              <span class="p-value">#{{ rule.id }}</span>
            </div>
            <div class="preview-row">
              <span class="p-label">前端:</span>
              <span class="p-value">{{ rule.frontend_url }}</span>
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
  border: 1px solid var(--color-border);
  border-radius: var(--radius-2xl);
  padding: var(--spacing-lg);
  display: flex;
  flex-direction: column;
  gap: var(--spacing-md);
  transition: all var(--transition-slow);
  box-shadow: var(--shadow-sm);
  position: relative;
  overflow: hidden;
}

.rule-card:hover {
  transform: translateY(-4px);
  box-shadow: var(--shadow-xl);
  border-color: var(--color-primary-light);
}

.rule-card::after {
  content: '';
  position: absolute;
  top: 0;
  left: 0;
  width: 100%;
  height: 4px;
  background: var(--gradient-primary);
  opacity: 0;
  transition: opacity var(--transition-base);
}

.rule-card:hover::after {
  opacity: 1;
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding-bottom: var(--spacing-sm);
  border-bottom: 1px solid var(--color-border-light);
}

.rule-info {
  display: flex;
  align-items: center;
  gap: 10px;
}

.rule-badge {
  background: var(--color-bg-tertiary);
  color: var(--color-primary);
  padding: 4px 12px;
  border-radius: var(--radius-full);
  font-size: var(--font-size-xs);
  font-weight: 800;
  font-family: var(--font-family-mono);
  box-shadow: inset 0 0 0 1px var(--color-primary-lighter);
}

.rule-status-dot {
  width: 8px;
  height: 8px;
  background: var(--color-success);
  border-radius: 50%;
  box-shadow: 0 0 0 3px var(--color-success-bg);
  animation: pulse 2s infinite;
}

@keyframes pulse {
  0% { transform: scale(0.95); box-shadow: 0 0 0 0 rgba(16, 185, 129, 0.4); }
  70% { transform: scale(1); box-shadow: 0 0 0 6px rgba(16, 185, 129, 0); }
  100% { transform: scale(0.95); box-shadow: 0 0 0 0 rgba(16, 185, 129, 0); }
}

.card-actions {
  display: flex;
  gap: 8px;
}

.btn-action {
  width: 36px;
  height: 36px;
  padding: 0;
  border-radius: var(--radius-lg);
  background: var(--color-bg-secondary);
  color: var(--color-text-tertiary);
  border: 1px solid var(--color-border);
  transition: all var(--transition-base);
  display: flex;
  align-items: center;
  justify-content: center;
}

.btn-action svg {
  width: 16px;
  height: 16px;
  stroke: currentColor;
  stroke-width: 2.2;
  fill: none;
}

.btn-action:hover {
  background: var(--color-bg-primary);
  transform: translateY(-2px);
  box-shadow: var(--shadow-md);
}

.btn-action.primary:hover {
  color: var(--color-primary);
  border-color: var(--color-primary-light);
  background: var(--color-primary-bg);
}

.btn-action.danger:hover {
  color: var(--color-danger);
  border-color: var(--color-danger-light);
  background: var(--color-danger-bg);
}

.card-body {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.url-row {
  background: var(--color-bg-secondary);
  padding: var(--spacing-md);
  border-radius: var(--radius-xl);
  border: 1px solid var(--color-border-light);
  transition: all var(--transition-base);
}

.rule-card:hover .url-row {
  border-color: var(--color-border);
  background: var(--color-bg-primary);
}

.url-meta {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 8px;
}

.url-icon-bg {
  width: 28px;
  height: 28px;
  border-radius: var(--radius-md);
  display: flex;
  align-items: center;
  justify-content: center;
}

.url-icon-bg.primary {
  background: var(--color-primary-bg);
  color: var(--color-primary);
}

.url-icon-bg.secondary {
  background: var(--color-secondary-bg);
  color: var(--color-secondary);
}

.url-icon {
  width: 14px;
  height: 14px;
  stroke: currentColor;
  stroke-width: 2.5;
  fill: none;
}

.url-label {
  font-size: var(--font-size-xs);
  color: var(--color-text-muted);
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.url-value-container {
  padding-left: 2px;
}

.url-value {
  font-family: var(--font-family-mono);
  font-size: 0.95rem;
  word-break: break-all;
  color: var(--color-heading);
  line-height: 1.5;
  font-weight: 500;
}

.url-flow-indicator {
  position: relative;
  height: 24px;
  display: flex;
  justify-content: center;
  align-items: center;
  margin: -8px 0;
  z-index: 1;
}

.flow-line {
  position: absolute;
  left: 28px; /* Align with icons center approx */
  top: -10px;
  bottom: -10px;
  width: 2px;
  background: repeating-linear-gradient(
    to bottom,
    transparent,
    transparent 4px,
    var(--color-border) 4px,
    var(--color-border) 8px
  );
  display: none; /* Keep it clean for now */
}

.flow-arrow {
  background: var(--color-bg-card);
  border: 1px solid var(--color-border);
  width: 28px;
  height: 28px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--color-text-muted);
  box-shadow: var(--shadow-sm);
}

.flow-arrow svg {
  width: 16px;
  height: 16px;
  stroke: currentColor;
  stroke-width: 2.5;
  fill: none;
}

/* Delete Modal Enhancements */
.delete-confirm-content {
  text-align: center;
  padding: var(--spacing-md) 0;
}

.warning-icon-wrapper {
  width: 64px;
  height: 64px;
  background: var(--color-danger-bg);
  color: var(--color-danger);
  border-radius: 20px;
  display: flex;
  align-items: center;
  justify-content: center;
  margin: 0 auto var(--spacing-lg);
  transform: rotate(-5deg);
}

.warning-icon-wrapper svg {
  width: 32px;
  height: 32px;
  stroke: currentColor;
  stroke-width: 2;
  fill: none;
}

.confirm-msg-title {
  font-size: 1.25rem;
  color: var(--color-heading);
  margin-bottom: var(--spacing-sm);
}

.confirm-msg-desc {
  color: var(--color-text-secondary);
  font-size: var(--font-size-sm);
  line-height: 1.6;
  margin-bottom: var(--spacing-xl);
}

.rule-preview-box {
  background: var(--color-bg-secondary);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  padding: var(--spacing-md);
  text-align: left;
}

.preview-row {
  display: flex;
  gap: 12px;
  margin-bottom: 6px;
  font-size: 0.9rem;
  font-family: var(--font-family-mono);
}

.preview-row:last-child {
  margin-bottom: 0;
}

.p-label {
  color: var(--color-text-muted);
  width: 50px;
  flex-shrink: 0;
}

.p-value {
  color: var(--color-heading);
  word-break: break-all;
}
</style>

<template>
  <Teleport to="body">
    <Transition name="dialog">
      <div v-if="show" class="delete-dialog-overlay" @click.self="handleCancel">
        <div class="delete-dialog">
          <!-- 图标区域 -->
          <div class="delete-dialog__icon-wrapper">
            <div class="delete-dialog__icon">
              <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <polyline points="3 6 5 6 21 6"/>
                <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
                <line x1="10" y1="11" x2="10" y2="17"/>
                <line x1="14" y1="11" x2="14" y2="17"/>
              </svg>
            </div>
          </div>

          <!-- 标题 -->
          <h3 class="delete-dialog__title">{{ title }}</h3>

          <!-- 内容 -->
          <p class="delete-dialog__message">
            <slot>{{ message }}</slot>
          </p>

          <!-- 高亮显示的名称 -->
          <div v-if="name" class="delete-dialog__highlight">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>
              <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>
            </svg>
            <span>{{ name }}</span>
          </div>

          <!-- 警告提示 -->
          <div class="delete-dialog__warning">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <circle cx="12" cy="12" r="10"/>
              <line x1="12" y1="8" x2="12" y2="12"/>
              <line x1="12" y1="16" x2="12.01" y2="16"/>
            </svg>
            <span>此操作不可撤销，请谨慎操作</span>
          </div>

          <!-- 按钮区域 -->
          <div class="delete-dialog__actions">
            <button class="delete-dialog__btn delete-dialog__btn--cancel" @click="handleCancel">
              取消
            </button>
            <button
              class="delete-dialog__btn delete-dialog__btn--confirm"
              :class="{ 'delete-dialog__btn--loading': loading }"
              :disabled="loading"
              @click="handleConfirm"
            >
              <span v-if="loading" class="delete-dialog__spinner"></span>
              <svg v-else width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <polyline points="3 6 5 6 21 6"/>
                <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
              </svg>
              {{ confirmText }}
            </button>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup>
const props = defineProps({
  show: { type: Boolean, required: true },
  title: { type: String, default: '确认删除' },
  message: { type: String, default: '确定要删除以下项目吗？' },
  name: { type: String, default: '' },
  confirmText: { type: String, default: '确认删除' },
  loading: { type: Boolean, default: false }
})

const emit = defineEmits(['confirm', 'cancel'])

const handleConfirm = () => {
  if (!props.loading) {
    emit('confirm')
  }
}

const handleCancel = () => {
  if (!props.loading) {
    emit('cancel')
  }
}
</script>

<style scoped>
/* 遮罩层 */
.delete-dialog-overlay {
  position: fixed;
  inset: 0;
  background: rgba(37, 23, 54, 0.4);
  backdrop-filter: blur(8px);
  -webkit-backdrop-filter: blur(8px);
  z-index: var(--z-modal);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--space-4);
  transition: opacity 0.3s ease;
}

/* 对话框 */
.delete-dialog {
  background: var(--color-bg-surface);
  border-radius: var(--radius-2xl);
  box-shadow: var(--shadow-2xl);
  border: 1.5px solid var(--color-border-default);
  width: min(420px, 90vw);
  max-width: 100%;
  padding: var(--space-6);
  text-align: center;
  transform-origin: center;
  transition: transform 0.3s cubic-bezier(0.34, 1.56, 0.64, 1), opacity 0.3s ease;
}

/* 图标区域 */
.delete-dialog__icon-wrapper {
  margin-bottom: var(--space-4);
}

.delete-dialog__icon {
  width: 64px;
  height: 64px;
  border-radius: 50%;
  background: var(--color-danger-50);
  color: var(--color-danger);
  display: flex;
  align-items: center;
  justify-content: center;
  margin: 0 auto;
  animation: iconPulse 2s ease-in-out infinite;
}

@keyframes iconPulse {
  0%, 100% { transform: scale(1); box-shadow: 0 0 0 0 var(--color-danger-50); }
  50% { transform: scale(1.05); box-shadow: 0 0 0 10px transparent; }
}

/* 标题 */
.delete-dialog__title {
  font-size: 1.25rem;
  font-weight: 700;
  color: var(--color-text-primary);
  margin: 0 0 var(--space-2);
  letter-spacing: -0.02em;
}

/* 消息文本 */
.delete-dialog__message {
  font-size: 0.9375rem;
  color: var(--color-text-secondary);
  line-height: 1.5;
  margin: 0 0 var(--space-4);
}

/* 高亮显示的名称 */
.delete-dialog__highlight {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  padding: var(--space-3) var(--space-4);
  background: var(--color-bg-subtle);
  border-radius: var(--radius-lg);
  margin-bottom: var(--space-4);
  font-family: var(--font-mono);
  font-size: 0.875rem;
  color: var(--color-text-primary);
  word-break: break-all;
}

.delete-dialog__highlight svg {
  flex-shrink: 0;
  color: var(--color-text-tertiary);
}

/* 警告提示 */
.delete-dialog__warning {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  padding: var(--space-3);
  background: var(--color-warning-50);
  border-radius: var(--radius-lg);
  margin-bottom: var(--space-5);
  font-size: 0.8125rem;
  color: var(--color-warning);
  font-weight: 500;
}

.delete-dialog__warning svg {
  flex-shrink: 0;
  color: var(--color-warning);
}

/* 按钮区域 */
.delete-dialog__actions {
  display: flex;
  gap: var(--space-3);
  justify-content: center;
}

/* 按钮基础样式 */
.delete-dialog__btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  padding: 0.625rem 1.25rem;
  border-radius: var(--radius-lg);
  font-size: 0.9375rem;
  font-weight: 600;
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-default);
  border: none;
  font-family: inherit;
  min-width: 100px;
}

.delete-dialog__btn:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

/* 取消按钮 */
.delete-dialog__btn--cancel {
  background: var(--color-bg-subtle);
  color: var(--color-text-secondary);
  border: 1px solid var(--color-border-default);
}

.delete-dialog__btn--cancel:hover:not(:disabled) {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
  transform: translateY(-1px);
}

/* 确认删除按钮 - 使用 danger 色系 */
.delete-dialog__btn--confirm {
  background: var(--color-danger);
  color: var(--color-text-inverse);
  box-shadow: var(--shadow-sm);
}

.delete-dialog__btn--confirm:hover:not(:disabled) {
  background: var(--color-danger);
  filter: brightness(0.9);
  transform: translateY(-1px);
  box-shadow: var(--shadow-md);
}

.delete-dialog__btn--confirm:active:not(:disabled) {
  transform: translateY(0);
}

/* 加载状态 */
.delete-dialog__btn--loading {
  position: relative;
  color: transparent;
}

.delete-dialog__spinner {
  position: absolute;
  width: 18px;
  height: 18px;
  border: 2px solid rgba(255, 255, 255, 0.3);
  border-top-color: white;
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

/* 入场/离场动画 */
.dialog-enter-active,
.dialog-leave-active {
  transition: opacity 0.3s ease;
}

.dialog-enter-active .delete-dialog,
.dialog-leave-active .delete-dialog {
  transition: transform 0.3s cubic-bezier(0.34, 1.56, 0.64, 1), opacity 0.3s ease;
}

.dialog-enter-from,
.dialog-leave-to {
  opacity: 0;
}

.dialog-enter-from .delete-dialog,
.dialog-leave-to .delete-dialog {
  opacity: 0;
  transform: scale(0.9) translateY(20px);
}

/* 移动端适配 */
@media (max-width: 640px) {
  .delete-dialog {
    padding: var(--space-5);
    width: min(360px, 92vw);
  }

  .delete-dialog__icon {
    width: 56px;
    height: 56px;
  }

  .delete-dialog__title {
    font-size: 1.125rem;
  }

  .delete-dialog__message {
    font-size: 0.875rem;
  }

  .delete-dialog__actions {
    flex-direction: column-reverse;
  }

  .delete-dialog__btn {
    width: 100%;
    min-width: auto;
  }
}

/* 减少动效偏好 */
@media (prefers-reduced-motion: reduce) {
  .delete-dialog-overlay,
  .delete-dialog,
  .delete-dialog__icon,
  .delete-dialog__btn,
  .delete-dialog__spinner {
    transition: none;
    animation: none;
  }
}
</style>

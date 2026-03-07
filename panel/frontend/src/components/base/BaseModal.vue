<template>
  <Transition name="modal-fade">
    <div v-if="modelValue" class="modal-overlay" @click="handleBackdropClick">
      <div
        class="modal-wrapper"
        :class="{ 'modal-mobile-bottom': mobileBottom }"
        @click.stop
      >
        <!-- Modal Header -->
        <div class="modal-header">
          <div class="header-main">
            <h3 class="modal-title">{{ title }}</h3>
            <p v-if="subtitle" class="modal-subtitle">{{ subtitle }}</p>
          </div>
          <button class="modal-close-btn" @click="close" aria-label="关闭">
            <svg viewBox="0 0 24 24"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
          </button>
        </div>

        <!-- Modal Body -->
        <div class="modal-body" :class="{ 'no-padding': noPadding }">
          <slot />
        </div>

        <!-- Modal Footer -->
        <div class="modal-footer" v-if="$slots.footer || showDefaultFooter">
          <slot name="footer">
            <button class="btn-modal-cancel" @click="close">
              {{ cancelText }}
            </button>
            <button
              :class="['btn-modal-confirm', confirmVariant]"
              @click="confirm"
              :disabled="loading"
            >
              <div v-if="loading" class="loading-spinner"></div>
              <span v-else>{{ confirmText }}</span>
            </button>
          </slot>
        </div>
      </div>
    </div>
  </Transition>
</template>

<script setup>
import { onMounted, onUnmounted } from 'vue'

const props = defineProps({
  modelValue: { type: Boolean, default: false },
  title: { type: String, default: '确认' },
  subtitle: { type: String, default: '' },
  confirmText: { type: String, default: '确定' },
  cancelText: { type: String, default: '取消' },
  confirmVariant: { type: String, default: 'primary' },
  loading: { type: Boolean, default: false },
  showDefaultFooter: { type: Boolean, default: true },
  closeOnBackdrop: { type: Boolean, default: true },
  noPadding: { type: Boolean, default: false },
  mobileBottom: { type: Boolean, default: true }
})

const emit = defineEmits(['update:modelValue', 'confirm', 'close'])

const close = () => {
  if (props.loading) return
  emit('update:modelValue', false)
  emit('close')
}

const confirm = () => { emit('confirm') }

const handleBackdropClick = () => {
  if (props.closeOnBackdrop) close()
}

const handleEsc = (e) => {
  if (e.key === 'Escape' && props.modelValue) close()
}

onMounted(() => { window.addEventListener('keydown', handleEsc) })
onUnmounted(() => { window.removeEventListener('keydown', handleEsc) })
</script>

<style scoped>
.modal-overlay {
  position: fixed;
  top: 0; left: 0; right: 0; bottom: 0;
  background: rgba(15, 23, 42, 0.4);
  backdrop-filter: blur(8px);
  -webkit-backdrop-filter: blur(8px);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: var(--z-modal-backdrop, 1000);
  padding: var(--spacing-md);
  animation: modal-overlay-in 0.2s ease-out;
}

@keyframes modal-overlay-in {
  from {
    opacity: 0;
    backdrop-filter: blur(0px);
  }
  to {
    opacity: 1;
    backdrop-filter: blur(8px);
  }
}

.modal-wrapper {
  background: var(--color-bg-card);
  border: 1px solid var(--color-border-light);
  border-radius: var(--radius-xl);
  box-shadow: 0 25px 50px -12px rgba(0, 0, 0, 0.25), 0 0 0 1px rgba(0, 0, 0, 0.05);
  max-width: 480px;
  width: 100%;
  max-height: 90vh;
  display: flex;
  flex-direction: column;
  position: relative;
  overflow: hidden;
  animation: modal-scale-in 0.25s cubic-bezier(0.34, 1.56, 0.64, 1);
}

@keyframes modal-scale-in {
  from {
    opacity: 0;
    transform: scale(0.95) translateY(-10px);
  }
  to {
    opacity: 1;
    transform: scale(1) translateY(0);
  }
}

.modal-header {
  padding: var(--spacing-lg) var(--spacing-lg) 0;
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--spacing-md);
}

.modal-title {
  font-size: 1.25rem;
  font-weight: 800;
  color: var(--color-text-primary);
  margin: 0;
  letter-spacing: -0.02em;
}

.modal-subtitle {
  font-size: 0.85rem;
  color: var(--color-text-muted);
  margin: 4px 0 0;
  font-weight: 500;
}

.modal-close-btn {
  width: 36px;
  height: 36px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--color-bg-secondary);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  color: var(--color-text-secondary);
  cursor: pointer;
  transition: all 0.2s cubic-bezier(0.4, 0, 0.2, 1);
  padding: 0;
  position: relative;
  overflow: hidden;
}

.modal-close-btn::before {
  content: '';
  position: absolute;
  top: 50%;
  left: 50%;
  width: 0;
  height: 0;
  border-radius: 50%;
  background: var(--color-danger-bg);
  transform: translate(-50%, -50%);
  transition: width 0.3s ease, height 0.3s ease;
}

.modal-close-btn:hover::before {
  width: 100%;
  height: 100%;
}

.modal-close-btn:hover {
  color: var(--color-danger);
  transform: rotate(90deg);
  border-color: var(--color-danger-light);
}

.modal-close-btn svg {
  width: 20px;
  height: 20px;
  stroke: currentColor;
  stroke-width: 2.5;
  position: relative;
  z-index: 1;
}

.modal-body {
  padding: var(--spacing-lg);
  overflow-y: auto;
  flex: 1;
}

.modal-body.no-padding { padding: 0; }

.modal-footer {
  padding: var(--spacing-md) var(--spacing-lg);
  background: var(--color-bg-secondary);
  border-top: 1px solid var(--color-border-light);
  display: flex;
  justify-content: flex-end;
  gap: var(--spacing-sm);
}

.btn-modal-cancel {
  padding: 0 var(--spacing-lg);
  height: 42px;
  border-radius: var(--radius-md);
  background: var(--color-bg-primary);
  color: var(--color-text-secondary);
  font-weight: 600;
  font-size: 0.9rem;
  cursor: pointer;
  border: 1px solid var(--color-border);
  transition: all 0.2s cubic-bezier(0.4, 0, 0.2, 1);
}

.btn-modal-cancel:hover {
  background: var(--color-bg-secondary);
  border-color: var(--color-border-dark);
  color: var(--color-text-primary);
  transform: translateY(-1px);
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.08);
}

.btn-modal-cancel:active {
  transform: translateY(0);
}

.btn-modal-confirm {
  padding: 0 var(--spacing-xl);
  height: 42px;
  border-radius: var(--radius-md);
  color: white;
  font-weight: 700;
  font-size: 0.9rem;
  cursor: pointer;
  border: none;
  display: flex;
  align-items: center;
  justify-content: center;
  min-width: 90px;
  transition: all 0.2s cubic-bezier(0.4, 0, 0.2, 1);
  position: relative;
  overflow: hidden;
}

.btn-modal-confirm::before {
  content: '';
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: linear-gradient(135deg, rgba(255, 255, 255, 0.2) 0%, rgba(255, 255, 255, 0) 100%);
  opacity: 0;
  transition: opacity 0.2s ease;
}

.btn-modal-confirm:hover::before {
  opacity: 1;
}

.btn-modal-confirm.primary {
  background: linear-gradient(135deg, var(--color-primary) 0%, var(--color-primary-dark) 100%);
  box-shadow: 0 4px 12px rgba(37, 99, 235, 0.3);
}

.btn-modal-confirm.primary:hover {
  transform: translateY(-2px);
  box-shadow: 0 6px 16px rgba(37, 99, 235, 0.4);
}

.btn-modal-confirm.primary:active {
  transform: translateY(0);
}

.btn-modal-confirm.danger {
  background: linear-gradient(135deg, var(--color-danger) 0%, var(--color-danger-dark) 100%);
  box-shadow: 0 4px 12px rgba(239, 68, 68, 0.3);
}

.btn-modal-confirm.danger:hover {
  transform: translateY(-2px);
  box-shadow: 0 6px 16px rgba(239, 68, 68, 0.4);
}

.btn-modal-confirm.danger:active {
  transform: translateY(0);
}

.loading-spinner {
  width: 16px; height: 16px;
  border: 2px solid rgba(255,255,255,0.3);
  border-top-color: white;
  border-radius: 50%;
  animation: modal-spin 0.8s linear infinite;
}

@keyframes modal-spin { to { transform: rotate(360deg); } }

.modal-fade-enter-active, .modal-fade-leave-active { transition: opacity 0.2s ease; }
.modal-fade-enter-from, .modal-fade-leave-to { opacity: 0; }

@media (max-width: 768px) {
  .modal-overlay { padding: 0; align-items: flex-end; }
  .modal-wrapper.modal-mobile-bottom {
    max-width: 100%;
    border-radius: var(--radius-xl) var(--radius-xl) 0 0;
    animation: modal-slide-up 0.3s ease-out;
  }
  @keyframes modal-slide-up { from { transform: translateY(100%); } to { transform: translateY(0); } }
  .modal-footer { flex-direction: column-reverse; padding-bottom: calc(var(--spacing-md) + env(safe-area-inset-bottom)); }
  .btn-modal-cancel, .btn-modal-confirm { width: 100%; height: 44px; }
}
</style>

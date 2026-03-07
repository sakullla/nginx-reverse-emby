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
              <span v-if="loading" class="loading-spinner"></span>
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
  modelValue: {
    type: Boolean,
    default: false
  },
  title: {
    type: String,
    default: '确认'
  },
  subtitle: {
    type: String,
    default: ''
  },
  confirmText: {
    type: String,
    default: '确定'
  },
  cancelText: {
    type: String,
    default: '取消'
  },
  confirmVariant: {
    type: String,
    default: 'primary'
  },
  loading: {
    type: Boolean,
    default: false
  },
  showDefaultFooter: {
    type: Boolean,
    default: true
  },
  closeOnBackdrop: {
    type: Boolean,
    default: true
  },
  noPadding: {
    type: Boolean,
    default: false
  },
  mobileBottom: {
    type: Boolean,
    default: true
  }
})

const emit = defineEmits(['update:modelValue', 'confirm', 'close'])

const close = () => {
  if (props.loading) return
  emit('update:modelValue', false)
  emit('close')
}

const confirm = () => {
  emit('confirm')
}

const handleBackdropClick = () => {
  if (props.closeOnBackdrop) {
    close()
  }
}

// Handle ESC key
const handleEsc = (e) => {
  if (e.key === 'Escape' && props.modelValue) {
    close()
  }
}

onMounted(() => {
  window.addEventListener('keydown', handleEsc)
})

onUnmounted(() => {
  window.removeEventListener('keydown', handleEsc)
})
</script>

<style scoped>
.modal-overlay {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: var(--color-bg-overlay);
  backdrop-filter: blur(10px);
  -webkit-backdrop-filter: blur(10px);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: var(--z-modal-backdrop, 1000);
  padding: var(--spacing-lg);
  transition: all var(--transition-base);
}

.modal-wrapper {
  background: var(--color-bg-card);
  border: 1px solid var(--glass-border);
  border-radius: var(--radius-2xl);
  box-shadow: var(--shadow-2xl);
  max-width: 500px;
  width: 100%;
  max-height: 90vh;
  display: flex;
  flex-direction: column;
  position: relative;
  overflow: hidden;
  animation: modal-appear 0.4s cubic-bezier(0.16, 1, 0.3, 1);
}

@keyframes modal-appear {
  from { opacity: 0; transform: scale(0.95) translateY(10px); }
  to { opacity: 1; transform: scale(1) translateY(0); }
}

.modal-header {
  padding: var(--spacing-xl) var(--spacing-xl) var(--spacing-md);
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--spacing-md);
}

.header-main {
  flex: 1;
}

.modal-title {
  font-size: 1.35rem;
  font-weight: 800;
  color: var(--color-heading);
  margin: 0;
  letter-spacing: -0.02em;
}

.modal-subtitle {
  font-size: 0.875rem;
  color: var(--color-text-tertiary);
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
  border: 1px solid var(--color-border-light);
  border-radius: var(--radius-full);
  color: var(--color-text-muted);
  cursor: pointer;
  transition: all var(--transition-base);
  padding: 0;
  flex-shrink: 0;
}

.modal-close-btn:hover {
  background: var(--color-danger-bg);
  color: var(--color-danger);
  border-color: var(--color-danger-light);
  transform: rotate(90deg);
}

.modal-close-btn svg {
  width: 20px;
  height: 20px;
  stroke: currentColor;
  stroke-width: 2.5;
}

.modal-body {
  padding: var(--spacing-md) var(--spacing-xl) var(--spacing-xl);
  overflow-y: auto;
  flex: 1;
}

.modal-body.no-padding {
  padding: 0;
}

.modal-footer {
  padding: var(--spacing-lg) var(--spacing-xl);
  background: var(--color-bg-secondary);
  border-top: 1px solid var(--color-border-light);
  display: flex;
  justify-content: flex-end;
  gap: var(--spacing-md);
}

.btn-modal-cancel {
  padding: 0 var(--spacing-xl);
  height: 48px;
  border-radius: var(--radius-lg);
  background: transparent;
  color: var(--color-text-secondary);
  font-weight: 600;
  cursor: pointer;
  transition: all var(--transition-base);
  border: 1px solid transparent;
}

.btn-modal-cancel:hover {
  background: var(--color-bg-tertiary);
  color: var(--color-text-primary);
}

.btn-modal-confirm {
  padding: 0 var(--spacing-2xl);
  height: 48px;
  border-radius: var(--radius-lg);
  color: white;
  font-weight: 700;
  cursor: pointer;
  transition: all var(--transition-base);
  border: none;
  box-shadow: var(--shadow-md);
  display: flex;
  align-items: center;
  justify-content: center;
  min-width: 120px;
}

.btn-modal-confirm.primary { background: var(--color-primary); }
.btn-modal-confirm.primary:hover { background: var(--color-primary-dark); transform: translateY(-2px); box-shadow: var(--shadow-lg); }

.btn-modal-confirm.danger { background: var(--color-danger); }
.btn-modal-confirm.danger:hover { background: var(--color-danger-dark); transform: translateY(-2px); box-shadow: var(--shadow-lg); }

.btn-modal-confirm:disabled {
  opacity: 0.6;
  cursor: not-allowed;
  transform: none !important;
}

.loading-spinner {
  width: 20px;
  height: 20px;
  border: 3px solid rgba(255,255,255,0.2);
  border-top-color: white;
  border-radius: 50%;
  animation: modal-spin 0.8s linear infinite;
}

@keyframes modal-spin {
  to { transform: rotate(360deg); }
}

/* Transitions */
.modal-fade-enter-active,
.modal-fade-leave-active {
  transition: all 0.3s ease;
}

.modal-fade-enter-from,
.modal-fade-leave-to {
  opacity: 0;
}

/* Responsive Styling */
@media (max-width: 768px) {
  .modal-overlay {
    padding: 0;
    align-items: flex-end;
  }

  .modal-wrapper.modal-mobile-bottom {
    max-width: 100%;
    border-radius: var(--radius-2xl) var(--radius-2xl) 0 0;
    max-height: 95vh;
    animation: modal-slide-up 0.4s cubic-bezier(0.16, 1, 0.3, 1);
  }

  @keyframes modal-slide-up {
    from { transform: translateY(100%); }
    to { transform: translateY(0); }
  }

  .modal-header {
    padding: var(--spacing-lg) var(--spacing-lg) var(--spacing-sm);
  }

  .modal-body {
    padding: var(--spacing-sm) var(--spacing-lg) var(--spacing-xl);
  }

  .modal-footer {
    padding: var(--spacing-md) var(--spacing-lg) calc(var(--spacing-md) + env(safe-area-inset-bottom));
    flex-direction: column-reverse;
  }

  .btn-modal-cancel,
  .btn-modal-confirm {
    width: 100%;
    height: 52px;
  }
}
</style>

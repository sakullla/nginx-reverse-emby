<template>
  <Teleport to="body">
    <Transition name="modal">
      <div v-if="modelValue" class="modal-backdrop" @click.self="handleBackdropClick">
        <div
          class="modal"
          :class="modalSizeClass"
          tabindex="-1"
          ref="modalRef"
          @click.stop
        >
          <div class="modal__header">
            <div>
              <h3 class="modal__title">{{ title }}</h3>
              <p v-if="subtitle" class="modal__subtitle">{{ subtitle }}</p>
            </div>
            <button class="modal__close" @click="close">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <line x1="18" y1="6" x2="6" y2="18"/>
                <line x1="6" y1="6" x2="18" y2="18"/>
              </svg>
            </button>
          </div>
          <div class="modal__body">
            <slot />
          </div>
          <div v-if="showFooter" class="modal__footer">
            <slot name="footer">
              <button class="btn btn--secondary" @click="close">取消</button>
              <button class="btn btn--primary" @click="confirm">确认</button>
            </slot>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup>
import { ref, watch, onUnmounted, computed } from 'vue'

const props = defineProps({
  modelValue: { type: Boolean, required: true },
  title: { type: String, required: true },
  subtitle: { type: String, default: '' },
  size: {
    type: String,
    default: 'md',
    validator: (v) => ['md', 'lg', 'xl'].includes(v)
  },
  large: { type: Boolean, default: false },
  showFooter: { type: Boolean, default: false },
  closeOnClickModal: { type: Boolean, default: true }
})

const emit = defineEmits(['update:modelValue', 'confirm'])
const modalRef = ref(null)

const modalSizeClass = computed(() => {
  if (props.large) return 'modal--lg'
  return `modal--${props.size}`
})

const close = () => {
  emit('update:modelValue', false)
}

const handleBackdropClick = () => {
  if (props.closeOnClickModal) {
    close()
  }
}

const confirm = () => {
  emit('confirm')
}

// Handle ESC key to close modal
const handleKeydown = (e) => {
  if (e.key === 'Escape') {
    close()
  }
}

// Add/remove ESC listener when modal opens/closes
watch(() => props.modelValue, (isOpen) => {
  if (isOpen) {
    setTimeout(() => {
      modalRef.value?.focus()
    }, 50)
    document.addEventListener('keydown', handleKeydown)
  } else {
    document.removeEventListener('keydown', handleKeydown)
  }
}, { immediate: true })

onUnmounted(() => {
  document.removeEventListener('keydown', handleKeydown)
})
</script>

<style scoped>
.modal-enter-active,
.modal-leave-active {
  transition: opacity var(--duration-normal) var(--ease-default);
}

.modal-enter-from,
.modal-leave-to {
  opacity: 0;
}

.modal-enter-active .modal,
.modal-leave-active .modal {
  transition: transform var(--duration-slow) var(--ease-bounce),
              opacity var(--duration-slow) var(--ease-bounce);
}

.modal-enter-from .modal,
.modal-leave-to .modal {
  transform: scale(0.9);
  opacity: 0;
}

.modal-backdrop {
  position: fixed;
  inset: 0;
  background: rgba(37, 23, 54, 0.4);
  backdrop-filter: blur(8px);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: var(--z-modal-backdrop);
  padding: var(--space-4);
}

.modal {
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-3xl);
  box-shadow: var(--shadow-2xl);
  width: 100%;
  max-width: min(520px, 92vw);
  max-height: calc(100vh - var(--space-8));
  display: flex;
  flex-direction: column;
  overflow: hidden;
  backdrop-filter: blur(20px);
}

.modal--lg {
  max-width: min(640px, 90vw);
}

.modal--xl {
  max-width: min(800px, 92vw);
}

/* 2K / 1440p */
@media (min-width: 2560px) {
  .modal--lg {
    max-width: min(720px, 80vw);
  }
  .modal--xl {
    max-width: min(960px, 80vw);
  }
}

/* 4K */
@media (min-width: 3840px) {
  .modal--lg {
    max-width: min(800px, 70vw);
  }
  .modal--xl {
    max-width: min(1200px, 70vw);
  }
}

.modal__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-4);
  padding: var(--space-5) var(--space-6);
  border-bottom: 1px solid var(--color-border-subtle);
  flex-shrink: 0;
  background: var(--gradient-soft);
}

.modal__title {
  font-size: var(--text-lg);
  font-weight: var(--font-semibold);
  color: var(--color-text-primary);
  margin: 0;
}

.modal__subtitle {
  font-size: var(--text-sm);
  color: var(--color-text-secondary);
  margin: var(--space-1) 0 0;
}

.modal__close {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  border-radius: var(--radius-full);
  color: var(--color-text-tertiary);
  transition: all var(--duration-normal) var(--ease-bounce);
  flex-shrink: 0;
  border: none;
  background: transparent;
  cursor: pointer;
}

.modal__close:hover {
  background: var(--color-danger-50);
  color: var(--color-danger);
  transform: rotate(90deg);
}

.modal__body {
  padding: var(--space-6);
  overflow-y: auto;
  flex: 1;
}

.modal__footer {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: var(--space-3);
  padding: var(--space-4) var(--space-6);
  border-top: 1px solid var(--color-border-subtle);
  background: var(--gradient-soft);
  flex-shrink: 0;
}

.btn {
  padding: 0.5rem 1rem;
  border-radius: var(--radius-lg);
  font-size: 0.875rem;
  font-weight: 500;
  cursor: pointer;
  transition: all 0.15s;
  border: none;
  font-family: inherit;
  display: inline-flex;
  align-items: center;
  gap: 0.375rem;
}

.btn--primary {
  background: var(--gradient-primary);
  color: white;
}

.btn--secondary {
  background: var(--color-bg-subtle);
  color: var(--color-text-primary);
  border: 1px solid var(--color-border-default);
}

@media (max-width: 640px) {
  .modal-backdrop {
    padding: var(--space-4);
    align-items: flex-end;
  }

  .modal {
    max-height: calc(100vh - var(--space-8));
    border-radius: var(--radius-3xl) var(--radius-3xl) 0 0;
  }

  .modal-enter-active .modal,
  .modal-leave-active .modal {
    transition: transform var(--duration-slow) var(--ease-bounce);
  }

  .modal-enter-from .modal,
  .modal-leave-to .modal {
    transform: translateY(100%);
  }
}

/* iPhone SE 等小屏幕全屏 */
@media (max-width: 375px) and (max-height: 812px) {
  .modal-backdrop {
    padding: 0;
    align-items: flex-end;
  }

  .modal {
    width: 100%;
    height: 100%;
    max-height: 100vh;
    border-radius: var(--radius-2xl) var(--radius-2xl) 0 0;
  }
}
</style>

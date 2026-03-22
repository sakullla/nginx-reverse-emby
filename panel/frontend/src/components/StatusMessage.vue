<template>
  <Teleport to="body">
    <TransitionGroup 
      name="toast" 
      tag="div" 
      class="status-message-container"
    >
      <div
        v-for="msg in messages"
        :key="msg.id"
        class="status-message"
        :class="`status-message--${msg.type}`"
      >
        <span class="status-message__icon">
          <svg v-if="msg.type === 'success'" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M22 11.08V12a10 10 0 1 1-5.93-9.14"/>
            <polyline points="22 4 12 14.01 9 11.01"/>
          </svg>
          <svg v-else-if="msg.type === 'error'" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <circle cx="12" cy="12" r="10"/>
            <line x1="15" y1="9" x2="9" y2="15"/>
            <line x1="9" y1="9" x2="15" y2="15"/>
          </svg>
          <svg v-else width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <circle cx="12" cy="12" r="10"/>
            <line x1="12" y1="16" x2="12" y2="12"/>
            <line x1="12" y1="8" x2="12.01" y2="8"/>
          </svg>
        </span>
        <span class="status-message__text">{{ msg.text }}</span>
        <button class="status-message__close" @click="remove(msg.id)">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <line x1="18" y1="6" x2="6" y2="18"/>
            <line x1="6" y1="6" x2="18" y2="18"/>
          </svg>
        </button>
      </div>
    </TransitionGroup>
  </Teleport>
</template>

<script setup>
import { ref, watch } from 'vue'
import { useRuleStore } from '../stores/rules'

const ruleStore = useRuleStore()
const messages = ref([])
let idCounter = 0

watch(
  () => ruleStore.statusMessage,
  (newMsg) => {
    if (newMsg) {
      const id = ++idCounter
      messages.value.push({ ...newMsg, id })
      
      setTimeout(() => {
        remove(id)
      }, newMsg.type === 'error' ? 8000 : 5000)
    }
  },
  { immediate: false }
)

const remove = (id) => {
  const index = messages.value.findIndex((m) => m.id === id)
  if (index > -1) {
    messages.value.splice(index, 1)
  }
}
</script>

<style scoped>
.status-message-container {
  position: fixed;
  top: var(--space-4);
  right: var(--space-4);
  z-index: var(--z-toast);
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  pointer-events: none;
}

.status-message {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
  background: var(--color-bg-surface);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-lg), var(--shadow-sm);
  border-left: 4px solid;
  pointer-events: auto;
  min-width: 300px;
  max-width: 480px;
}

.status-message--success {
  border-left-color: var(--color-success);
}

.status-message--error {
  border-left-color: var(--color-danger);
}

.status-message--info {
  border-left-color: var(--color-primary);
}

.status-message__icon {
  flex-shrink: 0;
}

.status-message--success .status-message__icon {
  color: var(--color-success);
}

.status-message--error .status-message__icon {
  color: var(--color-danger);
}

.status-message--info .status-message__icon {
  color: var(--color-primary);
}

.status-message__text {
  flex: 1;
  font-size: var(--text-sm);
  color: var(--color-text-primary);
  font-weight: var(--font-medium);
  line-height: var(--leading-snug);
}

.status-message__close {
  flex-shrink: 0;
  width: 24px;
  height: 24px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--color-text-muted);
  cursor: pointer;
  border-radius: var(--radius-md);
  transition: all var(--duration-fast) var(--ease-default);
}

.status-message__close:hover {
  color: var(--color-text-primary);
  background: var(--color-bg-hover);
}

/* Transitions */
.toast-enter-active,
.toast-leave-active {
  transition: all var(--duration-normal) var(--ease-default);
}

.toast-enter-from {
  opacity: 0;
  transform: translateX(100%);
}

.toast-leave-to {
  opacity: 0;
  transform: translateX(100%);
}

.toast-move {
  transition: transform var(--duration-normal) var(--ease-default);
}

@media (max-width: 640px) {
  .status-message-container {
    top: auto;
    bottom: var(--space-4);
    left: var(--space-4);
    right: var(--space-4);
  }

  .status-message {
    min-width: auto;
    max-width: 100%;
  }

  .toast-enter-from {
    opacity: 0;
    transform: translateY(100%);
  }

  .toast-leave-to {
    opacity: 0;
    transform: translateY(100%);
  }
}
</style>

<template>
  <button
    v-if="canCopy"
    type="button"
    class="copy-button"
    :class="[`copy-button--${size}`, { 'copy-button--copied': copied }]"
    :title="buttonTitle"
    :aria-label="buttonTitle"
    :disabled="disabled"
    @click.stop="handleCopy"
    @keydown.enter.stop
    @keydown.space.stop
  >
    <svg
      v-if="copied"
      class="copy-button__icon"
      width="14"
      height="14"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="2.2"
      stroke-linecap="round"
      stroke-linejoin="round"
      aria-hidden="true"
    >
      <polyline points="20 6 9 17 4 12" />
    </svg>
    <svg
      v-else
      class="copy-button__icon"
      width="14"
      height="14"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="1.9"
      stroke-linecap="round"
      stroke-linejoin="round"
      aria-hidden="true"
    >
      <rect x="9" y="9" width="13" height="13" rx="2" ry="2" />
      <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
    </svg>
  </button>
</template>

<script setup>
import { computed, onUnmounted, ref } from 'vue'
import { copyText, isCopyableText } from '../../utils/clipboard.js'
import { messageStore } from '../../stores/messages.js'

const props = defineProps({
  value: { type: [String, Number], default: '' },
  label: { type: String, default: '复制' },
  successMessage: { type: String, default: '' },
  errorMessage: { type: String, default: '复制失败，请手动复制' },
  size: {
    type: String,
    default: 'xs',
    validator: (v) => ['xs', 'sm'].includes(v)
  },
  disabled: { type: Boolean, default: false }
})

const emit = defineEmits(['copied', 'error'])

const copied = ref(false)
let resetTimer = null

const normalizedValue = computed(() => {
  if (props.value === null || props.value === undefined) return ''
  return String(props.value).trim()
})

const canCopy = computed(() => isCopyableText(normalizedValue.value))

const buttonTitle = computed(() => (copied.value ? '已复制' : props.label || '复制'))

function clearResetTimer() {
  if (resetTimer) {
    clearTimeout(resetTimer)
    resetTimer = null
  }
}

async function handleCopy() {
  if (props.disabled || !canCopy.value) return
  const text = normalizedValue.value
  const result = await copyText(text)
  if (result.ok) {
    copied.value = true
    clearResetTimer()
    resetTimer = setTimeout(() => {
      copied.value = false
      resetTimer = null
    }, 1600)
    messageStore.success(props.successMessage || '已复制到剪贴板')
    emit('copied', text)
    return
  }
  const message = result.error || props.errorMessage
  messageStore.error(message, '复制失败')
  emit('error', message)
}

onUnmounted(() => {
  clearResetTimer()
})
</script>

<style scoped>
.copy-button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: none;
  background: transparent;
  color: var(--color-text-muted);
  cursor: pointer;
  border-radius: var(--radius-sm);
  padding: 0;
  flex-shrink: 0;
  vertical-align: middle;
  transition: background var(--duration-fast) var(--ease-default),
              color var(--duration-fast) var(--ease-default),
              transform var(--duration-fast) var(--ease-default);
}

.copy-button--xs {
  width: 24px;
  height: 24px;
}

.copy-button--sm {
  width: 28px;
  height: 28px;
}

.copy-button__icon {
  display: block;
  flex-shrink: 0;
}

.copy-button:hover:not(:disabled) {
  background: var(--color-bg-hover);
  color: var(--color-primary);
}

.copy-button:focus-visible {
  outline: none;
  box-shadow: var(--shadow-focus);
  color: var(--color-primary);
}

.copy-button:active:not(:disabled) {
  transform: scale(0.94);
}

.copy-button:disabled {
  opacity: 0.48;
  cursor: not-allowed;
}

.copy-button--copied,
.copy-button--copied:hover:not(:disabled) {
  color: var(--color-success);
  background: var(--color-success-subtle);
}

@media (pointer: coarse) {
  .copy-button--xs {
    width: 30px;
    height: 30px;
  }

  .copy-button--sm {
    width: 34px;
    height: 34px;
  }
}

@media (prefers-reduced-motion: reduce) {
  .copy-button {
    transition: none;
  }

  .copy-button:active:not(:disabled) {
    transform: none;
  }
}
</style>

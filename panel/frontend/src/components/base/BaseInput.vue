<template>
  <div class="base-input">
    <label v-if="label" :for="inputId" class="base-input__label">
      {{ label }}
      <span v-if="required" class="base-input__required" aria-hidden="true">*</span>
    </label>
    <input
      :id="inputId"
      :type="inputType"
      :value="modelValue"
      :placeholder="placeholder"
      :disabled="disabled"
      :required="required"
      :aria-invalid="error ? 'true' : undefined"
      :aria-describedby="error ? `${inputId}-error` : undefined"
      @input="handleInput"
      @focus="handleFocus"
      @blur="handleBlur"
    />
    <FieldError v-if="error" :id="`${inputId}-error`">{{ error }}</FieldError>
  </div>
</template>

<script setup>
import { useId } from 'vue'
import FieldError from './FieldError.vue'

const props = defineProps({
  modelValue: {
    type: String,
    default: ''
  },
  inputType: {
    type: String,
    default: 'text',
    validator: (value) => ['text', 'password', 'email', 'url'].includes(value)
  },
  label: {
    type: String,
    default: ''
  },
  id: {
    type: String,
    default: ''
  },
  error: {
    type: String,
    default: ''
  },
  placeholder: {
    type: String,
    default: ''
  },
  disabled: {
    type: Boolean,
    default: false
  },
  required: {
    type: Boolean,
    default: false
  }
})

const emit = defineEmits(['update:modelValue', 'focus', 'blur'])

const generatedId = `base-input-${useId()}`
const inputId = props.id || generatedId

const handleInput = (event) => {
  emit('update:modelValue', event.target.value)
}

const handleFocus = (event) => {
  emit('focus', event)
}

const handleBlur = (event) => {
  emit('blur', event)
}
</script>

<style scoped>
.base-input {
  display: flex;
  flex-direction: column;
  gap: var(--space-1-5);
}

.base-input__label {
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  color: var(--color-text-secondary);
}

.base-input__required {
  color: var(--color-danger);
}

input {
  width: 100%;
  padding: var(--space-2-5) var(--space-4);
  border-radius: var(--radius-md);
  border: var(--border-width-thick) solid var(--color-border-default);
  background: var(--color-bg-surface);
  font-size: var(--text-sm);
  color: var(--color-text-primary);
  outline: none;
  font-family: inherit;
  box-sizing: border-box;
  transition: border-color var(--duration-fast) var(--ease-default),
              box-shadow var(--duration-fast) var(--ease-default);
}

input:focus {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

input[aria-invalid='true'] {
  border-color: var(--color-danger);
}

input[aria-invalid='true']:focus {
  border-color: var(--color-danger);
  box-shadow: var(--shadow-focus-danger);
}

input::placeholder {
  color: var(--color-text-muted);
}

input:disabled {
  opacity: 0.48;
  cursor: not-allowed;
  background: var(--color-bg-subtle);
}
</style>

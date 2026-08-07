<script setup>
defineProps({
  modelValue: { type: String, default: '' },
  label: { type: String, default: '凭据值' },
  required: { type: Boolean, default: false }
})

defineEmits(['update:modelValue'])
</script>

<template>
  <label class="secret-field">
    <span>{{ label }}</span>
    <input
      type="password"
      autocomplete="new-password"
      :required="required"
      :value="modelValue"
      placeholder="仅本次提交，之后不可取回"
      @input="$emit('update:modelValue', $event.target.value)"
    >
    <small>保存后只显示指纹；页面不会再次读取明文。</small>
  </label>
</template>

<style scoped>
.secret-field {
  display: grid;
  gap: 0.4rem;
}

input {
  border: 1px solid var(--border-color, #d1d5db);
  border-radius: 0.5rem;
  padding: 0.65rem 0.75rem;
}

small {
  color: var(--text-muted, #6b7280);
}
</style>

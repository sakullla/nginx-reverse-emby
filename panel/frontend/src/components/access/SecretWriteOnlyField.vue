<script setup>
defineProps({
  modelValue: { type: String, default: '' },
  label: { type: String, default: '凭据值' },
  required: { type: Boolean, default: false },
  present: { type: Boolean, default: false },
  clearable: { type: Boolean, default: false }
})

defineEmits(['update:modelValue', 'clear'])
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
    <small v-if="present">已有安全句柄；留空会保留现值，输入新值会轮换。页面不会读取明文。</small>
    <small v-else>保存后只显示指纹；页面不会再次读取明文。</small>
    <button v-if="clearable" class="secret-field__clear" type="button" @click="$emit('clear')">清除已保存值</button>
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

.secret-field__clear {
  justify-self: start;
}
</style>

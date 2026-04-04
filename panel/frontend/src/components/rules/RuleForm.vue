<template>
  <div class="rule-form">
    <div class="form-group">
      <label>前端地址</label>
      <input v-model="localForm.frontend_url" class="input-base" placeholder="https://example.com">
    </div>
    <div class="form-group">
      <label>后端地址</label>
      <input v-model="localForm.backend_url" class="input-base" placeholder="http://192.168.1.100:8096">
    </div>
    <div class="form-group">
      <label>标签（逗号分隔）</label>
      <input v-model="localForm.tags" class="input-base" placeholder="emby, media">
    </div>
    <div class="form-group form-group--check">
      <label>
        <input type="checkbox" v-model="localForm.enabled"> 启用规则
      </label>
    </div>
  </div>
</template>

<script setup>
import { ref, watch } from 'vue'

const props = defineProps({
  rule: { type: Object, default: null }
})

const emit = defineEmits(['submit'])

const localForm = ref({
  frontend_url: '',
  backend_url: '',
  tags: '',
  enabled: true
})

watch(() => props.rule, (r) => {
  if (r) {
    localForm.value = {
      frontend_url: r.frontend_url || '',
      backend_url: r.backend_url || '',
      tags: (r.tags || []).join(', '),
      enabled: r.enabled !== false
    }
  } else {
    localForm.value = { frontend_url: '', backend_url: '', tags: '', enabled: true }
  }
}, { immediate: true })

function submit() {
  emit('submit', {
    ...localForm.value,
    tags: localForm.value.tags.split(',').map(t => t.trim()).filter(Boolean)
  })
}

defineExpose({ submit, localForm })
</script>

<style scoped>
.rule-form { display: flex; flex-direction: column; gap: 1rem; }
.form-group { display: flex; flex-direction: column; gap: 0.375rem; }
.form-group label { font-size: 0.875rem; font-weight: 500; color: var(--color-text-secondary); }
.form-group--check { flex-direction: row; align-items: center; }
.form-group--check label { display: flex; align-items: center; gap: 0.5rem; cursor: pointer; font-weight: normal; }
.input-base { width: 100%; padding: 0.5rem 0.75rem; border-radius: var(--radius-lg); border: 1.5px solid var(--color-border-default); background: var(--color-bg-subtle); font-size: 0.875rem; color: var(--color-text-primary); outline: none; font-family: inherit; transition: border-color 0.15s; }
.input-base:focus { border-color: var(--color-primary); }
</style>

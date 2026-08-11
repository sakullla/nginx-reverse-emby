<script setup>
import { computed, reactive, watch } from 'vue'
import { normalizePluginConfigSchema } from '../../api/pluginSecurity'
import SecretWriteOnlyField from '../access/SecretWriteOnlyField.vue'

const props = defineProps({
  schema: { type: Object, default: () => ({}) },
  config: { type: Object, default: () => ({}) },
  saving: { type: Boolean, default: false }
})
const emit = defineEmits(['submit'])
const model = reactive({})
const fields = computed(() => normalizePluginConfigSchema(props.schema))

watch([fields, () => props.config], initialize, { immediate: true, deep: true })

function initialize() {
  for (const key of Object.keys(model)) delete model[key]
  for (const field of fields.value) {
    if (field.secret) {
      model[field.name] = ''
    } else if (Object.hasOwn(props.config || {}, field.name)) {
      model[field.name] = props.config[field.name]
    } else if (Object.hasOwn(field, 'default')) {
      model[field.name] = field.default
    } else if (field.type === 'boolean') {
      model[field.name] = false
    } else {
      model[field.name] = ''
    }
  }
}

function submit() {
  const config = {}
  for (const field of fields.value) {
    const value = model[field.name]
    if (field.secret && value === '') continue
    if (field.type === 'integer') config[field.name] = Number.parseInt(value, 10)
    else if (field.type === 'number') config[field.name] = Number(value)
    else config[field.name] = value
  }
  emit('submit', config)
}
</script>

<template>
  <form class="plugin-config-form" @submit.prevent="submit">
    <p v-if="!fields.length" class="plugin-config-form__empty">此插件没有宿主允许的可配置字段。</p>
    <template v-for="field in fields" :key="field.name">
      <SecretWriteOnlyField
        v-if="field.secret"
        v-model="model[field.name]"
        :label="field.title"
        :required="field.required"
      />
      <label v-else-if="field.type === 'boolean'" class="plugin-config-form__check">
        <input v-model="model[field.name]" type="checkbox">
        <span>{{ field.title }}</span>
        <small v-if="field.description">{{ field.description }}</small>
      </label>
      <label v-else class="plugin-config-form__field">
        <span>{{ field.title }}</span>
        <select v-if="field.enum.length" v-model="model[field.name]" :required="field.required">
          <option v-for="option in field.enum" :key="String(option)" :value="option">{{ option }}</option>
        </select>
        <input
          v-else
          v-model="model[field.name]"
          :type="field.type === 'string' ? 'text' : 'number'"
          :step="field.type === 'integer' ? '1' : 'any'"
          :min="field.minimum"
          :max="field.maximum"
          :required="field.required"
        >
        <small v-if="field.description">{{ field.description }}</small>
      </label>
    </template>
    <button v-if="fields.length" class="btn btn-primary" type="submit" :disabled="saving">
      {{ saving ? '保存中…' : '保存配置并生成 revision' }}
    </button>
    <p class="plugin-config-form__boundary">表单仅由宿主支持的 JSON Schema 字段生成；插件包中的 HTML、脚本和远程组件不会加载。</p>
  </form>
</template>

<style scoped>
.plugin-config-form { display: grid; gap: var(--space-4); }
.plugin-config-form__field, .plugin-config-form__check { display: grid; gap: var(--space-2); font-size: var(--text-sm); }
.plugin-config-form__check { grid-template-columns: auto 1fr; align-items: center; }
.plugin-config-form__check small { grid-column: 2; }
input:not([type='checkbox']), select { width: 100%; box-sizing: border-box; padding: .65rem .75rem; border: 1px solid var(--color-border-default); border-radius: var(--radius-md); background: var(--color-bg-surface); color: var(--color-text-primary); }
small, .plugin-config-form__empty, .plugin-config-form__boundary { margin: 0; color: var(--color-text-muted); font-size: var(--text-xs); }
.plugin-config-form__boundary { padding: var(--space-3); border-radius: var(--radius-md); background: var(--color-bg-subtle); }
</style>

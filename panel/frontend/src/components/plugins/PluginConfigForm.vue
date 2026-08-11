<script setup>
import { computed, reactive, watch } from 'vue'
import { normalizePluginConfigSchema } from '../../api/pluginSecurity'
import SecretWriteOnlyField from '../access/SecretWriteOnlyField.vue'

const props = defineProps({
  schema: { type: Object, default: () => ({}) },
  config: { type: Object, default: () => ({}) },
  secretFields: { type: Array, default: () => [] },
  saving: { type: Boolean, default: false }
})
const emit = defineEmits(['submit'])
const model = reactive({})
const clearedSecrets = reactive(new Set())
const fields = computed(() => normalizePluginConfigSchema(props.schema))
const secretPointers = computed(() => new Set((props.secretFields || [])
  .filter((field) => field && typeof field === 'object' && typeof field.pointer === 'string' && field.present === true)
  .map((field) => field.pointer)))

watch([fields, () => props.config, () => props.secretFields], initialize, { immediate: true, deep: true })

function pointerFor(name) {
  return `/${name.replaceAll('~', '~0').replaceAll('/', '~1')}`
}

function hasSecret(field) {
  return secretPointers.value.has(pointerFor(field.name))
}

function initialize() {
  for (const key of Object.keys(model)) delete model[key]
  clearedSecrets.clear()
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
  const secretReplacements = {}
  for (const field of fields.value) {
    const value = model[field.name]
    const pointer = pointerFor(field.name)
    if (field.secret && clearedSecrets.has(pointer)) {
      secretReplacements[pointer] = null
      continue
    }
    if (field.secret && value === '') continue
    const normalized = field.type === 'integer' ? Number.parseInt(value, 10) : field.type === 'number' ? Number(value) : value
    if (field.secret) secretReplacements[pointer] = normalized
    else config[field.name] = normalized
  }
  emit('submit', { config, secret_replacements: secretReplacements })
}

function clearSecret(field) {
  model[field.name] = ''
  clearedSecrets.add(pointerFor(field.name))
}

function replaceSecret(field, value) {
  model[field.name] = value
  clearedSecrets.delete(pointerFor(field.name))
}
</script>

<template>
  <form class="plugin-config-form" @submit.prevent="submit">
    <p v-if="!fields.length" class="plugin-config-form__empty">此插件没有宿主允许的可配置字段。</p>
    <template v-for="field in fields" :key="field.name">
      <SecretWriteOnlyField
        v-if="field.secret"
        :model-value="model[field.name]"
        :label="field.title"
        :required="field.required && !hasSecret(field)"
        :present="hasSecret(field)"
        :clearable="!field.required && hasSecret(field)"
        @update:model-value="replaceSecret(field, $event)"
        @clear="clearSecret(field)"
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

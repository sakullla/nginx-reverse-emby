<script setup>
import { reactive, ref, watch } from 'vue'
import PluginDeclarativeComponent from './PluginDeclarativeComponent.vue'
import { collectHiddenPointers, prunePointer, resolvePointer } from '../../api/pluginCondition.js'
import { collectDeclarativeConstraintErrors } from '../../api/pluginSecurity.js'

const props = defineProps({ document: { type: Object, required: true }, config: { type: Object, default: () => ({}) }, secretFields: { type: Array, default: () => [] }, saving: { type: Boolean, default: false }, actionBusy: { type: Boolean, default: false }, canConfigure: { type: Boolean, default: false }, canAct: { type: Boolean, default: false } })
const emit = defineEmits(['submit', 'dynamic'])
const model = reactive({})
const targets = reactive({})
const secretReplacements = reactive({})
const forceValidate = ref(false)
watch(() => props.config, reset, { immediate: true, deep: true })
function clone(value) { return JSON.parse(JSON.stringify(value || {})) }
function reset() {
  forceValidate.value = false
  for (const key of Object.keys(model)) delete model[key]
  for (const key of Object.keys(secretReplacements)) delete secretReplacements[key]
  Object.assign(model, clone(props.config))
  seedDefaults()
}

// Fill missing leaf pointers with their synthesized schema defaults so a fresh
// config still shows (and can save) declared defaults and boolean false.
function seedDefaults() {
  const walk = (components, basePointer) => {
    for (const component of components || []) {
      if (!component || typeof component !== 'object') continue
      const full = basePointer + (component.binding || '')
      if (component.type === 'section' || component.type === 'grid') {
        walk(component.children || [], basePointer)
      } else if (component.type === 'array') {
        let items = resolvePointer(model, full)
        if (items === undefined && component.default !== undefined) {
          setValue(full, clone(component.default))
          items = resolvePointer(model, full)
        }
        if (Array.isArray(items) && Array.isArray(component.children) && component.children.length) {
          items.forEach((item, index) => walk(component.children, `${full}/${index}`))
        }
      } else if (component.binding && component.type !== 'secret') {
        const current = resolvePointer(model, full)
        if (component.type === 'toggle') {
          if (current === undefined) setValue(full, component.default === undefined ? false : component.default)
        } else if (component.default !== undefined && current === undefined) {
          setValue(full, component.default)
        }
      }
    }
  }
  walk(props.document.components || [], '')
}
function secretPresent(pointer) { return props.secretFields.some((field) => field.pointer === pointer && field.present) }
function setSecret(pointer, value) {
  if (value === '') delete secretReplacements[pointer]
  else secretReplacements[pointer] = value
}
function setValue(pointer, value) {
  const parts = String(pointer).split('/').slice(1).map((part) => part.replaceAll('~1', '/').replaceAll('~0', '~'))
  let current = model
  for (const part of parts.slice(0, -1)) current = current[part] ||= {}
  current[parts.at(-1)] = value
}
function action(action) {
	if (action.type === 'dynamic' ? !props.canAct : !props.canConfigure) return
  if (action.type === 'submit') {
    const hidden = collectHiddenPointers(props.document.components || [], model)
    const errors = collectDeclarativeConstraintErrors(props.document.components || [], model, {
      secretFields: props.secretFields,
      secretReplacements
    }).filter((item) => !hidden.includes(item.pointer))
    if (errors.length) {
      forceValidate.value = true
      return
    }
    const config = clone(model)
    for (const pointer of hidden) prunePointer(config, pointer)
    const secret_replacements = clone(secretReplacements)
    for (const pointer of hidden) delete secret_replacements[pointer]
    emit('submit', { config, secret_replacements })
  }
  else if (action.type === 'reset' && (!action.confirm || window.confirm(action.confirm))) reset()
  else if (action.type === 'dynamic' && (!action.confirm || window.confirm(action.confirm))) emit('dynamic', { action, target_id: targets[action.id] || '', confirmed: true })
}
</script>

<template>
  <section class="declarative-ui">
    <header class="declarative-ui__header">
      <h3>{{ document.title }}</h3>
      <p v-if="document.description">{{ document.description }}</p>
    </header>
    <div v-if="canConfigure" class="declarative-ui__body">
      <PluginDeclarativeComponent v-for="component in document.components || []" :key="component.id" :component="component" :model="model" :secret-replacements="secretReplacements" :secret-present="secretPresent" :force-validate="forceValidate" @change="setValue" @secret="setSecret" />
    </div>
    <div class="declarative-actions">
      <template v-for="item in (document.actions || []).filter((action) => action.type === 'dynamic' ? canAct : canConfigure)" :key="item.id">
        <label v-if="item.type === 'dynamic'" class="declarative-target"><span>{{ item.target_kind }} ID</span><input v-model="targets[item.id]" type="text" autocomplete="off"></label>
        <button class="btn" :class="item.type === 'submit' ? 'btn-primary' : 'btn-secondary'" type="button" :disabled="saving || actionBusy || (item.type === 'dynamic' && !targets[item.id])" @click="action(item)">{{ item.label }}</button>
      </template>
    </div>
    <p class="declarative-boundary">此界面仅使用宿主内置组件渲染经过验证的声明式数据，不加载插件 HTML、JavaScript 或远程组件。</p>
  </section>
</template>

<style scoped>
.declarative-ui { display: grid; gap: var(--space-5); min-width: 0; }
.declarative-ui__header { display: grid; gap: var(--space-1); }
.declarative-ui__header h3, .declarative-ui__header p { margin: 0; }
.declarative-ui__header h3 { font-size: var(--text-lg); }
.declarative-ui__header p, .declarative-boundary { color: var(--color-text-muted); }
.declarative-ui__header p, .declarative-boundary { font-size: var(--text-sm); }
.declarative-ui__body { display: grid; gap: var(--space-4); min-width: 0; }
.declarative-boundary { margin: 0; font-size: var(--text-xs); }
.declarative-actions {
  display: flex; align-items: end; flex-wrap: wrap; gap: var(--space-3);
  padding-top: var(--space-4); border-top: 1px solid var(--color-border-subtle);
}
.declarative-target { min-width: 12rem; display: grid; gap: var(--space-1); color: var(--color-text-secondary); font-size: var(--text-sm); }
.declarative-target input { padding: .6rem .75rem; border: 1px solid var(--color-border-default); border-radius: var(--radius-md); background: var(--color-bg-surface); color: var(--color-text-primary); }
</style>

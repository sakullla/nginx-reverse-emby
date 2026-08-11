<script setup>
import { reactive, watch } from 'vue'
import PluginDeclarativeComponent from './PluginDeclarativeComponent.vue'

const props = defineProps({ document: { type: Object, required: true }, config: { type: Object, default: () => ({}) }, saving: { type: Boolean, default: false }, actionBusy: { type: Boolean, default: false }, canConfigure: { type: Boolean, default: false }, canAct: { type: Boolean, default: false } })
const emit = defineEmits(['submit', 'dynamic'])
const model = reactive({})
const targets = reactive({})
watch(() => props.config, reset, { immediate: true, deep: true })
function clone(value) { return JSON.parse(JSON.stringify(value || {})) }
function reset() { for (const key of Object.keys(model)) delete model[key]; Object.assign(model, clone(props.config)) }
function setValue(pointer, value) {
  const parts = String(pointer).split('/').slice(1).map((part) => part.replaceAll('~1', '/').replaceAll('~0', '~'))
  let current = model
  for (const part of parts.slice(0, -1)) current = current[part] ||= {}
  current[parts.at(-1)] = value
}
function action(action) {
	if (action.type === 'dynamic' ? !props.canAct : !props.canConfigure) return
  if (action.type === 'submit') emit('submit', { config: clone(model), secret_replacements: {} })
  else if (action.type === 'reset' && (!action.confirm || window.confirm(action.confirm))) reset()
  else if (action.type === 'dynamic' && (!action.confirm || window.confirm(action.confirm))) emit('dynamic', { action, target_id: targets[action.id] || '', confirmed: true })
}
</script>

<template>
  <section class="declarative-ui">
    <header><h3>{{ document.title }}</h3><p v-if="document.description">{{ document.description }}</p></header>
		<template v-if="canConfigure"><PluginDeclarativeComponent v-for="component in document.components || []" :key="component.id" :component="component" :model="model" @change="setValue" /></template>
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
.declarative-ui { display: grid; gap: var(--space-4); }.declarative-ui h3, .declarative-ui p { margin: 0; }.declarative-ui header p, .declarative-boundary { color: var(--color-text-muted); }.declarative-actions { display: flex; align-items: end; flex-wrap: wrap; gap: var(--space-3); }.declarative-target { min-width: 12rem; display: grid; gap: var(--space-1); }.declarative-target input { padding: .6rem; border: 1px solid var(--color-border-default); border-radius: var(--radius-md); background: var(--color-bg-surface); color: var(--color-text-primary); }
</style>

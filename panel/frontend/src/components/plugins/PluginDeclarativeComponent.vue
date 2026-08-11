<script setup>
defineOptions({ name: 'PluginDeclarativeComponent' })
const props = defineProps({ component: { type: Object, required: true }, model: { type: Object, required: true } })
const emit = defineEmits(['change'])

function tokens(pointer) {
  return String(pointer || '').split('/').slice(1).map((part) => part.replaceAll('~1', '/').replaceAll('~0', '~'))
}
function value() {
  let current = props.model
  for (const token of tokens(props.component.binding)) current = current?.[token]
  return current
}
function change(next) { emit('change', props.component.binding, next) }
</script>

<template>
  <fieldset v-if="component.type === 'section'" class="declarative-section">
    <legend>{{ component.label }}</legend><p v-if="component.description">{{ component.description }}</p>
    <PluginDeclarativeComponent v-for="child in component.children || []" :key="child.id" :component="child" :model="model" @change="(...args) => emit('change', ...args)" />
  </fieldset>
  <aside v-else-if="component.type === 'notice'" class="declarative-notice" :data-tone="component.tone"><strong>{{ component.label }}</strong><p v-if="component.description">{{ component.description }}</p></aside>
  <label v-else-if="component.type === 'toggle'" class="declarative-toggle"><input type="checkbox" :checked="value() === true" :disabled="component.read_only" @change="change($event.target.checked)"><span>{{ component.label }}</span></label>
  <label v-else-if="component.type === 'select'" class="declarative-field"><span>{{ component.label }}</span><select :value="value()" :required="component.required" :disabled="component.read_only" @change="change($event.target.value)"><option v-for="option in component.options || []" :key="option.value" :value="option.value">{{ option.label }}</option></select><small v-if="component.description">{{ component.description }}</small></label>
  <label v-else-if="component.type === 'number'" class="declarative-field"><span>{{ component.label }}</span><input type="number" :value="value()" :min="component.minimum" :max="component.maximum" :step="component.step || 'any'" :required="component.required" :readonly="component.read_only" @input="change($event.target.value === '' ? null : Number($event.target.value))"><small v-if="component.description">{{ component.description }}</small></label>
  <label v-else-if="component.type === 'text' || component.type === 'textarea'" class="declarative-field"><span>{{ component.label }}</span><textarea v-if="component.type === 'textarea'" :value="value()" :placeholder="component.placeholder" :required="component.required" :readonly="component.read_only" @input="change($event.target.value)" /><input v-else type="text" :value="value()" :placeholder="component.placeholder" :required="component.required" :readonly="component.read_only" @input="change($event.target.value)"><small v-if="component.description">{{ component.description }}</small></label>
</template>

<style scoped>
.declarative-section, .declarative-field { min-width: 0; display: grid; gap: var(--space-2); }.declarative-section { padding: var(--space-4); border: 1px solid var(--color-border-subtle); border-radius: var(--radius-md); }.declarative-section p, .declarative-notice p, small { margin: 0; color: var(--color-text-muted); }.declarative-field input, .declarative-field textarea, .declarative-field select { width: 100%; box-sizing: border-box; padding: .65rem; border: 1px solid var(--color-border-default); border-radius: var(--radius-md); background: var(--color-bg-surface); color: var(--color-text-primary); }.declarative-toggle { display: flex; gap: var(--space-2); }.declarative-notice { padding: var(--space-3); border-radius: var(--radius-md); background: var(--color-bg-subtle); }.declarative-notice[data-tone='warning'], .declarative-notice[data-tone='danger'] { border-left: 3px solid var(--color-warning); }
</style>

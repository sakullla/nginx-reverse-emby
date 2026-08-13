<script setup>
import { computed, ref } from 'vue'
import { evaluateCondition, resolvePointer, isEmptyValue } from '../../api/pluginCondition.js'

defineOptions({ name: 'PluginDeclarativeComponent' })

const props = defineProps({
  component: { type: Object, required: true },
  model: { type: Object, required: true },
  secretReplacements: { type: Object, default: () => ({}) },
  secretPresent: { type: Function, default: () => false },
  basePointer: { type: String, default: '' },
  conditionScope: { type: Object, default: null }
})
const emit = defineEmits(['change', 'secret'])

const scope = computed(() => props.conditionScope || props.model)
const fullPointer = computed(() => props.basePointer + (props.component.binding || ''))
const isVisible = computed(() => evaluateCondition(props.component.visible_when, scope.value))
const isObjectItems = computed(() => Array.isArray(props.component.children) && props.component.children.length > 0)

const touched = ref(false)

function value() { return resolvePointer(props.model, fullPointer.value) }
function change(next) { touched.value = true; emit('change', fullPointer.value, next) }
function changeSecret(next) { emit('secret', fullPointer.value, next) }
// Preserve the typed option value (number vs string) instead of the DOM's
// always-string `select` value, so numeric enums round-trip unchanged.
function selectValue(raw) {
  const option = (props.component.options || []).find((item) => String(item.value) === raw)
  return option ? option.value : raw
}

const items = computed(() => Array.isArray(value()) ? value() : [])
function addItem() { change([...items.value, isObjectItems.value ? {} : '']) }
function removeItem(index) { change(items.value.filter((_, i) => i !== index)) }
function moveItem(index, delta) {
  const next = [...items.value]
  const target = index + delta
  if (target < 0 || target >= next.length) return
  ;[next[index], next[target]] = [next[target], next[index]]
  change(next)
}
function changeScalar(index, text) { const next = [...items.value]; next[index] = text; change(next) }

function constraintHint(component) {
  const parts = []
  if (component.required) parts.push('必填')
  if (component.type === 'number') {
    if (component.minimum != null && component.maximum != null) parts.push(`范围 ${component.minimum}–${component.maximum}`)
    else if (component.minimum != null) parts.push(`≥ ${component.minimum}`)
    else if (component.maximum != null) parts.push(`≤ ${component.maximum}`)
  } else if (component.type === 'text' || component.type === 'textarea') {
    if (component.min_length != null && component.max_length != null) parts.push(`${component.min_length}–${component.max_length} 字符`)
    else if (component.min_length != null) parts.push(`≥ ${component.min_length} 字符`)
    else if (component.max_length != null) parts.push(`≤ ${component.max_length} 字符`)
  }
  return parts.join(' · ')
}

function constraintError(component, current) {
  if (component.required && isEmptyValue(current)) return '此项为必填'
  if (component.type === 'number' && current != null && current !== '') {
    if (component.minimum != null && current < component.minimum) return `不能小于 ${component.minimum}`
    if (component.maximum != null && current > component.maximum) return `不能大于 ${component.maximum}`
  }
  if ((component.type === 'text' || component.type === 'textarea') && typeof current === 'string') {
    if (component.min_length != null && current.length < component.min_length) return `至少 ${component.min_length} 个字符`
    if (component.max_length != null && current.length > component.max_length) return `最多 ${component.max_length} 个字符`
    if (component.pattern) { try { if (!new RegExp(component.pattern).test(current)) return '格式不匹配' } catch { /* ignore invalid pattern */ } }
  }
  return ''
}

const hintText = computed(() => constraintHint(props.component))
const error = computed(() => touched.value ? constraintError(props.component, value()) : '')
</script>

<template>
  <template v-if="isVisible">
    <fieldset v-if="component.type === 'section'" class="declarative-section">
      <legend>{{ component.label }}</legend>
      <p v-if="component.description">{{ component.description }}</p>
      <PluginDeclarativeComponent v-for="child in component.children || []" :key="child.id" :component="child" :model="model" :secret-replacements="secretReplacements" :secret-present="secretPresent" :base-pointer="basePointer" :condition-scope="scope" @change="(...args) => emit('change', ...args)" @secret="(...args) => emit('secret', ...args)" />
    </fieldset>

    <aside v-else-if="component.type === 'notice'" class="declarative-notice" :data-tone="component.tone"><strong>{{ component.label }}</strong><p v-if="component.description">{{ component.description }}</p></aside>

    <label v-else-if="component.type === 'toggle'" class="declarative-toggle"><input type="checkbox" :checked="value() === true" :disabled="component.read_only" @change="change($event.target.checked)"><span>{{ component.label }}</span></label>

    <label v-else-if="component.type === 'select'" class="declarative-field"><span>{{ component.label }}</span><select :value="value()" :required="component.required" :disabled="component.read_only" @change="change(selectValue($event.target.value))"><option v-for="option in component.options || []" :key="option.value" :value="option.value">{{ option.label }}</option></select><small v-if="component.description || hintText" class="declarative-hint">{{ [component.description, hintText].filter(Boolean).join(' · ') }}</small><small v-if="error" class="declarative-error">{{ error }}</small></label>

    <label v-else-if="component.type === 'number'" class="declarative-field"><span>{{ component.label }}</span><input type="number" :value="value()" :min="component.minimum" :max="component.maximum" :step="component.step || 'any'" :required="component.required" :readonly="component.read_only" @input="change($event.target.value === '' ? null : Number($event.target.value))"><small v-if="component.description || hintText" class="declarative-hint">{{ [component.description, hintText].filter(Boolean).join(' · ') }}</small><small v-if="error" class="declarative-error">{{ error }}</small></label>

    <label v-else-if="component.type === 'secret'" class="declarative-field declarative-secret"><span>{{ component.label }}</span><input type="password" autocomplete="new-password" :value="secretReplacements[fullPointer] ?? ''" :required="component.required && !secretPresent(fullPointer)" :readonly="component.read_only" placeholder="留空以保留现有凭据" @input="changeSecret($event.target.value)"><small>{{ secretPresent(fullPointer) ? '已有凭据；留空保留，输入新值轮换。' : '尚未配置；明文仅随本次提交发送。' }}</small><button v-if="secretPresent(fullPointer) && !component.required && !component.read_only" class="btn btn-secondary" type="button" @click="changeSecret(null)">清除凭据</button></label>

    <label v-else-if="component.type === 'text' || component.type === 'textarea'" class="declarative-field"><span>{{ component.label }}</span><textarea v-if="component.type === 'textarea'" :value="value()" :placeholder="component.placeholder" :required="component.required" :readonly="component.read_only" @input="change($event.target.value)" /><input v-else type="text" :value="value()" :placeholder="component.placeholder" :required="component.required" :readonly="component.read_only" @input="change($event.target.value)"><small v-if="component.description || hintText" class="declarative-hint">{{ [component.description, hintText].filter(Boolean).join(' · ') }}</small><small v-if="error" class="declarative-error">{{ error }}</small></label>

    <div v-else-if="component.type === 'array'" class="declarative-array">
      <div class="declarative-array-head"><span>{{ component.label }}</span><small v-if="component.description || hintText" class="declarative-hint">{{ [component.description, hintText].filter(Boolean).join(' · ') }}</small></div>
      <template v-if="isObjectItems">
        <fieldset v-for="(item, index) in items" :key="index" class="declarative-array-item">
          <legend>#{{ index + 1 }}</legend>
          <PluginDeclarativeComponent v-for="child in component.children" :key="child.id" :component="child" :model="model" :secret-replacements="secretReplacements" :secret-present="secretPresent" :base-pointer="`${fullPointer}/${index}`" :condition-scope="item" @change="(...args) => emit('change', ...args)" @secret="(...args) => emit('secret', ...args)" />
          <div class="declarative-array-actions">
            <button class="btn btn-secondary" type="button" :disabled="component.read_only" @click="removeItem(index)">移除</button>
            <button class="btn btn-secondary" type="button" :disabled="component.read_only || index === 0" @click="moveItem(index, -1)">上移</button>
            <button class="btn btn-secondary" type="button" :disabled="component.read_only || index === items.length - 1" @click="moveItem(index, 1)">下移</button>
          </div>
        </fieldset>
      </template>
      <template v-else>
        <div v-for="(item, index) in items" :key="index" class="declarative-array-item declarative-array-scalar">
          <input type="text" :value="item" :disabled="component.read_only" @input="changeScalar(index, $event.target.value)">
          <button class="btn btn-secondary" type="button" :disabled="component.read_only" @click="removeItem(index)">移除</button>
        </div>
      </template>
      <button class="btn btn-secondary" type="button" :disabled="component.read_only" @click="addItem">+ 添加</button>
    </div>
  </template>
</template>

<style scoped>
.declarative-section, .declarative-field { min-width: 0; display: grid; gap: var(--space-2); }.declarative-section { padding: var(--space-4); border: 1px solid var(--color-border-subtle); border-radius: var(--radius-md); }.declarative-section p, .declarative-notice p, small { margin: 0; color: var(--color-text-muted); }.declarative-field input, .declarative-field textarea, .declarative-field select, .declarative-array-scalar input { width: 100%; box-sizing: border-box; padding: .65rem; border: 1px solid var(--color-border-default); border-radius: var(--radius-md); background: var(--color-bg-surface); color: var(--color-text-primary); }.declarative-toggle { display: flex; gap: var(--space-2); }.declarative-notice { padding: var(--space-3); border-radius: var(--radius-md); background: var(--color-bg-subtle); }.declarative-notice[data-tone='warning'], .declarative-notice[data-tone='danger'] { border-left: 3px solid var(--color-warning); }.declarative-array { display: grid; gap: var(--space-3); }.declarative-array-head { display: flex; gap: var(--space-2); align-items: baseline; }.declarative-array-item { min-width: 0; display: grid; gap: var(--space-2); padding: var(--space-3); border: 1px solid var(--color-border-subtle); border-radius: var(--radius-md); }.declarative-array-actions { display: flex; gap: var(--space-2); flex-wrap: wrap; }.declarative-array-scalar { grid-template-columns: 1fr auto; align-items: center; }.declarative-hint { color: var(--color-text-muted); }.declarative-error { color: var(--color-danger, #c0392b); }
</style>

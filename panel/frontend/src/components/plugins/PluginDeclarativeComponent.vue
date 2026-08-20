<script>
// Module-level sequence giving each component instance a unique radio group
// suffix (see radioGroupName below).
let declarativeComponentSeq = 0
</script>

<script setup>
import { computed, ref, watch } from 'vue'
import { evaluateCondition, resolvePointer } from '../../api/pluginCondition.js'
import { fieldConstraintError, secretConstraintError } from '../../api/pluginSecurity.js'

defineOptions({ name: 'PluginDeclarativeComponent' })

const props = defineProps({
  component: { type: Object, required: true },
  model: { type: Object, required: true },
  secretReplacements: { type: Object, default: () => ({}) },
  secretPresent: { type: Function, default: () => false },
  basePointer: { type: String, default: '' },
  conditionScope: { type: Object, default: null },
  forceValidate: { type: Boolean, default: false }
})
const emit = defineEmits(['change', 'secret'])

// Radio groups must not leak across sibling forms: the page may mount several
// declarative UIs for the same document (e.g. deploy + instance config), and a
// bare pointer-based name would make their same-binding radios mutually
// exclusive at the DOM level while each model keeps its own value.
const instanceSeq = ++declarativeComponentSeq
const radioGroupName = computed(() => `${fullPointer.value}#${instanceSeq}`)

const scope = computed(() => props.conditionScope || props.model)
const fullPointer = computed(() => props.basePointer + (props.component.binding || ''))
const isVisible = computed(() => evaluateCondition(props.component.visible_when, scope.value))
const isObjectItems = computed(() => Array.isArray(props.component.children) && props.component.children.length > 0)

const touched = ref(false)
// Collapsible sections start collapsed only when the document asks for it;
// collapsing never unmounts children, so bound values and conditions survive.
const collapsed = ref(props.component.type === 'section' && props.component.default_collapsed === true)
// A forced validation pass (failed submit) must surface nested errors: expand
// the section so the offending field and its message become visible.
watch(() => props.forceValidate, (forced) => { if (forced) collapsed.value = false })

function value() { return resolvePointer(props.model, fullPointer.value) }
function change(next) { touched.value = true; emit('change', fullPointer.value, next) }
function changeSecret(next) { emit('secret', fullPointer.value, next) }
// Preserve the typed option value (number vs string) instead of the DOM's
// always-string `select` value, so numeric enums round-trip unchanged.
function selectValue(raw) {
  const option = (props.component.options || []).find((item) => String(item.value) === raw)
  return option ? option.value : raw
}

// --- multiselect: array value kept in declared option order ---
const selectedValues = computed(() => (Array.isArray(value()) ? value() : []))
function toggleOption(optionValue, checked) {
  const selected = new Set(selectedValues.value)
  if (checked) selected.add(optionValue)
  else selected.delete(optionValue)
  change((props.component.options || []).map((option) => option.value).filter((item) => selected.has(item)))
}

// --- keyvalue: local rows mirror the object; the model stays the source of
// truth and only non-empty trimmed keys are emitted ---
const keyValueRows = ref([])
let keyValueLastEmitted = ''
const keyValueModel = computed(() => (props.component.type === 'keyvalue' ? value() : undefined))
watch(keyValueModel, (next) => {
  const signature = JSON.stringify(next || {})
  if (signature === keyValueLastEmitted) return
  keyValueLastEmitted = signature
  keyValueRows.value = Object.entries(next || {}).map(([key, item]) => ({ key, value: typeof item === 'string' ? item : String(item ?? '') }))
}, { immediate: true, deep: true })
// Duplicate trimmed keys are flagged on the later rows and only the first
// occurrence is emitted, so the UI and the submitted config never diverge.
const keyValueDuplicateIndexes = computed(() => {
  const seen = new Set()
  const duplicates = new Set()
  keyValueRows.value.forEach((row, index) => {
    const key = String(row.key || '').trim()
    if (!key) return
    if (seen.has(key)) duplicates.add(index)
    else seen.add(key)
  })
  return duplicates
})
function emitKeyValue() {
  const next = {}
  for (const row of keyValueRows.value) {
    const key = String(row.key || '').trim()
    if (!key || Object.hasOwn(next, key)) continue
    next[key] = String(row.value ?? '')
  }
  keyValueLastEmitted = JSON.stringify(next)
  change(next)
}
function changeKeyValueKey(index, key) { keyValueRows.value[index].key = key; emitKeyValue() }
function changeKeyValueValue(index, item) { keyValueRows.value[index].value = item; emitKeyValue() }
function addKeyValueRow() { keyValueRows.value.push({ key: '', value: '' }) }
function removeKeyValueRow(index) { keyValueRows.value.splice(index, 1); emitKeyValue() }

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
  } else if (component.type === 'array' || component.type === 'multiselect') {
    if (component.min_items > 0 && component.max_items != null) parts.push(`${component.min_items}–${component.max_items} 项`)
    else if (component.min_items > 0) parts.push(`≥ ${component.min_items} 项`)
    else if (component.max_items != null) parts.push(`≤ ${component.max_items} 项`)
  }
  return parts.join(' · ')
}

const hintText = computed(() => constraintHint(props.component))
const error = computed(() => {
  if (!touched.value && !props.forceValidate) return ''
  if (props.component.type === 'secret') {
    return secretConstraintError(props.component, fullPointer.value, [{ pointer: fullPointer.value, present: props.secretPresent(fullPointer.value) }], props.secretReplacements)
  }
  return fieldConstraintError(props.component, value())
})
</script>

<template>
  <template v-if="isVisible">
    <fieldset v-if="component.type === 'section'" class="declarative-section" :class="{ 'declarative-section--collapsible': component.collapsible }">
      <legend v-if="!component.collapsible" class="declarative-section__legend">{{ component.label }}</legend>
      <button v-else type="button" class="declarative-section__toggle" :aria-expanded="String(!collapsed)" @click="collapsed = !collapsed">
        <span class="declarative-section__chevron" :class="{ 'declarative-section__chevron--open': !collapsed }" aria-hidden="true">▸</span>
        <span>{{ component.label }}</span>
      </button>
      <p v-if="component.description" class="declarative-section__description">{{ component.description }}</p>
      <div v-show="!collapsed" class="declarative-section__body">
        <PluginDeclarativeComponent v-for="child in component.children || []" :key="child.id" :component="child" :model="model" :secret-replacements="secretReplacements" :secret-present="secretPresent" :base-pointer="basePointer" :condition-scope="scope" :force-validate="forceValidate" @change="(...args) => emit('change', ...args)" @secret="(...args) => emit('secret', ...args)" />
      </div>
    </fieldset>

    <div v-else-if="component.type === 'grid'" class="declarative-grid-block">
      <div v-if="component.label || component.description" class="declarative-grid-block__head">
        <span v-if="component.label" class="declarative-grid-block__label">{{ component.label }}</span>
        <p v-if="component.description" class="declarative-grid-block__description">{{ component.description }}</p>
      </div>
      <div class="declarative-grid" :style="{ '--declarative-grid-columns': component.columns || 2 }">
        <PluginDeclarativeComponent v-for="child in component.children || []" :key="child.id" :component="child" :model="model" :secret-replacements="secretReplacements" :secret-present="secretPresent" :base-pointer="basePointer" :condition-scope="scope" :force-validate="forceValidate" @change="(...args) => emit('change', ...args)" @secret="(...args) => emit('secret', ...args)" />
      </div>
    </div>

    <aside v-else-if="component.type === 'notice'" class="declarative-notice" :data-tone="component.tone"><strong>{{ component.label }}</strong><p v-if="component.description">{{ component.description }}</p></aside>

    <label v-else-if="component.type === 'toggle'" class="declarative-toggle" :class="{ 'declarative-toggle--on': value() === true, 'declarative-toggle--readonly': component.read_only }">
      <input type="checkbox" :checked="value() === true" :disabled="component.read_only" @change="change($event.target.checked)">
      <span class="declarative-toggle__track" aria-hidden="true"><span class="declarative-toggle__thumb"></span></span>
      <span class="declarative-toggle__text">
        <span class="declarative-toggle__label">{{ component.label }}</span>
        <small v-if="component.description" class="declarative-hint">{{ component.description }}</small>
      </span>
    </label>

    <div v-else-if="component.type === 'radio'" class="declarative-field declarative-radio-group-block">
      <span class="declarative-field__label">{{ component.label }}<small v-if="hintText" class="declarative-hint">{{ hintText }}</small></span>
      <p v-if="component.description" class="declarative-field__description">{{ component.description }}</p>
      <div class="declarative-radio-group" role="radiogroup" :aria-label="component.label">
        <label v-for="option in component.options || []" :key="option.value" class="declarative-choice" :class="{ 'declarative-choice--selected': value() === option.value }">
          <input type="radio" :name="radioGroupName" :checked="value() === option.value" :disabled="component.read_only" @change="change(option.value)">
          <span class="declarative-choice__control" aria-hidden="true"></span>
          <span class="declarative-choice__text">{{ option.label }}<small v-if="option.description" class="declarative-hint">{{ option.description }}</small></span>
        </label>
      </div>
      <small v-if="error" class="declarative-error">{{ error }}</small>
    </div>

    <div v-else-if="component.type === 'multiselect'" class="declarative-field declarative-multiselect-block">
      <span class="declarative-field__label">{{ component.label }}<small v-if="hintText" class="declarative-hint">{{ hintText }}</small></span>
      <p v-if="component.description" class="declarative-field__description">{{ component.description }}</p>
      <div class="declarative-multiselect-group">
        <label v-for="option in component.options || []" :key="option.value" class="declarative-choice" :class="{ 'declarative-choice--selected': selectedValues.includes(option.value) }">
          <input type="checkbox" :checked="selectedValues.includes(option.value)" :disabled="component.read_only" @change="toggleOption(option.value, $event.target.checked)">
          <span class="declarative-choice__control declarative-choice__control--box" aria-hidden="true"></span>
          <span class="declarative-choice__text">{{ option.label }}<small v-if="option.description" class="declarative-hint">{{ option.description }}</small></span>
        </label>
      </div>
      <small v-if="error" class="declarative-error">{{ error }}</small>
    </div>

    <div v-else-if="component.type === 'keyvalue'" class="declarative-field declarative-keyvalue">
      <span class="declarative-field__label">{{ component.label }}<small v-if="hintText" class="declarative-hint">{{ hintText }}</small></span>
      <p v-if="component.description" class="declarative-field__description">{{ component.description }}</p>
      <div class="declarative-keyvalue__rows">
        <div v-for="(row, index) in keyValueRows" :key="index" class="declarative-keyvalue__row" :class="{ 'declarative-keyvalue__row--duplicate': keyValueDuplicateIndexes.has(index) }">
          <input type="text" :value="row.key" placeholder="键" :disabled="component.read_only" @input="changeKeyValueKey(index, $event.target.value)">
          <input type="text" :value="row.value" placeholder="值" :disabled="component.read_only" @input="changeKeyValueValue(index, $event.target.value)">
          <button class="btn btn-secondary btn-sm" type="button" :disabled="component.read_only" @click="removeKeyValueRow(index)">移除</button>
          <small v-if="keyValueDuplicateIndexes.has(index)" class="declarative-error declarative-keyvalue__duplicate-hint">键重复，此行不会保存</small>
        </div>
        <p v-if="!keyValueRows.length" class="declarative-keyvalue__empty">暂无条目。</p>
      </div>
      <button class="btn btn-secondary btn-sm" type="button" :disabled="component.read_only" @click="addKeyValueRow">+ 添加</button>
      <small v-if="error" class="declarative-error">{{ error }}</small>
    </div>

    <label v-else-if="component.type === 'select'" class="declarative-field"><span class="declarative-field__label">{{ component.label }}<small v-if="hintText" class="declarative-hint">{{ hintText }}</small></span><p v-if="component.description" class="declarative-field__description">{{ component.description }}</p><select :value="value()" :required="component.required" :disabled="component.read_only" @change="change(selectValue($event.target.value))"><option v-for="option in component.options || []" :key="option.value" :value="option.value">{{ option.label }}</option></select><small v-if="error" class="declarative-error">{{ error }}</small></label>

    <label v-else-if="component.type === 'number'" class="declarative-field"><span class="declarative-field__label">{{ component.label }}<small v-if="hintText" class="declarative-hint">{{ hintText }}</small></span><p v-if="component.description" class="declarative-field__description">{{ component.description }}</p><input type="number" :value="value()" :min="component.minimum" :max="component.maximum" :step="component.step || 'any'" :required="component.required" :readonly="component.read_only" @input="change($event.target.value === '' ? null : Number($event.target.value))"><small v-if="error" class="declarative-error">{{ error }}</small></label>

    <div v-else-if="component.type === 'secret'" class="declarative-field declarative-secret"><span class="declarative-field__label">{{ component.label }}</span><input type="password" autocomplete="new-password" :value="secretReplacements[fullPointer] ?? ''" :required="component.required && !secretPresent(fullPointer)" :readonly="component.read_only" placeholder="留空以保留现有凭据" @input="changeSecret($event.target.value)"><small class="declarative-hint">{{ secretPresent(fullPointer) ? '已有凭据；留空保留，输入新值轮换。' : '尚未配置；明文仅随本次提交发送。' }}</small><small v-if="error" class="declarative-error">{{ error }}</small><button v-if="secretPresent(fullPointer) && !component.required && !component.read_only" class="btn btn-secondary btn-sm declarative-secret__clear" type="button" @click="changeSecret(null)">清除凭据</button></div>

    <label v-else-if="component.type === 'text' || component.type === 'textarea'" class="declarative-field"><span class="declarative-field__label">{{ component.label }}<small v-if="hintText" class="declarative-hint">{{ hintText }}</small></span><p v-if="component.description" class="declarative-field__description">{{ component.description }}</p><textarea v-if="component.type === 'textarea'" :value="value()" :placeholder="component.placeholder" :required="component.required" :readonly="component.read_only" @input="change($event.target.value)" /><input v-else type="text" :value="value()" :placeholder="component.placeholder" :required="component.required" :readonly="component.read_only" @input="change($event.target.value)"><small v-if="error" class="declarative-error">{{ error }}</small></label>

    <div v-else-if="component.type === 'array'" class="declarative-array">
      <div class="declarative-array-head"><span class="declarative-field__label">{{ component.label }}<small v-if="hintText" class="declarative-hint">{{ hintText }}</small></span><small v-if="error" class="declarative-error">{{ error }}</small></div>
      <p v-if="component.description" class="declarative-field__description">{{ component.description }}</p>
      <template v-if="isObjectItems">
        <fieldset v-for="(item, index) in items" :key="index" class="declarative-array-item">
          <div class="declarative-array-item__head">
            <span class="declarative-array-item__index">#{{ index + 1 }}</span>
            <div class="declarative-array-actions">
              <button class="btn btn-secondary btn-sm" type="button" :disabled="component.read_only || index === 0" @click="moveItem(index, -1)">上移</button>
              <button class="btn btn-secondary btn-sm" type="button" :disabled="component.read_only || index === items.length - 1" @click="moveItem(index, 1)">下移</button>
              <button class="btn btn-secondary btn-sm" type="button" :disabled="component.read_only" @click="removeItem(index)">移除</button>
            </div>
          </div>
          <PluginDeclarativeComponent v-for="child in component.children" :key="child.id" :component="child" :model="model" :secret-replacements="secretReplacements" :secret-present="secretPresent" :base-pointer="`${fullPointer}/${index}`" :condition-scope="item" :force-validate="forceValidate" @change="(...args) => emit('change', ...args)" @secret="(...args) => emit('secret', ...args)" />
        </fieldset>
      </template>
      <template v-else>
        <div v-for="(item, index) in items" :key="index" class="declarative-array-item declarative-array-scalar">
          <input type="text" :value="item" :disabled="component.read_only" @input="changeScalar(index, $event.target.value)">
          <button class="btn btn-secondary btn-sm" type="button" :disabled="component.read_only" @click="removeItem(index)">移除</button>
        </div>
      </template>
      <button class="btn btn-secondary btn-sm declarative-array__add" type="button" :disabled="component.read_only || (component.max_items != null && items.length >= component.max_items)" @click="addItem">+ 添加</button>
    </div>
  </template>
</template>

<style scoped>
/* --- shared field anatomy --- */
.declarative-field,
.declarative-array,
.declarative-grid-block { min-width: 0; display: grid; gap: var(--space-2); }
.declarative-field__label { display: flex; align-items: baseline; gap: var(--space-2); flex-wrap: wrap; color: var(--color-text-primary); font-size: var(--text-sm); font-weight: 600; }
.declarative-field__description,
.declarative-section__description,
.declarative-grid-block__description { margin: 0; color: var(--color-text-muted); font-size: var(--text-xs); }
.declarative-hint { color: var(--color-text-muted); font-size: var(--text-xs); font-weight: 400; }
.declarative-error { color: var(--color-danger); font-size: var(--text-xs); }
.declarative-field input,
.declarative-field textarea,
.declarative-field select,
.declarative-array-scalar input,
.declarative-keyvalue__row input {
  width: 100%; box-sizing: border-box; padding: .6rem .75rem;
  border: 1px solid var(--color-border-default); border-radius: var(--radius-md);
  background: var(--color-bg-surface); color: var(--color-text-primary); font: inherit;
  transition: border-color var(--duration-fast) var(--ease-default), box-shadow var(--duration-fast) var(--ease-default);
}
.declarative-field input:focus-visible,
.declarative-field textarea:focus-visible,
.declarative-field select:focus-visible,
.declarative-keyvalue__row input:focus-visible {
  outline: none; border-color: var(--color-primary);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--color-primary) 18%, transparent);
}
.declarative-field textarea { min-height: 5.5rem; resize: vertical; }

/* --- section --- */
.declarative-section {
  min-width: 0; margin: 0; padding: var(--space-4);
  border: 1px solid var(--color-border-subtle); border-radius: var(--radius-lg);
  background: var(--color-bg-surface);
  display: grid; gap: var(--space-3);
}
.declarative-section__legend { padding: 0 var(--space-1); color: var(--color-text-primary); font-size: var(--text-sm); font-weight: 600; }
.declarative-section__toggle {
  display: flex; align-items: center; gap: var(--space-2); justify-self: start;
  padding: var(--space-1) var(--space-2); margin-left: calc(-1 * var(--space-2));
  border: 0; border-radius: var(--radius-md); background: transparent; cursor: pointer;
  color: var(--color-text-primary); font-size: var(--text-sm); font-weight: 600;
}
.declarative-section__toggle:hover { background: var(--color-bg-subtle); }
.declarative-section__chevron { display: inline-block; transition: transform var(--duration-fast) var(--ease-default); color: var(--color-text-muted); }
.declarative-section__chevron--open { transform: rotate(90deg); }
.declarative-section__body { display: grid; gap: var(--space-4); min-width: 0; }

/* --- grid --- */
.declarative-grid-block__head { display: grid; gap: var(--space-1); }
.declarative-grid-block__label { color: var(--color-text-primary); font-size: var(--text-sm); font-weight: 600; }
.declarative-grid { display: grid; grid-template-columns: repeat(var(--declarative-grid-columns, 2), minmax(0, 1fr)); gap: var(--space-4); align-items: start; }
@media (max-width: 640px) {
  .declarative-grid { grid-template-columns: 1fr; }
}

/* --- notice --- */
.declarative-notice { padding: var(--space-3); border-radius: var(--radius-md); background: var(--color-bg-subtle); border-left: 3px solid var(--color-border-default); display: grid; gap: var(--space-1); }
.declarative-notice p { margin: 0; color: var(--color-text-muted); font-size: var(--text-sm); }
.declarative-notice[data-tone='warning'] { border-left-color: var(--color-warning); }
.declarative-notice[data-tone='danger'] { border-left-color: var(--color-danger); }

/* --- toggle switch --- */
.declarative-toggle { display: flex; align-items: center; gap: var(--space-3); cursor: pointer; padding: var(--space-1) 0; }
.declarative-toggle--readonly { cursor: default; opacity: .7; }
.declarative-toggle input { position: absolute; opacity: 0; width: 0; height: 0; }
.declarative-toggle__track {
  flex-shrink: 0; width: 2.25rem; height: 1.25rem; border-radius: var(--radius-full, 999px);
  background: var(--color-border-default); position: relative;
  transition: background var(--duration-fast) var(--ease-default);
}
.declarative-toggle__thumb {
  position: absolute; top: .15rem; left: .15rem; width: .95rem; height: .95rem; border-radius: 50%;
  background: var(--color-bg-surface); box-shadow: var(--shadow-xs);
  transition: transform var(--duration-fast) var(--ease-default);
}
.declarative-toggle--on .declarative-toggle__track { background: var(--color-primary); }
.declarative-toggle--on .declarative-toggle__thumb { transform: translateX(1rem); }
.declarative-toggle input:focus-visible + .declarative-toggle__track { box-shadow: 0 0 0 3px color-mix(in srgb, var(--color-primary) 18%, transparent); }
.declarative-toggle__text { display: grid; gap: 2px; min-width: 0; }
.declarative-toggle__label { color: var(--color-text-primary); font-size: var(--text-sm); font-weight: 600; }

/* --- radio / multiselect choices --- */
.declarative-radio-group,
.declarative-multiselect-group { display: grid; gap: var(--space-2); }
.declarative-choice {
  display: flex; align-items: flex-start; gap: var(--space-3); padding: var(--space-3);
  border: 1px solid var(--color-border-subtle); border-radius: var(--radius-lg);
  background: var(--color-bg-surface); cursor: pointer;
  transition: border-color var(--duration-fast) var(--ease-default), background var(--duration-fast) var(--ease-default);
}
.declarative-choice:hover { border-color: var(--color-border-default); }
.declarative-choice--selected { border-color: var(--color-primary); background: color-mix(in srgb, var(--color-primary) 6%, var(--color-bg-surface)); }
.declarative-choice input { position: absolute; opacity: 0; width: 0; height: 0; }
.declarative-choice__control {
  flex-shrink: 0; width: 1rem; height: 1rem; margin-top: .15rem; border-radius: 50%;
  border: 2px solid var(--color-border-default); background: var(--color-bg-surface);
  transition: border-color var(--duration-fast) var(--ease-default), box-shadow var(--duration-fast) var(--ease-default);
}
.declarative-choice__control--box { border-radius: var(--radius-sm, 4px); }
.declarative-choice--selected .declarative-choice__control { border-color: var(--color-primary); box-shadow: inset 0 0 0 3px var(--color-bg-surface); background: var(--color-primary); }
.declarative-choice input:focus-visible + .declarative-choice__control { box-shadow: 0 0 0 3px color-mix(in srgb, var(--color-primary) 18%, transparent); }
.declarative-choice__text { min-width: 0; display: grid; gap: 2px; color: var(--color-text-primary); font-size: var(--text-sm); }

/* --- keyvalue --- */
.declarative-keyvalue__rows { display: grid; gap: var(--space-2); }
.declarative-keyvalue__row { display: grid; grid-template-columns: minmax(0, 2fr) minmax(0, 3fr) auto; gap: var(--space-2); align-items: center; }
.declarative-keyvalue__empty { margin: 0; color: var(--color-text-muted); font-size: var(--text-xs); }
.declarative-keyvalue__row--duplicate input:first-child { border-color: var(--color-danger); }
.declarative-keyvalue__duplicate-hint { grid-column: 1 / -1; }
.declarative-keyvalue > .btn { justify-self: start; }
@media (max-width: 640px) {
  .declarative-keyvalue__row { grid-template-columns: 1fr; }
}

/* --- secret --- */
.declarative-secret__clear { justify-self: start; }

/* --- array --- */
.declarative-array-head { display: flex; gap: var(--space-2); align-items: baseline; justify-content: space-between; flex-wrap: wrap; }
.declarative-array-item {
  min-width: 0; margin: 0; padding: var(--space-3);
  border: 1px solid var(--color-border-subtle); border-radius: var(--radius-lg);
  background: var(--color-bg-surface);
  display: grid; gap: var(--space-3);
}
.declarative-array-item__head { display: flex; align-items: center; justify-content: space-between; gap: var(--space-2); }
.declarative-array-item__index { color: var(--color-text-muted); font-size: var(--text-xs); font-weight: 600; }
.declarative-array-actions { display: flex; gap: var(--space-2); flex-wrap: wrap; }
.declarative-array-scalar { grid-template-columns: 1fr auto; align-items: center; display: grid; }
.declarative-array__add { justify-self: start; }
</style>

# Frontend Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a unified `components/base/` design system and add card/list view toggles to the five resource pages, while preserving the existing Vue 3 + UnoCSS + 4-theme architecture.

**Architecture:** Add small, stateless Base components and a `useViewMode` composable. Pages keep their existing TanStack Query hooks and only replace presentation markup. New list rows are paired with existing card components and rendered through a generic `BaseTable`.

**Tech Stack:** Vue 3, Vite, Pinia, TanStack Query, Vue Router, UnoCSS, Vitest, `@vue/test-utils`.

---

## File Structure

### New files

- `panel/frontend/src/composables/useViewMode.js` — persistent card/list preference.
- `panel/frontend/src/composables/useViewMode.test.js`
- `panel/frontend/src/components/base/BaseButton.vue` — themed button primitive.
- `panel/frontend/src/components/base/BaseButton.test.js`
- `panel/frontend/src/components/base/BaseInput.vue` — themed input primitive.
- `panel/frontend/src/components/base/BaseInput.test.js`
- `panel/frontend/src/components/base/BaseSearch.vue` — search input with icon/clear.
- `panel/frontend/src/components/base/BaseSearch.test.js`
- `panel/frontend/src/components/base/BaseTag.vue` — user tag pill.
- `panel/frontend/src/components/base/BaseTag.test.js`
- `panel/frontend/src/components/base/BaseEmptyState.vue` — empty state illustration block.
- `panel/frontend/src/components/base/BaseEmptyState.test.js`
- `panel/frontend/src/components/base/BaseSkeleton.vue` — loading placeholder.
- `panel/frontend/src/components/base/BaseSkeleton.test.js`
- `panel/frontend/src/components/base/BaseErrorState.vue` — error block with retry.
- `panel/frontend/src/components/base/BaseErrorState.test.js`
- `panel/frontend/src/components/base/BaseTable.vue` — generic table with sort/empty/error.
- `panel/frontend/src/components/base/BaseTable.test.js`
- `panel/frontend/src/components/base/ViewToggle.vue` — card/list toggle.
- `panel/frontend/src/components/base/ViewToggle.test.js`
- `panel/frontend/src/components/rules/RuleListRow.vue` — HTTP rule table row.
- `panel/frontend/src/components/l4/L4RuleListRow.vue` — L4 rule table row.
- `panel/frontend/src/components/certs/CertListRow.vue` — certificate table row.
- `panel/frontend/src/components/relay/RelayListRow.vue` — relay listener table row.
- `panel/frontend/src/components/wireguard/WireGuardProfileListRow.vue` — WG profile table row.
- `panel/frontend/src/pages/RulesPage.test.js` — integration tests for view toggle.

### Modified files

- `panel/frontend/src/styles/utilities.css` — gradually remove migrated global classes (Phase 3).
- `panel/frontend/src/pages/RulesPage.vue` — add `ViewToggle`, `BaseTable`, `RuleListRow`; migrate to Base components.
- `panel/frontend/src/pages/L4RulesPage.vue` — same for L4.
- `panel/frontend/src/pages/CertsPage.vue` — same for certificates.
- `panel/frontend/src/pages/RelayListenersPage.vue` — same for relay listeners.
- `panel/frontend/src/pages/WireGuardProfilesPage.vue` — same for WireGuard profiles.
- `panel/frontend/src/components/rules/RuleTable.vue` — delete after `BaseTable` replacement.

---

## Task 1: Create `useViewMode` composable

**Files:**
- Create: `panel/frontend/src/composables/useViewMode.js`
- Test: `panel/frontend/src/composables/useViewMode.test.js`

- [ ] **Step 1: Write the failing test**

```js
import { describe, expect, it, beforeEach } from 'vitest'
import { useViewMode } from './useViewMode.js'

describe('useViewMode', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  it('defaults to card', () => {
    const { viewMode } = useViewMode('rules')
    expect(viewMode.value).toBe('card')
  })

  it('reads saved value from localStorage', () => {
    localStorage.setItem('nre.viewMode.rules', 'list')
    const { viewMode } = useViewMode('rules')
    expect(viewMode.value).toBe('list')
  })

  it('ignores invalid saved values', () => {
    localStorage.setItem('nre.viewMode.rules', 'gallery')
    const { viewMode } = useViewMode('rules')
    expect(viewMode.value).toBe('card')
  })

  it('persists changes to localStorage', () => {
    const { setViewMode } = useViewMode('rules')
    setViewMode('list')
    expect(localStorage.getItem('nre.viewMode.rules')).toBe('list')
  })

  it('uses a different key per resource', () => {
    const { setViewMode } = useViewMode('certs')
    setViewMode('list')
    expect(localStorage.getItem('nre.viewMode.certs')).toBe('list')
    expect(localStorage.getItem('nre.viewMode.rules')).toBeNull()
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd panel/frontend && npx vitest run src/composables/useViewMode.test.js`

Expected: FAIL — `useViewMode` not found.

- [ ] **Step 3: Write minimal implementation**

```js
import { ref } from 'vue'

const VALID_MODES = new Set(['card', 'list'])

export function useViewMode(key, defaultMode = 'card') {
  const storageKey = `nre.viewMode.${key}`
  const initial = readStoredMode(storageKey, defaultMode)
  const viewMode = ref(initial)

  function setViewMode(value) {
    if (!VALID_MODES.has(value)) return
    viewMode.value = value
    if (typeof localStorage !== 'undefined') {
      localStorage.setItem(storageKey, value)
    }
  }

  return { viewMode, setViewMode }
}

function readStoredMode(storageKey, defaultMode) {
  if (typeof localStorage === 'undefined') return defaultMode
  const saved = localStorage.getItem(storageKey)
  return VALID_MODES.has(saved) ? saved : defaultMode
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd panel/frontend && npx vitest run src/composables/useViewMode.test.js`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add panel/frontend/src/composables/useViewMode.js panel/frontend/src/composables/useViewMode.test.js
git commit -m "feat(frontend): add useViewMode composable for card/list persistence"
```

---

## Task 2: Create `BaseButton`

**Files:**
- Create: `panel/frontend/src/components/base/BaseButton.vue`
- Test: `panel/frontend/src/components/base/BaseButton.test.js`

- [ ] **Step 1: Write the failing test**

```js
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import BaseButton from './BaseButton.vue'

describe('BaseButton', () => {
  it('renders variant class', () => {
    const wrapper = mount(BaseButton, { props: { variant: 'primary' }, slots: { default: 'Save' } })
    expect(wrapper.classes()).toContain('base-button--primary')
    expect(wrapper.text()).toBe('Save')
  })

  it('renders size class', () => {
    const wrapper = mount(BaseButton, { props: { variant: 'primary', size: 'sm' } })
    expect(wrapper.classes()).toContain('base-button--sm')
  })

  it('emits click when enabled', async () => {
    const wrapper = mount(BaseButton)
    await wrapper.trigger('click')
    expect(wrapper.emitted('click')).toHaveLength(1)
  })

  it('does not emit click when disabled', async () => {
    const wrapper = mount(BaseButton, { props: { disabled: true } })
    await wrapper.trigger('click')
    expect(wrapper.emitted('click')).toBeUndefined()
  })

  it('does not emit click when loading', async () => {
    const wrapper = mount(BaseButton, { props: { loading: true } })
    await wrapper.trigger('click')
    expect(wrapper.emitted('click')).toBeUndefined()
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd panel/frontend && npx vitest run src/components/base/BaseButton.test.js`

Expected: FAIL.

- [ ] **Step 3: Write minimal implementation**

```vue
<template>
  <component
    :is="as"
    type="button"
    class="base-button"
    :class="[
      `base-button--${variant}`,
      `base-button--${size}`,
      { 'base-button--loading': loading }
    ]"
    :disabled="disabled || loading"
    @click="handleClick"
  >
    <span v-if="loading" class="spinner spinner--sm" aria-hidden="true" />
    <slot />
  </component>
</template>

<script setup>
import { computed } from 'vue'

const props = defineProps({
  variant: {
    type: String,
    default: 'primary',
    validator: (v) => ['primary', 'secondary', 'ghost', 'danger'].includes(v)
  },
  size: {
    type: String,
    default: 'md',
    validator: (v) => ['sm', 'md', 'lg'].includes(v)
  },
  as: { type: String, default: 'button' },
  disabled: { type: Boolean, default: false },
  loading: { type: Boolean, default: false }
})

const emit = defineEmits(['click'])

function handleClick(e) {
  if (!props.disabled && !props.loading) {
    emit('click', e)
  }
}
</script>

<style scoped>
.base-button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 0.375rem;
  border-radius: var(--radius-full);
  font-size: var(--text-sm);
  font-weight: var(--font-semibold);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-default);
  border: 1.5px solid transparent;
  font-family: inherit;
  text-decoration: none;
  white-space: nowrap;
  position: relative;
  overflow: hidden;
  line-height: 1.25;
}

.base-button:disabled {
  opacity: 0.5;
  cursor: not-allowed;
  transform: none;
}

.base-button--primary {
  background: var(--color-primary);
  color: white;
}

.base-button--primary:hover:not(:disabled) {
  background: var(--color-primary-hover);
  transform: translateY(-1px);
}

.base-button--secondary {
  background: transparent;
  color: var(--color-text-secondary);
  border-color: var(--color-border-default);
}

.base-button--secondary:hover:not(:disabled) {
  border-color: var(--color-primary);
  color: var(--color-primary);
  background: var(--color-primary-subtle);
}

.base-button--ghost {
  background: transparent;
  color: var(--color-text-secondary);
  border: none;
  padding: 10px 16px;
}

.base-button--ghost:hover:not(:disabled) {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}

.base-button--danger {
  background: var(--color-danger);
  color: white;
}

.base-button--danger:hover:not(:disabled) {
  background: #dc2626;
  transform: translateY(-1px);
}

.base-button--sm { padding: 6px 16px; font-size: var(--text-xs); }
.base-button--md { padding: 10px 24px; font-size: var(--text-sm); }
.base-button--lg { padding: 14px 32px; font-size: var(--text-base); }

.spinner {
  width: 16px;
  height: 16px;
  border: 2px solid currentColor;
  border-top-color: transparent;
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}
</style>
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd panel/frontend && npx vitest run src/components/base/BaseButton.test.js`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add panel/frontend/src/components/base/BaseButton.vue panel/frontend/src/components/base/BaseButton.test.js
git commit -m "feat(frontend): add BaseButton component"
```

---

## Task 3: Create `BaseInput`

**Files:**
- Create: `panel/frontend/src/components/base/BaseInput.vue`
- Test: `panel/frontend/src/components/base/BaseInput.test.js`

- [ ] **Step 1: Write the failing test**

```js
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import BaseInput from './BaseInput.vue'

describe('BaseInput', () => {
  it('renders value', () => {
    const wrapper = mount(BaseInput, { props: { modelValue: 'hello' } })
    expect(wrapper.find('input').element.value).toBe('hello')
  })

  it('emits update:modelValue on input', async () => {
    const wrapper = mount(BaseInput)
    await wrapper.find('input').setValue('world')
    expect(wrapper.emitted('update:modelValue')).toEqual([['world']])
  })

  it('adds error class when error is true', () => {
    const wrapper = mount(BaseInput, { props: { error: true } })
    expect(wrapper.classes()).toContain('base-input--error')
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd panel/frontend && npx vitest run src/components/base/BaseInput.test.js`

Expected: FAIL.

- [ ] **Step 3: Write minimal implementation**

```vue
<template>
  <input
    class="base-input"
    :class="{ 'base-input--error': error }"
    :type="type"
    :value="modelValue"
    :placeholder="placeholder"
    :disabled="disabled"
    @input="$emit('update:modelValue', $event.target.value)"
  />
</template>

<script setup>
defineProps({
  modelValue: { type: String, default: '' },
  type: { type: String, default: 'text' },
  placeholder: { type: String, default: '' },
  disabled: { type: Boolean, default: false },
  error: { type: Boolean, default: false }
})
defineEmits(['update:modelValue'])
</script>

<style scoped>
.base-input {
  width: 100%;
  padding: 10px 16px;
  border-radius: 10px;
  border: 1.5px solid var(--color-border-default);
  background: var(--color-bg-surface);
  font-size: var(--text-sm);
  color: var(--color-text-primary);
  outline: none;
  font-family: inherit;
  box-sizing: border-box;
  transition: border-color var(--duration-fast) var(--ease-default),
              box-shadow var(--duration-fast) var(--ease-default);
}

.base-input:focus {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.base-input::placeholder {
  color: var(--color-text-muted);
}

.base-input--error {
  border-color: var(--color-danger);
}

.base-input--error:focus {
  box-shadow: 0 0 0 3px var(--color-danger-50);
}
</style>
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd panel/frontend && npx vitest run src/components/base/BaseInput.test.js`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add panel/frontend/src/components/base/BaseInput.vue panel/frontend/src/components/base/BaseInput.test.js
git commit -m "feat(frontend): add BaseInput component"
```

---

## Task 4: Create `BaseSearch`

**Files:**
- Create: `panel/frontend/src/components/base/BaseSearch.vue`
- Test: `panel/frontend/src/components/base/BaseSearch.test.js`

- [ ] **Step 1: Write the failing test**

```js
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import BaseSearch from './BaseSearch.vue'

describe('BaseSearch', () => {
  it('renders placeholder', () => {
    const wrapper = mount(BaseSearch, { props: { modelValue: '', placeholder: 'Search' } })
    expect(wrapper.find('input').attributes('placeholder')).toBe('Search')
  })

  it('emits update:modelValue', async () => {
    const wrapper = mount(BaseSearch)
    await wrapper.find('input').setValue('query')
    expect(wrapper.emitted('update:modelValue')).toEqual([['query']])
  })

  it('clears value when clear button clicked', async () => {
    const wrapper = mount(BaseSearch, { props: { modelValue: 'query' } })
    await wrapper.find('.base-search__clear').trigger('click')
    expect(wrapper.emitted('update:modelValue')).toEqual([['']])
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd panel/frontend && npx vitest run src/components/base/BaseSearch.test.js`

Expected: FAIL.

- [ ] **Step 3: Write minimal implementation**

```vue
<template>
  <div class="base-search">
    <svg class="base-search__icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
      <circle cx="11" cy="11" r="8"/>
      <line x1="21" y1="21" x2="16.65" y2="16.65"/>
    </svg>
    <BaseInput
      class="base-search__input"
      :model-value="modelValue"
      :placeholder="placeholder"
      @update:model-value="$emit('update:modelValue', $event)"
    />
    <button
      v-if="modelValue"
      type="button"
      class="base-search__clear"
      aria-label="清除"
      @click="$emit('update:modelValue', '')"
    >
      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
        <line x1="18" y1="6" x2="6" y2="18"/>
        <line x1="6" y1="6" x2="18" y2="18"/>
      </svg>
    </button>
  </div>
</template>

<script setup>
import BaseInput from './BaseInput.vue'

defineProps({
  modelValue: { type: String, default: '' },
  placeholder: { type: String, default: '搜索...' }
})
defineEmits(['update:modelValue'])
</script>

<style scoped>
.base-search {
  display: flex;
  align-items: center;
  position: relative;
  width: 100%;
}

.base-search__icon {
  position: absolute;
  left: 12px;
  color: var(--color-text-muted);
  pointer-events: none;
}

.base-search__input {
  padding-left: 2.25rem;
  padding-right: 2rem;
}

.base-search__clear {
  position: absolute;
  right: 10px;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  border: none;
  background: var(--color-bg-hover);
  border-radius: 50%;
  color: var(--color-text-secondary);
  cursor: pointer;
  padding: 0;
}
</style>
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd panel/frontend && npx vitest run src/components/base/BaseSearch.test.js`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add panel/frontend/src/components/base/BaseSearch.vue panel/frontend/src/components/base/BaseSearch.test.js
git commit -m "feat(frontend): add BaseSearch component"
```

---

## Task 5: Create `BaseTag`

**Files:**
- Create: `panel/frontend/src/components/base/BaseTag.vue`
- Test: `panel/frontend/src/components/base/BaseTag.test.js`

- [ ] **Step 1: Write the failing test**

```js
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import BaseTag from './BaseTag.vue'

describe('BaseTag', () => {
  it('renders slot content', () => {
    const wrapper = mount(BaseTag, { slots: { default: 'tag-name' } })
    expect(wrapper.text()).toBe('tag-name')
    expect(wrapper.classes()).toContain('base-tag')
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd panel/frontend && npx vitest run src/components/base/BaseTag.test.js`

Expected: FAIL.

- [ ] **Step 3: Write minimal implementation**

```vue
<template>
  <span class="base-tag">
    <slot />
  </span>
</template>

<style scoped>
.base-tag {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 4px 12px;
  border-radius: var(--radius-full);
  font-size: var(--text-xs);
  font-weight: var(--font-semibold);
  background: var(--color-primary-subtle);
  color: var(--color-primary);
  border: 1px solid var(--color-border-subtle);
  line-height: 1.4;
  white-space: nowrap;
  transition: all var(--duration-fast) var(--ease-default);
}
</style>
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd panel/frontend && npx vitest run src/components/base/BaseTag.test.js`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add panel/frontend/src/components/base/BaseTag.vue panel/frontend/src/components/base/BaseTag.test.js
git commit -m "feat(frontend): add BaseTag component"
```

---

## Task 6: Create `BaseEmptyState`, `BaseSkeleton`, `BaseErrorState`

**Files:**
- Create: `panel/frontend/src/components/base/BaseEmptyState.vue`
- Test: `panel/frontend/src/components/base/BaseEmptyState.test.js`
- Create: `panel/frontend/src/components/base/BaseSkeleton.vue`
- Test: `panel/frontend/src/components/base/BaseSkeleton.test.js`
- Create: `panel/frontend/src/components/base/BaseErrorState.vue`
- Test: `panel/frontend/src/components/base/BaseErrorState.test.js`

- [ ] **Step 1: Write the failing tests**

`panel/frontend/src/components/base/BaseEmptyState.test.js`:

```js
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import BaseEmptyState from './BaseEmptyState.vue'

describe('BaseEmptyState', () => {
  it('renders title and message', () => {
    const wrapper = mount(BaseEmptyState, {
      props: { title: 'Nothing here', message: 'Add one to start' }
    })
    expect(wrapper.text()).toContain('Nothing here')
    expect(wrapper.text()).toContain('Add one to start')
  })

  it('renders action slot', () => {
    const wrapper = mount(BaseEmptyState, {
      props: { title: 'Empty' },
      slots: { action: '<button>Add</button>' }
    })
    expect(wrapper.find('button').exists()).toBe(true)
  })
})
```

`panel/frontend/src/components/base/BaseSkeleton.test.js`:

```js
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import BaseSkeleton from './BaseSkeleton.vue'

describe('BaseSkeleton', () => {
  it('renders the requested number of rows', () => {
    const wrapper = mount(BaseSkeleton, { props: { rows: 3 } })
    expect(wrapper.findAll('.base-skeleton__row').length).toBe(3)
  })
})
```

`panel/frontend/src/components/base/BaseErrorState.test.js`:

```js
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import BaseErrorState from './BaseErrorState.vue'

describe('BaseErrorState', () => {
  it('renders title and message', () => {
    const wrapper = mount(BaseErrorState, {
      props: { title: 'Failed', message: 'Try again' }
    })
    expect(wrapper.text()).toContain('Failed')
    expect(wrapper.text()).toContain('Try again')
  })

  it('emits retry when button clicked', async () => {
    const wrapper = mount(BaseErrorState, { props: { title: 'Failed' } })
    await wrapper.find('button').trigger('click')
    expect(wrapper.emitted('retry')).toHaveLength(1)
  })
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd panel/frontend && npx vitest run src/components/base/BaseEmptyState.test.js src/components/base/BaseSkeleton.test.js src/components/base/BaseErrorState.test.js`

Expected: FAIL.

- [ ] **Step 3: Write minimal implementations**

`panel/frontend/src/components/base/BaseEmptyState.vue`:

```vue
<template>
  <div class="base-empty-state">
    <div class="base-empty-state__icon">
      <slot name="icon">
        <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
          <rect x="3" y="3" width="18" height="18" rx="2" ry="2" />
          <line x1="9" y1="9" x2="15" y2="15" />
          <line x1="15" y1="9" x2="9" y2="15" />
        </svg>
      </slot>
    </div>
    <h3 class="base-empty-state__title">{{ title }}</h3>
    <p v-if="message" class="base-empty-state__message">{{ message }}</p>
    <div v-if="$slots.action" class="base-empty-state__action">
      <slot name="action" />
    </div>
  </div>
</template>

<script setup>
defineProps({
  title: { type: String, required: true },
  message: { type: String, default: '' }
})
</script>

<style scoped>
.base-empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 0.75rem;
  padding: 4rem 2rem;
  color: var(--color-text-muted);
  text-align: center;
  animation: fadeIn 0.3s var(--ease-default) both;
}

.base-empty-state__icon {
  color: var(--color-primary);
  opacity: 0.4;
}

.base-empty-state__title {
  font-size: var(--text-base);
  font-weight: var(--font-semibold);
  color: var(--color-text-secondary);
  margin: 0;
}

.base-empty-state__message {
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
  margin: 0;
}

.base-empty-state__action {
  margin-top: 0.5rem;
}
</style>
```

`panel/frontend/src/components/base/BaseSkeleton.vue`:

```vue
<template>
  <div class="base-skeleton">
    <div v-for="i in rows" :key="i" class="base-skeleton__row">
      <div class="base-skeleton__line" />
    </div>
  </div>
</template>

<script setup>
defineProps({
  rows: { type: Number, default: 5 }
})
</script>

<style scoped>
.base-skeleton {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
  padding: 1rem 0;
}

.base-skeleton__row {
  height: 48px;
  background: var(--color-bg-subtle);
  border-radius: var(--radius-md);
  animation: pulse 1.5s ease-in-out infinite;
}

@keyframes pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.5; }
}
</style>
```

`panel/frontend/src/components/base/BaseErrorState.vue`:

```vue
<template>
  <div class="base-error-state">
    <div class="base-error-state__icon">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <circle cx="12" cy="12" r="10" />
        <line x1="12" y1="8" x2="12" y2="12" />
        <line x1="12" y1="16" x2="12.01" y2="16" />
      </svg>
    </div>
    <h3 class="base-error-state__title">{{ title }}</h3>
    <p v-if="message" class="base-error-state__message">{{ message }}</p>
    <BaseButton variant="secondary" size="sm" @click="$emit('retry')">
      重试
    </BaseButton>
  </div>
</template>

<script setup>
import BaseButton from './BaseButton.vue'

defineProps({
  title: { type: String, default: '加载失败' },
  message: { type: String, default: '' }
})
defineEmits(['retry'])
</script>

<style scoped>
.base-error-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 0.75rem;
  padding: 4rem 2rem;
  color: var(--color-text-muted);
  text-align: center;
}

.base-error-state__icon {
  color: var(--color-danger);
  opacity: 0.6;
}

.base-error-state__title {
  font-size: var(--text-base);
  font-weight: var(--font-semibold);
  color: var(--color-text-secondary);
  margin: 0;
}

.base-error-state__message {
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
  margin: 0;
}
</style>
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd panel/frontend && npx vitest run src/components/base/BaseEmptyState.test.js src/components/base/BaseSkeleton.test.js src/components/base/BaseErrorState.test.js`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add panel/frontend/src/components/base/BaseEmptyState.vue panel/frontend/src/components/base/BaseEmptyState.test.js panel/frontend/src/components/base/BaseSkeleton.vue panel/frontend/src/components/base/BaseSkeleton.test.js panel/frontend/src/components/base/BaseErrorState.vue panel/frontend/src/components/base/BaseErrorState.test.js
git commit -m "feat(frontend): add BaseEmptyState, BaseSkeleton, BaseErrorState"
```

---

## Task 7: Create `BaseTable`

**Files:**
- Create: `panel/frontend/src/components/base/BaseTable.vue`
- Test: `panel/frontend/src/components/base/BaseTable.test.js`

- [ ] **Step 1: Write the failing test**

```js
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import BaseTable from './BaseTable.vue'

describe('BaseTable', () => {
  const columns = [
    { key: 'name', label: 'Name' },
    { key: 'status', label: 'Status', sortable: true }
  ]
  const rows = [
    { id: 1, name: 'Alpha', status: 'ok' },
    { id: 2, name: 'Beta', status: 'pending' }
  ]

  it('renders headers', () => {
    const wrapper = mount(BaseTable, { props: { columns, rows } })
    const headers = wrapper.findAll('th').map(th => th.text())
    expect(headers).toEqual(['Name', 'Status'])
  })

  it('renders rows', () => {
    const wrapper = mount(BaseTable, { props: { columns, rows } })
    expect(wrapper.text()).toContain('Alpha')
    expect(wrapper.text()).toContain('Beta')
  })

  it('emits sort event when sortable header clicked', async () => {
    const wrapper = mount(BaseTable, { props: { columns, rows } })
    const statusHeader = wrapper.findAll('th').find(th => th.text().includes('Status'))
    await statusHeader.trigger('click')
    expect(wrapper.emitted('sort')).toHaveLength(1)
    expect(wrapper.emitted('sort')[0]).toEqual(['status'])
  })

  it('renders empty state when no rows', () => {
    const wrapper = mount(BaseTable, {
      props: { columns, rows: [], emptyTitle: 'No data' }
    })
    expect(wrapper.text()).toContain('No data')
  })

  it('renders error state and emits retry', async () => {
    const wrapper = mount(BaseTable, {
      props: { columns, rows: [], error: 'boom' }
    })
    expect(wrapper.text()).toContain('boom')
    await wrapper.find('.base-error-state button').trigger('click')
    expect(wrapper.emitted('retry')).toHaveLength(1)
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd panel/frontend && npx vitest run src/components/base/BaseTable.test.js`

Expected: FAIL.

- [ ] **Step 3: Write minimal implementation**

```vue
<template>
  <div class="base-table__wrapper">
    <table class="base-table">
      <thead>
        <tr>
          <th
            v-for="col in columns"
            :key="col.key"
            :class="{ 'base-table__th--sortable': col.sortable }"
            @click="col.sortable && $emit('sort', col.key)"
          >
            {{ col.label }}
            <span v-if="col.sortable" class="base-table__sort">↕</span>
          </th>
        </tr>
      </thead>
      <tbody>
        <slot name="row" v-for="row in rows" :key="rowKey(row)" :row="row">
          <tr>
            <td v-for="col in columns" :key="col.key">
              <slot :name="`cell-${col.key}`" :row="row" :value="row[col.key]">
                {{ row[col.key] }}
              </slot>
            </td>
          </tr>
        </slot>
      </tbody>
    </table>

    <BaseSkeleton v-if="loading" />

    <BaseEmptyState
      v-else-if="rows.length === 0 && !error"
      :title="emptyTitle"
      :message="emptyMessage"
    >
      <template v-if="$slots.emptyAction" #action>
        <slot name="emptyAction" />
      </template>
    </BaseEmptyState>

    <BaseErrorState
      v-else-if="error"
      :title="errorTitle"
      :message="errorMessage || error"
      @retry="$emit('retry')"
    />
  </div>
</template>

<script setup>
import BaseSkeleton from './BaseSkeleton.vue'
import BaseEmptyState from './BaseEmptyState.vue'
import BaseErrorState from './BaseErrorState.vue'

const props = defineProps({
  columns: { type: Array, required: true },
  rows: { type: Array, default: () => [] },
  rowKey: { type: Function, default: (row) => row.id },
  loading: { type: Boolean, default: false },
  error: { type: [String, Object], default: '' },
  emptyTitle: { type: String, default: '暂无数据' },
  emptyMessage: { type: String, default: '' },
  errorTitle: { type: String, default: '加载失败' },
  errorMessage: { type: String, default: '' }
})

defineEmits(['sort', 'retry'])
</script>

<style scoped>
.base-table__wrapper {
  overflow-x: auto;
}

.base-table {
  width: 100%;
  border-collapse: collapse;
}

.base-table th {
  text-align: left;
  padding: 0.75rem 1rem;
  font-size: 0.75rem;
  font-weight: var(--font-semibold);
  color: var(--color-text-tertiary);
  border-bottom: 1px solid var(--color-border-default);
  white-space: nowrap;
}

.base-table__th--sortable {
  cursor: pointer;
  user-select: none;
}

.base-table__th--sortable:hover {
  color: var(--color-text-primary);
}

.base-table__sort {
  margin-left: 0.25rem;
  opacity: 0.5;
}

.base-table td {
  padding: 0.875rem 1rem;
  vertical-align: middle;
  border-bottom: 1px solid var(--color-border-subtle);
}

.base-table tbody tr:hover {
  background: var(--color-bg-hover);
}
</style>
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd panel/frontend && npx vitest run src/components/base/BaseTable.test.js`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add panel/frontend/src/components/base/BaseTable.vue panel/frontend/src/components/base/BaseTable.test.js
git commit -m "feat(frontend): add BaseTable component"
```

---

## Task 8: Create `ViewToggle`

**Files:**
- Create: `panel/frontend/src/components/base/ViewToggle.vue`
- Test: `panel/frontend/src/components/base/ViewToggle.test.js`

- [ ] **Step 1: Write the failing test**

```js
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import ViewToggle from './ViewToggle.vue'

describe('ViewToggle', () => {
  it('renders both options', () => {
    const wrapper = mount(ViewToggle)
    expect(wrapper.findAll('button').length).toBe(2)
  })

  it('marks active option', () => {
    const wrapper = mount(ViewToggle, { props: { modelValue: 'list' } })
    const active = wrapper.find('.view-toggle__btn--active')
    expect(active.exists()).toBe(true)
    expect(active.text()).toContain('列表')
  })

  it('emits update:modelValue on click', async () => {
    const wrapper = mount(ViewToggle, { props: { modelValue: 'card' } })
    const listBtn = wrapper.findAll('button').find(btn => btn.text().includes('列表'))
    await listBtn.trigger('click')
    expect(wrapper.emitted('update:modelValue')).toEqual([['list']])
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd panel/frontend && npx vitest run src/components/base/ViewToggle.test.js`

Expected: FAIL.

- [ ] **Step 3: Write minimal implementation**

```vue
<template>
  <div class="view-toggle" role="group" :aria-label="label">
    <button
      v-for="opt in options"
      :key="opt.value"
      type="button"
      class="view-toggle__btn"
      :class="{ 'view-toggle__btn--active': modelValue === opt.value }"
      :aria-pressed="modelValue === opt.value"
      @click="$emit('update:modelValue', opt.value)"
    >
      <slot name="icon" :option="opt">
        {{ opt.label }}
      </slot>
    </button>
  </div>
</template>

<script setup>
defineProps({
  modelValue: { type: String, default: 'card' },
  options: {
    type: Array,
    default: () => [
      { value: 'card', label: '卡片' },
      { value: 'list', label: '列表' }
    ]
  },
  label: { type: String, default: '视图切换' }
})
defineEmits(['update:modelValue'])
</script>

<style scoped>
.view-toggle {
  display: inline-flex;
  align-items: center;
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-md);
  overflow: hidden;
}

.view-toggle__btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 0.25rem;
  padding: 6px 12px;
  background: var(--color-bg-surface);
  border: none;
  color: var(--color-text-secondary);
  font-size: var(--text-xs);
  font-weight: var(--font-semibold);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-default);
}

.view-toggle__btn:hover:not(.view-toggle__btn--active) {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}

.view-toggle__btn--active {
  background: var(--color-primary);
  color: white;
}
</style>
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd panel/frontend && npx vitest run src/components/base/ViewToggle.test.js`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add panel/frontend/src/components/base/ViewToggle.vue panel/frontend/src/components/base/ViewToggle.test.js
git commit -m "feat(frontend): add ViewToggle component"
```

---

## Task 9: Add list view to RulesPage

**Files:**
- Create: `panel/frontend/src/components/rules/RuleListRow.vue`
- Modify: `panel/frontend/src/pages/RulesPage.vue`
- Test: `panel/frontend/src/pages/RulesPage.test.js`

- [ ] **Step 1: Write the failing integration test**

```js
import { describe, expect, it } from 'vitest'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)
const source = fs.readFileSync(path.resolve(__dirname, 'RulesPage.vue'), 'utf8')

describe('RulesPage list view', () => {
  it('imports ViewToggle and useViewMode', () => {
    expect(source).toMatch(/import\s+ViewToggle\s+from\s+['"]\.\.\/components\/base\/ViewToggle\.vue['"]/)
    expect(source).toMatch(/import\s+\{\s*useViewMode\s*\}\s+from\s+['"]\.\.\/composables\/useViewMode['"]/)
  })

  it('imports BaseTable and RuleListRow', () => {
    expect(source).toMatch(/import\s+BaseTable\s+from\s+['"]\.\.\/components\/base\/BaseTable\.vue['"]/)
    expect(source).toMatch(/import\s+RuleListRow\s+from\s+['"]\.\.\/components\/rules\/RuleListRow\.vue['"]/)
  })

  it('renders list view conditionally', () => {
    expect(source).toMatch(/viewMode\s*===\s*['"]list['"]/)
    expect(source).toMatch(/ViewToggle/)
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd panel/frontend && npx vitest run src/pages/RulesPage.test.js`

Expected: FAIL.

- [ ] **Step 3: Create `RuleListRow.vue`**

```vue
<template>
  <tr class="rule-list-row">
    <td>
      <button
        class="rule-list-row__toggle"
        :class="{ 'rule-list-row__toggle--on': rule.enabled }"
        @click="$emit('toggle', rule)"
      >
        <span class="rule-list-row__knob" />
      </button>
    </td>
    <td class="rule-list-row__url">{{ rule.frontend_url }}</td>
    <td class="rule-list-row__backend" :title="backendsTooltip">{{ backendLabel }}</td>
    <td>
      <div class="rule-list-row__tags">
        <BaseTag v-for="tag in rule.tags || []" :key="tag">{{ tag }}</BaseTag>
      </div>
    </td>
    <td>
      <div class="rule-list-row__actions">
        <BaseIconButton title="复制" @click="$emit('copy', rule)">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <rect x="9" y="9" width="13" height="13" rx="2"/>
            <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/>
          </svg>
        </BaseIconButton>
        <BaseIconButton title="编辑" @click="$emit('edit', rule)">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
            <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
          </svg>
        </BaseIconButton>
        <BaseIconButton tone="primary" title="诊断" @click="$emit('diagnose', rule)">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M3 12h4l2-6 4 12 2-6h6"/>
          </svg>
        </BaseIconButton>
        <BaseIconButton tone="danger" title="删除" @click="$emit('delete', rule)">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <polyline points="3 6 5 6 21 6"/>
            <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
          </svg>
        </BaseIconButton>
      </div>
    </td>
  </tr>
</template>

<script setup>
import { computed } from 'vue'
import BaseTag from '../base/BaseTag.vue'
import BaseIconButton from '../base/BaseIconButton.vue'

const props = defineProps({
  rule: { type: Object, required: true }
})

defineEmits(['edit', 'toggle', 'copy', 'diagnose', 'delete'])

const backends = computed(() => {
  if (Array.isArray(props.rule?.backends) && props.rule.backends.length > 0) {
    return props.rule.backends
      .map((b) => String(b?.url || '').trim())
      .filter(Boolean)
  }
  return []
})

const backendLabel = computed(() => {
  const list = backends.value
  if (list.length === 0) return '-'
  if (list.length === 1) return list[0]
  return `${list[0]} +${list.length - 1}`
})

const backendsTooltip = computed(() => backends.value.join('\n'))
</script>

<style scoped>
.rule-list-row__url,
.rule-list-row__backend {
  font-family: var(--font-mono);
  font-size: 0.8125rem;
  color: var(--color-text-primary);
}

.rule-list-row__backend {
  color: var(--color-text-secondary);
}

.rule-list-row__tags {
  display: flex;
  gap: 0.25rem;
  flex-wrap: wrap;
}

.rule-list-row__actions {
  display: flex;
  gap: 0.25rem;
}

.rule-list-row__toggle {
  width: 40px;
  height: 22px;
  border-radius: 11px;
  border: none;
  background: var(--color-bg-subtle);
  cursor: pointer;
  position: relative;
  transition: background 0.2s;
  padding: 0;
}

.rule-list-row__toggle--on {
  background: var(--color-primary);
}

.rule-list-row__knob {
  position: absolute;
  top: 3px;
  left: 3px;
  width: 16px;
  height: 16px;
  border-radius: 50%;
  background: white;
  transition: transform 0.2s;
}

.rule-list-row__toggle--on .rule-list-row__knob {
  transform: translateX(18px);
}
</style>
```

- [ ] **Step 4: Modify `RulesPage.vue`**

Add imports near the top of `<script setup>`:

```js
import { useViewMode } from '../composables/useViewMode'
import ViewToggle from '../components/base/ViewToggle.vue'
import BaseTable from '../components/base/BaseTable.vue'
import RuleListRow from '../components/rules/RuleListRow.vue'
```

Add after `enabledCount`:

```js
const { viewMode, setViewMode } = useViewMode('rules')

const ruleColumns = [
  { key: 'enabled', label: '' },
  { key: 'frontend_url', label: '前端地址' },
  { key: 'backends', label: '后端地址' },
  { key: 'tags', label: '标签' },
  { key: 'actions', label: '操作' }
]
```

Replace the card-grid block in `RulesPage.vue:67-83` with:

```vue
    <div v-if="agentId && filteredRules.length" class="rules-page__content">
      <div class="rules-page__toolbar">
        <ViewToggle :model-value="viewMode" @update:model-value="setViewMode" />
      </div>

      <BaseTable
        v-if="viewMode === 'list'"
        :columns="ruleColumns"
        :rows="filteredRules"
        :loading="isLoading"
        empty-title="暂无规则"
      >
        <template #row="{ row }">
          <RuleListRow
            :rule="row"
            @edit="startEdit"
            @toggle="toggleRule"
            @copy="handleCopy"
            @diagnose="openDiagnostic"
            @delete="startDelete"
          />
        </template>
      </BaseTable>

      <div v-else class="rule-grid">
        <RuleCard
          v-for="rule in filteredRules"
          :key="rule.id"
          :rule="rule"
          :agent="selectedAgent"
          :traffic="trafficForRule(rule)"
          :agent-node-total="agentNodeTotal"
          @edit="startEdit"
          @toggle="toggleRule"
          @copy="handleCopy"
          @diagnose="openDiagnostic"
          @traffic-click="openTrendModal"
          @delete="startDelete"
        />
      </div>
    </div>
```

Add scoped style for toolbar:

```css
.rules-page__toolbar {
  display: flex;
  justify-content: flex-end;
  margin-bottom: 1rem;
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd panel/frontend && npx vitest run src/pages/RulesPage.test.js`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add panel/frontend/src/components/rules/RuleListRow.vue panel/frontend/src/pages/RulesPage.vue panel/frontend/src/pages/RulesPage.test.js
git commit -m "feat(frontend): add list view to RulesPage"
```

---

## Task 10: Add list view to L4RulesPage

**Files:**
- Create: `panel/frontend/src/components/l4/L4RuleListRow.vue`
- Modify: `panel/frontend/src/pages/L4RulesPage.vue`

- [ ] **Step 1: Create `L4RuleListRow.vue`**

```vue
<template>
  <tr class="l4-rule-list-row">
    <td>
      <button
        class="l4-rule-list-row__toggle"
        :class="{ 'l4-rule-list-row__toggle--on': rule.enabled }"
        @click="$emit('toggle', rule)"
      >
        <span class="l4-rule-list-row__knob" />
      </button>
    </td>
    <td>{{ rule.protocol }}</td>
    <td>{{ rule.listen_host }}:{{ rule.listen_port }}</td>
    <td class="l4-rule-list-row__backend" :title="backendsTooltip">{{ backendLabel }}</td>
    <td>
      <div class="l4-rule-list-row__tags">
        <BaseTag v-for="tag in rule.tags || []" :key="tag">{{ tag }}</BaseTag>
      </div>
    </td>
    <td>
      <div class="l4-rule-list-row__actions">
        <BaseIconButton title="编辑" @click="$emit('edit', rule)">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
            <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
          </svg>
        </BaseIconButton>
        <BaseIconButton title="复制" @click="$emit('copy', rule)">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <rect x="9" y="9" width="13" height="13" rx="2"/>
            <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/>
          </svg>
        </BaseIconButton>
        <BaseIconButton tone="primary" title="诊断" @click="$emit('diagnose', rule)">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M3 12h4l2-6 4 12 2-6h6"/>
          </svg>
        </BaseIconButton>
        <BaseIconButton tone="danger" title="删除" @click="$emit('delete', rule)">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <polyline points="3 6 5 6 21 6"/>
            <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
          </svg>
        </BaseIconButton>
      </div>
    </td>
  </tr>
</template>

<script setup>
import { computed } from 'vue'
import BaseTag from '../base/BaseTag.vue'
import BaseIconButton from '../base/BaseIconButton.vue'

const props = defineProps({
  rule: { type: Object, required: true }
})

defineEmits(['edit', 'toggle', 'copy', 'diagnose', 'delete'])

const backends = computed(() => {
  if (Array.isArray(props.rule?.backends) && props.rule.backends.length > 0) {
    return props.rule.backends
      .map((b) => {
        const host = String(b?.host || '').trim()
        const port = Number(b?.port)
        return host && Number.isInteger(port) && port > 0 ? `${host}:${port}` : ''
      })
      .filter(Boolean)
  }
  return []
})

const backendLabel = computed(() => {
  const list = backends.value
  if (list.length === 0) return '-'
  if (list.length === 1) return list[0]
  return `${list[0]} +${list.length - 1}`
})

const backendsTooltip = computed(() => backends.value.join('\n'))
</script>

<style scoped>
.l4-rule-list-row__backend {
  font-family: var(--font-mono);
  font-size: 0.8125rem;
  color: var(--color-text-secondary);
}

.l4-rule-list-row__tags {
  display: flex;
  gap: 0.25rem;
  flex-wrap: wrap;
}

.l4-rule-list-row__actions {
  display: flex;
  gap: 0.25rem;
}

.l4-rule-list-row__toggle {
  width: 40px;
  height: 22px;
  border-radius: 11px;
  border: none;
  background: var(--color-bg-subtle);
  cursor: pointer;
  position: relative;
  transition: background 0.2s;
  padding: 0;
}

.l4-rule-list-row__toggle--on {
  background: var(--color-primary);
}

.l4-rule-list-row__knob {
  position: absolute;
  top: 3px;
  left: 3px;
  width: 16px;
  height: 16px;
  border-radius: 50%;
  background: white;
  transition: transform 0.2s;
}

.l4-rule-list-row__toggle--on .l4-rule-list-row__knob {
  transform: translateX(18px);
}
</style>
```

- [ ] **Step 2: Modify `L4RulesPage.vue`**

Add imports:

```js
import { useViewMode } from '../composables/useViewMode'
import ViewToggle from '../components/base/ViewToggle.vue'
import BaseTable from '../components/base/BaseTable.vue'
import L4RuleListRow from '../components/l4/L4RuleListRow.vue'
```

Add after `enabledCount`:

```js
const { viewMode, setViewMode } = useViewMode('l4Rules')

const l4Columns = [
  { key: 'enabled', label: '' },
  { key: 'protocol', label: '协议' },
  { key: 'listen', label: '监听' },
  { key: 'backends', label: '后端' },
  { key: 'tags', label: '标签' },
  { key: 'actions', label: '操作' }
]
```

Replace the card-grid block in `L4RulesPage.vue:66-81` with:

```vue
    <div v-if="agentId && filteredRules.length" class="rules-page__content">
      <div class="rules-page__toolbar">
        <ViewToggle :model-value="viewMode" @update:model-value="setViewMode" />
      </div>

      <BaseTable
        v-if="viewMode === 'list'"
        :columns="l4Columns"
        :rows="filteredRules"
        :loading="isLoading"
        empty-title="暂无 L4 规则"
      >
        <template #row="{ row }">
          <L4RuleListRow
            :rule="row"
            @edit="startEdit"
            @toggle="toggleRule"
            @copy="handleCopy"
            @diagnose="openDiagnostic"
            @delete="startDelete"
          />
        </template>
      </BaseTable>

      <div v-else class="rule-grid">
        <L4RuleItem
          v-for="rule in filteredRules"
          :key="rule.id"
          :rule="rule"
          :agent="selectedAgent"
          :traffic="trafficForRule(rule)"
          :agent-node-total="agentNodeTotal"
          @edit="startEdit"
          @delete="startDelete"
          @copy="handleCopy"
          @toggle="toggleRule"
          @diagnose="openDiagnostic"
          @traffic-click="openTrendModal"
        />
      </div>
    </div>
```

Use the same `.rules-page__toolbar` style from RulesPage; add it to the existing `<style scoped>` block if not already shared.

- [ ] **Step 3: Run build and tests**

Run: `cd panel/frontend && npx vitest run && npm run build`

Expected: PASS / build success.

- [ ] **Step 4: Commit**

```bash
git add panel/frontend/src/components/l4/L4RuleListRow.vue panel/frontend/src/pages/L4RulesPage.vue
git commit -m "feat(frontend): add list view to L4RulesPage"
```

---

## Task 11: Add list view to CertsPage

**Files:**
- Create: `panel/frontend/src/components/certs/CertListRow.vue`
- Modify: `panel/frontend/src/pages/CertsPage.vue`

- [ ] **Step 1: Create `CertListRow.vue`**

```vue
<template>
  <tr class="cert-list-row">
    <td>
      <BaseBadge tone="neutral" subtone="secondary" mono>#{{ cert.id }}</BaseBadge>
    </td>
    <td class="cert-list-row__domain">{{ cert.domain }}</td>
    <td>
      <BaseBadge :tone="statusTone" dot>{{ statusLabel }}</BaseBadge>
    </td>
    <td>
      <BaseBadge tone="neutral" subtone="secondary" shape="square" mono>{{ scopeLabel }}</BaseBadge>
    </td>
    <td class="cert-list-row__date">{{ formattedDate }}</td>
    <td>
      <div class="cert-list-row__actions">
        <BaseIconButton
          v-if="cert.status === 'pending' || cert.status === 'error'"
          tone="success"
          title="签发"
          @click="$emit('issue', cert)"
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/>
          </svg>
        </BaseIconButton>
        <BaseIconButton title="编辑" @click="$emit('edit', cert)">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
            <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
          </svg>
        </BaseIconButton>
        <BaseIconButton
          v-if="!isSystemRelayCA(cert)"
          tone="danger"
          title="删除"
          @click="$emit('delete', cert)"
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <polyline points="3 6 5 6 21 6"/>
            <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
          </svg>
        </BaseIconButton>
      </div>
    </td>
  </tr>
</template>

<script setup>
import { computed } from 'vue'
import BaseBadge from '../base/BaseBadge.vue'
import BaseIconButton from '../base/BaseIconButton.vue'
import { isSystemRelayCA } from '../../utils/certificateTemplates'

const props = defineProps({
  cert: { type: Object, required: true }
})

defineEmits(['edit', 'delete', 'issue'])

const STATUS_TONE = {
  active: 'success',
  pending: 'warning',
  error: 'danger'
}

const statusTone = computed(() => {
  if (!props.cert.enabled) return 'neutral'
  return STATUS_TONE[props.cert.status] || 'neutral'
})

const statusLabel = computed(() => {
  if (!props.cert.enabled) return '已禁用'
  if (props.cert.status === 'active') return '生效中'
  if (props.cert.status === 'pending') return '待签发'
  if (props.cert.status === 'error') return '签发失败'
  return '未知'
})

const scopeLabel = computed(() => props.cert.scope === 'ip' ? 'IP' : '域名')

const formattedDate = computed(() => {
  const dateStr = props.cert.last_issue_at
  if (!dateStr) return '-'
  try {
    return new Date(dateStr).toLocaleString('zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit'
    })
  } catch {
    return dateStr
  }
})
</script>

<style scoped>
.cert-list-row__domain {
  font-weight: var(--font-medium);
  color: var(--color-text-primary);
}

.cert-list-row__date {
  font-size: var(--text-xs);
  color: var(--color-text-muted);
}

.cert-list-row__actions {
  display: flex;
  gap: 0.25rem;
}
</style>
```

- [ ] **Step 2: Modify `CertsPage.vue`**

Add imports:

```js
import { useViewMode } from '../composables/useViewMode'
import ViewToggle from '../components/base/ViewToggle.vue'
import BaseTable from '../components/base/BaseTable.vue'
import CertListRow from '../components/certs/CertListRow.vue'
```

Add after `activeCount`:

```js
const { viewMode, setViewMode } = useViewMode('certs')

const certColumns = [
  { key: 'id', label: 'ID' },
  { key: 'domain', label: '域名' },
  { key: 'status', label: '状态' },
  { key: 'scope', label: '类型' },
  { key: 'issued', label: '签发时间' },
  { key: 'actions', label: '操作' }
]
```

Wrap the existing `cert-grid` block (`CertsPage.vue:59-68`) with view toggle and list branch:

```vue
    <div v-else-if="certificates.length && filteredCerts.length" class="certs-page__content">
      <div class="rules-page__toolbar">
        <ViewToggle :model-value="viewMode" @update:model-value="setViewMode" />
      </div>

      <BaseTable
        v-if="viewMode === 'list'"
        :columns="certColumns"
        :rows="filteredCerts"
        :loading="isLoading"
        empty-title="暂无证书"
      >
        <template #row="{ row }">
          <CertListRow
            :cert="row"
            @edit="startEdit"
            @delete="startDelete"
            @issue="issueCert"
          />
        </template>
      </BaseTable>

      <div v-else class="cert-grid">
        <CertCard
          v-for="cert in filteredCerts"
          :key="cert.id"
          :cert="cert"
          @edit="startEdit"
          @delete="startDelete"
          @issue="issueCert"
        />
      </div>
    </div>
```

- [ ] **Step 3: Run build and tests**

Run: `cd panel/frontend && npx vitest run && npm run build`

Expected: PASS / build success.

- [ ] **Step 4: Commit**

```bash
git add panel/frontend/src/components/certs/CertListRow.vue panel/frontend/src/pages/CertsPage.vue
git commit -m "feat(frontend): add list view to CertsPage"
```

---

## Task 12: Add list view to RelayListenersPage

**Files:**
- Create: `panel/frontend/src/components/relay/RelayListRow.vue`
- Modify: `panel/frontend/src/pages/RelayListenersPage.vue`

- [ ] **Step 1: Create `RelayListRow.vue`**

```vue
<template>
  <tr class="relay-list-row">
    <td>
      <BaseBadge tone="neutral" subtone="secondary" mono>#{{ listener.id }}</BaseBadge>
    </td>
    <td class="relay-list-row__name">{{ listener.name }}</td>
    <td>
      <BaseBadge :tone="listener.enabled ? 'success' : 'neutral'" dot>
        {{ listener.enabled ? '启用' : '已禁用' }}
      </BaseBadge>
    </td>
    <td class="relay-list-row__endpoint">{{ publicEndpoint }}</td>
    <td class="relay-list-row__endpoint">{{ bindEndpoint }}</td>
    <td>
      <div class="relay-list-row__actions">
        <BaseIconButton
          tone="warning"
          :title="listener.enabled ? '停用' : '启用'"
          @click="$emit('toggle', listener)"
        >
          <svg v-if="listener.enabled" width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
            <rect x="6" y="4" width="4" height="16" rx="1"/>
            <rect x="14" y="4" width="4" height="16" rx="1"/>
          </svg>
          <svg v-else width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
            <polygon points="5 3 19 12 5 21 5 3"/>
          </svg>
        </BaseIconButton>
        <BaseIconButton title="编辑" @click="$emit('edit', listener)">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
            <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
          </svg>
        </BaseIconButton>
        <BaseIconButton tone="danger" title="删除" @click="$emit('delete', listener)">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <polyline points="3 6 5 6 21 6"/>
            <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
          </svg>
        </BaseIconButton>
      </div>
    </td>
  </tr>
</template>

<script setup>
import { computed } from 'vue'
import BaseBadge from '../base/BaseBadge.vue'
import BaseIconButton from '../base/BaseIconButton.vue'

const props = defineProps({
  listener: { type: Object, required: true }
})

defineEmits(['edit', 'delete', 'toggle'])

function normalizePort(port) {
  const value = Number(port)
  return Number.isInteger(value) && value > 0 ? value : null
}

function resolveBindHosts(listener) {
  if (Array.isArray(listener?.bind_hosts) && listener.bind_hosts.length) {
    return listener.bind_hosts
      .map((item) => String(item || '').trim())
      .filter(Boolean)
  }
  const legacyHost = String(listener?.listen_host || '').trim()
  return legacyHost ? [legacyHost] : []
}

const publicEndpoint = computed(() => {
  const publicHost = String(props.listener?.public_host || '').trim()
  const bindHosts = resolveBindHosts(props.listener)
  const host = publicHost || bindHosts[0] || '-'
  const port = normalizePort(props.listener?.public_port) ?? normalizePort(props.listener?.listen_port)
  return port ? `${host}:${port}` : host
})

const bindEndpoint = computed(() => {
  const bindHosts = resolveBindHosts(props.listener)
  const bindLabel = bindHosts.length ? bindHosts.join(', ') : '-'
  const listenPort = normalizePort(props.listener?.listen_port)
  return listenPort ? `${bindLabel}:${listenPort}` : bindLabel
})
</script>

<style scoped>
.relay-list-row__name {
  font-weight: var(--font-medium);
  color: var(--color-text-primary);
}

.relay-list-row__endpoint {
  font-family: var(--font-mono);
  font-size: 0.8125rem;
  color: var(--color-text-secondary);
}

.relay-list-row__actions {
  display: flex;
  gap: 0.25rem;
}
</style>
```

- [ ] **Step 2: Modify `RelayListenersPage.vue`**

Add imports:

```js
import { useViewMode } from '../composables/useViewMode'
import ViewToggle from '../components/base/ViewToggle.vue'
import BaseTable from '../components/base/BaseTable.vue'
import RelayListRow from '../components/relay/RelayListRow.vue'
```

Add after listeners computed:

```js
const { viewMode, setViewMode } = useViewMode('relayListeners')

const relayColumns = [
  { key: 'id', label: 'ID' },
  { key: 'name', label: '名称' },
  { key: 'status', label: '状态' },
  { key: 'public', label: '公网入口' },
  { key: 'bind', label: '绑定监听' },
  { key: 'actions', label: '操作' }
]
```

Wrap the existing `relay-grid` block (`RelayListenersPage.vue:45-57`) with:

```vue
    <div v-if="agentId && listeners.length" class="relay-page__content">
      <div class="rules-page__toolbar">
        <ViewToggle :model-value="viewMode" @update:model-value="setViewMode" />
      </div>

      <BaseTable
        v-if="viewMode === 'list'"
        :columns="relayColumns"
        :rows="listeners"
        :loading="isLoading"
        empty-title="暂无 Relay 监听器"
      >
        <template #row="{ row }">
          <RelayListRow
            :listener="row"
            @edit="startEdit"
            @toggle="toggleListener"
            @delete="startDelete"
          />
        </template>
      </BaseTable>

      <div v-else class="relay-grid">
        <RelayCard
          v-for="listener in listeners"
          :key="listener.id"
          :listener="listener"
          :traffic="trafficForListener(listener)"
          :agent-node-total="agentNodeTotal"
          @edit="startEdit"
          @toggle="toggleListener"
          @delete="startDelete"
          @traffic-click="openTrendModal"
        />
      </div>
    </div>
```

- [ ] **Step 3: Run build and tests**

Run: `cd panel/frontend && npx vitest run && npm run build`

Expected: PASS / build success.

- [ ] **Step 4: Commit**

```bash
git add panel/frontend/src/components/relay/RelayListRow.vue panel/frontend/src/pages/RelayListenersPage.vue
git commit -m "feat(frontend): add list view to RelayListenersPage"
```

---

## Task 13: Add list view to WireGuardProfilesPage

**Files:**
- Create: `panel/frontend/src/components/wireguard/WireGuardProfileListRow.vue`
- Modify: `panel/frontend/src/pages/WireGuardProfilesPage.vue`

- [ ] **Step 1: Create `WireGuardProfileListRow.vue`**

```vue
<template>
  <tr class="wg-profile-list-row">
    <td>
      <BaseBadge tone="neutral" subtone="secondary" mono>#{{ profile.id }}</BaseBadge>
    </td>
    <td class="wg-profile-list-row__name">{{ profile.name || `Profile ${profile.id}` }}</td>
    <td>
      <BaseBadge :tone="profile.enabled === false ? 'neutral' : 'success'" dot>
        {{ profile.enabled === false ? '停用' : '启用' }}
      </BaseBadge>
    </td>
    <td class="wg-profile-list-row__endpoint">{{ profile.public_endpoint || '-' }}</td>
    <td>{{ profile.listen_port || '-' }}</td>
    <td>{{ clientCount }} 客户端</td>
    <td>
      <div class="wg-profile-list-row__actions">
        <BaseIconButton
          :tone="profile.enabled === false ? 'success' : 'warning'"
          :title="profile.enabled === false ? '启用' : '停用'"
          @click="$emit('toggle', profile)"
        >
          <svg v-if="profile.enabled === false" width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
            <polygon points="5 3 19 12 5 21 5 3"/>
          </svg>
          <svg v-else width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
            <rect x="6" y="4" width="4" height="16" rx="1"/>
            <rect x="14" y="4" width="4" height="16" rx="1"/>
          </svg>
        </BaseIconButton>
        <BaseIconButton title="编辑" @click="$emit('edit', profile)">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
            <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
          </svg>
        </BaseIconButton>
        <BaseIconButton title="管理客户端" @click="$emit('manage', profile)">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/>
            <circle cx="9" cy="7" r="4"/>
            <path d="M23 21v-2a4 4 0 0 0-3-3.87"/>
            <path d="M16 3.13a4 4 0 0 1 0 7.75"/>
          </svg>
        </BaseIconButton>
        <BaseIconButton tone="danger" title="删除" @click="$emit('delete', profile)">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <polyline points="3 6 5 6 21 6"/>
            <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
          </svg>
        </BaseIconButton>
      </div>
    </td>
  </tr>
</template>

<script setup>
import BaseBadge from '../base/BaseBadge.vue'
import BaseIconButton from '../base/BaseIconButton.vue'

defineProps({
  profile: { type: Object, required: true },
  clientCount: { type: Number, default: 0 }
})

defineEmits(['toggle', 'edit', 'delete', 'manage'])
</script>

<style scoped>
.wg-profile-list-row__name {
  font-weight: var(--font-medium);
  color: var(--color-text-primary);
}

.wg-profile-list-row__endpoint {
  font-family: var(--font-mono);
  font-size: 0.8125rem;
  color: var(--color-text-secondary);
}

.wg-profile-list-row__actions {
  display: flex;
  gap: 0.25rem;
}
</style>
```

- [ ] **Step 2: Modify `WireGuardProfilesPage.vue`**

Add imports:

```js
import { useViewMode } from '../composables/useViewMode'
import ViewToggle from '../components/base/ViewToggle.vue'
import BaseTable from '../components/base/BaseTable.vue'
import WireGuardProfileListRow from '../components/wireguard/WireGuardProfileListRow.vue'
```

Add after `enabledCount`:

```js
const { viewMode, setViewMode } = useViewMode('wireGuardProfiles')

const profileColumns = [
  { key: 'id', label: 'ID' },
  { key: 'name', label: '名称' },
  { key: 'status', label: '状态' },
  { key: 'endpoint', label: 'Endpoint' },
  { key: 'port', label: '端口' },
  { key: 'clients', label: '客户端' },
  { key: 'actions', label: '操作' }
]
```

Wrap the existing `profile-grid` block (`WireGuardProfilesPage.vue:39-50`) with:

```vue
      <div v-else class="wg-page__content">
        <div class="rules-page__toolbar">
          <ViewToggle :model-value="viewMode" @update:model-value="setViewMode" />
        </div>

        <BaseTable
          v-if="viewMode === 'list'"
          :columns="profileColumns"
          :rows="profiles"
          :loading="isLoading"
          empty-title="暂无 WireGuard 配置"
        >
          <template #row="{ row }">
            <WireGuardProfileListRow
              :profile="row"
              :client-count="Number(row.client_count || 0)"
              @toggle="toggleProfileEnabled"
              @edit="startEditProfile"
              @delete="deletingProfile = profile"
              @manage="navigateToClientsFor(row)"
            />
          </template>
        </BaseTable>

        <div v-else class="profile-grid">
          <WireGuardProfileCard
            v-for="profile in profiles"
            :key="profile.id"
            :profile="profile"
            :client-count="Number(profile.client_count || 0)"
            @toggle="toggleProfileEnabled"
            @edit="startEditProfile"
            @delete="deletingProfile = profile"
          />
        </div>
      </div>
```

Add helper method:

```js
function navigateToClientsFor(profile) {
  const agentId = typeof route.query.agentId === 'string' ? route.query.agentId : ''
  const target = { path: `/wireguard-profiles/${profile.id}` }
  if (agentId) target.query = { agentId }
  router.push(target)
}
```

- [ ] **Step 3: Run build and tests**

Run: `cd panel/frontend && npx vitest run && npm run build`

Expected: PASS / build success.

- [ ] **Step 4: Commit**

```bash
git add panel/frontend/src/components/wireguard/WireGuardProfileListRow.vue panel/frontend/src/pages/WireGuardProfilesPage.vue
git commit -m "feat(frontend): add list view to WireGuardProfilesPage"
```

---

## Task 14: Replace `RuleTable.vue` with `BaseTable`

**Files:**
- Delete: `panel/frontend/src/components/rules/RuleTable.vue`
- Modify: any import references (search first)

- [ ] **Step 1: Verify no consumers**

Run: `cd panel/frontend && grep -R "RuleTable" src/`

Expected: only `RuleTable.vue` itself and possibly its test. If `RulesPage.vue` no longer imports it, safe to delete.

- [ ] **Step 2: Delete file**

```bash
rm panel/frontend/src/components/rules/RuleTable.vue
```

- [ ] **Step 3: Run build and tests**

Run: `cd panel/frontend && npx vitest run && npm run build`

Expected: PASS / build success.

- [ ] **Step 4: Commit**

```bash
git rm panel/frontend/src/components/rules/RuleTable.vue
git commit -m "refactor(frontend): remove obsolete RuleTable in favor of BaseTable"
```

---

## Task 15: Migrate `RulesPage` to Base components

**Files:**
- Modify: `panel/frontend/src/pages/RulesPage.vue`

- [ ] **Step 1: Replace `.btn`/`.btn-primary` with `BaseButton`**

In `RulesPage.vue`:

- Replace the header "添加规则" button:

```vue
<BaseButton v-if="agentId" variant="primary" @click="showAddForm = true">
  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
    <line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/>
  </svg>
  <span class="btn-text">添加规则</span>
</BaseButton>
```

- Replace empty-state buttons similarly.
- Add `import BaseButton from '../components/base/BaseButton.vue'`.

- [ ] **Step 2: Replace search wrapper with `BaseSearch`**

Replace the `.search-wrapper` block with:

```vue
<BaseSearch
  v-if="agentId && rules.length"
  v-model="searchQuery"
  placeholder="搜索 URL / 标签 / #id=..."
/>
```

Remove `searchInputRef` and `focusSearch` if no longer used.

- [ ] **Step 3: Replace custom empty/loading states with Base components**

For "Agent selected, no rules":

```vue
<BaseEmptyState
  v-else-if="agentId && !rules.length && !isLoading"
  title="暂无规则"
  message="当前节点还没有配置 HTTP 规则"
>
  <template #action>
    <BaseButton variant="primary" @click="showAddForm = true">添加第一条规则</BaseButton>
  </template>
</BaseEmptyState>
```

For "No search results":

```vue
<BaseEmptyState
  v-else-if="agentId && rules.length && !filteredRules.length"
  title="没有匹配的规则"
/>
```

For loading:

```vue
<BaseSkeleton v-if="isLoading" rows="5" />
```

- [ ] **Step 4: Run build and tests**

Run: `cd panel/frontend && npx vitest run && npm run build`

Expected: PASS / build success.

- [ ] **Step 5: Commit**

```bash
git add panel/frontend/src/pages/RulesPage.vue
git commit -m "refactor(frontend): migrate RulesPage to Base components"
```

---

## Task 16: Migrate `L4RulesPage` to Base components

**Files:**
- Modify: `panel/frontend/src/pages/L4RulesPage.vue`

- [ ] **Step 1: Import `BaseButton` and `BaseSearch`**

```js
import BaseButton from '../components/base/BaseButton.vue'
import BaseSearch from '../components/base/BaseSearch.vue'
```

- [ ] **Step 2: Replace buttons and search**

Replace `.btn.btn-primary` with `BaseButton variant="primary"`.
Replace `.search-wrapper` with `BaseSearch v-model="searchQuery" placeholder="搜索协议 / 地址 / 端口 / 标签 / #id=..."`.

- [ ] **Step 3: Replace empty/loading states with Base components**

Same pattern as RulesPage.

- [ ] **Step 4: Run build and tests**

Run: `cd panel/frontend && npx vitest run && npm run build`

Expected: PASS / build success.

- [ ] **Step 5: Commit**

```bash
git add panel/frontend/src/pages/L4RulesPage.vue
git commit -m "refactor(frontend): migrate L4RulesPage to Base components"
```

---

## Task 17: Migrate `CertsPage` to Base components

**Files:**
- Modify: `panel/frontend/src/pages/CertsPage.vue`

- [ ] **Step 1: Import `BaseButton` and `BaseSearch`**

```js
import BaseButton from '../components/base/BaseButton.vue'
import BaseSearch from '../components/base/BaseSearch.vue'
```

- [ ] **Step 2: Replace buttons and search**

Replace `.btn.btn-primary` with `BaseButton variant="primary"`.
Replace `.search-wrapper` with `BaseSearch v-model="searchQuery" placeholder="搜索域名 / 标签 / #id=..."`.

- [ ] **Step 3: Replace empty/loading states with Base components**

Use `BaseEmptyState` and `BaseSkeleton`.

- [ ] **Step 4: Run build and tests**

Run: `cd panel/frontend && npx vitest run && npm run build`

Expected: PASS / build success.

- [ ] **Step 5: Commit**

```bash
git add panel/frontend/src/pages/CertsPage.vue
git commit -m "refactor(frontend): migrate CertsPage to Base components"
```

---

## Task 18: Migrate `RelayListenersPage` to Base components

**Files:**
- Modify: `panel/frontend/src/pages/RelayListenersPage.vue`

- [ ] **Step 1: Import `BaseButton`**

```js
import BaseButton from '../components/base/BaseButton.vue'
```

- [ ] **Step 2: Replace buttons and empty/loading states**

Replace `.btn.btn-primary` with `BaseButton variant="primary"`.
Use `BaseEmptyState` and `BaseSkeleton`.

- [ ] **Step 3: Run build and tests**

Run: `cd panel/frontend && npx vitest run && npm run build`

Expected: PASS / build success.

- [ ] **Step 4: Commit**

```bash
git add panel/frontend/src/pages/RelayListenersPage.vue
git commit -m "refactor(frontend): migrate RelayListenersPage to Base components"
```

---

## Task 19: Migrate `WireGuardProfilesPage` to Base components

**Files:**
- Modify: `panel/frontend/src/pages/WireGuardProfilesPage.vue`

- [ ] **Step 1: Import `BaseButton`**

```js
import BaseButton from '../components/base/BaseButton.vue'
```

- [ ] **Step 2: Replace `.btn.btn--primary`/`.btn.btn--secondary` with `BaseButton`**

Map:
- `.btn.btn--primary` → `BaseButton variant="primary"`
- `.btn.btn--secondary` → `BaseButton variant="secondary"`
- `.btn.btn--sm` → `BaseButton size="sm"`

Remove the duplicate `.btn`/`.btn--primary` styles from the page's `<style scoped>` block.

- [ ] **Step 3: Replace empty/loading inline states**

Use `BaseEmptyState` and `BaseSkeleton` for the initial loading and empty profile list.

- [ ] **Step 4: Run build and tests**

Run: `cd panel/frontend && npx vitest run && npm run build`

Expected: PASS / build success.

- [ ] **Step 5: Commit**

```bash
git add panel/frontend/src/pages/WireGuardProfilesPage.vue
git commit -m "refactor(frontend): migrate WireGuardProfilesPage to Base components"
```

---

## Task 20: Clean up `utilities.css` and global styles

**Files:**
- Modify: `panel/frontend/src/styles/utilities.css`
- Modify: `panel/frontend/src/styles/index.css` (if needed)

- [ ] **Step 1: Remove migrated button classes**

Delete the entire `.btn`, `.btn-primary`, `.btn-secondary`, `.btn-danger`, `.btn-ghost`, `.btn-sm`, `.btn-lg`, `.btn-icon` block from `utilities.css` once all consumers use `BaseButton`.

- [ ] **Step 2: Remove migrated search classes**

Delete `.search-wrapper`, `.search-icon-btn`, `.search-input`, `.clear-btn`, and the mobile media query block.

- [ ] **Step 3: Remove migrated empty/modal/form classes only if fully replaced**

Keep `.page-header`, `.page-title`, `.page-subtitle`, `.card-grid`, `.modal-overlay`, `.modal`, `.form-group`, `.form-label`, `.input-base`, `.status-dot` until Phase 3 fully replaces them. Do not delete classes still in use.

- [ ] **Step 4: Verify no remaining consumers**

Run: `cd panel/frontend && grep -R "btn-primary\|btn-secondary\|btn-danger\|btn-ghost\|search-input" src/pages src/components --include="*.vue"`

Expected: no matches.

- [ ] **Step 5: Run build and tests**

Run: `cd panel/frontend && npx vitest run && npm run build`

Expected: PASS / build success.

- [ ] **Step 6: Commit**

```bash
git add panel/frontend/src/styles/utilities.css
git commit -m "refactor(frontend): remove migrated global classes from utilities.css"
```

---

## Task 21: Final verification

**Files:** all modified

- [ ] **Step 1: Run full frontend test suite**

Run: `cd panel/frontend && npm test`

Expected: all tests pass.

- [ ] **Step 2: Run production build**

Run: `cd panel/frontend && npm run build`

Expected: no build errors.

- [ ] **Step 3: Spot-check each target page visually**

Run: `cd panel/frontend && npm run dev` (with backend running) and verify:
- HTTP 规则页：卡片/列表切换正常，搜索在两种视图下一致。
- L4 规则页：同上。
- 证书页：同上。
- Relay 监听器页：同上。
- WireGuard 配置页：Profile 列表切换正常。

- [ ] **Step 4: Commit if any fixes**

If any fixes were needed, commit them with a `fix(frontend): ...` message.

---

## Self-Review

### Spec coverage

| Spec requirement | Implementing task |
|---|---|
| `useViewMode` composable + localStorage | Task 1 |
| Base component layer | Tasks 2-8 |
| Card/list toggle on 5 pages | Tasks 9-13 |
| Shared data source / same filtering | Tasks 9-13 |
| Replace RuleTable with BaseTable | Task 14 |
| Migrate pages to Base components | Tasks 15-19 |
| Clean up utilities.css | Task 20 |
| Tests for Base components and pages | Tasks 1-9, 21 |
| Loading/empty/error states | Tasks 6, 15-19 |

### Placeholder scan

No TBD/TODO placeholders. Each step includes exact file paths, code, commands, and expected output.

### Type consistency

- `useViewMode` returns `{ viewMode, setViewMode }` and is consumed consistently.
- `BaseTable` emits `sort` and `retry` and accepts `error` as `String | Object`.
- `ViewToggle` uses `modelValue`/`update:modelValue` convention.
- Page row components emit the same events as their card counterparts.

### Gaps

None identified. The plan covers the spec end-to-end.

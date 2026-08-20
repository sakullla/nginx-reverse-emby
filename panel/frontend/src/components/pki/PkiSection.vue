<template>
  <section
    class="pki-section"
    :class="[
      `pki-section--${tone}`,
      {
        'pki-section--split': split,
        'pki-section--collapsed': collapsible && collapsed,
      },
    ]"
    :aria-label="ariaLabel"
  >
    <header v-if="title || $slots.actions || $slots.description" class="pki-section__header">
      <button
        v-if="collapsible"
        type="button"
        class="pki-section__toggle"
        :aria-expanded="(!collapsed).toString()"
        @click="toggle"
      >
        <span class="pki-section__chevron" aria-hidden="true">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.25">
            <polyline points="6 9 12 15 18 9" />
          </svg>
        </span>
        <div class="pki-section__titles">
          <div v-if="eyebrow" class="pki-section__eyebrow">{{ eyebrow }}</div>
          <h2 v-if="title" class="pki-section__title">{{ title }}</h2>
          <p v-if="!collapsed && (description || $slots.description)" class="pki-section__desc">
            <slot name="description">{{ description }}</slot>
          </p>
        </div>
      </button>
      <div v-else class="pki-section__titles">
        <div v-if="eyebrow" class="pki-section__eyebrow">{{ eyebrow }}</div>
        <h2 v-if="title" class="pki-section__title">{{ title }}</h2>
        <p v-if="description || $slots.description" class="pki-section__desc">
          <slot name="description">{{ description }}</slot>
        </p>
      </div>

      <div v-if="$slots.actions" class="pki-section__actions" @click.stop>
        <slot name="actions" />
      </div>
    </header>

    <div
      v-show="!collapsible || !collapsed"
      class="pki-section__body"
      :class="{ 'pki-section__body--split': split }"
    >
      <slot />
    </div>
  </section>
</template>

<script setup>
import { ref, watch } from 'vue'

const props = defineProps({
  title: { type: String, default: '' },
  description: { type: String, default: '' },
  eyebrow: { type: String, default: '' },
  ariaLabel: { type: String, default: undefined },
  tone: {
    type: String,
    default: 'default',
    validator: (v) => ['default', 'attention', 'danger', 'quiet'].includes(v),
  },
  split: { type: Boolean, default: false },
  collapsible: { type: Boolean, default: false },
  defaultCollapsed: { type: Boolean, default: false },
  storageKey: { type: String, default: '' },
})

const collapsed = ref(false)

function storage() {
  try {
    return typeof window !== 'undefined' ? window.localStorage : null
  } catch {
    return null
  }
}

function readStored() {
  if (!props.storageKey) return null
  try {
    const raw = storage()?.getItem(props.storageKey)
    if (raw === '1') return true
    if (raw === '0') return false
  } catch {
    // ignore
  }
  return null
}

function writeStored(value) {
  if (!props.storageKey) return
  try {
    storage()?.setItem(props.storageKey, value ? '1' : '0')
  } catch {
    // ignore
  }
}

function toggle() {
  if (!props.collapsible) return
  collapsed.value = !collapsed.value
  writeStored(collapsed.value)
}

watch(
  () => [props.collapsible, props.defaultCollapsed, props.storageKey],
  () => {
    if (!props.collapsible) {
      collapsed.value = false
      return
    }
    const stored = readStored()
    collapsed.value = stored == null ? props.defaultCollapsed : stored
  },
  { immediate: true }
)
</script>

<style scoped>
.pki-section {
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-2xl);
  background: var(--color-bg-surface);
  box-shadow: var(--shadow-xs);
  padding: var(--space-5);
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  min-width: 0;
  overflow: hidden;
}

.pki-section--collapsed {
  gap: 0;
}

.pki-section--attention {
  border-color: color-mix(in srgb, var(--color-warning) 28%, var(--color-border-subtle));
  background:
    linear-gradient(180deg, color-mix(in srgb, var(--color-warning) 6%, var(--color-bg-surface)), var(--color-bg-surface));
}

.pki-section--danger {
  border-color: color-mix(in srgb, var(--color-danger) 24%, var(--color-border-subtle));
}

.pki-section--quiet {
  background: color-mix(in srgb, var(--color-bg-subtle) 55%, var(--color-bg-surface));
}

.pki-section__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-4);
  flex-wrap: wrap;
}

.pki-section__toggle {
  display: flex;
  align-items: flex-start;
  gap: var(--space-2);
  min-width: 0;
  flex: 1;
  border: 0;
  padding: 0;
  background: transparent;
  text-align: left;
  cursor: pointer;
  color: inherit;
  font: inherit;
}

.pki-section__toggle:hover .pki-section__title {
  color: var(--color-primary);
}

.pki-section__chevron {
  width: 1.5rem;
  height: 1.5rem;
  margin-top: 0.15rem;
  border-radius: var(--radius-md);
  display: grid;
  place-items: center;
  color: var(--color-text-tertiary);
  background: var(--color-bg-subtle);
  flex-shrink: 0;
  transition: transform var(--duration-fast) var(--ease-default),
              color var(--duration-fast) var(--ease-default),
              background var(--duration-fast) var(--ease-default);
}

.pki-section--collapsed .pki-section__chevron {
  transform: rotate(-90deg);
}

.pki-section__toggle:hover .pki-section__chevron {
  color: var(--color-primary);
  background: var(--color-primary-subtle);
}

.pki-section__titles {
  min-width: 0;
  flex: 1;
}

.pki-section__eyebrow {
  color: var(--color-primary);
  font-size: 0.7rem;
  font-weight: 700;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  margin-bottom: 0.2rem;
}

.pki-section__title {
  margin: 0 0 0.25rem;
  color: var(--color-text-primary);
  font-size: var(--text-lg);
  font-weight: 700;
  letter-spacing: -0.01em;
  transition: color var(--duration-fast) var(--ease-default);
}

.pki-section__desc {
  margin: 0;
  color: var(--color-text-tertiary);
  font-size: var(--text-sm);
  line-height: 1.5;
}

.pki-section__actions {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  flex-wrap: wrap;
  flex-shrink: 0;
  min-width: 0;
}

.pki-section__body--split {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  gap: var(--space-6);
}

@media (min-width: 1920px) {
  .pki-section {
    padding: var(--space-6);
  }

  .pki-section__body--split {
    gap: var(--space-8);
  }
}

@media (max-width: 960px) {
  .pki-section__body--split {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 640px) {
  .pki-section {
    padding: var(--space-4);
    border-radius: var(--radius-xl);
    gap: var(--space-3);
  }

  .pki-section__header {
    gap: var(--space-2);
  }

  .pki-section__actions {
    width: 100%;
  }

  .pki-section__actions :deep(.btn),
  .pki-section__actions .btn {
    flex: 1 1 auto;
    min-height: 2.35rem;
    justify-content: center;
  }

  .pki-section__title {
    font-size: var(--text-base);
  }

  .pki-section__desc {
    font-size: var(--text-xs);
  }
}
</style>

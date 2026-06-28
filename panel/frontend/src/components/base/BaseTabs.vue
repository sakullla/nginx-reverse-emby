<template>
  <div class="base-tabs" role="tablist">
    <button
      v-for="tab in tabs"
      :key="tab.id"
      type="button"
      class="base-tabs__tab"
      :class="{ 'base-tabs__tab--active': modelValue === tab.id }"
      :aria-selected="modelValue === tab.id"
      :aria-controls="`tab-panel-${tab.id}`"
      role="tab"
      @click="selectTab(tab.id)"
    >
      {{ tab.label }}
    </button>
  </div>
</template>

<script setup>
const props = defineProps({
  tabs: {
    type: Array,
    required: true,
    validator: (value) =>
      Array.isArray(value) &&
      value.every((tab) => tab && typeof tab.id === 'string' && typeof tab.label === 'string'),
  },
  modelValue: {
    type: String,
    default: '',
  },
})

const emit = defineEmits(['update:modelValue'])

function selectTab(id) {
  if (id !== props.modelValue) {
    emit('update:modelValue', id)
  }
}
</script>

<style scoped>
.base-tabs {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  border-bottom: 1.5px solid var(--color-border-default);
  padding: 0 var(--space-2);
}

.base-tabs__tab {
  position: relative;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-1);
  padding: var(--space-2) var(--space-3);
  font-size: var(--text-sm);
  font-weight: var(--font-semibold);
  color: var(--color-text-secondary);
  background: transparent;
  border: none;
  border-radius: var(--radius-md) var(--radius-md) 0 0;
  cursor: pointer;
  transition: color var(--duration-fast) var(--ease-default),
    background-color var(--duration-fast) var(--ease-default);
  white-space: nowrap;
}

.base-tabs__tab:hover:not(.base-tabs__tab--active) {
  color: var(--color-text-primary);
  background: var(--color-bg-hover);
}

.base-tabs__tab--active {
  color: var(--color-primary);
}

.base-tabs__tab--active::after {
  content: '';
  position: absolute;
  bottom: -1.5px;
  left: 0;
  right: 0;
  height: 2px;
  background: var(--color-primary);
  border-radius: var(--radius-full) var(--radius-full) 0 0;
}

.base-tabs__tab:focus-visible {
  outline: none;
  box-shadow: var(--shadow-focus, 0 0 0 2px var(--color-primary-subtle));
}
</style>

<template>
  <div class="theme-selector" ref="selectorRef">
    <button
      class="theme-trigger"
      :class="{ 'theme-trigger--open': isOpen }"
      @click="isOpen = !isOpen"
      :title="currentTheme.label"
    >
      <span class="theme-trigger__emoji">{{ currentTheme.emoji }}</span>
      <svg class="theme-trigger__arrow" :class="{ 'theme-trigger__arrow--up': isOpen }" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
        <polyline points="6 9 12 15 18 9"/>
      </svg>
    </button>

    <Transition name="dropdown">
      <div v-if="isOpen" class="theme-dropdown">
        <div class="theme-dropdown__header">选择主题</div>
        <button
          v-for="theme in themes"
          :key="theme.id"
          class="theme-option"
          :class="{ 'theme-option--active': currentThemeId === theme.id }"
          @click="selectTheme(theme.id)"
        >
          <span class="theme-option__emoji">{{ theme.emoji }}</span>
          <span class="theme-option__label">{{ theme.label }}</span>
          <svg v-if="currentThemeId === theme.id" class="theme-option__check" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3">
            <polyline points="20 6 9 17 4 12"/>
          </svg>
        </button>
      </div>
    </Transition>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { themes } from '../../context/ThemeContext'
import { useTheme } from '../../context/ThemeContext'

const isOpen = ref(false)
const selectorRef = ref(null)
const { currentThemeId, setTheme } = useTheme()

const currentTheme = computed(() => {
  return themes.find(t => t.id === currentThemeId.value) || themes[0]
})

const selectTheme = (id) => {
  setTheme(id)
  isOpen.value = false
}

const handleClickOutside = (e) => {
  if (selectorRef.value && !selectorRef.value.contains(e.target)) {
    isOpen.value = false
  }
}

onMounted(() => {
  document.addEventListener('click', handleClickOutside)
})

onUnmounted(() => {
  document.removeEventListener('click', handleClickOutside)
})
</script>

<style scoped>
.theme-selector {
  position: relative;
}

.theme-trigger {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-full);
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  transition: all var(--duration-normal) var(--ease-bounce);
  cursor: pointer;
  font-family: inherit;
  color: var(--color-text-primary);
  backdrop-filter: blur(8px);
}

.theme-trigger:hover {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-sm);
  transform: translateY(-1px);
}

.theme-trigger--open {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.theme-trigger__emoji {
  font-size: 16px;
  line-height: 1;
}

.theme-trigger__arrow {
  color: var(--color-text-tertiary);
  transition: transform var(--duration-normal) var(--ease-bounce);
}

.theme-trigger__arrow--up {
  transform: rotate(180deg);
}

/* Dropdown */
.theme-dropdown {
  position: absolute;
  top: calc(100% + var(--space-2));
  right: 0;
  min-width: 180px;
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-2xl);
  box-shadow: var(--shadow-xl);
  padding: var(--space-2);
  z-index: var(--z-dropdown);
  backdrop-filter: blur(20px);
}

.theme-dropdown__header {
  font-size: var(--text-xs);
  font-weight: var(--font-semibold);
  color: var(--color-text-tertiary);
  padding: var(--space-2) var(--space-3);
}

.theme-option {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  width: 100%;
  padding: var(--space-2-5) var(--space-3);
  border-radius: var(--radius-xl);
  cursor: pointer;
  transition: all var(--duration-normal) var(--ease-bounce);
  border: 1.5px solid transparent;
  background: transparent;
  font-family: inherit;
  color: var(--color-text-primary);
  text-align: left;
}

.theme-option:hover {
  background: var(--color-bg-hover);
  border-color: rgba(192, 132, 252, 0.15);
  transform: translateX(4px);
}

.theme-option--active {
  background: var(--color-primary-subtle);
  border-color: var(--color-primary);
}

.theme-option__preview {
  width: 20px;
  height: 20px;
  border-radius: var(--radius-md);
  flex-shrink: 0;
  border: 1px solid rgba(255,255,255,0.2);
}

.theme-option__emoji {
  font-size: 16px;
  line-height: 1;
  flex-shrink: 0;
}

.theme-option__label {
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  flex: 1;
}

.theme-option__check {
  color: var(--color-primary);
  flex-shrink: 0;
}

/* Dropdown Animation */
.dropdown-enter-active,
.dropdown-leave-active {
  transition: opacity var(--duration-normal) var(--ease-default),
              transform var(--duration-normal) var(--ease-bounce);
}

.dropdown-enter-from,
.dropdown-leave-to {
  opacity: 0;
  transform: translateY(-8px) scale(0.95);
}
</style>

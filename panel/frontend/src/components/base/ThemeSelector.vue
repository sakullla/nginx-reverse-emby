<template>
  <div ref="selectorRef" class="theme-selector">
    <button
      type="button"
      class="theme-trigger"
      :class="{ 'theme-trigger--open': isOpen }"
      :title="`外观：${currentTheme.label}`"
      :aria-label="`选择主题，当前${currentTheme.label}`"
      aria-haspopup="menu"
      :aria-expanded="isOpen ? 'true' : 'false'"
      @click="isOpen = !isOpen"
    >
      <ThemeIcon :name="resolvedScheme === 'dark' ? 'moon' : 'sun'" />
    </button>

    <Transition name="dropdown">
      <div v-if="isOpen" class="theme-menu" role="menu" aria-label="外观" @keydown.escape.stop="isOpen = false">
        <template v-for="(theme, index) in themes" :key="theme.id">
        <div v-if="index === 1" class="theme-menu__sep" role="separator"></div>
        <button
          type="button"
          role="menuitemradio"
          class="theme-menu__item"
          :class="{ 'theme-menu__item--active': currentThemeId === theme.id }"
          :aria-checked="currentThemeId === theme.id ? 'true' : 'false'"
          @click="selectTheme(theme.id)"
        >
          <span v-if="theme.id === 'system'" class="theme-menu__swatch theme-menu__swatch--split" aria-hidden="true">
            <span :data-theme="SYSTEM_LIGHT_THEME"></span><span :data-theme="SYSTEM_DARK_THEME"></span>
          </span>
          <span v-else class="theme-menu__swatch" :data-theme="theme.id" aria-hidden="true"></span>
          <span class="theme-menu__label">{{ theme.label }}</span>
          <svg v-if="currentThemeId === theme.id" class="theme-menu__check" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" aria-hidden="true">
            <polyline points="20 6 9 17 4 12" />
          </svg>
        </button>
        </template>
      </div>
    </Transition>
  </div>
</template>

<script setup>
import { computed, h, onMounted, onUnmounted, ref } from 'vue'
import { SYSTEM_DARK_THEME, SYSTEM_LIGHT_THEME, themes, useTheme } from '../../context/ThemeContext'

const ICONS = {
  sun: [h('circle', { cx: 12, cy: 12, r: 4 }), h('path', { d: 'M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M4.93 19.07l1.41-1.41M17.66 6.34l1.41-1.41' })],
  moon: [h('path', { d: 'M20.5 14.5A8.5 8.5 0 0 1 9.5 3.5a8.5 8.5 0 1 0 11 11z' })],
  system: [h('rect', { x: 3, y: 4, width: 18, height: 12, rx: 2 }), h('path', { d: 'M8 20h8M12 16v4' })],
}

const ThemeIcon = (props) => h('svg', {
  width: 16, height: 16, viewBox: '0 0 24 24', fill: 'none', stroke: 'currentColor',
  'stroke-width': 1.8, 'stroke-linecap': 'round', 'stroke-linejoin': 'round', 'aria-hidden': 'true'
}, ICONS[props.name] || ICONS.system)
ThemeIcon.props = ['name']

const isOpen = ref(false)
const selectorRef = ref(null)
const { currentThemeId, resolvedScheme, setTheme } = useTheme()

const currentTheme = computed(() => themes.find((t) => t.id === currentThemeId.value) || themes[0])

function selectTheme(id) {
  setTheme(id)
  isOpen.value = false
}

function handleClickOutside(e) {
  if (selectorRef.value && !selectorRef.value.contains(e.target)) isOpen.value = false
}

function handleKeydown(e) {
  if (e.key === 'Escape') isOpen.value = false
}

onMounted(() => {
  document.addEventListener('click', handleClickOutside)
  document.addEventListener('keydown', handleKeydown)
})
onUnmounted(() => {
  document.removeEventListener('click', handleClickOutside)
  document.removeEventListener('keydown', handleKeydown)
})
</script>

<style scoped>
.theme-selector {
  position: relative;
}

.theme-trigger {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: var(--control-height-icon);
  height: var(--control-height-icon);
  border-radius: var(--radius-md);
  border: 1px solid transparent;
  background: transparent;
  color: var(--color-text-secondary);
  cursor: pointer;
  transition: background var(--duration-fast) var(--ease-default), color var(--duration-fast) var(--ease-default);
}

.theme-trigger:hover,
.theme-trigger--open {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}

.theme-menu {
  position: absolute;
  top: calc(100% + var(--space-1-5));
  right: 0;
  z-index: var(--z-dropdown);
  min-width: 10rem;
  padding: var(--space-1);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  background: var(--color-bg-surface-raised);
  box-shadow: var(--shadow-lg);
}

.theme-menu__item {
  display: flex;
  align-items: center;
  gap: var(--space-2-5);
  width: 100%;
  min-height: 2rem;
  padding: 0 var(--space-2-5);
  border: 0;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--color-text-secondary);
  font: inherit;
  font-size: var(--text-sm);
  text-align: left;
  cursor: pointer;
}

.theme-menu__item:hover,
.theme-menu__item:focus-visible {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
  outline: none;
}

.theme-menu__item--active {
  color: var(--color-text-primary);
  font-weight: var(--font-medium);
}

.theme-menu__label {
  flex: 1;
}

.theme-menu__sep {
  height: 1px;
  margin: var(--space-1) 0;
  background: var(--color-border-subtle);
}

/* Swatch paints with the target theme's own tokens via data-theme. */
.theme-menu__swatch {
  display: inline-flex;
  width: 16px;
  height: 16px;
  border-radius: var(--radius-full);
  background: var(--color-brand-bg);
  box-shadow: inset 0 0 0 1px rgb(0 0 0 / 0.08), 0 0 0 2px var(--color-bg-canvas);
  overflow: hidden;
  flex-shrink: 0;
}

.theme-menu__swatch--split {
  background: none;
}

.theme-menu__swatch--split > span {
  flex: 1;
  background: var(--color-brand-bg);
}


.theme-menu__check {
  color: var(--color-primary);
}

.dropdown-enter-active,
.dropdown-leave-active {
  transition: opacity var(--duration-fast) var(--ease-default), transform var(--duration-fast) var(--ease-default);
}

.dropdown-enter-from,
.dropdown-leave-to {
  opacity: 0;
  transform: translateY(-4px) scale(0.98);
}
</style>

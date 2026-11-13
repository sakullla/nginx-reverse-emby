import { computed, defineComponent, inject, onBeforeUnmount, provide, ref } from 'vue'

// Theme preferences. Five palettes plus "system", which follows the OS
// colour scheme using the sakura pair. The document always receives a
// concrete palette id in data-theme.
export const themes = [
  { id: 'system', emoji: '🖥️', label: '跟随系统', scheme: 'auto' },
  { id: 'fresh-green', emoji: '🌿', label: '清新绿', scheme: 'light' },
  { id: 'sakura-day', emoji: '🌸', label: '昼樱', scheme: 'light' },
  { id: 'sakura-night', emoji: '🌙', label: '夜樱', scheme: 'dark' },
  { id: 'business', emoji: '☀️', label: '晴空', scheme: 'light' },
  { id: 'neko-dark', emoji: '🐱', label: '夜猫', scheme: 'dark' },
]

export const SYSTEM_LIGHT_THEME = 'sakura-day'
export const SYSTEM_DARK_THEME = 'sakura-night'

const VALID_THEME_IDS = themes.map((t) => t.id)
const LEGACY_THEME_MAP = {
  sakura: 'sakura-day',
  cyberpunk: 'sakura-day',
  midnight: 'sakura-night',
  light: 'sakura-day',
  dark: 'sakura-night',
}
const ThemeContextKey = Symbol('ThemeContext')

export function normalizeThemePreference(value) {
  const mapped = LEGACY_THEME_MAP[value] || value
  return VALID_THEME_IDS.includes(mapped) ? mapped : 'system'
}

export function themeScheme(id) {
  return themes.find((t) => t.id === id)?.scheme || 'light'
}

function readStoredTheme() {
  try {
    return localStorage.getItem('theme')
  } catch {
    return null
  }
}

function writeStoredTheme(id) {
  try {
    localStorage.setItem('theme', id)
  } catch {
    // Storage unavailable (private mode); the choice still applies this session.
  }
}

function systemMedia() {
  return typeof window !== 'undefined' && typeof window.matchMedia === 'function'
    ? window.matchMedia('(prefers-color-scheme: dark)')
    : null
}

export const ThemeProvider = defineComponent({
  name: 'ThemeProvider',
  setup(props, { slots }) {
    const media = systemMedia()
    const systemDark = ref(Boolean(media?.matches))
    const currentThemeId = ref(normalizeThemePreference(readStoredTheme()))

    // Concrete palette id written to <html data-theme>.
    const resolvedTheme = computed(() => {
      if (currentThemeId.value === 'system') return systemDark.value ? SYSTEM_DARK_THEME : SYSTEM_LIGHT_THEME
      return currentThemeId.value
    })
    const resolvedScheme = computed(() => themeScheme(resolvedTheme.value))

    function apply() {
      document.documentElement.setAttribute('data-theme', resolvedTheme.value)
    }

    function setTheme(id) {
      if (!VALID_THEME_IDS.includes(id)) return
      currentThemeId.value = id
      writeStoredTheme(id)
      apply()
    }

    function onSystemChange(event) {
      systemDark.value = Boolean(event.matches)
      apply()
    }

    media?.addEventListener?.('change', onSystemChange)
    onBeforeUnmount(() => media?.removeEventListener?.('change', onSystemChange))

    apply()

    provide(ThemeContextKey, { currentThemeId, resolvedTheme, resolvedScheme, setTheme, themes })

    return () => slots.default?.()
  },
})

export function useTheme() {
  const ctx = inject(ThemeContextKey)
  if (!ctx) throw new Error('useTheme must be used within ThemeProvider')
  return ctx
}

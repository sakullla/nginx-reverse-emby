import { defineComponent, provide, inject, ref } from 'vue'

export const themes = [
  { id: 'sakura-day',   emoji: '🌸', label: '昼樱' },
  { id: 'sakura-night', emoji: '🌙', label: '夜樱' },
  { id: 'business',     emoji: '☀️', label: '晴空' },
]

const VALID_THEME_IDS = themes.map(t => t.id)
const ThemeContextKey = Symbol('ThemeContext')

export const ThemeProvider = defineComponent({
  name: 'ThemeProvider',
  setup(props, { slots }) {
    const savedTheme = localStorage.getItem('theme')
    // Migrate old theme IDs
    const migrated = savedTheme === 'sakura' ? 'sakura-day'
      : savedTheme === 'midnight' ? 'sakura-night'
      : savedTheme === 'cyberpunk' ? 'sakura-day'
      : savedTheme

    const initialTheme = (migrated && VALID_THEME_IDS.includes(migrated))
      ? migrated
      : (window.matchMedia('(prefers-color-scheme: dark)').matches ? 'sakura-night' : 'sakura-day')

    const currentThemeId = ref(initialTheme)

    function setTheme(id) {
      if (!VALID_THEME_IDS.includes(id)) return
      currentThemeId.value = id
      document.documentElement.setAttribute('data-theme', id)
      localStorage.setItem('theme', id)
    }

    // Apply on init
    document.documentElement.setAttribute('data-theme', currentThemeId.value)

    provide(ThemeContextKey, { currentThemeId, setTheme, themes })

    return () => slots.default?.()
  }
})

export function useTheme() {
  const ctx = inject(ThemeContextKey)
  if (!ctx) throw new Error('useTheme must be used within ThemeProvider')
  return ctx
}

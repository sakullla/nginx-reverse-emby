import { provide, inject, ref } from 'vue'

const THEMES = ['sakura', 'business', 'midnight']
const ThemeContextKey = Symbol('ThemeContext')

export function ThemeProvider({ children }) {
  const currentThemeId = ref(
    localStorage.getItem('theme') ||
    (window.matchMedia('(prefers-color-scheme: dark)').matches ? 'midnight' : 'sakura')
  )

  function setTheme(id) {
    if (!THEMES.includes(id)) return
    currentThemeId.value = id
    document.documentElement.setAttribute('data-theme', id)
    localStorage.setItem('theme', id)
  }

  // Apply on init
  document.documentElement.setAttribute('data-theme', currentThemeId.value)

  provide(ThemeContextKey, { currentThemeId, setTheme, themes: THEMES })

  return children
}

export function useTheme() {
  const ctx = inject(ThemeContextKey)
  if (!ctx) throw new Error('useTheme must be used within ThemeProvider')
  return ctx
}
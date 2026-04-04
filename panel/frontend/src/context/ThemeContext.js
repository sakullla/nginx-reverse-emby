import { defineComponent, h, provide, inject, ref } from 'vue'

export const themes = [
  { id: 'sakura',   emoji: '🌸', label: '二次元' },
  { id: 'business', emoji: '☀️', label: '晴空'    },
  { id: 'midnight', emoji: '🌙', label: '暗夜'   }
]

const VALID_THEME_IDS = themes.map(t => t.id)
const ThemeContextKey = Symbol('ThemeContext')

export const ThemeProvider = defineComponent({
  name: 'ThemeProvider',
  setup(props, { slots }) {
    const savedTheme = localStorage.getItem('theme')
    const initialTheme = (savedTheme && VALID_THEME_IDS.includes(savedTheme))
      ? savedTheme
      : (savedTheme === 'cyberpunk' ? 'sakura' : null) ||
        (window.matchMedia('(prefers-color-scheme: dark)').matches ? 'midnight' : 'sakura')

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

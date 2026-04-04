import { createContext, useContext, ref } from 'vue'

const ThemeContext = createContext(null)

const THEMES = ['sakura', 'business', 'midnight']

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

  return (
    <ThemeContext.Provider value={{ currentThemeId, setTheme, themes: THEMES }}>
      {children}
    </ThemeContext.Provider>
  )
}

export function useTheme() {
  const ctx = useContext(ThemeContext)
  if (!ctx) throw new Error('useTheme must be used within ThemeProvider')
  return ctx
}

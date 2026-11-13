import { defineConfig, presetWind, presetIcons } from 'unocss'

export default defineConfig({
  presets: [
    presetWind(),
    presetIcons({
      scale: 1.2,
      extraProperties: {
        display: 'inline-block',
        'vertical-align': 'middle'
      }
    })
  ],
  theme: {
    // Share CSS variable tokens with our themes.css
    colors: {
      primary: 'var(--color-primary)',
      surface: 'var(--color-bg-surface)',
      canvas: 'var(--color-bg-canvas)',
      subtle: 'var(--color-bg-subtle)',
      hover: 'var(--color-bg-hover)',
      default: 'var(--color-border-default)'
    }
  },
  shortcuts: {
    // Buttons are defined once in styles/utilities.css; no shape shortcuts here
    // so the stylesheet stays the single source of truth.
    'card': 'bg-surface rounded-xl border border-default shadow-sm',
    'input-base': 'w-full px-3 py-2 rounded-lg bg-surface border border-default text-sm outline-none focus:border-primary transition-all duration-150'
  }
})

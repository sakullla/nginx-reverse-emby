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
      canvas: 'var(--color-bg-canvas)'
    }
  },
  shortcuts: {
    // Common utility groups used across components
    'btn': 'px-4 py-2 rounded-xl font-medium text-sm transition-all duration-250 cursor-pointer',
    'btn-primary': 'btn bg-primary text-white hover:opacity-90',
    'btn-secondary': 'btn bg-surface border border-default hover:bg-hover',
    'card': 'bg-surface rounded-2xl border border-default shadow-sm',
    'input-base': 'w-full px-3 py-2 rounded-xl bg-subtle border border-default text-sm outline-none focus:border-primary transition-all duration-250'
  }
})

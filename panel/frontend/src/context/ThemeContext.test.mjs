import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { defineComponent, h, nextTick } from 'vue'
import { mount } from '@vue/test-utils'

import { ThemeProvider, normalizeThemePreference, themes, useTheme } from './ThemeContext.js'

const Harness = defineComponent({
  setup() {
    const { currentThemeId, resolvedTheme, setTheme, themes } = useTheme()
    return () => h('button', {
      id: 'theme-button',
      'data-current-theme': currentThemeId.value,
      'data-resolved-theme': resolvedTheme.value,
      'data-theme-count': themes.length,
      onClick: () => setTheme('business')
    })
  }
})

function mountProvider() {
  return mount(ThemeProvider, {
    slots: {
      default: () => h(Harness)
    }
  })
}

function stubScheme(dark) {
  const listeners = new Set()
  const media = {
    matches: dark,
    addEventListener: (_, fn) => listeners.add(fn),
    removeEventListener: (_, fn) => listeners.delete(fn)
  }
  vi.stubGlobal('matchMedia', vi.fn(() => media))
  return {
    flip(next) {
      media.matches = next
      listeners.forEach((fn) => fn({ matches: next }))
    }
  }
}

describe('ThemeContext', () => {
  beforeEach(() => {
    localStorage.clear()
    document.documentElement.removeAttribute('data-theme')
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('keeps all five palettes and adds a follow-system option', () => {
    expect(themes.map((theme) => theme.id)).toEqual([
      'system', 'fresh-green', 'sakura-day', 'sakura-night', 'business', 'neko-dark'
    ])
    expect(themes.find((theme) => theme.id === 'fresh-green')).toMatchObject({ emoji: '🌿', label: '清新绿' })
  })

  it('follows the OS scheme with the sakura pair by default without persisting', () => {
    const scheme = stubScheme(true)
    const wrapper = mountProvider()

    expect(wrapper.get('#theme-button').attributes('data-current-theme')).toBe('system')
    expect(document.documentElement.getAttribute('data-theme')).toBe('sakura-night')
    expect(localStorage.getItem('theme')).toBeNull()

    scheme.flip(false)
    expect(document.documentElement.getAttribute('data-theme')).toBe('sakura-day')
  })

  it('keeps an existing palette preference', () => {
    stubScheme(false)
    localStorage.setItem('theme', 'neko-dark')
    mountProvider()
    expect(document.documentElement.getAttribute('data-theme')).toBe('neko-dark')
  })

  it('migrates retired theme ids', () => {
    expect(normalizeThemePreference('midnight')).toBe('sakura-night')
    expect(normalizeThemePreference('sakura')).toBe('sakura-day')
    expect(normalizeThemePreference('unknown')).toBe('system')
  })

  it('persists explicit user changes', async () => {
    stubScheme(false)
    const wrapper = mountProvider()

    await wrapper.get('#theme-button').trigger('click')
    await nextTick()

    expect(document.documentElement.getAttribute('data-theme')).toBe('business')
    expect(localStorage.getItem('theme')).toBe('business')
  })
})

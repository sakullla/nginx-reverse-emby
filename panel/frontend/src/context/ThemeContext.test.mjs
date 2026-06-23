import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { defineComponent, h, nextTick } from 'vue'
import { mount } from '@vue/test-utils'

import { ThemeProvider, themes, useTheme } from './ThemeContext.js'

const Harness = defineComponent({
  setup() {
    const { currentThemeId, setTheme, themes } = useTheme()
    return () => h('button', {
      id: 'theme-button',
      'data-current-theme': currentThemeId.value,
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

describe('ThemeContext', () => {
  beforeEach(() => {
    localStorage.clear()
    document.documentElement.removeAttribute('data-theme')
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('registers the fresh green theme for selectors', () => {
    expect(themes.map(theme => theme.id)).toContain('fresh-green')
    expect(themes.find(theme => theme.id === 'fresh-green')).toMatchObject({
      emoji: '🌿',
      label: '清新绿'
    })
  })

  it('defaults to sakura day when no user preference exists', () => {
    const matchMedia = vi.fn(() => ({ matches: true }))
    vi.stubGlobal('matchMedia', matchMedia)

    const wrapper = mountProvider()

    expect(document.documentElement.getAttribute('data-theme')).toBe('sakura-day')
    expect(wrapper.get('#theme-button').attributes('data-current-theme')).toBe('sakura-day')
    expect(localStorage.getItem('theme')).toBeNull()
    expect(matchMedia).not.toHaveBeenCalled()
  })

  it('keeps an existing valid theme preference', () => {
    localStorage.setItem('theme', 'sakura-night')

    const wrapper = mountProvider()

    expect(document.documentElement.getAttribute('data-theme')).toBe('sakura-night')
    expect(wrapper.get('#theme-button').attributes('data-current-theme')).toBe('sakura-night')
  })

  it('migrates old theme ids and persists explicit user changes', async () => {
    localStorage.setItem('theme', 'midnight')

    const wrapper = mountProvider()
    expect(document.documentElement.getAttribute('data-theme')).toBe('sakura-night')

    await wrapper.get('#theme-button').trigger('click')
    await nextTick()

    expect(document.documentElement.getAttribute('data-theme')).toBe('business')
    expect(localStorage.getItem('theme')).toBe('business')
  })
})

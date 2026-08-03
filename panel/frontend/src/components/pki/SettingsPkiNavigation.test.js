// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import SettingsDataMgmt from '../settings/SettingsDataMgmt.vue'

vi.mock('../../api', () => ({
  fetchBackupResourceCounts: vi.fn(() => Promise.resolve({ counts: {} }))
}))

describe('Settings internal PKI navigation', () => {
  afterEach(() => {
    delete window.__NRE_PANEL_BASE__
  })

  it('keeps the protected-backup link inside a non-root panel base', () => {
    window.__NRE_PANEL_BASE__ = '/control-panel/random-base/'

    const wrapper = mount(SettingsDataMgmt, {
      global: {
        stubs: {
          ExportPanel: true,
          ImportWizard: true
        }
      }
    })

    expect(wrapper.find('.pki-backup-boundary a').attributes('href'))
      .toBe('/control-panel/random-base/pki')
  })
})

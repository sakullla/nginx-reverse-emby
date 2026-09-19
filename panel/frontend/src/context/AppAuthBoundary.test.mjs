import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import App from '../App.vue'
import { clearCredentials, setAuthToken, setSessionToken } from '../api/authState'

const { replace, route } = vi.hoisted(() => ({
  replace: vi.fn(),
  route: { name: 'dashboard' }
}))

vi.mock('vue-router', () => ({
  useRouter: () => ({ replace }),
  useRoute: () => route
}))

vi.mock('./AgentContext.js', () => ({
  AgentProvider: { template: '<div><slot /></div>' }
}))

vi.mock('./ThemeContext.js', () => ({
  ThemeProvider: { template: '<div><slot /></div>' }
}))

describe('App authentication boundary', () => {
  beforeEach(() => {
    clearCredentials()
    replace.mockReset()
    route.name = 'dashboard'
  })

  it('unmounts the protected route when a panel token is cleared', async () => {
    setAuthToken('panel-token')
    mount(App, { global: { stubs: { RouterView: true, StatusMessage: true } } })

    clearCredentials()
    await nextTick()

    expect(replace).toHaveBeenCalledWith({ name: 'login' })
  })

  it('does not treat leftover panel_session as authenticated', async () => {
    setSessionToken('account-session')
    mount(App, { global: { stubs: { RouterView: true, StatusMessage: true } } })

    clearCredentials()
    await nextTick()

    expect(replace).not.toHaveBeenCalled()
  })
})

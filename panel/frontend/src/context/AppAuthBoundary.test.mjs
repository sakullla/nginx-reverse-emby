import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import App from '../App.vue'
import { clearCredentials, setSessionToken } from '../api/authState'

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

  it('unmounts the protected route when an account session expires', async () => {
    setSessionToken('account-session')
    mount(App, { global: { stubs: { RouterView: true, StatusMessage: true } } })

    clearCredentials()
    await nextTick()

    expect(replace).toHaveBeenCalledWith({ name: 'login' })
  })
})

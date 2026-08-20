import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import LoginPage from '../pages/LoginPage.vue'

const { push, login, verifyToken } = vi.hoisted(() => ({
  push: vi.fn(),
  login: vi.fn(),
  verifyToken: vi.fn()
}))

vi.mock('vue-router', () => ({
  useRouter: () => ({ push }),
  useRoute: () => ({ query: {} })
}))

vi.mock('../api', () => ({ verifyToken }))
vi.mock('../api/access', () => ({ login }))

describe('LoginPage', () => {
  beforeEach(() => {
    localStorage.clear()
    push.mockReset()
    login.mockReset()
    verifyToken.mockReset()
  })

  it('logs in with a username and password by default', async () => {
    login.mockResolvedValue({ token: 'session-token' })
    const wrapper = mount(LoginPage)

    await wrapper.get('#username-input').setValue('alice')
    await wrapper.get('#password-input').setValue('correct-horse-battery')
    await wrapper.get('form').trigger('submit')

    expect(login).toHaveBeenCalledWith('alice', 'correct-horse-battery')
    expect(push).toHaveBeenCalledWith({ name: 'dashboard' })
  })

  it('keeps bootstrap token login as an explicit compatibility mode', async () => {
    localStorage.setItem('panel_session', 'stale-session')
    verifyToken.mockResolvedValue(true)
    const wrapper = mount(LoginPage)

    await wrapper.findAll('.login-mode__option')[1].trigger('click')
    await wrapper.get('#token-input').setValue('bootstrap-token')
    await wrapper.get('form').trigger('submit')

    expect(verifyToken).toHaveBeenCalledWith('bootstrap-token')
    expect(localStorage.getItem('panel_session')).toBeNull()
    expect(localStorage.getItem('panel_token')).toBe('bootstrap-token')
    expect(push).toHaveBeenCalledWith({ name: 'dashboard' })
  })
})

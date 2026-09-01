import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import LoginPage from '../pages/LoginPage.vue'
import { clearCredentials, setSessionToken } from '../api/authState'

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
    clearCredentials()
    localStorage.clear()
    push.mockReset()
    login.mockReset()
    verifyToken.mockReset()
  })

  it('renders a token-only form without account login controls', () => {
    const wrapper = mount(LoginPage)

    expect(wrapper.find('#username-input').exists()).toBe(false)
    expect(wrapper.find('#password-input').exists()).toBe(false)
    expect(wrapper.find('.login-mode').exists()).toBe(false)
    expect(wrapper.findAll('button').some((button) => button.text() === '账号密码')).toBe(false)
    expect(wrapper.get('#token-input').attributes('type')).toBe('password')
    expect(wrapper.get('button[type="submit"]').text()).toBe('连接')
  })

  it('enters the panel after a valid access token and does not call account login', async () => {
    setSessionToken('stale-session')
    verifyToken.mockResolvedValue(true)
    const wrapper = mount(LoginPage)

    await wrapper.get('#token-input').setValue('panel-token')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(login).not.toHaveBeenCalled()
    expect(verifyToken).toHaveBeenCalledWith('panel-token')
    expect(localStorage.getItem('panel_session')).toBeNull()
    expect(localStorage.getItem('panel_token')).toBe('panel-token')
    expect(push).toHaveBeenCalledWith({ name: 'dashboard' })
  })

  it('stays on the login page with a visible error when the token is empty', async () => {
    const wrapper = mount(LoginPage)

    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(verifyToken).not.toHaveBeenCalled()
    expect(login).not.toHaveBeenCalled()
    expect(push).not.toHaveBeenCalled()
    expect(wrapper.get('.login-error').text()).toBe('令牌无效')
  })

  it('stays on the login page with a visible error when the token is invalid', async () => {
    verifyToken.mockResolvedValue(false)
    const wrapper = mount(LoginPage)

    await wrapper.get('#token-input').setValue('bad-token')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(verifyToken).toHaveBeenCalledWith('bad-token')
    expect(login).not.toHaveBeenCalled()
    expect(push).not.toHaveBeenCalled()
    expect(localStorage.getItem('panel_token')).toBeNull()
    expect(wrapper.get('.login-error').text()).toBe('令牌无效')
  })

  it('shows the API error message when token verification fails', async () => {
    verifyToken.mockRejectedValue({ response: { data: { message: '令牌已过期' } } })
    const wrapper = mount(LoginPage)

    await wrapper.get('#token-input').setValue('expired-token')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(push).not.toHaveBeenCalled()
    expect(wrapper.get('.login-error').text()).toBe('令牌已过期')
  })
})

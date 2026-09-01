import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import router from '../../router'
import UsersPage from './UsersPage.vue'

const mocks = vi.hoisted(() => ({
  fetchUsers: vi.fn(),
  createUser: vi.fn(),
  replace: vi.fn()
}))

vi.mock('../../api/access', () => ({
  fetchUsers: mocks.fetchUsers,
  createUser: mocks.createUser
}))

vi.mock('vue-router', async (original) => {
  const actual = await original()
  return {
    ...actual,
    useRouter: () => ({ replace: mocks.replace })
  }
})

function isUsableManagementPage(path) {
  const record = router.getRoutes().find((route) => route.path === path)
  if (!record) return false
  if (record.redirect) return false
  return Boolean(record.components?.default)
}

describe('UsersPage', () => {
  beforeEach(() => {
    mocks.fetchUsers.mockReset()
    mocks.createUser.mockReset()
    mocks.replace.mockReset()
  })

  it('does not provide user management and leaves /access/users', async () => {
    const wrapper = mount(UsersPage)
    await flushPromises()

    expect(mocks.replace).toHaveBeenCalledWith({ name: 'dashboard' })
    expect(wrapper.get('[data-test="access-retired"]').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('创建首个管理员')
    expect(wrapper.text()).not.toContain('正在读取用户')
    expect(wrapper.text()).not.toContain('修改密码')
    expect(wrapper.find('[data-test="open-create"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="users-grid"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="account-security"]').exists()).toBe(false)
    expect(mocks.fetchUsers).not.toHaveBeenCalled()
    expect(mocks.createUser).not.toHaveBeenCalled()
    expect(isUsableManagementPage('/access/users')).toBe(false)
    expect(router.getRoutes().some((route) => route.name === 'access-users')).toBe(false)
  })
})

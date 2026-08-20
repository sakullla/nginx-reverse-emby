import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import UsersPage from './UsersPage.vue'

const mocks = vi.hoisted(() => ({
  fetchUsers: vi.fn(),
  fetchRoles: vi.fn(),
  createUser: vi.fn(),
  updateUser: vi.fn(),
  deleteUser: vi.fn(),
  changePassword: vi.fn(),
  resetUserPassword: vi.fn(),
  logout: vi.fn(),
  refreshActor: vi.fn(),
  replace: vi.fn(),
  actor: { id: 'usr-admin', username: 'alice', permissions: ['*'], bootstrap: false }
}))

vi.mock('../../api/access', () => ({
  fetchUsers: mocks.fetchUsers,
  fetchRoles: mocks.fetchRoles,
  createUser: mocks.createUser,
  updateUser: mocks.updateUser,
  deleteUser: mocks.deleteUser,
  changePassword: mocks.changePassword,
  resetUserPassword: mocks.resetUserPassword,
  logout: mocks.logout
}))

vi.mock('../../context/useAccessControl', async (original) => {
  const actual = await original()
  return {
    ...actual,
    useAccessControl: () => ({
      actor: { value: mocks.actor },
      can: (permission) => (mocks.actor.permissions || []).includes('*') || (mocks.actor.permissions || []).includes(permission),
      refreshActor: mocks.refreshActor
    })
  }
})

vi.mock('vue-router', () => ({
  useRouter: () => ({ replace: mocks.replace })
}))

const builtinRoles = [
  { id: 'administrator', name: 'administrator' },
  { id: 'operator', name: 'operator' },
  { id: 'readonly', name: 'readonly' }
]

const alice = {
  id: 'usr-admin',
  username: 'alice',
  display_name: 'Alice',
  role_ids: ['administrator'],
  disabled: false
}

const bob = {
  id: 'usr-bob',
  username: 'bob',
  display_name: 'Bob',
  role_ids: ['operator'],
  disabled: false
}

function buttonByText(wrapper, text) {
  return wrapper.findAll('button').find((button) => button.text() === text)
}

function accessError(data) {
  return Object.assign(new Error(data.message || 'access error'), data)
}

const modalStubs = {
  RouterLink: { props: ['to'], template: '<a :href="to"><slot /></a>' },
  BaseModal: {
    props: ['modelValue', 'title', 'subtitle'],
    template: '<div v-if="modelValue"><slot /><slot name="footer" /></div>'
  }
}

async function mountPage() {
  const wrapper = mount(UsersPage, { global: { stubs: modalStubs } })
  await flushPromises()
  return wrapper
}

async function openCreate(wrapper) {
  await wrapper.get('[data-test="open-create"]').trigger('click')
}

beforeEach(() => {
  localStorage.removeItem('view:access-users')
  mocks.fetchUsers.mockReset().mockResolvedValue([alice, bob])
  mocks.fetchRoles.mockReset().mockResolvedValue(builtinRoles)
  mocks.createUser.mockReset().mockImplementation(async (input) => {
    const created = {
      id: 'usr-new',
      username: String(input.username || '').trim().toLowerCase(),
      display_name: input.display_name,
      role_ids: input.role_ids,
      disabled: false
    }
    mocks.fetchUsers.mockResolvedValue([alice, bob, created])
    return created
  })
  mocks.updateUser.mockReset().mockImplementation(async (id, input) => {
    const current = id === bob.id ? bob : alice
    return { ...current, ...input, username: current.username }
  })
  mocks.deleteUser.mockReset().mockImplementation(async (id) => {
    mocks.fetchUsers.mockResolvedValue([alice, bob].filter((user) => user.id !== id))
    return { ok: true }
  })
  mocks.changePassword.mockReset().mockResolvedValue({ ok: true })
  mocks.resetUserPassword.mockReset().mockResolvedValue({ ok: true })
  mocks.logout.mockReset().mockResolvedValue({ ok: true })
  mocks.refreshActor.mockReset()
  mocks.replace.mockReset()
  mocks.actor = { id: 'usr-admin', username: 'alice', permissions: ['*'], bootstrap: false }
})

describe('UsersPage', () => {
  it('shows loading then the searchable user list', async () => {
    let resolveUsers
    mocks.fetchUsers.mockReturnValue(new Promise((resolve) => {
      resolveUsers = resolve
    }))

    const wrapper = mount(UsersPage, { global: { stubs: modalStubs } })
    expect(wrapper.text()).toContain('正在读取用户')

    resolveUsers([alice, bob])
    await flushPromises()

    expect(wrapper.text()).toContain('Alice')
    expect(wrapper.text()).toContain('alice')
    expect(wrapper.get('[data-test="users-grid"]').text()).toContain('Bob')
    expect(wrapper.find('[data-test="profile-display-name"]').exists()).toBe(false)
    expect(wrapper.find('input[data-test="user-username"]').exists()).toBe(false)
    expect(wrapper.findAll('[data-test="delete-user"]').length).toBe(2)
    expect(wrapper.find('[data-test="account-security"]').exists()).toBe(true)
  })

  it('guides first-admin initialization when the account list is empty', async () => {
    mocks.fetchUsers.mockResolvedValue([])
    mocks.actor = { id: 'bootstrap', username: 'token', permissions: ['*'], bootstrap: true }

    const wrapper = await mountPage()

    expect(wrapper.text()).toContain('还没有账号')
    expect(wrapper.text()).toContain('首个管理员')
    expect(wrapper.find('[data-test="account-security"]').exists()).toBe(false)
    await openCreate(wrapper)
    expect(wrapper.text()).toContain('administrator')
    expect(wrapper.get('[data-test="create-role-administrator"]').element.checked).toBe(true)
    expect(buttonByText(wrapper, '创建首个管理员')).toBeTruthy()
  })

  it('creates the first administrator and offers a token logout path', async () => {
    mocks.fetchUsers.mockResolvedValue([])
    mocks.actor = { id: 'bootstrap', username: 'token', permissions: ['*'], bootstrap: true }
    mocks.createUser.mockImplementation(async (input) => {
      const created = {
        id: 'usr-admin',
        username: input.username,
        display_name: input.display_name,
        role_ids: input.role_ids,
        disabled: false
      }
      mocks.fetchUsers.mockResolvedValue([created])
      return created
    })

    const wrapper = await mountPage()
    await openCreate(wrapper)
    await wrapper.get('[data-test="create-username"]').setValue('Alice')
    await wrapper.get('[data-test="create-display-name"]').setValue('站点管理员')
    await wrapper.get('[data-test="create-password"]').setValue('correct-horse')
    await wrapper.get('[data-test="create-confirm-password"]').setValue('correct-horse')
    await wrapper.get('[data-test="create-form"]').trigger('submit')
    await flushPromises()

    expect(mocks.createUser).toHaveBeenCalledWith({
      username: 'Alice',
      display_name: '站点管理员',
      password: 'correct-horse',
      role_ids: ['administrator']
    })
    expect(wrapper.text()).toContain('首个管理员已创建')
    expect(wrapper.text()).toContain('账号密码')
    await wrapper.get('[data-test="logout-to-account"]').trigger('click')
    await flushPromises()
    expect(mocks.logout).toHaveBeenCalledTimes(1)
    expect(mocks.replace).toHaveBeenCalledWith({ name: 'login' })
  })

  it('surfaces field errors and does not create when the form is invalid', async () => {
    const wrapper = await mountPage()
    await openCreate(wrapper)
    await wrapper.get('[data-test="create-username"]').setValue(' ')
    await wrapper.get('[data-test="create-password"]').setValue('short')
    await wrapper.get('[data-test="create-confirm-password"]').setValue('other')
    await wrapper.get('[data-test="create-form"]').trigger('submit')
    await flushPromises()

    expect(mocks.createUser).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('请填写用户名')
    expect(wrapper.text()).toContain('至少 10 个字符')
    expect(wrapper.text()).toContain('两次输入的密码不一致')
    expect(wrapper.text()).toContain('至少选择一个角色')
  })

  it('shows server field errors when create is rejected', async () => {
    mocks.createUser.mockRejectedValue(accessError({
      code: 'invalid_input',
      message: 'invalid request',
      fields: {
        username: 'username already exists',
        password: 'password must be at least 10 characters',
        role_ids: 'at least one role is required'
      }
    }))

    const wrapper = await mountPage()
    await openCreate(wrapper)
    await wrapper.get('[data-test="create-username"]').setValue('alice')
    await wrapper.get('[data-test="create-password"]').setValue('correct-horse')
    await wrapper.get('[data-test="create-confirm-password"]').setValue('correct-horse')
    await wrapper.get('[data-test="create-role-operator"]').setValue(true)
    await wrapper.get('[data-test="create-form"]').trigger('submit')
    await flushPromises()

    expect(wrapper.text()).toContain('username already exists')
    expect(wrapper.text()).toContain('password must be at least 10 characters')
    expect(wrapper.text()).toContain('at least one role is required')
  })

  it('updates display name and roles for the selected user', async () => {
    const wrapper = await mountPage()
    await wrapper.get('[data-test="user-row-usr-bob"]').trigger('click')
    await wrapper.get('[data-test="profile-display-name"]').setValue('Bobby')
    await wrapper.get('[data-test="profile-role-readonly"]').setValue(true)
    await wrapper.get('[data-test="profile-form"]').trigger('submit')
    await flushPromises()

    expect(mocks.updateUser).toHaveBeenCalledWith('usr-bob', {
      display_name: 'Bobby',
      role_ids: ['operator', 'readonly']
    })
  })

  it('requires confirmation before disabling and keeps state when cancelled', async () => {
    const wrapper = await mountPage()
    await wrapper.get('[data-test="disable-user"]').trigger('click')
    expect(wrapper.get('[data-test="confirm-dialog"]').text()).toContain('确认停用')

    await wrapper.get('[data-test="confirm-cancel"]').trigger('click')
    expect(wrapper.find('[data-test="confirm-dialog"]').exists()).toBe(false)
    expect(mocks.updateUser).not.toHaveBeenCalled()
  })

  it('cancels a dangerous confirm with Escape and does not write', async () => {
    const wrapper = await mountPage()
    await wrapper.get('[data-test="disable-user"]').trigger('click')
    await wrapper.get('[data-test="confirm-dialog"]').trigger('keydown.escape')
    expect(wrapper.find('[data-test="confirm-dialog"]').exists()).toBe(false)
    expect(mocks.updateUser).not.toHaveBeenCalled()
  })

  it('disables a user after confirmation and shows last-admin protection', async () => {
    const wrapper = await mountPage()
    await wrapper.get('[data-test="disable-user"]').trigger('click')
    await wrapper.get('[data-test="confirm-accept"]').trigger('click')
    await flushPromises()
    expect(mocks.updateUser).toHaveBeenCalledWith('usr-admin', { disabled: true })

    mocks.updateUser.mockRejectedValueOnce(accessError({
      code: 'last_admin_protected',
      message: 'cannot remove the last sign-in administrator',
      details: { reason: 'last_admin' }
    }))
    await wrapper.get('[data-test="disable-user"]').trigger('click')
    await wrapper.get('[data-test="confirm-accept"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('cannot remove the last sign-in administrator')
  })

  it('deletes a user after confirmation and protects the last administrator', async () => {
    const wrapper = await mountPage()
    await wrapper.get('[data-test="user-row-usr-bob"]').get('[data-test="delete-user"]').trigger('click')
    expect(wrapper.get('[data-test="confirm-dialog"]').text()).toContain('确认删除')
    await wrapper.get('[data-test="confirm-cancel"]').trigger('click')
    expect(mocks.deleteUser).not.toHaveBeenCalled()

    await wrapper.get('[data-test="user-row-usr-bob"]').get('[data-test="delete-user"]').trigger('click')
    await wrapper.get('[data-test="confirm-accept"]').trigger('click')
    await flushPromises()
    expect(mocks.deleteUser).toHaveBeenCalledWith('usr-bob')
    expect(wrapper.text()).not.toContain('Bob')

    mocks.deleteUser.mockRejectedValueOnce(accessError({
      code: 'last_admin_protected',
      message: 'cannot disable, delete or demote the last sign-in capable full administrator',
      details: { reason: 'last_admin' }
    }))
    await wrapper.get('[data-test="delete-user"]').trigger('click')
    await wrapper.get('[data-test="confirm-accept"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('cannot disable, delete or demote the last sign-in capable full administrator')
  })

  it('resets another user password only after confirmation', async () => {
    const wrapper = await mountPage()
    await wrapper.get('[data-test="user-row-usr-bob"]').get('[data-test="reset-user"]').trigger('click')
    await wrapper.get('[data-test="reset-password"]').setValue('reset-password-1')
    await wrapper.get('[data-test="reset-confirm-password"]').setValue('reset-password-1')
    await wrapper.get('[data-test="reset-form"]').trigger('submit')
    expect(mocks.resetUserPassword).not.toHaveBeenCalled()

    await wrapper.get('[data-test="confirm-accept"]').trigger('click')
    await flushPromises()
    expect(mocks.resetUserPassword).toHaveBeenCalledWith('usr-bob', { new_password: 'reset-password-1' })
  })

  it('changes the current account password and returns to login', async () => {
    const wrapper = await mountPage()
    await wrapper.get('[data-test="open-password"]').trigger('click')
    await wrapper.get('[data-test="current-password"]').setValue('old-password-1')
    await wrapper.get('[data-test="own-new-password"]').setValue('new-password-1')
    await wrapper.get('[data-test="own-confirm-password"]').setValue('new-password-1')
    await wrapper.get('[data-test="password-form"]').trigger('submit')
    await flushPromises()

    expect(mocks.changePassword).toHaveBeenCalledWith({
      current_password: 'old-password-1',
      new_password: 'new-password-1'
    })
    expect(mocks.replace).toHaveBeenCalledWith({ name: 'login' })
  })

  it('keeps the current password when confirmation does not match', async () => {
    const wrapper = await mountPage()
    await wrapper.get('[data-test="open-password"]').trigger('click')
    await wrapper.get('[data-test="current-password"]').setValue('old-password-1')
    await wrapper.get('[data-test="own-new-password"]').setValue('new-password-1')
    await wrapper.get('[data-test="own-confirm-password"]').setValue('mismatch-password')
    await wrapper.get('[data-test="password-form"]').trigger('submit')
    await flushPromises()

    expect(mocks.changePassword).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('两次输入的新密码不一致')
  })

  it('filters users locally and retries a load failure', async () => {
    const wrapper = await mountPage()
    await wrapper.get('[data-test="user-search"]').setValue('  bob ')
    expect(wrapper.text()).toContain('Bob')
    expect(wrapper.text()).not.toContain('Alice')
    expect(mocks.fetchUsers).toHaveBeenLastCalledWith()

    mocks.fetchUsers.mockRejectedValueOnce(new Error('network down'))
    const wrapperError = mount(UsersPage, { global: { stubs: modalStubs } })
    await flushPromises()
    expect(wrapperError.text()).toContain('Alice')
    expect(wrapperError.text()).toContain('Bob')
  })

  it('restores the user list after clearing a search instead of showing first-admin empty state', async () => {
    const wrapper = await mountPage()
    await wrapper.get('[data-test="user-search"]').setValue('nobody')
    expect(wrapper.text()).toContain('没有匹配的用户')
    expect(wrapper.text()).not.toContain('还没有账号')
    expect(mocks.fetchUsers).toHaveBeenCalledTimes(1)

    await wrapper.get('[data-test="clear-search"]').trigger('click')
    expect(mocks.fetchUsers).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).toContain('Alice')
    expect(wrapper.text()).toContain('Bob')
    expect(wrapper.text()).not.toContain('还没有账号')
    expect(wrapper.text()).not.toContain('创建首个管理员')
  })

  it('hides management actions from an unauthorized identity', async () => {
    mocks.actor = { id: 'usr-ro', username: 'reader', permissions: ['resource.read'], bootstrap: false }
    const wrapper = await mountPage()

    expect(wrapper.text()).toContain('无权查看用户')
    expect(wrapper.find('[data-test="create-form"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="account-security"]').exists()).toBe(false)
    expect(mocks.fetchUsers).not.toHaveBeenCalled()
    expect(mocks.createUser).not.toHaveBeenCalled()
  })

  it('shows submitting labels while a create request is in flight', async () => {
    let resolveCreate
    mocks.createUser.mockReturnValue(new Promise((resolve) => {
      resolveCreate = resolve
    }))
    const wrapper = await mountPage()
    await openCreate(wrapper)
    await wrapper.get('[data-test="create-username"]').setValue('carol')
    await wrapper.get('[data-test="create-password"]').setValue('correct-horse')
    await wrapper.get('[data-test="create-confirm-password"]').setValue('correct-horse')
    await wrapper.get('[data-test="create-role-operator"]').setValue(true)
    const submit = wrapper.get('[data-test="create-submit"]')
    await wrapper.get('[data-test="create-form"]').trigger('submit')
    expect(submit.text()).toBe('创建中…')
    expect(submit.element.disabled).toBe(true)

    resolveCreate({
      id: 'usr-carol',
      username: 'carol',
      display_name: '',
      role_ids: ['operator'],
      disabled: false
    })
    await flushPromises()
  })
})

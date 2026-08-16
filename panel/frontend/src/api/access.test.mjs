import { beforeEach, describe, expect, it, vi } from 'vitest'

const get = vi.fn()
const post = vi.fn()
const put = vi.fn()
vi.mock('./client', () => ({ api: { get, post, put } }))

const access = await import('./access.js')
const {
  login,
  logout,
  fetchUsers,
  fetchUser,
  createUser,
  updateUser,
  changePassword,
  resetUserPassword
} = access

function accessError(data) {
  return Object.assign(new Error(data.message || 'access error'), {
    response: { data }
  })
}

describe('session credential lifecycle', () => {
  beforeEach(() => {
    localStorage.clear()
    get.mockReset()
    post.mockReset()
    put.mockReset()
  })

  it('replaces a legacy panel token with the authenticated session', async () => {
    localStorage.setItem('panel_token', 'bootstrap-token')
    post.mockResolvedValue({ data: { session: { token: 'session-token' } } })

    await login('alice', 'password')

    expect(localStorage.getItem('panel_token')).toBeNull()
    expect(localStorage.getItem('panel_session')).toBe('session-token')
  })

  it('clears both credential modes even when server logout fails', async () => {
    localStorage.setItem('panel_token', 'bootstrap-token')
    localStorage.setItem('panel_session', 'session-token')
    post.mockRejectedValue(new Error('offline'))

    await expect(logout()).rejects.toThrow('offline')

    expect(localStorage.getItem('panel_token')).toBeNull()
    expect(localStorage.getItem('panel_session')).toBeNull()
  })
})

describe('authz user account contract', () => {
  beforeEach(() => {
    localStorage.clear()
    get.mockReset()
    post.mockReset()
    put.mockReset()
  })

  it('lists and reads users without password material and supports q', async () => {
    const listed = {
      id: 'usr-1',
      username: 'alice',
      display_name: 'Alice',
      password: 'leaked',
      password_hash: 'hash'
    }
    get.mockResolvedValueOnce({ data: { users: [listed] } })
    get.mockResolvedValueOnce({ data: { user: { ...listed, role_ids: ['administrator'] } } })

    await expect(fetchUsers({ q: ' ali ' })).resolves.toEqual([
      { id: 'usr-1', username: 'alice', display_name: 'Alice' }
    ])
    await expect(fetchUser('usr/1')).resolves.toEqual({
      id: 'usr-1',
      username: 'alice',
      display_name: 'Alice',
      role_ids: ['administrator']
    })

    expect(get).toHaveBeenNthCalledWith(1, '/access/users', { params: { q: 'ali' } })
    expect(get).toHaveBeenNthCalledWith(2, '/access/users/usr%2F1')
  })

  it('creates users with the closed write payload and surfaces field errors', async () => {
    post.mockResolvedValueOnce({
      data: {
        user: {
          id: 'usr-1',
          username: 'alice',
          display_name: 'Alice',
          role_ids: ['operator'],
          password: 'correct-horse',
          password_hash: 'hash'
        }
      }
    })

    await expect(createUser({
      username: 'alice',
      display_name: 'Alice',
      password: 'correct-horse',
      role_ids: ['operator'],
      disabled: true,
      extra: 'nope'
    })).resolves.toEqual({
      id: 'usr-1',
      username: 'alice',
      display_name: 'Alice',
      role_ids: ['operator']
    })
    expect(post).toHaveBeenCalledWith('/access/users', {
      username: 'alice',
      display_name: 'Alice',
      password: 'correct-horse',
      role_ids: ['operator']
    })

    post.mockRejectedValueOnce(accessError({
      code: 'invalid_input',
      message: 'invalid request',
      fields: {
        username: 'already exists',
        password: 'must be at least 10 characters',
        role_ids: 'at least one delegable role is required'
      }
    }))

    await expect(createUser({
      username: 'Alice',
      password: 'short',
      role_ids: []
    })).rejects.toMatchObject({
      code: 'invalid_input',
      fields: {
        username: 'already exists',
        password: 'must be at least 10 characters',
        role_ids: 'at least one delegable role is required'
      }
    })
    expect(post).toHaveBeenLastCalledWith('/access/users', {
      username: 'Alice',
      display_name: undefined,
      password: 'short',
      role_ids: []
    })
  })

  it('updates display_name, role_ids and disabled without username or delete', async () => {
    put.mockResolvedValue({
      data: { user: { id: 'usr-1', username: 'alice', display_name: 'Alicia', role_ids: ['operator'], disabled: true } }
    })

    await expect(updateUser('usr-1', {
      username: 'bob',
      display_name: 'Alicia',
      role_ids: ['operator'],
      disabled: true,
      password: 'should-not-send'
    })).resolves.toEqual({
      id: 'usr-1',
      username: 'alice',
      display_name: 'Alicia',
      role_ids: ['operator'],
      disabled: true
    })
    expect(put).toHaveBeenCalledWith('/access/users/usr-1', {
      display_name: 'Alicia',
      role_ids: ['operator'],
      disabled: true
    })
    expect(access.deleteUser).toBeUndefined()
  })

  it('propagates last-administrator protection without sending a delete', async () => {
    put.mockRejectedValue(accessError({
      code: 'last_admin_protected',
      message: 'cannot remove the last sign-in administrator',
      details: { reason: 'last_admin' }
    }))

    await expect(updateUser('usr-1', { disabled: true })).rejects.toMatchObject({
      code: 'last_admin_protected',
      details: { reason: 'last_admin' }
    })
    expect(put).toHaveBeenCalledWith('/access/users/usr-1', { disabled: true })
    expect(post).not.toHaveBeenCalled()
  })

  it('changes own password then drops only the account session', async () => {
    localStorage.setItem('panel_token', 'bootstrap-token')
    localStorage.setItem('panel_session', 'session-token')
    post.mockResolvedValue({
      data: { ok: true, current_password: 'old-password-1', new_password: 'new-password-1' }
    })

    await expect(changePassword({
      current_password: 'old-password-1',
      new_password: 'new-password-1',
      confirm_password: 'new-password-1'
    })).resolves.toEqual({ ok: true })
    expect(post).toHaveBeenCalledWith('/access/me/password', {
      current_password: 'old-password-1',
      new_password: 'new-password-1'
    })
    expect(localStorage.getItem('panel_session')).toBeNull()
    expect(localStorage.getItem('panel_token')).toBe('bootstrap-token')
  })

  it('keeps the current session when password writes fail', async () => {
    localStorage.setItem('panel_session', 'session-token')
    post.mockRejectedValueOnce(accessError({
      code: 'invalid_credentials',
      message: 'invalid username or password'
    }))
    post.mockRejectedValueOnce(accessError({
      code: 'invalid_input',
      message: 'invalid request',
      fields: { new_password: 'must be at least 10 characters' }
    }))

    await expect(changePassword({
      current_password: 'wrong-password',
      new_password: 'new-password-1'
    })).rejects.toMatchObject({ code: 'invalid_credentials' })
    await expect(resetUserPassword('usr-2', { new_password: 'short' })).rejects.toMatchObject({
      code: 'invalid_input',
      fields: { new_password: 'must be at least 10 characters' }
    })
    expect(localStorage.getItem('panel_session')).toBe('session-token')
  })

  it('resets another user password without dropping the operator session', async () => {
    localStorage.setItem('panel_session', 'admin-session')
    post.mockResolvedValue({ data: { ok: true, password: 'reset-password-1' } })

    await expect(resetUserPassword('usr/2', { new_password: 'reset-password-1' })).resolves.toEqual({ ok: true })
    expect(post).toHaveBeenCalledWith('/access/users/usr%2F2/password', {
      new_password: 'reset-password-1'
    })
    expect(localStorage.getItem('panel_session')).toBe('admin-session')
  })
})

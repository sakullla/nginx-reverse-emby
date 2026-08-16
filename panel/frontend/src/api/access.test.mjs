import { beforeEach, describe, expect, it, vi } from 'vitest'

const get = vi.fn()
const post = vi.fn()
const put = vi.fn()
const del = vi.fn()
vi.mock('./client', () => ({ api: { get, post, put, delete: del } }))

const access = await import('./access.js')
const {
  login,
  logout,
  fetchUsers,
  fetchUser,
  createUser,
  updateUser,
  changePassword,
  resetUserPassword,
  fetchResourceGroups,
  fetchResourceGroup,
  createResourceGroup,
  updateResourceGroup,
  deleteResourceGroup,
  grantResourceGroup,
  revokeResourceGroupGrant,
  fetchResources,
  bindResource,
  unbindResource
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
    del.mockReset()
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
    del.mockReset()
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
      message: 'current password is incorrect',
      fields: { current_password: 'current password is incorrect' }
    }))
    post.mockRejectedValueOnce(accessError({
      code: 'invalid_input',
      message: 'invalid request',
      fields: { new_password: 'must be at least 10 characters' }
    }))

    await expect(changePassword({
      current_password: 'wrong-password',
      new_password: 'new-password-1'
    })).rejects.toMatchObject({
      code: 'invalid_credentials',
      fields: { current_password: 'current password is incorrect' }
    })
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

describe('authz resource group contract', () => {
  beforeEach(() => {
    localStorage.clear()
    get.mockReset()
    post.mockReset()
    put.mockReset()
    del.mockReset()
  })

  it('lists and reads groups with counts, grants, members by kind and q', async () => {
    const listed = {
      id: 'rg-1',
      name: '团队组',
      description: '团队可见',
      builtin: false,
      grant_count: 2,
      resource_count: 3
    }
    const detail = {
      ...listed,
      grants: [
        { subject_kind: 'user', subject_id: 'usr-1', resource_group_id: 'rg-1' },
        { subject_kind: 'role', subject_id: 'operator', resource_group_id: 'rg-1' }
      ],
      members: {
        agent: [{ id: 'edge-a', name: 'edge-a', resource_group_id: 'rg-1' }],
        http_rule: [{ id: 'edge-a:1', name: 'emby', resource_group_id: 'rg-1' }],
        l4_rule: [],
        relay_listener: [],
        certificate: [{ id: '12', name: 'edge-cert', resource_group_id: 'rg-1' }],
        egress_profile: []
      }
    }
    get.mockResolvedValueOnce({ data: { resource_groups: [listed] } })
    get.mockResolvedValueOnce({ data: { resource_group: detail } })

    await expect(fetchResourceGroups({ q: ' 团队 ' })).resolves.toEqual([listed])
    await expect(fetchResourceGroup('rg/1')).resolves.toEqual(detail)
    expect(get).toHaveBeenNthCalledWith(1, '/access/resource-groups', { params: { q: '团队' } })
    expect(get).toHaveBeenNthCalledWith(2, '/access/resource-groups/rg%2F1')
  })

  it('creates and updates name or description without id or builtin identity', async () => {
    post.mockResolvedValueOnce({
      data: { resource_group: { id: 'rg-1', name: '团队组', description: '团队可见', builtin: false } }
    })
    put.mockResolvedValueOnce({
      data: { resource_group: { id: 'rg-1', name: '核心组', description: '更新后', builtin: false } }
    })

    await expect(createResourceGroup({
      name: '团队组',
      description: '团队可见',
      id: 'default',
      builtin: true,
      extra: 'nope'
    })).resolves.toEqual({
      id: 'rg-1',
      name: '团队组',
      description: '团队可见',
      builtin: false
    })
    expect(post).toHaveBeenCalledWith('/access/resource-groups', {
      name: '团队组',
      description: '团队可见'
    })

    await expect(updateResourceGroup('rg/1', {
      id: 'default',
      builtin: false,
      name: '核心组',
      description: '更新后',
      extra: 'nope'
    })).resolves.toEqual({
      id: 'rg-1',
      name: '核心组',
      description: '更新后',
      builtin: false
    })
    expect(put).toHaveBeenCalledWith('/access/resource-groups/rg%2F1', {
      name: '核心组',
      description: '更新后'
    })
  })

  it('deletes an unused group and surfaces classified dependencies or builtin protection', async () => {
    del.mockResolvedValueOnce({ data: { ok: true } })
    await expect(deleteResourceGroup('rg/1')).resolves.toEqual({ ok: true })
    expect(del).toHaveBeenCalledWith('/access/resource-groups/rg%2F1')

    del.mockRejectedValueOnce(accessError({
      code: 'resource_group_in_use',
      message: 'resource group still has grants or bindings',
      details: {
        grants: [{ subject_kind: 'user', subject_id: 'usr-1', resource_group_id: 'rg-1' }],
        bindings: [{ resource_kind: 'agent', resource_id: 'edge-a', resource_group_id: 'rg-1' }]
      }
    }))
    await expect(deleteResourceGroup('rg-1')).rejects.toMatchObject({
      code: 'resource_group_in_use',
      details: {
        grants: [{ subject_kind: 'user', subject_id: 'usr-1', resource_group_id: 'rg-1' }],
        bindings: [{ resource_kind: 'agent', resource_id: 'edge-a', resource_group_id: 'rg-1' }]
      }
    })

    del.mockRejectedValueOnce(accessError({
      code: 'resource_group_protected',
      message: 'builtin resource group cannot be deleted',
      details: { reason: 'builtin', id: 'default' }
    }))
    await expect(deleteResourceGroup('default')).rejects.toMatchObject({
      code: 'resource_group_protected',
      details: { reason: 'builtin', id: 'default' }
    })
    expect(put).not.toHaveBeenCalled()
  })

  it('grants and revokes with a closed payload and keeps duplicate grants as one request shape', async () => {
    post.mockResolvedValue({ data: { ok: true } })
    del.mockResolvedValue({ data: { ok: true } })
    const extra = {
      subject_kind: 'user',
      subject_id: 'usr-1',
      resource_group_id: 'rg-1',
      extra: 'nope'
    }

    await expect(grantResourceGroup(extra)).resolves.toEqual({ ok: true })
    await expect(grantResourceGroup(extra)).resolves.toEqual({ ok: true })
    await expect(revokeResourceGroupGrant(extra)).resolves.toEqual({ ok: true })

    expect(post).toHaveBeenCalledTimes(2)
    expect(post).toHaveBeenNthCalledWith(1, '/access/resource-group-grants', {
      subject_kind: 'user',
      subject_id: 'usr-1',
      resource_group_id: 'rg-1'
    })
    expect(post).toHaveBeenNthCalledWith(2, '/access/resource-group-grants', {
      subject_kind: 'user',
      subject_id: 'usr-1',
      resource_group_id: 'rg-1'
    })
    expect(del).toHaveBeenCalledWith('/access/resource-group-grants', {
      data: {
        subject_kind: 'user',
        subject_id: 'usr-1',
        resource_group_id: 'rg-1'
      }
    })
  })

  it('moves a resource by group id and unbinds without binding to default', async () => {
    post.mockResolvedValueOnce({ data: { ok: true } })
    del.mockResolvedValueOnce({ data: { ok: true } })
    get.mockResolvedValueOnce({
      data: {
        resources: [{
          id: 'edge-a',
          name: 'edge-a',
          resource_kind: 'agent',
          resource_group_id: 'default'
        }]
      }
    })

    await expect(bindResource({
      resource_kind: 'agent',
      resource_id: 'edge-a',
      resource_group_id: 'rg-1',
      extra: 'nope'
    })).resolves.toEqual({ ok: true })
    await expect(unbindResource({
      resource_kind: 'agent',
      resource_id: 'edge/a',
      resource_group_id: 'default',
      extra: 'nope'
    })).resolves.toEqual({ ok: true })
    await expect(fetchResources({ kind: ' agent ', q: ' edge ' })).resolves.toEqual([{
      id: 'edge-a',
      name: 'edge-a',
      resource_kind: 'agent',
      resource_group_id: 'default'
    }])

    expect(post).toHaveBeenCalledWith('/access/resource-bindings', {
      resource_kind: 'agent',
      resource_id: 'edge-a',
      resource_group_id: 'rg-1'
    })
    expect(del).toHaveBeenCalledWith('/access/resource-bindings', {
      data: {
        resource_kind: 'agent',
        resource_id: 'edge/a'
      }
    })
    expect(get).toHaveBeenCalledWith('/access/resources', {
      params: { kind: 'agent', q: 'edge' }
    })
  })

  it('surfaces permission_denied and field errors on group write paths', async () => {
    put.mockRejectedValueOnce(accessError({
      code: 'permission_denied',
      message: 'permission denied'
    }))
    del.mockRejectedValueOnce(accessError({
      code: 'permission_denied',
      message: 'permission denied'
    }))
    post.mockRejectedValueOnce(accessError({
      code: 'invalid_input',
      message: 'invalid request',
      fields: { name: 'name is required' }
    }))

    await expect(updateResourceGroup('rg-1', { description: 'x' })).rejects.toMatchObject({
      code: 'permission_denied'
    })
    await expect(unbindResource({ resource_kind: 'agent', resource_id: 'edge-a' })).rejects.toMatchObject({
      code: 'permission_denied'
    })
    await expect(createResourceGroup({ description: 'missing name' })).rejects.toMatchObject({
      code: 'invalid_input',
      fields: { name: 'name is required' }
    })
    expect(post).toHaveBeenLastCalledWith('/access/resource-groups', {
      name: undefined,
      description: 'missing name'
    })
  })
})

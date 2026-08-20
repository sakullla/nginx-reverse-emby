import { afterEach, describe, expect, it, vi } from 'vitest'
import { fetchCurrentActor } from '../api/access'
import { clearCredentials, setSessionToken } from '../api/authState'
import {
  accessManagementNavigation,
  accessNavigation,
  canChangeOwnPassword,
  filterPluginDetailForActor,
  isAccessManagementChildActive,
  isBootstrapActor,
  pickDefaultResourceGroupID,
  resourceGroupDisplayDescription,
  resourceGroupDisplayName,
  shouldPromptLeaveBootstrap,
  shouldShowFirstAdminSetup,
  useAccessControl,
  validateOwnPasswordChange,
  visibleAccessManagementNavForActor,
  visibleResourceGroupsForActor
} from './useAccessControl'

vi.mock('../api/access', () => ({
  fetchCurrentActor: vi.fn()
}))

describe('useAccessControl identity changes', () => {
  afterEach(() => {
    useAccessControl().clearActor()
    clearCredentials()
    vi.clearAllMocks()
  })

  it('clears loading when credentials change during a pending actor request', async () => {
    let resolveActor
    fetchCurrentActor.mockReturnValue(new Promise((resolve) => {
      resolveActor = resolve
    }))
    const access = useAccessControl()

    const pending = access.refreshActor()
    expect(access.loading.value).toBe(true)

    setSessionToken('replacement-session')
    expect(access.loading.value).toBe(false)

    resolveActor({ id: 'stale-actor', permissions: ['*'] })
    await expect(pending).resolves.toBeNull()
    expect(access.actor.value).toBeNull()
    expect(access.loading.value).toBe(false)
  })
})

describe('plugin resource-group projection', () => {
  const detail = {
    instances: [{ id: 'a', resource_group_id: 'group-a' }, { id: 'b', resource_group_id: 'group-b' }],
    agent_statuses: [{ instance_id: 'a', agent_id: 'edge-a' }, { instance_id: 'b', agent_id: 'edge-b' }]
  }

  it('keeps only instances and Agent status in member-visible groups', () => {
    const visible = filterPluginDetailForActor(detail, { permissions: ['resource.read'], visible_resource_groups: ['group-a'] })
    expect(visible.instances.map((item) => item.id)).toEqual(['a'])
    expect(visible.agent_statuses.map((item) => item.agent_id)).toEqual(['edge-a'])
  })

  it('keeps installed plugins without exposing instances outside visible groups', () => {
    const hidden = filterPluginDetailForActor(detail, { permissions: ['resource.read'], visible_resource_groups: [] })
    expect(hidden.instances).toEqual([])
    expect(hidden.agent_statuses).toEqual([])
    expect(filterPluginDetailForActor(detail, { permissions: ['*'], visible_resource_groups: [] })).toBe(detail)
  })

  it('keeps an installed plugin that has no instances yet', () => {
    const installed = { plugin: { plugin_id: 'official.waf' }, instances: [], agent_statuses: [] }
    const visible = filterPluginDetailForActor(installed, { permissions: ['resource.read'], visible_resource_groups: ['default'] })
    expect(visible.plugin.plugin_id).toBe('official.waf')
    expect(visible.instances).toEqual([])
    expect(visible.agent_statuses).toEqual([])
  })
})

describe('visible resource group selection', () => {
  it('lists only groups the actor can see and prefers default when visible', () => {
    const groups = [
      { id: 'team', name: '团队' },
      { id: 'default', name: '默认组' },
      { id: 'hidden', name: '隐藏组' }
    ]
    expect(visibleResourceGroupsForActor(groups, { permissions: ['resource.read'], visible_resource_groups: ['team', 'default'] }).map((group) => group.id)).toEqual(['team', 'default'])
    expect(pickDefaultResourceGroupID(groups)).toBe('default')
    expect(pickDefaultResourceGroupID([{ id: 'team', name: '团队' }])).toBe('team')
    expect(pickDefaultResourceGroupID([])).toBe('')
    expect(resourceGroupDisplayName({ id: 'default', name: 'default' })).toBe('默认组')
    expect(resourceGroupDisplayName({ id: 'team', name: '团队' })).toBe('团队')
    expect(resourceGroupDisplayDescription({
      id: 'default',
      description: 'resources not explicitly assigned to another group'
    })).toBe('未另行指定的资源')
    expect(resourceGroupDisplayDescription({ id: 'default', description: '' })).toBe('未另行指定的资源')
    expect(resourceGroupDisplayDescription({ id: 'team', description: '团队可见' })).toBe('团队可见')
  })
})

describe('access management navigation', () => {
  it('keeps overview cards off the sidebar group and hides the group without visible children', () => {
    expect(accessManagementNavigation).toMatchObject({
      id: 'users-and-resources',
      label: '用户与资源管理'
    })
    expect(accessManagementNavigation.children.map((item) => item.id)).toEqual(['users', 'resource-groups'])
    expect(accessManagementNavigation.children.map((item) => item.path)).toEqual([
      '/access/users',
      '/access/resource-groups'
    ])
    expect(accessManagementNavigation.children.some((item) => ['roles', 'quotas', 'secrets', 'audit'].includes(item.id))).toBe(false)
    expect(accessNavigation.find((item) => item.id === 'users')).toMatchObject({ permission: 'access.manage' })
    expect(accessNavigation.find((item) => item.id === 'users').path).toBeUndefined()
    expect(visibleAccessManagementNavForActor({ permissions: ['*'] }).children.map((item) => item.id)).toEqual(['users', 'resource-groups'])
    expect(visibleAccessManagementNavForActor({ permissions: ['access.manage'] }).children.map((item) => item.id)).toEqual(['users'])
    expect(visibleAccessManagementNavForActor({ permissions: ['resource.read'] }).children.map((item) => item.id)).toEqual(['resource-groups'])
    expect(visibleAccessManagementNavForActor({ permissions: ['audit.read'] })).toBeNull()
    expect(visibleAccessManagementNavForActor(null)).toBeNull()
  })

  it('marks authorized child pages as active for expanded and collapsed nav', () => {
    const users = accessManagementNavigation.children[0]
    const groups = accessManagementNavigation.children[1]
    expect(isAccessManagementChildActive(users, { name: 'access-users', path: '/access/users' })).toBe(true)
    expect(isAccessManagementChildActive(users, { name: 'access', path: '/access' })).toBe(false)
    expect(isAccessManagementChildActive(groups, { name: 'access-resource-groups', path: '/access/resource-groups' })).toBe(true)
    expect(isAccessManagementChildActive(groups, { path: '/access/resource-groups' })).toBe(true)
  })
})

describe('first-admin setup and account security', () => {
  const bootstrap = { id: 'bootstrap-administrator', bootstrap: true, permissions: ['*'] }
  const admin = { id: 'usr-admin', username: 'alice', permissions: ['access.manage'] }
  const reader = { id: 'usr-reader', username: 'bob', permissions: ['resource.read'] }

  afterEach(() => {
    useAccessControl().clearActor()
    clearCredentials()
    vi.clearAllMocks()
  })

  it('shows empty-directory setup only to identities that can manage users', () => {
    expect(shouldShowFirstAdminSetup(bootstrap, [])).toBe(true)
    expect(shouldShowFirstAdminSetup(admin, [])).toBe(true)
    expect(shouldShowFirstAdminSetup(admin, null)).toBe(true)
    expect(shouldShowFirstAdminSetup(reader, [])).toBe(false)
    expect(shouldShowFirstAdminSetup(admin, [{ id: 'usr-admin' }])).toBe(false)
    expect(shouldPromptLeaveBootstrap(bootstrap, [{ id: 'usr-admin' }])).toBe(true)
    expect(shouldPromptLeaveBootstrap(bootstrap, [])).toBe(false)
    expect(shouldPromptLeaveBootstrap(admin, [{ id: 'usr-admin' }])).toBe(false)
  })

  it('allows own password change only for non-bootstrap account sessions', () => {
    expect(isBootstrapActor(bootstrap)).toBe(true)
    expect(canChangeOwnPassword(bootstrap)).toBe(false)
    expect(canChangeOwnPassword(admin)).toBe(true)
    expect(canChangeOwnPassword(null)).toBe(false)
    expect(validateOwnPasswordChange({
      actor: admin,
      current_password: 'old-password-1',
      new_password: 'new-password-1',
      confirm_password: 'new-password-1'
    })).toEqual({ ok: true, fields: {} })
    expect(validateOwnPasswordChange({
      actor: bootstrap,
      current_password: 'old-password-1',
      new_password: 'new-password-1',
      confirm_password: 'new-password-1'
    })).toMatchObject({ ok: false, code: 'permission_denied' })
    expect(validateOwnPasswordChange({
      actor: admin,
      current_password: '',
      new_password: 'short',
      confirm_password: 'other'
    }).fields).toMatchObject({
      current_password: 'current password is required',
      new_password: 'password must be at least 10 characters',
      confirm_password: 'passwords do not match'
    })
  })

  it('projects sidebar group and account-security flags from the current actor', async () => {
    fetchCurrentActor.mockResolvedValue(admin)
    const access = useAccessControl()
    await access.refreshActor()
    expect(access.visibleAccessManagement.value.children.map((item) => item.label)).toEqual(['用户管理'])
    expect(access.isBootstrap.value).toBe(false)
    expect(access.canChangePassword.value).toBe(true)

    fetchCurrentActor.mockResolvedValue(bootstrap)
    await access.refreshActor()
    expect(access.visibleAccessManagement.value.children.map((item) => item.id)).toEqual(['users', 'resource-groups'])
    expect(access.isBootstrap.value).toBe(true)
    expect(access.canChangePassword.value).toBe(false)

    fetchCurrentActor.mockResolvedValue(reader)
    await access.refreshActor()
    expect(access.visibleAccessManagement.value.children.map((item) => item.id)).toEqual(['resource-groups'])
    expect(access.canChangePassword.value).toBe(true)
    expect(access.can('access.manage')).toBe(false)
  })
})

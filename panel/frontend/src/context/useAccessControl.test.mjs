import { afterEach, describe, expect, it, vi } from 'vitest'
import { fetchCurrentActor } from '../api/access'
import { clearCredentials, setSessionToken } from '../api/authState'
import { filterPluginDetailForActor, pickDefaultResourceGroupID, resourceGroupDisplayName, useAccessControl, visibleResourceGroupsForActor } from './useAccessControl'

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
  })
})

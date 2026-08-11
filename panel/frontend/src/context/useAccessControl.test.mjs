import { afterEach, describe, expect, it, vi } from 'vitest'
import { fetchCurrentActor } from '../api/access'
import { clearCredentials, setSessionToken } from '../api/authState'
import { filterPluginDetailForActor, useAccessControl } from './useAccessControl'

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

  it('hides plugins with no visible instances and preserves wildcard administration', () => {
    expect(filterPluginDetailForActor(detail, { permissions: ['resource.read'], visible_resource_groups: [] })).toBeNull()
    expect(filterPluginDetailForActor(detail, { permissions: ['*'], visible_resource_groups: [] })).toBe(detail)
  })
})

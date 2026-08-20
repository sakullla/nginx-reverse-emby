import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import AccessOverview from './AccessOverview.vue'

const mocks = vi.hoisted(() => ({
  fetchResourceGroups: vi.fn(),
  fetchUsers: vi.fn(),
  fetchRoles: vi.fn(),
  fetchQuotaOverview: vi.fn(),
  fetchSecrets: vi.fn(),
  fetchAuditEvents: vi.fn(),
  refreshActor: vi.fn()
}))

vi.mock('vue-router', () => ({
  useRouter: () => ({ replace: vi.fn() })
}))

vi.mock('../../api/access', () => ({
  fetchResourceGroups: mocks.fetchResourceGroups,
  fetchUsers: mocks.fetchUsers,
  fetchRoles: mocks.fetchRoles,
  fetchQuotaOverview: mocks.fetchQuotaOverview,
  fetchSecrets: mocks.fetchSecrets,
  fetchAuditEvents: mocks.fetchAuditEvents
}))

vi.mock('../../context/useAccessControl', async (original) => {
  const actual = await original()
  return {
    ...actual,
    useAccessControl: () => ({
      can: (permission) => permission === 'resource.read',
      refreshActor: mocks.refreshActor,
      visibleNavigation: {
        value: [
          { id: 'resource-groups', label: '资源组', permission: 'resource.read', path: '/access/resource-groups' }
        ]
      }
    })
  }
})

describe('AccessOverview resource group entry', () => {
  beforeEach(() => {
    mocks.fetchResourceGroups.mockReset().mockResolvedValue([{ id: 'default', name: 'default' }])
    mocks.fetchUsers.mockReset().mockResolvedValue([])
    mocks.fetchRoles.mockReset().mockResolvedValue([])
    mocks.fetchQuotaOverview.mockReset().mockResolvedValue({ quota_usage: [] })
    mocks.fetchSecrets.mockReset().mockResolvedValue([])
    mocks.fetchAuditEvents.mockReset().mockResolvedValue([])
    mocks.refreshActor.mockReset().mockResolvedValue({})
  })

  it('links the resource group card to the dedicated route', async () => {
    const wrapper = mount(AccessOverview, {
      global: { stubs: { RouterLink: { props: ['to'], template: '<a :href="to"><slot /></a>' } } }
    })
    await flushPromises()
    const link = wrapper.get('a.access-card-link')
    expect(link.text()).toContain('资源组')
    expect(link.attributes('href')).toBe('/access/resource-groups')
  })
})

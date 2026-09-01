import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import router from '../../router'
import ResourceGroupsPage from './ResourceGroupsPage.vue'

const mocks = vi.hoisted(() => ({
  fetchResourceGroups: vi.fn(),
  createResourceGroup: vi.fn(),
  replace: vi.fn()
}))

vi.mock('../../api/access', () => ({
  fetchResourceGroups: mocks.fetchResourceGroups,
  createResourceGroup: mocks.createResourceGroup
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

describe('ResourceGroupsPage', () => {
  beforeEach(() => {
    mocks.fetchResourceGroups.mockReset()
    mocks.createResourceGroup.mockReset()
    mocks.replace.mockReset()
  })

  it('does not provide resource-group management and leaves /access/resource-groups', async () => {
    const wrapper = mount(ResourceGroupsPage)
    await flushPromises()

    expect(mocks.replace).toHaveBeenCalledWith({ name: 'dashboard' })
    expect(wrapper.get('[data-test="access-retired"]').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('正在读取资源组')
    expect(wrapper.find('[data-test="groups-grid"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="open-create"]').exists()).toBe(false)
    expect(mocks.fetchResourceGroups).not.toHaveBeenCalled()
    expect(mocks.createResourceGroup).not.toHaveBeenCalled()
    expect(isUsableManagementPage('/access/resource-groups')).toBe(false)
    expect(router.getRoutes().some((route) => route.name === 'access-resource-groups')).toBe(false)
  })
})

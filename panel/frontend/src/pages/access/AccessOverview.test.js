import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import router from '../../router'
import AccessOverview from './AccessOverview.vue'

const mocks = vi.hoisted(() => ({
  replace: vi.fn()
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

describe('AccessOverview resource group entry', () => {
  beforeEach(() => {
    mocks.replace.mockReset()
  })

  it('does not keep a resource-group management entry', async () => {
    const wrapper = mount(AccessOverview, {
      global: { stubs: { RouterLink: { props: ['to'], template: '<a :href="to"><slot /></a>' } } }
    })
    await flushPromises()
    const hrefs = wrapper.findAll('a').map((link) => link.attributes('href'))
    expect(hrefs).not.toContain('/access/resource-groups')
    expect(wrapper.text()).not.toContain('资源组管理')
    expect(mocks.replace).toHaveBeenCalledWith({ name: 'dashboard' })
    expect(isUsableManagementPage('/access')).toBe(false)
    expect(isUsableManagementPage('/access/resource-groups')).toBe(false)
  })
})

import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import QuotaUsage from '../../components/access/QuotaUsage.vue'
import SecretWriteOnlyField from '../../components/access/SecretWriteOnlyField.vue'
import { accessNavigation } from '../../context/useAccessControl'
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

const blockedAccessPages = [
  '/access',
  '/access/users',
  '/access/resource-groups',
  '/access/roles',
  '/access/permissions',
  '/access/quotas',
  '/access/quota-policies',
  '/access/secrets',
  '/access/credentials',
  '/access/audit',
  '/access/audit-events'
]

const retiredAccessNames = ['access', 'access-users', 'access-resource-groups']

function isUsableManagementPage(path) {
  const record = router.getRoutes().find((route) => route.path === path)
  if (!record) return false
  if (record.redirect) return false
  return Boolean(record.components?.default)
}

function mountOverview() {
  return mount(AccessOverview, {
    global: { stubs: { RouterLink: { props: ['to'], template: '<a :href="to"><slot /></a>' } } }
  })
}

describe('access security foundation', () => {
  it('renders quota current, limit and recovery context together', () => {
    const wrapper = mount(QuotaUsage, {
      props: { current: 8, limit: 10, recoveryCondition: '下个结算周期' }
    })

    expect(wrapper.text()).toContain('8')
    expect(wrapper.text()).toContain('10')
    expect(wrapper.text()).toContain('下个结算周期')
    expect(wrapper.get('progress').attributes('value')).toBe('80')
  })

  it('keeps secret entry write-only and locks access management pages out', () => {
    const wrapper = mount(SecretWriteOnlyField)

    expect(wrapper.get('input').attributes('type')).toBe('password')
    expect(wrapper.get('input').attributes('placeholder')).toContain('不可取回')
    expect(accessNavigation.every((item) => item.permission && !item.path)).toBe(true)
    expect(retiredAccessNames.every((name) => !router.getRoutes().some((route) => route.name === name))).toBe(true)
    expect(blockedAccessPages.every((path) => !isUsableManagementPage(path))).toBe(true)
    expect(router.getRoutes().find((route) => route.path === '/resource-groups')?.redirect).toEqual({ name: 'plugins' })
    expect(router.resolve('/settings').name).toBe('settings')
  })
})

describe('access overview compatibility', () => {
  it('does not render user or resource-group management', async () => {
    mocks.replace.mockReset()
    const wrapper = mountOverview()
    await flushPromises()

    const hrefs = wrapper.findAll('a').map((link) => link.attributes('href'))
    expect(hrefs).not.toContain('/access/users')
    expect(hrefs).not.toContain('/access/resource-groups')
    expect(wrapper.text()).not.toContain('创建首个管理员')
    expect(wrapper.text()).not.toContain('用户管理')
    expect(wrapper.text()).not.toContain('资源组管理')
    expect(wrapper.find('.access-bootstrap-link').exists()).toBe(false)
    expect(wrapper.find('form[aria-label="修改密码"]').exists()).toBe(false)
    expect(wrapper.find('#access-current-password').exists()).toBe(false)
    expect(mocks.replace).toHaveBeenCalledWith({ name: 'dashboard' })
  })
})

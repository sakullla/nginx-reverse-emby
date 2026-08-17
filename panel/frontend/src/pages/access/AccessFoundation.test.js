import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import QuotaUsage from '../../components/access/QuotaUsage.vue'
import SecretWriteOnlyField from '../../components/access/SecretWriteOnlyField.vue'
import { accessNavigation } from '../../context/useAccessControl'
import router from '../../router'
import AccessOverview from './AccessOverview.vue'

const mocks = vi.hoisted(() => ({
  fetchResourceGroups: vi.fn(),
  fetchUsers: vi.fn(),
  fetchRoles: vi.fn(),
  fetchQuotaOverview: vi.fn(),
  fetchSecrets: vi.fn(),
  fetchAuditEvents: vi.fn(),
  changePassword: vi.fn(),
  refreshActor: vi.fn(),
  can: vi.fn(),
  actor: { value: null },
  visibleNavigation: { value: [] }
}))

vi.mock('../../api/access', () => ({
  fetchResourceGroups: mocks.fetchResourceGroups,
  fetchUsers: mocks.fetchUsers,
  fetchRoles: mocks.fetchRoles,
  fetchQuotaOverview: mocks.fetchQuotaOverview,
  fetchSecrets: mocks.fetchSecrets,
  fetchAuditEvents: mocks.fetchAuditEvents,
  changePassword: mocks.changePassword
}))

vi.mock('../../context/useAccessControl', async (original) => {
  const actual = await original()
  return {
    ...actual,
    useAccessControl: () => ({
      actor: mocks.actor,
      can: mocks.can,
      refreshActor: mocks.refreshActor,
      visibleNavigation: mocks.visibleNavigation
    })
  }
})

const blockedAccessPages = [
  '/access/roles',
  '/access/permissions',
  '/access/quotas',
  '/access/quota-policies',
  '/access/secrets',
  '/access/credentials',
  '/access/audit',
  '/access/audit-events'
]

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

  it('keeps secret entry write-only and locks role quota credential and audit pages out', () => {
    const wrapper = mount(SecretWriteOnlyField)
    const usersNav = accessNavigation.find((item) => item.id === 'users')

    expect(wrapper.get('input').attributes('type')).toBe('password')
    expect(wrapper.get('input').attributes('placeholder')).toContain('不可取回')
    expect(accessNavigation.filter((item) => !['users', 'resource-groups'].includes(item.id)).every((item) => item.permission && !item.path)).toBe(true)
    expect(usersNav).toMatchObject({ permission: 'access.manage' })
    if (usersNav.path) expect(usersNav.path).toBe('/access/users')
    expect(accessNavigation.find((item) => item.id === 'resource-groups')).toMatchObject({
      permission: 'resource.read',
      path: '/access/resource-groups'
    })
    expect(router.resolve('/access').name).toBe('access')
    expect(router.resolve('/access/resource-groups').name).toBe('access-resource-groups')
    expect(router.getRoutes().some((route) => route.path === '/access/users')).toBe(true)
    expect(router.resolve('/access/users').name).toBe('access-users')
    expect(router.getRoutes().some((route) => blockedAccessPages.includes(route.path))).toBe(false)
  })
})

describe('access overview compatibility', () => {
  beforeEach(() => {
    mocks.fetchUsers.mockReset().mockResolvedValue([])
    mocks.fetchRoles.mockReset().mockResolvedValue([])
    mocks.fetchResourceGroups.mockReset().mockResolvedValue([{ id: 'default', name: 'default' }])
    mocks.fetchQuotaOverview.mockReset().mockResolvedValue({ quota_usage: [] })
    mocks.fetchSecrets.mockReset().mockResolvedValue([])
    mocks.fetchAuditEvents.mockReset().mockResolvedValue([])
    mocks.changePassword.mockReset().mockResolvedValue({ ok: true })
    mocks.refreshActor.mockReset().mockResolvedValue({})
    mocks.can.mockReset().mockImplementation((permission) => permission === 'access.manage' || permission === 'resource.read')
    mocks.actor.value = {
      id: 'usr-1',
      username: 'alice',
      bootstrap: false,
      permissions: ['access.manage', 'resource.read']
    }
    mocks.visibleNavigation.value = [
      { id: 'users', label: '用户', permission: 'access.manage' },
      { id: 'roles', label: '角色', permission: 'access.manage' },
      { id: 'resource-groups', label: '资源组', permission: 'resource.read', path: '/access/resource-groups' },
      { id: 'quotas', label: '配额', permission: 'resource.read' },
      { id: 'secrets', label: '凭据', permission: 'secret.metadata.read' },
      { id: 'audit', label: '审计', permission: 'audit.read' }
    ]
  })

  it('links authorized user and resource-group cards without adding extra access pages', async () => {
    const wrapper = mountOverview()
    await flushPromises()

    const hrefs = wrapper.findAll('a').map((link) => link.attributes('href'))
    expect(hrefs).toContain('/access/users')
    expect(hrefs).toContain('/access/resource-groups')
    expect(hrefs.some((href) => blockedAccessPages.includes(href))).toBe(false)
    expect(wrapper.text()).toContain('创建首个管理员')
    expect(wrapper.get('.access-bootstrap-link').attributes('href')).toBe('/access/users')
  })

  it('lets an account session complete a password change and hides it for bootstrap actors', async () => {
    const wrapper = mountOverview()
    await flushPromises()

    await wrapper.get('#access-current-password').setValue('correct-horse-battery')
    await wrapper.get('#access-new-password').setValue('new-correct-horse')
    await wrapper.get('#access-confirm-password').setValue('mismatch-password')
    await wrapper.get('form[aria-label="修改密码"]').trigger('submit')
    expect(mocks.changePassword).not.toHaveBeenCalled()

    await wrapper.get('#access-confirm-password').setValue('new-correct-horse')
    await wrapper.get('form[aria-label="修改密码"]').trigger('submit')
    await flushPromises()
    expect(mocks.changePassword).toHaveBeenCalledWith({
      current_password: 'correct-horse-battery',
      new_password: 'new-correct-horse'
    })
    expect(wrapper.text()).toContain('请使用新密码重新登录')

    mocks.actor.value = { id: 'bootstrap', bootstrap: true, permissions: ['*'] }
    const bootstrap = mountOverview()
    await flushPromises()
    expect(bootstrap.find('#access-current-password').exists()).toBe(false)
  })
})

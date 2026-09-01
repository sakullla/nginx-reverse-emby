import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { ref } from 'vue'
import Sidebar from './Sidebar.vue'

const pluginRoutes = ref([])

vi.mock('vue-router', () => ({
  useRoute: () => ({ name: 'dashboard', path: '/' }),
  RouterLink: { props: ['to'], template: '<a :href="typeof to === \'string\' ? to : to.path"><slot /></a>' }
}))

vi.mock('../../hooks/usePluginUIRoutes', () => ({
  usePluginUIRoutes: () => ({ routes: pluginRoutes }),
  pluginChildrenForGroup: (routes, group) => (routes || [])
    .filter((route) => route.group === group)
    .map((route) => ({ label: route.label, href: route.href, id: route.id }))
}))

vi.mock('../../context/useAccessControl', () => ({
  useAccessControl: () => ({
    refreshActor: async () => undefined,
    visibleAccessManagement: {
      value: {
        id: 'users-and-resources',
        label: '用户与资源管理',
        children: [
          { id: 'users', label: '用户管理', path: '/access/users', routeName: 'access-users' },
          { id: 'resource-groups', label: '资源组管理', path: '/access/resource-groups', routeName: 'access-resource-groups' }
        ]
      }
    }
  }),
  isAccessManagementChildActive: () => false
}))

const retiredAccessLabels = ['用户与资源管理', '用户管理', '资源组管理']
const retiredAccessHrefs = ['/access', '/access/users', '/access/resource-groups']

describe('Sidebar plugin UI routes', () => {
  it('does not hardcode a Cloudflare mapping page', () => {
    pluginRoutes.value = []
    const sidebar = mount(Sidebar)
    expect(sidebar.findAll('a').some((item) => (item.attributes('href') || '').includes('cloudflare-dns'))).toBe(false)
    expect(sidebar.text()).not.toContain('域名 Token')
    sidebar.unmount()
  })

  it('keeps marketplace and installed plugins in the plugin menu', () => {
    pluginRoutes.value = []
    const sidebar = mount(Sidebar)
    const hrefs = sidebar.findAll('a').map((item) => item.attributes('href'))
    expect(hrefs).toContain('/plugins/marketplace')
    expect(hrefs).toContain('/plugins')
    expect(hrefs).not.toContain('/resource-groups')
    expect(hrefs).not.toContain('/plugins/repositories')
    expect(hrefs).toContain('/settings')
    expect(sidebar.text()).toContain('插件市场')
    expect(sidebar.text()).toContain('已安装插件')
    expect(sidebar.text()).not.toContain('插件资源组')
    expect(sidebar.text()).not.toContain('插件仓库')
    expect(sidebar.text()).toContain('设置')
    sidebar.unmount()
  })

  it('does not show retired access management even when an actor would have been authorized', () => {
    pluginRoutes.value = []
    const sidebar = mount(Sidebar)
    const hrefs = sidebar.findAll('a').map((item) => item.attributes('href'))
    for (const label of retiredAccessLabels) {
      expect(sidebar.text()).not.toContain(label)
    }
    for (const href of retiredAccessHrefs) {
      expect(hrefs).not.toContain(href)
    }
    expect(hrefs).not.toContain('/resource-groups')
    expect(hrefs).toContain('/settings')
    sidebar.unmount()
  })

  it('renders a declared plugin UI route instead of a panel-configured page', async () => {
    pluginRoutes.value = [{
      id: 'cloudflare-dns',
      label: '域名 Token',
      group: '基础设施',
      href: '/panel-api/plugins/cloudflare-dns/'
    }]
    const sidebar = mount(Sidebar)
    await flushPromises()
    const link = sidebar.findAll('a').find((item) => item.attributes('href') === '/panel-api/plugins/cloudflare-dns/')
    expect(link).toBeTruthy()
    expect(link.text()).toContain('域名 Token')
    expect(sidebar.find('[data-testid="mapping-create"]').exists()).toBe(false)
    sidebar.unmount()
  })
})

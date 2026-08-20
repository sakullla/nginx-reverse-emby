import { describe, expect, it, vi, beforeEach } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { ref } from 'vue'
import ResourceGroupsPage from './ResourceGroupsPage.vue'

const groups = ref([])
const loading = ref(false)
const error = ref('')

vi.mock('../hooks/usePluginResourceGroups', () => ({
  usePluginResourceGroups: () => ({ groups, loading, error })
}))

vi.mock('../components/base/BaseModal.vue', () => ({
  default: {
    name: 'BaseModal',
    props: ['modelValue', 'title', 'subtitle', 'showFooter'],
    emits: ['update:modelValue'],
    template: '<div v-if="modelValue" class="modal-stub"><h3>{{ title }}</h3><slot /><slot name="footer" /></div>'
  }
}))

describe('ResourceGroupsPage', () => {
  beforeEach(() => {
    groups.value = []
    loading.value = false
    error.value = ''
  })

  it('shows an empty catalog when no plugin has declared a group', () => {
    const page = mount(ResourceGroupsPage)
    expect(page.text()).toContain('没有已声明的资源组')
    expect(page.text()).toContain('resource.group')
    page.unmount()
  })

  it('lists a plugin-declared group and opens management detail', async () => {
    groups.value = [{
      id: 'cloudflare-dns',
      plugin_id: 'cloudflare-dns',
      ref: 'resource-group/cloudflare-dns',
      label: 'Cloudflare DNS',
      description: '按域名后缀隔离 Token 映射',
      status: 'registered',
      ui_route_id: 'cloudflare-dns',
      ui_href: '/panel-api/plugins/cloudflare-dns/'
    }]
    const page = mount(ResourceGroupsPage)
    await flushPromises()
    expect(page.text()).toContain('Cloudflare DNS')
    expect(page.text()).toContain('resource-group/cloudflare-dns')
    expect(page.text()).toContain('已注册')
    const manage = page.find('a[href="/panel-api/plugins/cloudflare-dns/"]')
    expect(manage.exists()).toBe(true)
    await page.find('.resource-group-card').trigger('click')
    expect(page.text()).toContain('资源组管理')
    page.unmount()
  })

  it('filters declared groups from the search box', async () => {
    groups.value = [
      {
        id: 'cloudflare-dns',
        plugin_id: 'cloudflare-dns',
        ref: 'resource-group/cloudflare-dns',
        label: 'Cloudflare DNS',
        description: '按域名后缀隔离 Token 映射',
        status: 'registered',
        ui_href: '/panel-api/plugins/cloudflare-dns/'
      },
      {
        id: 'other',
        plugin_id: 'other',
        ref: 'resource-group/other',
        label: '其他组',
        description: '无关',
        status: 'registered'
      }
    ]
    const page = mount(ResourceGroupsPage)
    await page.find('input[type="search"]').setValue('cloudflare')
    expect(page.text()).toContain('Cloudflare DNS')
    expect(page.text()).not.toContain('其他组')
    page.unmount()
  })
})

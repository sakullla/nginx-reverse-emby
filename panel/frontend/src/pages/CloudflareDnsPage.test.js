import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import CloudflareDnsPage from './CloudflareDnsPage.vue'
import Sidebar from '../components/layout/Sidebar.vue'

const api = vi.hoisted(() => ({
  fetchCloudflareDnsMappings: vi.fn(),
  fetchCloudflareDnsMapping: vi.fn(),
  createCloudflareDnsMapping: vi.fn(),
  renameCloudflareDnsMapping: vi.fn(),
  rotateCloudflareDnsMapping: vi.fn(),
  deleteCloudflareDnsMapping: vi.fn()
}))

vi.mock('../api', () => api)

vi.mock('vue-router', () => ({
  useRoute: () => ({ name: 'cloudflare-dns', path: '/cloudflare-dns' }),
  RouterLink: { props: ['to'], template: '<a :href="typeof to === \'string\' ? to : to.path"><slot /></a>' }
}))

const leakedToken = 'cf-secret-must-never-render'
let wrapper

function httpError(status, message) {
  const error = new Error(message)
  error.status = status
  error.response = { status, data: { message } }
  return error
}

function mappingPayload() {
  return {
    mappings: [
      { suffix: 'example.com', configured: true, updated_at: 1700000000, token: leakedToken }
    ],
    access: { can_read: true, can_write: true, can_rotate: true }
  }
}

async function mountPage() {
  wrapper = mount(CloudflareDnsPage, {
    global: {
      stubs: {
        DeleteConfirmDialog: {
          props: ['show', 'title', 'name'],
          emits: ['confirm', 'cancel'],
          template: `
            <div v-if="show" data-testid="confirm-dialog">
              <p>{{ title }} {{ name }}</p>
              <button type="button" data-testid="confirm-ok" @click="$emit('confirm')">ok</button>
              <button type="button" data-testid="confirm-cancel" @click="$emit('cancel')">cancel</button>
            </div>
          `
        }
      }
    }
  })
  await flushPromises()
  return wrapper
}

beforeEach(() => {
  api.fetchCloudflareDnsMappings.mockReset()
  api.fetchCloudflareDnsMapping.mockReset()
  api.createCloudflareDnsMapping.mockReset()
  api.renameCloudflareDnsMapping.mockReset()
  api.rotateCloudflareDnsMapping.mockReset()
  api.deleteCloudflareDnsMapping.mockReset()
  api.fetchCloudflareDnsMappings.mockResolvedValue(mappingPayload())
  api.fetchCloudflareDnsMapping.mockResolvedValue({
    suffix: 'example.com',
    configured: true,
    updated_at: 1700000000,
    token: leakedToken
  })
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = null
})

describe('CloudflareDnsPage', () => {
  it('lists saved mappings without showing or prefilling token material', async () => {
    await mountPage()

    expect(wrapper.get('[data-testid="mapping-list"]').text()).toContain('example.com')
    expect(wrapper.get('[data-testid="mapping-list"]').text()).toContain('是')
    expect(wrapper.text()).not.toContain(leakedToken)
    expect(wrapper.get('[data-testid="mapping-create-token"]').element.value).toBe('')
    expect(wrapper.get('[data-testid="mapping-rotate-token"]').element.value).toBe('')
    expect(wrapper.find('[name="zone_token"]').exists()).toBe(false)
    expect(wrapper.find('[name="zone-token"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="dns-record-form"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="dns-record-list"]').exists()).toBe(false)
    expect(wrapper.find('input[name="type"]').exists()).toBe(false)
    expect(wrapper.find('input[name="content"]').exists()).toBe(false)
  })

  it('loads detail without prefilling a token after reopen', async () => {
    await mountPage()
    await wrapper.get('[data-testid="mapping-suffix"]').trigger('click')
    await flushPromises()

    expect(api.fetchCloudflareDnsMapping).toHaveBeenCalledWith('example.com')
    const detail = wrapper.get('[data-testid="mapping-detail"]')
    expect(detail.text()).toContain('example.com')
    expect(detail.text()).toContain('是')
    expect(detail.text()).not.toContain(leakedToken)
    expect(wrapper.get('[data-testid="mapping-create-token"]').element.value).toBe('')
    expect(wrapper.get('[data-testid="mapping-rotate-token"]').element.value).toBe('')
  })

  it('creates, renames, rotates, and deletes through the plugin API', async () => {
    api.createCloudflareDnsMapping.mockResolvedValue({ suffix: 'api.example.com', configured: true, updated_at: 1 })
    api.renameCloudflareDnsMapping.mockResolvedValue({ suffix: 'www.example.com', configured: true, updated_at: 2 })
    api.rotateCloudflareDnsMapping.mockResolvedValue({ suffix: 'example.com', configured: true, updated_at: 3 })
    api.deleteCloudflareDnsMapping.mockResolvedValue({ suffix: 'example.com' })
    await mountPage()

    await wrapper.get('[data-testid="mapping-create-suffix"]').setValue('api.example.com')
    await wrapper.get('[data-testid="mapping-create-token"]').setValue('new-token')
    await wrapper.get('[data-testid="mapping-create-form"]').trigger('submit')
    await flushPromises()
    expect(api.createCloudflareDnsMapping).toHaveBeenCalledWith({ suffix: 'api.example.com', token: 'new-token' })
    expect(wrapper.get('[data-testid="mapping-create-token"]').element.value).toBe('')

    await wrapper.get('[data-testid="mapping-rename-suffix"]').setValue('www.example.com')
    await wrapper.get('[data-testid="mapping-rename-form"]').trigger('submit')
    await flushPromises()
    await wrapper.get('[data-testid="confirm-ok"]').trigger('click')
    await flushPromises()
    expect(api.renameCloudflareDnsMapping).toHaveBeenCalledWith('example.com', 'www.example.com')

    await wrapper.get('[data-testid="mapping-rotate-token"]').setValue('rotated-token')
    await wrapper.get('[data-testid="mapping-rotate-form"]').trigger('submit')
    await flushPromises()
    await wrapper.get('[data-testid="confirm-ok"]').trigger('click')
    await flushPromises()
    expect(api.rotateCloudflareDnsMapping).toHaveBeenCalledWith('example.com', 'rotated-token')

    await wrapper.get('[data-testid="mapping-delete"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="confirm-ok"]').trigger('click')
    await flushPromises()
    expect(api.deleteCloudflareDnsMapping).toHaveBeenCalledWith('example.com')
  })

  it('does not change mappings when a destructive confirm is cancelled', async () => {
    await mountPage()
    await wrapper.get('[data-testid="mapping-delete"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="confirm-cancel"]').trigger('click')
    await flushPromises()

    expect(api.deleteCloudflareDnsMapping).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="mapping-status"]').text()).toContain('已取消')
  })

  it('shows an explicit denial instead of a blank writable page when unauthorized', async () => {
    api.fetchCloudflareDnsMappings.mockRejectedValue(httpError(401, 'Unauthorized: Invalid or missing X-Panel-Token'))
    await mountPage()

    expect(wrapper.get('[data-testid="mapping-denied"]').text()).toContain('明确拒绝')
    expect(wrapper.find('[data-testid="mapping-create"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="mapping-list"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain(leakedToken)
  })

  it('exposes a sidebar entry for the dedicated mapping page', () => {
    const sidebar = mount(Sidebar)
    const link = sidebar.findAll('a').find((item) => item.attributes('href') === '/cloudflare-dns')
    expect(link).toBeTruthy()
    expect(link.text()).toContain('域名 Token')
    sidebar.unmount()
  })
})

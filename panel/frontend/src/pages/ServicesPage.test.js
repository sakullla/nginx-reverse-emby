import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { reactive, ref } from 'vue'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import ServicesPage from './ServicesPage.vue'

const agentsData = ref([
  { id: 'edge', name: '入口节点' },
  { id: 'origin', name: '源站节点' },
])
const route = reactive({ query: {}, path: '/services', name: 'services' })
const stored = []

const api = vi.hoisted(() => ({
  fetchAccessServices: vi.fn(),
  createAccessService: vi.fn(),
  updateAccessService: vi.fn(),
  correctAccessService: vi.fn(),
  retryAccessService: vi.fn(),
  deleteAccessService: vi.fn(),
  confirmAccessServiceClient: vi.fn(),
  retireAccessService: vi.fn(),
  abandonAccessService: vi.fn(),
}))

vi.mock('vue-router', () => ({
  useRoute: () => route,
  useRouter: () => ({
    replace: (target) => {
      route.query = { ...(target?.query || {}) }
    },
  }),
}))

vi.mock('../hooks/useAgents', () => ({
  useAgents: () => ({ data: agentsData, isLoading: ref(false) }),
}))

vi.mock('../api', () => api)

function view(overrides = {}) {
  return {
    id: 'svc-1',
    name: '家庭媒体',
    entry_agent_id: 'edge',
    service_agent_id: 'origin',
    backend_url: 'http://10.1.0.8:8096',
    entry_url: 'https://media.example.com',
    summary: '待确认',
    path: { mode: 'pending', summary: '待确认', pending: true },
    certificate: { state: 'none', https_ready: false },
    probes: [],
    rules: [],
    traffic: { state: 'unavailable' },
    apply: { pending: true },
    https_ready: false,
    advanced: false,
    ...overrides,
  }
}

function mountPage() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return mount(ServicesPage, {
    global: { plugins: [[VueQueryPlugin, { queryClient }]] },
  })
}

async function fillDraft(wrapper, overrides = {}) {
  const values = {
    name: '家庭媒体',
    backend: 'http://10.1.0.8:8096',
    serviceAgent: 'origin',
    entryAgent: 'edge',
    entry: 'https://media.example.com',
    ...overrides,
  }
  await wrapper.get('#service-name').setValue(values.name)
  await wrapper.get('#service-backend-url').setValue(values.backend)
  await wrapper.get('#service-agent').setValue(values.serviceAgent)
  await wrapper.get('#entry-agent').setValue(values.entryAgent)
  await wrapper.get('#service-entry-url').setValue(values.entry)
  return values
}

beforeEach(() => {
  stored.splice(0, stored.length)
  route.query = {}
  api.fetchAccessServices.mockReset()
  api.fetchAccessServices.mockImplementation(async () => stored.map((item) => ({ ...item })))
  api.createAccessService.mockReset()
  api.updateAccessService.mockReset()
  api.correctAccessService.mockReset()
  api.deleteAccessService.mockReset()
  api.confirmAccessServiceClient.mockReset()
  api.retryAccessService.mockReset()
})

describe('service page', () => {
  it('confirms on the same page and does not enable an entry before submit', async () => {
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('.page-header__right button').trigger('click')
    expect(wrapper.get('[data-testid="service-wizard"]').text()).toContain('Emby、Jellyfin 和普通 Web')
    expect(wrapper.find('input[type="password"]').exists()).toBe(false)
    await fillDraft(wrapper, { backend: 'http://127.0.0.1:8096' })
    await wrapper.get('#service-backend-url').setValue('http://10.2.0.9:8096')
    await wrapper.get('form').trigger('submit')
    expect(wrapper.get('[data-testid="service-confirm"]').text()).toContain('http://10.2.0.9:8096')
    expect(wrapper.text()).toContain('提交前不会创建或启用入口')
    expect(api.createAccessService).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="service-back"]').trigger('click')
    expect(wrapper.get('#service-backend-url').element.value).toBe('http://10.2.0.9:8096')
    expect(wrapper.get('#service-entry-url').element.value).toBe('https://media.example.com')
    expect(api.createAccessService).not.toHaveBeenCalled()
  })

  it('keeps the other fields when the URL, node, or entry conflicts', async () => {
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('.page-header__right button').trigger('click')
    await fillDraft(wrapper, { backend: 'notaurl' })
    await wrapper.get('form').trigger('submit')
    expect(wrapper.text()).toContain('后端 URL 必须是带主机名的 http 或 https 地址')
    expect(wrapper.get('#service-entry-url').element.value).toBe('https://media.example.com')
    expect(wrapper.get('#service-agent').element.value).toBe('origin')
    expect(api.createAccessService).not.toHaveBeenCalled()

    stored.push(view({ id: 'svc-missing', service_agent_id: 'gone', summary: '失败' }))
    wrapper.unmount()
    const edit = mountPage()
    await flushPromises()
    await edit.get('[data-testid="service-card"]').findAll('button').find((button) => button.text() === '修改').trigger('click')
    await edit.get('form').trigger('submit')
    expect(edit.text()).toContain('服务侧节点不存在')
    expect(edit.get('#service-backend-url').element.value).toBe('http://10.1.0.8:8096')
    expect(edit.get('#service-entry-url').element.value).toBe('https://media.example.com')

    edit.unmount()
    stored.splice(0, stored.length)
    api.createAccessService.mockRejectedValue(Object.assign(new Error('conflict'), {
      response: { status: 409, data: { message: '入口冲突，原规则未改' } },
    }))
    const conflict = mountPage()
    await flushPromises()
    await conflict.get('.page-header__right button').trigger('click')
    await fillDraft(conflict)
    await conflict.get('form').trigger('submit')
    await conflict.get('[data-testid="service-confirm"] button.btn-primary').trigger('click')
    await flushPromises()
    expect(conflict.text()).toContain('入口冲突，原规则未改')
    await conflict.get('[data-testid="service-back"]').trigger('click')
    expect(conflict.get('#service-backend-url').element.value).toBe('http://10.1.0.8:8096')
    expect(conflict.get('#service-entry-url').element.value).toBe('https://media.example.com')
    expect(conflict.get('#entry-agent').element.value).toBe('edge')
  })

  it('asks for relay input without clearing the rest of the form', async () => {
    api.createAccessService.mockRejectedValue(Object.assign(new Error('relay'), {
      response: { data: { message: 'relay_listen_port is required to create the single service-side listener' } },
    }))
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('.page-header__right button').trigger('click')
    await fillDraft(wrapper)
    await wrapper.get('form').trigger('submit')
    await wrapper.get('[data-testid="service-confirm"] button.btn-primary').trigger('click')
    await flushPromises()
    expect(wrapper.get('#relay-port').exists()).toBe(true)
    expect(wrapper.get('#service-backend-url').element.value).toBe('http://10.1.0.8:8096')
    expect(wrapper.get('#service-entry-url').element.value).toBe('https://media.example.com')
    expect(wrapper.get('#entry-agent').element.value).toBe('edge')
    expect(wrapper.text()).not.toContain('入口直连')
  })

  it('keeps the wizard open when a saved service still needs a relay host', async () => {
    const relayError = 'relay_public_host must be a concrete host the entry node can dial'
    api.createAccessService.mockImplementation(async (payload) => view({
      id: 'svc-relay',
      name: payload.name,
      backend_url: payload.backend_url,
      entry_url: payload.entry_url,
      entry_agent_id: payload.entry_agent_id,
      service_agent_id: payload.service_agent_id,
      summary: '待确认',
      last_error: relayError,
    }))
    api.correctAccessService.mockImplementation(async (_id, payload) => view({
      id: 'svc-relay',
      name: payload.name,
      backend_url: payload.backend_url,
      entry_url: payload.entry_url,
      summary: '待确认',
    }))
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('.page-header__right button').trigger('click')
    await fillDraft(wrapper)
    await wrapper.get('form').trigger('submit')
    await wrapper.get('[data-testid="service-confirm"] button.btn-primary').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="service-detail"]').exists()).toBe(false)
    expect(wrapper.get('#relay-host').exists()).toBe(true)
    expect(wrapper.get('#service-backend-url').element.value).toBe('http://10.1.0.8:8096')
    expect(wrapper.get('#service-entry-url').element.value).toBe('https://media.example.com')
    expect(wrapper.text()).toContain(relayError)
    expect(api.retryAccessService).not.toHaveBeenCalled()
    await wrapper.get('#relay-host').setValue('203.0.113.10')
    await wrapper.get('form').trigger('submit')
    await wrapper.get('[data-testid="service-confirm"] button.btn-primary').trigger('click')
    await flushPromises()
    expect(api.updateAccessService).not.toHaveBeenCalled()
    expect(api.correctAccessService).toHaveBeenCalledTimes(1)
    expect(api.correctAccessService.mock.calls[0][1].relay_public_host).toBe('203.0.113.10')
    expect(api.correctAccessService.mock.calls[0][1].backend_url).toBe('http://10.1.0.8:8096')
  })

  it('collects a relay host from the saved last_error when editing again', async () => {
    stored.push(view({
      summary: '待确认',
      last_error: 'relay_public_host must be a concrete host the entry node can dial',
    }))
    api.correctAccessService.mockImplementation(async () => view({ summary: '待确认' }))
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('[data-testid="service-card"]').findAll('button').find((button) => button.text() === '修改').trigger('click')
    expect(wrapper.get('#relay-host').exists()).toBe(true)
    await wrapper.get('#relay-host').setValue('203.0.113.10')
    await wrapper.get('form').trigger('submit')
    await wrapper.get('[data-testid="service-confirm"] button.btn-primary').trigger('click')
    await flushPromises()
    expect(api.retryAccessService).not.toHaveBeenCalled()
    expect(api.updateAccessService).not.toHaveBeenCalled()
    expect(api.correctAccessService.mock.calls[0][1].relay_public_host).toBe('203.0.113.10')
  })

  it('shows progress after submit and again after leaving the page', async () => {
    api.createAccessService.mockImplementation(async (payload) => {
      const service = view({
        id: 'svc-new',
        name: payload.name,
        backend_url: payload.backend_url,
        entry_url: payload.entry_url,
        entry_agent_id: payload.entry_agent_id,
        service_agent_id: payload.service_agent_id,
        summary: '待确认',
      })
      stored.push(service)
      return service
    })
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('.page-header__right button').trigger('click')
    await fillDraft(wrapper)
    await wrapper.get('form').trigger('submit')
    await wrapper.get('[data-testid="service-confirm"] button.btn-primary').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="detail-summary"]').text()).toContain('待确认')
    wrapper.unmount()
    route.query = {}
    const again = mountPage()
    await flushPromises()
    expect(again.get('[data-testid="service-summary"]').text()).toContain('待确认')
  })

  it('reuses one idempotency key until the request body changes', async () => {
    const keys = []
    api.createAccessService.mockImplementation(async (_payload, key) => {
      keys.push(key)
      throw Object.assign(new Error('offline'), { response: { data: { message: '暂时连不上' } } })
    })
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('.page-header__right button').trigger('click')
    await fillDraft(wrapper)
    await wrapper.get('form').trigger('submit')
    const submit = () => wrapper.get('[data-testid="service-confirm"] button.btn-primary').trigger('click')
    await submit()
    await flushPromises()
    await submit()
    await flushPromises()
    expect(keys).toHaveLength(2)
    expect(keys[0]).toBe(keys[1])
    await wrapper.get('[data-testid="service-back"]').trigger('click')
    await wrapper.get('#service-entry-url').setValue('https://other.example.com')
    await wrapper.get('form').trigger('submit')
    await submit()
    await flushPromises()
    expect(keys[2]).not.toBe(keys[0])
  })

  it('renders control-plane status, certificate, and missing traffic on the card', async () => {
    stored.push(view({
      id: 'svc-failed',
      name: '失败站点',
      summary: '失败',
      last_error: '证书签发失败',
      certificate: { state: 'error', last_error: 'dns failed', https_ready: false },
      traffic: { state: 'http_rule', label: '该规则在当前周期的用量', rx_bytes: null, tx_bytes: null },
      probes: [{ agent_id: 'edge', purpose: 'backend', status: 'http_status', http_status_code: 401 }],
      path: { mode: 'direct', summary: '入口直连，由 origin 拨号', pending: false },
      switch_phase: '等待新入口生效',
      apply: { pending: true, old_entry_may_receive: true },
    }))
    stored.push(view({
      id: 'svc-http',
      name: '普通站点',
      summary: '已就绪',
      entry_url: 'http://plain.example.com',
      certificate: { state: 'none' },
      traffic: { state: 'unavailable' },
      path: { mode: 'direct', summary: '入口直连，由 edge 拨号', pending: false },
      apply: { pending: false },
    }))
    stored.push(view({
      id: 'svc-cert',
      name: '证书站点',
      summary: '已就绪',
      certificate: { state: 'ready', not_after: '2030-01-02T00:00:00Z', https_ready: true },
      https_ready: true,
      probes: [{ status: 'http_status', http_status_code: 403, agent_id: 'edge', purpose: 'backend' }],
      path: { mode: 'relay', summary: 'edge → origin → media.example.com', pending: false },
      switch_phase: '',
      apply: { pending: false },
    }))
    const wrapper = mountPage()
    await flushPromises()
    const text = wrapper.text()
    expect(wrapper.findAll('[data-testid="service-card"]')).toHaveLength(3)
    expect(text).toContain('失败站点')
    expect(text).toContain('http_status 401')
    expect(text).toContain('http_status 403')
    expect(text).toContain('无数据')
    expect(text).not.toContain('0 B')
    expect(text).toContain('无证书')
    expect(text).toContain('失败：dns failed')
    expect(text).toContain('到期 2030-01-02T00:00:00Z')
    expect(text).toContain('等待新入口生效')
    expect(text).toContain('旧入口仍可能接收新请求')
    expect(text).toContain('入口直连，由 origin 拨号（direct）')
    expect(text).toContain('edge → origin → media.example.com（relay）')
    expect(text).not.toContain('接入完成')
  })

  it('renders a saved probe and 待验证 when the control plane marks it expired', async () => {
    stored.push(view({
      id: 'svc-stale',
      name: '待验证站点',
      summary: '待验证',
      path: { mode: 'direct', summary: '入口直连，由 edge 拨号', pending: false },
      apply: { pending: false },
      rules: [{ id: 3, agent_id: 'edge', frontend_url: 'https://media.example.com' }],
      probes: [
        {
          agent_id: 'edge',
          purpose: 'backend',
          status: 'http_status',
          http_status_code: 401,
          target: 'http://ready.example/emby',
          checked_at: '2026-10-20T01:02:03Z',
          verification: 'expired',
        },
        {
          agent_id: 'origin',
          purpose: 'listener',
          status: 'connected',
          target: '203.0.113.10:9443',
          checked_at: '2026-10-20T01:02:04Z',
        },
      ],
    }))
    const wrapper = mountPage()
    await flushPromises()
    const card = wrapper.get('[data-testid="service-card"]')
    expect(card.get('[data-testid="service-summary"]').text()).toBe('待验证')
    const staleProbe = card.findAll('[data-testid="service-probe"]')[0]
    expect(staleProbe.text()).toContain('http_status 401')
    expect(staleProbe.text()).toContain('http://ready.example/emby')
    expect(staleProbe.text()).toContain('2026-10-20T01:02:03Z')
    expect(staleProbe.text()).toContain('expired')
    const freshProbe = card.findAll('[data-testid="service-probe"]')[1]
    expect(freshProbe.text()).toContain('connected')
    expect(freshProbe.text()).toContain('203.0.113.10:9443')
    expect(freshProbe.text()).not.toContain('expired')
    expect(card.text()).not.toContain('接入完成')
    expect(card.text()).not.toContain('已就绪')
    await card.findAll('button').find((button) => button.text() === '详情').trigger('click')
    await flushPromises()
    const detail = wrapper.get('[data-testid="service-detail"]')
    const rows = detail.findAll('[data-testid="probe-row"]')
    expect(rows[0].text()).toContain('http_status 401')
    expect(rows[0].text()).toContain('expired')
    expect(rows[0].text()).toContain('http://ready.example/emby')
    expect(rows[0].text()).toContain('2026-10-20T01:02:03Z')
    expect(rows[1].text()).toContain('connected')
    expect(rows[1].text()).toContain('203.0.113.10:9443')
    expect(rows[1].text()).toContain('2026-10-20T01:02:04Z')
    expect(rows[1].text()).not.toContain('expired')
    expect(detail.get('[data-testid="detail-summary"]').text()).toContain('待验证')
    expect(detail.text()).not.toContain('接入完成')
  })

  it('keeps user confirmation apart from the node probe', async () => {
    stored.push(view({
      summary: '待确认',
      probes: [{ agent_id: 'edge', purpose: 'backend', status: 'http_status', http_status_code: 405 }],
    }))
    api.confirmAccessServiceClient.mockImplementation(async () => view({
      summary: '待确认',
      probes: [{ agent_id: 'edge', purpose: 'backend', status: 'http_status', http_status_code: 405 }],
      client_confirmation: { confirmed: true, confirmed_at: '2026-10-20T00:00:00Z', kind: 'user_confirmation' },
    }))
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('[data-testid="service-card"]').findAll('button').find((button) => button.text() === '详情').trigger('click')
    await flushPromises()
    const detail = wrapper.get('[data-testid="service-detail"]')
    expect(detail.get('[data-testid="probe-row"]').text()).toContain('http_status 405')
    expect(detail.get('[data-testid="client-confirmation"]').text()).toContain('客户端访问：未知')
    expect(detail.text()).toContain('不表示媒体播放通过')
    await detail.findAll('button').find((button) => button.text() === '我确认页面可打开').trigger('click')
    await flushPromises()
    const confirmed = wrapper.get('[data-testid="service-detail"]')
    expect(confirmed.text()).toContain('用户确认页面可打开')
    expect(confirmed.get('[data-testid="probe-row"]').text()).toContain('http_status 405')
    expect(confirmed.text()).toContain('不表示媒体播放通过')
  })

  it('does not offer reissue or reroute when only the name changes', async () => {
    stored.push(view({ summary: '失败', last_error: '证书签发失败', certificate: { state: 'error', last_error: 'dns failed' } }))
    api.updateAccessService.mockImplementation(async (_id, payload) => view({
      name: payload.name,
      summary: '失败',
      last_error: '证书签发失败',
    }))
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('[data-testid="service-card"]').findAll('button').find((button) => button.text() === '修改').trigger('click')
    await wrapper.get('#service-name').setValue('只改名字')
    await wrapper.get('form').trigger('submit')
    expect(wrapper.text()).not.toContain('重新签发')
    expect(wrapper.text()).not.toContain('重新选路')
    expect(wrapper.text()).not.toContain('修正')
    await wrapper.get('[data-testid="service-confirm"] button.btn-primary').trigger('click')
    await flushPromises()
    expect(api.updateAccessService).toHaveBeenCalledTimes(1)
    expect(api.correctAccessService).not.toHaveBeenCalled()
    expect(api.updateAccessService.mock.calls[0][1].name).toBe('只改名字')
    expect(api.updateAccessService.mock.calls[0][1].backend_url).toBe('http://10.1.0.8:8096')
  })

  it('shows the rule backend and disables simplified forwarding for an advanced service', async () => {
    stored.push(view({
      advanced: true,
      advanced_reason: 'multiple backends',
      summary: '失败',
      last_error: 'simplified forwarding edit is disabled (multiple backends)',
      backend_url: 'http://10.1.0.8:8096',
      rules: [{
        id: 9,
        agent_id: 'edge',
        frontend_url: 'https://media.example.com',
        backends: [
          { url: 'http://real.example/library' },
          { kind: 'plugin_provider', plugin_provider: { instance_id: 'inst', provider_id: 'emby' } },
        ],
      }],
    }))
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('[data-testid="service-card"]').findAll('button').find((button) => button.text() === '修改').trigger('click')
    const advanced = wrapper.get('[data-testid="advanced-backend"]')
    expect(advanced.text()).toContain('http://real.example/library')
    expect(advanced.text()).toContain('emby · inst')
    expect(wrapper.get('#service-backend-url').attributes('disabled')).toBeDefined()
    expect(wrapper.get('#service-agent').attributes('disabled')).toBeDefined()
    expect(wrapper.get('#entry-agent').attributes('disabled')).toBeDefined()
    expect(wrapper.get('#service-entry-url').attributes('disabled')).toBeDefined()
    expect(advanced.get('a').attributes('href')).toContain('/rules?')
    expect(advanced.get('a').attributes('href')).toContain('agentId=edge')
    await wrapper.get('form').trigger('submit')
    const confirm = wrapper.get('[data-testid="service-confirm"]')
    expect(confirm.text()).not.toContain('修正')
    expect(confirm.get('[data-testid="advanced-confirm"]').text()).toContain('在高级规则页查看')
    await confirm.get('button.btn-primary').trigger('click')
    await flushPromises()
    expect(api.correctAccessService).not.toHaveBeenCalled()
  })

  it('explains stopped entries and retained resources before deleting', async () => {
    stored.push(view({
      rules: [{ id: 4, agent_id: 'edge', frontend_url: 'https://media.example.com', backends: [{ url: 'http://10.1.0.8:8096' }] }],
    }))
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('[data-testid="service-card"]').findAll('button').find((button) => button.text() === '删除').trigger('click')
    const dialog = wrapper.get('[data-testid="service-delete"]')
    expect(dialog.text()).toContain('将停止的入口')
    expect(dialog.text()).toContain('https://media.example.com')
    expect(dialog.text()).toContain('会保留的共享资源')
    expect(dialog.text()).toContain('证书')
    expect(dialog.text()).toContain('监听器')
    expect(dialog.text()).toContain('节点')
    await dialog.findAll('button').find((button) => button.text() === '返回').trigger('click')
    expect(api.deleteAccessService).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="service-card"]').exists()).toBe(true)

    await wrapper.get('[data-testid="service-card"]').findAll('button').find((button) => button.text() === '删除').trigger('click')
    api.deleteAccessService.mockImplementation(async () => view({
      summary: '已保存、待生效',
      deletion: {
        pending: true,
        stopped_entries: [{ agent_id: 'edge', rule_id: 4, url: 'https://media.example.com' }],
        retained: [{ kind: 'agent', reason: '节点会保留' }],
        note: '删除尚未在节点生效',
      },
      apply: { pending: true },
    }))
    await wrapper.get('[data-testid="service-delete"]').findAll('button').find((button) => button.text() === '确认删除').trigger('click')
    await flushPromises()
    expect(api.deleteAccessService).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).toContain('已保存、待生效')
    expect(wrapper.find('[data-testid="service-card"]').exists()).toBe(true)
  })
})

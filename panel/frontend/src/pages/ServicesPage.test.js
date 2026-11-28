import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { reactive, ref } from 'vue'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import ServicesPage from './ServicesPage.vue'
import { messageStore } from '../stores/messages.js'

const agentsData = ref([
  { id: 'edge', name: '入口节点', status: 'online', last_seen_at: '2026-11-28T08:00:00Z' },
  { id: 'origin', name: '源站节点', status: 'online', last_seen_at: '2026-11-28T07:59:00Z' },
  { id: 'backup', name: '备用节点', status: 'offline', last_seen_at: '2026-11-27T08:00:00Z' },
])
const route = reactive({ query: {}, path: '/services', name: 'services' })
const stored = []
const mountedWrappers = []

const api = vi.hoisted(() => ({
  fetchAccessServices: vi.fn(),
  fetchAccessService: vi.fn(),
  createAccessService: vi.fn(),
  updateAccessService: vi.fn(),
  correctAccessService: vi.fn(),
  retryAccessService: vi.fn(),
  deleteAccessService: vi.fn(),
  confirmAccessServiceClient: vi.fn(),
  retireAccessService: vi.fn(),
  abandonAccessService: vi.fn(),
}))

let routerReplaceImpl = (target) => {
  route.query = { ...(target?.query || {}) }
}

vi.mock('vue-router', () => ({
  useRoute: () => route,
  useRouter: () => ({
    replace: (target) => routerReplaceImpl(target),
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
  const wrapper = mount(ServicesPage, {
    attachTo: document.body,
    global: { plugins: [[VueQueryPlugin, { queryClient }]] },
  })
  mountedWrappers.push(wrapper)
  return wrapper
}

function getModal(testName) {
  return document.body.querySelector(`[data-test="${testName}"]`)
}

function getFormModal() {
  return getModal('service-form-modal')
}

function getDeleteModal() {
  return getModal('service-delete-modal')
}

function setInputValue(selector, value) {
  const input = document.body.querySelector(selector)
  if (!input) throw new Error(`Missing input ${selector}`)
  input.value = value
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

async function selectAgentInForm(triggerSelector, agentId) {
  const trigger = document.body.querySelector(triggerSelector)
  if (!trigger) throw new Error(`Missing trigger ${triggerSelector}`)
  trigger.click()
  await flushPromises()
  const option = document.body.querySelector(`.agent-search-select__dropdown [data-agent-id="${agentId}"]`)
  if (!option) throw new Error(`Missing agent option ${agentId} for ${triggerSelector}`)
  option.click()
  await flushPromises()
}

async function fillDraft(overrides = {}) {
  const values = {
    name: '家庭媒体',
    backend: 'http://10.1.0.8:8096',
    serviceAgent: 'origin',
    entryAgent: 'edge',
    entry: 'https://media.example.com',
    ...overrides,
  }
  setInputValue('#service-name', values.name)
  setInputValue('#service-backend-url', values.backend)
  if (values.serviceAgent) await selectAgentInForm('#service-agent', values.serviceAgent)
  if (values.entryAgent) await selectAgentInForm('#entry-agent', values.entryAgent)
  setInputValue('#service-entry-url', values.entry)
  await flushPromises()
  return values
}

async function submitFormModal() {
  const submitBtn = document.body.querySelector('[data-testid="service-form-submit"]')
  if (!submitBtn) throw new Error('Missing service form submit button')
  submitBtn.click()
  await flushPromises()
}

async function openCardActionMenu(cardWrapper) {
  await cardWrapper.get('.base-action-menu__trigger').trigger('click')
  await flushPromises()
}

async function clickActionMenuItem(id) {
  const panels = Array.from(document.body.querySelectorAll('[data-testid="base-action-menu-panel"]'))
  const visiblePanel = panels.find((panel) => panel.style.display !== 'none')
  if (!visiblePanel) throw new Error('No visible action menu panel')
  const item = visiblePanel.querySelector(`[data-testid="base-action-menu-item-${id}"]`)
  if (!item) throw new Error(`Missing action menu item ${id}`)
  item.click()
  await flushPromises()
}

beforeEach(() => {
  document.body.innerHTML = ''
  messageStore.clearAll()
  stored.splice(0, stored.length)
  route.query = {}
  api.fetchAccessServices.mockReset()
  api.fetchAccessServices.mockImplementation(async () => stored.map((item) => ({ ...item })))
  api.fetchAccessService.mockReset()
  api.fetchAccessService.mockImplementation(async (id) => {
    const found = stored.find((item) => item.id === id)
    if (!found) {
      throw Object.assign(new Error('not found'), { response: { status: 404, data: { message: '服务不存在' } } })
    }
    return { ...found }
  })
  api.createAccessService.mockReset()
  api.updateAccessService.mockReset()
  api.correctAccessService.mockReset()
  api.deleteAccessService.mockReset()
  api.confirmAccessServiceClient.mockReset()
  api.retryAccessService.mockReset()
  api.retireAccessService.mockReset()
  api.abandonAccessService.mockReset()
})

afterEach(() => {
  while (mountedWrappers.length) {
    mountedWrappers.pop().unmount()
  }
  document.body.innerHTML = ''
})

describe('service page', () => {
  it('uses BaseModal single-step form with universal AgentSearchSelect and does not enable an entry before submit', async () => {
    api.createAccessService.mockImplementation(async (payload) => {
      const created = view({
        id: 'svc-new',
        name: payload.name,
        backend_url: payload.backend_url,
        entry_url: payload.entry_url,
        entry_agent_id: payload.entry_agent_id,
        service_agent_id: payload.service_agent_id,
        summary: '待确认',
      })
      stored.push(created)
      return created
    })

    const wrapper = mountPage()
    await flushPromises()

    await wrapper.get('[data-testid="services-add"]').trigger('click')
    await flushPromises()

    const modal = getFormModal()
    expect(modal).not.toBeNull()
    expect(modal.textContent).toContain('Emby、Jellyfin 和普通 Web')
    expect(modal.textContent).toContain('提交前不会创建或启用入口')
    expect(modal.textContent).not.toContain('入口直连')
    expect(modal.querySelector('input[type="password"]')).toBeNull()
    expect(modal.querySelectorAll('select')).toHaveLength(0)

    const serviceTrigger = modal.querySelector('#service-agent')
    const entryTrigger = modal.querySelector('#entry-agent')
    expect(serviceTrigger.tagName).toBe('BUTTON')
    expect(entryTrigger.tagName).toBe('BUTTON')

    await selectAgentInForm('#service-agent', 'origin')
    expect(serviceTrigger.textContent).toContain('源站节点')
    const syncBtn = modal.querySelector('[data-testid="sync-entry-agent"]')
    expect(syncBtn.disabled).toBe(false)
    syncBtn.click()
    await flushPromises()
    expect(entryTrigger.textContent).toContain('源站节点')
    expect(syncBtn.disabled).toBe(true)

    await fillDraft({ backend: 'http://10.2.0.9:8096' })
    expect(api.createAccessService).not.toHaveBeenCalled()

    await submitFormModal()
    expect(api.createAccessService).toHaveBeenCalledTimes(1)
    expect(api.createAccessService.mock.calls[0][0]).toMatchObject({
      name: '家庭媒体',
      backend_url: 'http://10.2.0.9:8096',
      service_agent_id: 'origin',
      entry_agent_id: 'edge',
      entry_url: 'https://media.example.com',
    })
    expect(wrapper.get('[data-testid="detail-summary"]').text()).toContain('待确认')

    wrapper.unmount()
    route.query = {}
    const again = mountPage()
    await flushPromises()
    expect(again.get('[data-testid="service-summary"]').text()).toContain('待确认')
  })

  it('keeps the other fields and sets accessible invalid attributes when URL, node, or entry conflicts', async () => {
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('[data-testid="services-add"]').trigger('click')
    await flushPromises()

    // Submit completely empty form: node triggers get aria-invalid & aria-describedby
    await submitFormModal()
    const serviceTrigger = document.body.querySelector('#service-agent')
    expect(serviceTrigger.getAttribute('aria-invalid')).toBe('true')
    expect(serviceTrigger.getAttribute('aria-describedby')).toBe('service-agent-error')
    expect(document.body.querySelector('#service-agent-error').textContent).toContain('服务侧节点不存在')
    expect(api.createAccessService).not.toHaveBeenCalled()

    // Invalid URL preserves other fields and selected nodes
    await fillDraft({ backend: 'notaurl' })
    await submitFormModal()
    expect(getFormModal().textContent).toContain('后端 URL 必须是带主机名的 http 或 https 地址')
    expect(document.body.querySelector('#service-entry-url').value).toBe('https://media.example.com')
    expect(document.body.querySelector('#service-agent').textContent).toContain('源站节点')
    expect(api.createAccessService).not.toHaveBeenCalled()

    // Editing a service with a deleted/missing node shows missing label and keeps URL fields
    wrapper.unmount()
    stored.push(view({ id: 'svc-missing', service_agent_id: 'gone', summary: '失败' }))
    const edit = mountPage()
    await flushPromises()
    await edit.get('[data-testid="service-card"] [data-action="edit"]').trigger('click')
    await flushPromises()
    expect(document.body.querySelector('#service-agent').textContent).toContain('不存在的节点')
    await submitFormModal()
    expect(getFormModal().textContent).toContain('服务侧节点不存在')
    expect(document.body.querySelector('#service-agent').getAttribute('aria-invalid')).toBe('true')
    expect(document.body.querySelector('#service-backend-url').value).toBe('http://10.1.0.8:8096')
    expect(document.body.querySelector('#service-entry-url').value).toBe('https://media.example.com')

    // 409 conflict keeps modal open and preserves all fields
    edit.unmount()
    stored.splice(0, stored.length)
    api.createAccessService.mockRejectedValue(Object.assign(new Error('conflict'), {
      response: { status: 409, data: { message: '入口冲突，原规则未改' } },
    }))
    const conflict = mountPage()
    await flushPromises()
    await conflict.get('[data-testid="services-add"]').trigger('click')
    await flushPromises()
    await fillDraft()
    await submitFormModal()
    expect(getFormModal()).not.toBeNull()
    expect(getFormModal().textContent).toContain('入口冲突，原规则未改')
    expect(document.body.querySelector('#service-backend-url').value).toBe('http://10.1.0.8:8096')
    expect(document.body.querySelector('#service-entry-url').value).toBe('https://media.example.com')
    expect(document.body.querySelector('#entry-agent').textContent).toContain('入口节点')
  })

  it('asks for relay input on error without clearing the rest of the form', async () => {
    api.createAccessService.mockRejectedValue(Object.assign(new Error('relay'), {
      response: { data: { message: 'relay_listen_port is required to create the single service-side listener' } },
    }))
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('[data-testid="services-add"]').trigger('click')
    await flushPromises()
    await fillDraft()
    await submitFormModal()

    expect(document.body.querySelector('#relay-port')).not.toBeNull()
    expect(document.body.querySelector('#service-backend-url').value).toBe('http://10.1.0.8:8096')
    expect(document.body.querySelector('#service-entry-url').value).toBe('https://media.example.com')
    expect(document.body.querySelector('#entry-agent').textContent).toContain('入口节点')
    expect(getFormModal().textContent).not.toContain('入口直连')
  })

  it('keeps the modal open when a saved service still needs a relay host and auto-routes to correctAccessService', async () => {
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
    await wrapper.get('[data-testid="services-add"]').trigger('click')
    await flushPromises()
    await fillDraft()
    await submitFormModal()

    expect(wrapper.find('[data-testid="service-detail"]').exists()).toBe(false)
    expect(document.body.querySelector('#relay-host')).not.toBeNull()
    expect(document.body.querySelector('#service-backend-url').value).toBe('http://10.1.0.8:8096')
    expect(document.body.querySelector('#service-entry-url').value).toBe('https://media.example.com')
    expect(getFormModal().textContent).toContain(relayError)
    expect(api.retryAccessService).not.toHaveBeenCalled()

    setInputValue('#relay-host', '203.0.113.10')
    await flushPromises()
    await submitFormModal()

    expect(api.updateAccessService).not.toHaveBeenCalled()
    expect(api.correctAccessService).toHaveBeenCalledTimes(1)
    expect(api.correctAccessService.mock.calls[0][1].relay_public_host).toBe('203.0.113.10')
    expect(api.correctAccessService.mock.calls[0][1].backend_url).toBe('http://10.1.0.8:8096')
  })

  it('collects a relay host from saved last_error when editing or retrying from detail', async () => {
    stored.push(view({
      summary: '待确认',
      last_error: 'relay_public_host must be a concrete host the entry node can dial',
    }))
    api.correctAccessService.mockImplementation(async () => view({ summary: '待确认' }))

    const wrapper = mountPage()
    await flushPromises()

    // Click card to enter detail, click Retry -> opens edit modal with relay input instead of calling retry API
    await wrapper.get('[data-testid="service-card"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="detail-retry"]').trigger('click')
    await flushPromises()

    expect(api.retryAccessService).not.toHaveBeenCalled()
    expect(document.body.querySelector('#relay-host')).not.toBeNull()
    setInputValue('#relay-host', '203.0.113.10')
    await flushPromises()
    await submitFormModal()

    expect(api.retryAccessService).not.toHaveBeenCalled()
    expect(api.updateAccessService).not.toHaveBeenCalled()
    expect(api.correctAccessService).toHaveBeenCalledTimes(1)
    expect(api.correctAccessService.mock.calls[0][1].relay_public_host).toBe('203.0.113.10')
  })

  it('auto-routes failed service edits: name-only uses update, forwarding change uses correct', async () => {
    stored.push(view({
      id: 'svc-fail',
      summary: '失败',
      last_error: '证书签发失败',
      certificate: { state: 'error', last_error: 'dns failed' },
    }))
    api.updateAccessService.mockImplementation(async (_id, payload) => view({
      id: 'svc-fail',
      name: payload.name,
      summary: '失败',
      last_error: '证书签发失败',
    }))
    api.correctAccessService.mockImplementation(async (_id, payload) => view({
      id: 'svc-fail',
      name: payload.name,
      backend_url: payload.backend_url,
      summary: '待确认',
    }))

    const wrapper = mountPage()
    await flushPromises()

    // Name-only edit on failed service -> updateAccessService, no 修正/重新签发/重新选路 copy
    await wrapper.get('[data-testid="service-card"] [data-action="edit"]').trigger('click')
    await flushPromises()
    expect(getFormModal().textContent).not.toContain('重新签发')
    expect(getFormModal().textContent).not.toContain('重新选路')
    expect(getFormModal().textContent).not.toContain('修正')
    setInputValue('#service-name', '只改名字')
    await flushPromises()
    await submitFormModal()
    expect(api.updateAccessService).toHaveBeenCalledTimes(1)
    expect(api.correctAccessService).not.toHaveBeenCalled()
    expect(api.updateAccessService.mock.calls[0][1].name).toBe('只改名字')

    // Forwarding change on failed service -> correctAccessService automatically
    await wrapper.get('[data-testid="service-detail"]').findAll('button').find((b) => b.text() === '修改').trigger('click')
    await flushPromises()
    setInputValue('#service-backend-url', 'http://10.1.0.99:8096')
    await flushPromises()
    await submitFormModal()
    expect(api.correctAccessService).toHaveBeenCalledTimes(1)
    expect(api.correctAccessService.mock.calls[0][1].backend_url).toBe('http://10.1.0.99:8096')
  })

  it('shows the rule backend and locks simplified forwarding for an advanced service', async () => {
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
    api.updateAccessService.mockImplementation(async (_id, payload) => view({
      advanced: true,
      name: payload.name,
      summary: '失败',
    }))

    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('[data-testid="service-card"] [data-action="edit"]').trigger('click')
    await flushPromises()

    const modal = getFormModal()
    const advanced = modal.querySelector('[data-testid="advanced-backend"]')
    expect(advanced.textContent).toContain('http://real.example/library')
    expect(advanced.textContent).toContain('emby · inst')
    expect(modal.querySelector('#service-backend-url').disabled).toBe(true)
    expect(modal.querySelector('#service-agent').disabled).toBe(true)
    expect(modal.querySelector('#entry-agent').disabled).toBe(true)
    expect(modal.querySelector('#service-entry-url').disabled).toBe(true)
    expect(modal.querySelector('[data-testid="sync-entry-agent"]')).toBeNull()

    // Clicking disabled AgentSearchSelect does not open dropdown
    modal.querySelector('#service-agent').click()
    await flushPromises()
    expect(document.body.querySelector('.agent-search-select__dropdown')).toBeNull()

    const link = advanced.querySelector('a')
    expect(link.getAttribute('href')).toContain('/rules?')
    expect(link.getAttribute('href')).toContain('agentId=edge')

    setInputValue('#service-name', '高级规则改名')
    await flushPromises()
    await submitFormModal()
    expect(api.updateAccessService).toHaveBeenCalledTimes(1)
    expect(api.correctAccessService).not.toHaveBeenCalled()
  })

  it('reuses one idempotency key on create and retry until the request body changes', async () => {
    const createKeys = []
    api.createAccessService.mockImplementation(async (_payload, key) => {
      createKeys.push(key)
      throw Object.assign(new Error('offline'), { response: { data: { message: '暂时连不上' } } })
    })
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('[data-testid="services-add"]').trigger('click')
    await flushPromises()
    await fillDraft()
    await submitFormModal()
    await submitFormModal()
    expect(createKeys).toHaveLength(2)
    expect(createKeys[0]).toBe(createKeys[1])

    setInputValue('#service-entry-url', 'https://other.example.com')
    await flushPromises()
    await submitFormModal()
    expect(createKeys[2]).not.toBe(createKeys[0])

    // Unified idempotency path for retrySelected
    wrapper.unmount()
    stored.push(view({ id: 'svc-retry', summary: '失败', last_error: '证书签发失败' }))
    const retryKeys = []
    api.retryAccessService.mockImplementation(async (_id, key) => {
      retryKeys.push(key)
      if (retryKeys.length < 2) {
        throw Object.assign(new Error('timeout'), { response: { data: { message: '重试超时' } } })
      }
      return view({ id: 'svc-retry', summary: '待确认' })
    })
    route.query = { id: 'svc-retry' }
    const detailWrapper = mountPage()
    await flushPromises()
    await detailWrapper.get('[data-testid="detail-retry"]').trigger('click')
    await flushPromises()
    await detailWrapper.get('[data-testid="detail-retry"]').trigger('click')
    await flushPromises()
    expect(retryKeys).toHaveLength(2)
    expect(retryKeys[0]).toBe(retryKeys[1])
  })

  it('renders header summary, supports refresh, multi-dimensional filter bar, and clear-filters empty state', async () => {
    stored.push(view({
      id: 'svc-ready',
      name: 'Emby 主站',
      entry_agent_id: 'edge',
      service_agent_id: 'origin',
      entry_url: 'https://emby.example.com',
      backend_url: 'http://10.0.0.10:8096',
      summary: '已就绪',
      path: { mode: 'direct', summary: '由 origin 拨号', pending: false },
      certificate: { state: 'ready', not_after: '2030-01-02T00:00:00Z', https_ready: true },
      apply: { pending: false },
    }))
    stored.push(view({
      id: 'svc-pending',
      name: 'Jellyfin 测试',
      entry_agent_id: 'backup',
      service_agent_id: 'backup',
      entry_url: 'http://jf.example.com',
      backend_url: 'http://10.0.0.20:8096',
      summary: '待确认',
      apply: { pending: true },
    }))
    stored.push(view({
      id: 'svc-failed',
      name: '失败站点',
      entry_agent_id: 'edge',
      service_agent_id: 'backup',
      entry_url: 'https://broken.example.com',
      backend_url: 'http://10.0.0.30:8096',
      summary: '失败',
      last_error: '证书签发失败',
      certificate: { state: 'error', last_error: 'dns failed', https_ready: false },
      apply: { pending: true },
    }))

    const wrapper = mountPage()
    await flushPromises()

    // Header summary counts
    expect(wrapper.get('[data-testid="services-summary"]').text()).toContain('共 3 项 · 已就绪 1 · 处理中 1 · 失败 1')
    expect(wrapper.findAll('[data-testid="service-card"]')).toHaveLength(3)

    // Manual refresh triggers refetch
    await wrapper.get('[data-testid="services-refresh"]').trigger('click')
    await flushPromises()
    expect(api.fetchAccessServices).toHaveBeenCalledTimes(2)

    // Ready card has no redundant "未完成环节" block; failed card shows it
    const cards = wrapper.findAll('[data-testid="service-card"]')
    expect(cards[0].find('[data-testid="service-steps"]').exists()).toBe(false)
    expect(cards[2].get('[data-testid="service-steps"]').text()).toContain('证书签发失败')

    // Filter by status chip inside ResourceListFilterBar
    await wrapper.get('.resource-list-filter-bar__filter-trigger').trigger('click')
    await flushPromises()
    const failedBtn = document.body.querySelector('.resource-list-filter-bar__segment[data-value="failed"]')
    failedBtn.click()
    await flushPromises()
    expect(wrapper.findAll('[data-testid="service-card"]')).toHaveLength(1)
    expect(wrapper.get('[data-testid="service-card"]').text()).toContain('失败站点')

    // Clear status filter via reset button inside filter bar and filter by agent (matches entry OR service agent)
    await wrapper.get('.resource-list-filter-bar__reset--inline').trigger('click')
    await flushPromises()
    await wrapper.get('.resource-list-filter-bar .agent-search-select__trigger').trigger('click')
    await flushPromises()
    document.body.querySelector('.agent-search-select__dropdown [data-agent-id="backup"]').click()
    await flushPromises()
    expect(wrapper.findAll('[data-testid="service-card"]')).toHaveLength(2)

    // Filter by keyword with no match -> shows filter empty state (not "还没有服务") & clear button restores all
    const searchInput = wrapper.get('.resource-list-filter-bar__input')
    await searchInput.setValue('nonexistent-keyword')
    await flushPromises()
    expect(wrapper.get('[data-testid="services-filter-empty"]').text()).toContain('没有匹配的服务')
    expect(wrapper.text()).not.toContain('还没有服务')

    await wrapper.get('[data-testid="services-clear-filters"]').trigger('click')
    await flushPromises()
    expect(wrapper.findAll('[data-testid="service-card"]')).toHaveLength(3)
  })

  it('renders control-plane status, certificate, missing traffic, and expired probes accurately', async () => {
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
      id: 'svc-stale',
      name: '待验证站点',
      summary: '待验证',
      entry_url: 'https://stale.example.com',
      certificate: { state: 'ready', not_after: '2030-01-02T00:00:00Z', https_ready: true },
      traffic: { state: 'unavailable' },
      path: { mode: 'relay', summary: 'edge → origin → stale.example.com', pending: false },
      apply: { pending: false },
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
    const text = wrapper.text()
    expect(text).toContain('无数据')
    expect(text).not.toContain('0 B')
    expect(text).toContain('失败：dns failed')
    expect(text).toContain('到期 2030-01-02T00:00:00Z')
    expect(text).toContain('等待新入口生效')
    expect(text).toContain('旧入口仍可能接收新请求')
    expect(text).toContain('edge → origin → stale.example.com（relay）')
    expect(text).not.toContain('接入完成')

    const staleCard = wrapper.findAll('[data-testid="service-card"]')[1]
    expect(staleCard.get('[data-testid="service-summary"]').text()).toBe('待验证')
    const staleProbe = staleCard.findAll('[data-testid="service-probe"]')[0]
    expect(staleProbe.text()).toContain('http_status 401')
    expect(staleProbe.text()).toContain('http://ready.example/emby')
    expect(staleProbe.text()).toContain('2026-10-20T01:02:03Z')
    expect(staleProbe.text()).toContain('expired')
    expect(staleCard.text()).not.toContain('已就绪')

    // Clicking whole card enters detail view
    await staleCard.trigger('click')
    await flushPromises()
    const detail = wrapper.get('[data-testid="service-detail"]')
    const rows = detail.findAll('[data-testid="probe-row"]')
    expect(rows[0].text()).toContain('http_status 401')
    expect(rows[0].text()).toContain('expired')
    expect(rows[1].text()).toContain('connected')
    expect(rows[1].text()).not.toContain('expired')
    expect(detail.get('[data-testid="detail-backend-url"]').text()).toContain('http://10.1.0.8:8096')
    expect(detail.get('[data-testid="detail-hero-open"]').attributes('href')).toBe('https://stale.example.com')
  })

  it('uses CopyButton without entering detail and without duplicate page status banner', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    const originalClipboard = navigator.clipboard
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    })

    try {
      stored.push(view({ entry_url: 'https://media.example.com' }))
      const wrapper = mountPage()
      await flushPromises()

      await wrapper.get('[data-testid="service-card-copy"]').trigger('click')
      await flushPromises()
      expect(writeText).toHaveBeenCalledWith('https://media.example.com')
      expect(wrapper.find('[data-testid="service-detail"]').exists()).toBe(false)
      expect(wrapper.find('.services-page__notice').exists()).toBe(false)
      expect(messageStore.state.messages.some((item) => item.type === 'success' && item.text === '已复制访问地址')).toBe(true)

      messageStore.clearAll()
      writeText.mockRejectedValueOnce(new Error('denied'))
      await wrapper.get('[data-testid="service-card-copy"]').trigger('click')
      await flushPromises()
      expect(messageStore.state.messages.some((item) => item.type === 'error')).toBe(true)
    } finally {
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        value: originalClipboard,
      })
    }
  })

  it('keeps user confirmation apart from node probe and supports single-service detail refresh', async () => {
    stored.push(view({
      id: 'svc-detail',
      summary: '待确认',
      probes: [{ agent_id: 'edge', purpose: 'backend', status: 'http_status', http_status_code: 405 }],
    }))
    api.confirmAccessServiceClient.mockImplementation(async () => view({
      id: 'svc-detail',
      summary: '待确认',
      probes: [{ agent_id: 'edge', purpose: 'backend', status: 'http_status', http_status_code: 405 }],
      client_confirmation: { confirmed: true, confirmed_at: '2026-10-20T00:00:00Z', kind: 'user_confirmation' },
    }))
    api.fetchAccessService.mockResolvedValueOnce(view({
      id: 'svc-detail',
      summary: '已就绪',
      probes: [{ agent_id: 'edge', purpose: 'backend', status: 'http_status', http_status_code: 405 }],
      client_confirmation: { confirmed: true, confirmed_at: '2026-10-20T00:00:00Z', kind: 'user_confirmation' },
      apply: { pending: false },
    }))

    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('[data-testid="service-card"]').trigger('click')
    await flushPromises()

    const detail = wrapper.get('[data-testid="service-detail"]')
    expect(detail.get('[data-testid="probe-row"]').text()).toContain('http_status 405')
    expect(detail.get('[data-testid="client-confirmation"]').text()).toContain('客户端访问：未知')
    expect(detail.text()).toContain('不表示媒体播放通过')

    await detail.findAll('button').find((button) => button.text() === '我确认页面可打开').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="service-detail"]').text()).toContain('用户确认页面可打开')
    expect(wrapper.get('[data-testid="probe-row"]').text()).toContain('http_status 405')
    expect(wrapper.get('[data-testid="service-detail"]').text()).toContain('不表示媒体播放通过')

    // Single-service refresh updates detail via fetchAccessService
    await wrapper.get('[data-testid="detail-refresh"]').trigger('click')
    await flushPromises()
    expect(api.fetchAccessService).toHaveBeenCalledWith('svc-detail')
    expect(wrapper.get('[data-testid="detail-summary"]').text()).toContain('已就绪')

    // Refresh error retains current detail and shows alert
    api.fetchAccessService.mockRejectedValueOnce(Object.assign(new Error('network'), {
      response: { status: 500, data: { message: '控制面暂时超时' } },
    }))
    await wrapper.get('[data-testid="detail-refresh"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="service-detail"]').exists()).toBe(true)
    expect(wrapper.get('[role="alert"]').text()).toContain('控制面暂时超时')

    // 404 on detail refresh transitions to explicit not-found fallback
    api.fetchAccessService.mockRejectedValueOnce(Object.assign(new Error('not found'), {
      response: { status: 404, data: { message: '服务不存在' } },
    }))
    await wrapper.get('[data-testid="detail-refresh"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="service-detail"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="service-not-found"]').text()).toContain('未找到对应服务')
  })

  it('hides unsafe open links and empty copy buttons on ServiceCard', async () => {
    stored.push(view({
      id: 'svc-unsafe',
      name: '非 HTTP 地址',
      entry_url: 'javascript:alert(1)',
      summary: '已就绪',
      apply: { pending: false },
    }))
    stored.push(view({
      id: 'svc-empty-url',
      name: '空地址',
      entry_url: '',
      summary: '待确认',
    }))
    const wrapper = mountPage()
    await flushPromises()

    const cards = wrapper.findAll('[data-testid="service-card"]')
    expect(cards[0].find('[data-action="open"]').exists()).toBe(false)
    expect(cards[1].find('[data-action="open"]').exists()).toBe(false)
    expect(cards[1].find('[data-testid="service-card-copy"]').exists()).toBe(false)
  })

  it('shows not-found fallback for invalid ?id= and returns to list cleanly', async () => {
    stored.push(view({ id: 'svc-1', name: '有效服务', summary: '已就绪' }))
    route.query = { id: 'not-exists' }

    const wrapper = mountPage()
    await flushPromises()

    expect(api.fetchAccessService).toHaveBeenCalledWith('not-exists')
    expect(wrapper.get('[data-testid="service-not-found"]').text()).toContain('未找到对应服务')
    await wrapper.get('[data-testid="service-not-found-back"]').trigger('click')
    await flushPromises()

    expect(route.query.id).toBeUndefined()
    expect(wrapper.get('[data-testid="service-card"]').text()).toContain('有效服务')
  })

  it('opens BaseModal alertdialog for deletion with impact checklist, error display, and pending state retention', async () => {
    stored.push(view({
      id: 'svc-del',
      name: '就绪站点',
      summary: '已就绪',
      rules: [{ id: 4, agent_id: 'edge', frontend_url: 'https://media.example.com', backends: [{ url: 'http://10.1.0.8:8096' }] }],
    }))
    const wrapper = mountPage()
    await flushPromises()

    await openCardActionMenu(wrapper.get('[data-testid="service-card"]'))
    await clickActionMenuItem('delete')

    const modal = getDeleteModal()
    expect(modal).not.toBeNull()
    expect(modal.querySelector('[role="alertdialog"]')).not.toBeNull()
    expect(modal.querySelector('.service-delete__target-badge').textContent).toBe('就绪站点')
    expect(modal.textContent).toContain('将停止的入口')
    expect(modal.textContent).toContain('https://media.example.com')
    expect(modal.textContent).toContain('会保留的共享资源')
    expect(modal.textContent).toContain('证书')
    expect(modal.textContent).toContain('监听器')
    expect(modal.textContent).toContain('节点')
    expect(modal.textContent).toContain('节点尚未应用删除时，服务会留在列表并显示待生效。这不表示入口已经关闭。')

    // Cancel does not call deleteAccessService
    Array.from(modal.querySelectorAll('button')).find((b) => b.textContent.trim() === '返回').click()
    await flushPromises()
    expect(api.deleteAccessService).not.toHaveBeenCalled()
    expect(getDeleteModal()).toBeNull()

    // Error keeps delete modal open and shows error inside modal
    await openCardActionMenu(wrapper.get('[data-testid="service-card"]'))
    await clickActionMenuItem('delete')
    api.deleteAccessService.mockRejectedValueOnce(Object.assign(new Error('busy'), {
      response: { data: { message: '节点正忙，请稍后重试' } },
    }))
    document.body.querySelector('[data-testid="service-delete-confirm"]').click()
    await flushPromises()
    expect(getDeleteModal()).not.toBeNull()
    expect(document.body.querySelector('[data-testid="service-delete-error"]').textContent).toContain('节点正忙，请稍后重试')

    // Pending delete retains service in list with pending status
    api.deleteAccessService.mockResolvedValueOnce(view({
      id: 'svc-del',
      name: '就绪站点',
      summary: '已保存、待生效',
      deletion: {
        pending: true,
        stopped_entries: [{ agent_id: 'edge', rule_id: 4, url: 'https://media.example.com' }],
        retained: [{ kind: 'agent', reason: '节点会保留' }],
        note: '删除尚未在节点生效',
      },
      apply: { pending: true },
    }))
    document.body.querySelector('[data-testid="service-delete-confirm"]').click()
    await flushPromises()
    expect(api.deleteAccessService).toHaveBeenCalledTimes(2)
    expect(getDeleteModal()).toBeNull()
    expect(wrapper.text()).toContain('已保存、待生效')
    expect(wrapper.find('[data-testid="service-card"]').exists()).toBe(true)
  })

  it('renders ErrorState with retry instead of an empty state when initial load fails', async () => {
    api.fetchAccessServices.mockRejectedValueOnce(Object.assign(new Error('load failed'), {
      response: { data: { message: '控制面服务列表暂不可用' } },
    }))
    const wrapper = mountPage()
    await flushPromises()
    const alert = wrapper.get('[role="alert"]')
    expect(alert.text()).toContain('控制面服务列表暂不可用')
    expect(wrapper.text()).not.toContain('还没有服务')

    stored.push(view({ id: 'svc-recovered', name: '恢复后的站点', summary: '已就绪' }))
    await alert.findAll('button').find((button) => button.text() === '重试').trigger('click')
    await flushPromises()
    expect(api.fetchAccessServices).toHaveBeenCalledTimes(2)
    expect(wrapper.get('[data-testid="service-card"]').text()).toContain('恢复后的站点')
  })

  it('does not trigger a redundant GET lookup when immediately deleting from detail view with async router.replace', async () => {
    stored.push(view({ id: 'svc-imm', name: '立即删除站点', summary: '已就绪' }))
    route.query = { id: 'svc-imm' }
    api.deleteAccessService.mockResolvedValueOnce({ ok: true })

    let resolveNav
    const prevReplace = routerReplaceImpl
    routerReplaceImpl = (target) => new Promise((resolve) => {
      resolveNav = () => {
        route.query = { ...(target?.query || {}) }
        resolve()
      }
    })

    try {
      const wrapper = mountPage()
      await flushPromises()
      expect(wrapper.find('[data-testid="service-detail"]').exists()).toBe(true)

      const deleteBtn = wrapper.get('[data-testid="service-detail"]')
        .findAll('button')
        .find((button) => button.text().includes('删除'))
      await deleteBtn.trigger('click')
      await flushPromises()

      document.body.querySelector('[data-testid="service-delete-confirm"]').click()
      await flushPromises()

      expect(api.deleteAccessService).toHaveBeenCalledTimes(1)
      expect(api.fetchAccessService).not.toHaveBeenCalled()

      resolveNav()
      await flushPromises()

      expect(api.fetchAccessService).not.toHaveBeenCalled()
      expect(wrapper.find('[data-testid="service-detail"]').exists()).toBe(false)
      expect(wrapper.findAll('[data-testid="service-card"]')).toHaveLength(0)
    } finally {
      routerReplaceImpl = prevReplace
    }
  })
})

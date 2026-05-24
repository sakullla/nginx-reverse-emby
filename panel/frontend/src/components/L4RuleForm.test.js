import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import L4RuleForm from './L4RuleForm.vue'

const mocks = vi.hoisted(() => ({
  createMutateAsync: vi.fn(),
  updateMutateAsync: vi.fn()
}))

vi.mock('../hooks/useL4Rules', () => ({
  useCreateL4Rule: () => ({
    isPending: { value: false },
    mutateAsync: mocks.createMutateAsync
  }),
  useUpdateL4Rule: () => ({
    isPending: { value: false },
    mutateAsync: mocks.updateMutateAsync
  })
}))

vi.mock('../hooks/useRelayListeners', () => ({
  useAllRelayListeners: () => ({
    data: { value: [] }
  })
}))

vi.mock('../hooks/useWireGuardProfiles', () => ({
  useWireGuardProfiles: () => ({
    data: { value: [{ id: 21, name: 'wg-default', enabled: true }] }
  })
}))

function mountForm() {
  return mount(L4RuleForm, {
    props: { agentId: 'local' },
    global: {
      stubs: {
        RouterLink: true
      }
    }
  })
}

function mountFormWithRules(l4Rules = []) {
  return mount(L4RuleForm, {
    props: { agentId: 'local', l4Rules },
    global: {
      stubs: {
        RouterLink: true
      }
    }
  })
}

function mountEditForm(initialData, l4Rules = []) {
  return mount(L4RuleForm, {
    props: { agentId: 'local', initialData, l4Rules },
    global: {
      stubs: {
        RouterLink: true
      }
    }
  })
}

function selectByLabel(wrapper, labelText) {
  const group = wrapper
    .findAll('.form-group')
    .find((item) => item.find('.form-label').exists() && item.find('.form-label').text() === labelText)
  if (!group) throw new Error(`Missing form group: ${labelText}`)
  return group.find('select').exists() ? group.get('select') : group.get('input')
}

async function switchTab(wrapper, name) {
  const tab = wrapper.findAll('.form-tabs__btn').find((btn) => btn.text().trim() === name)
  if (!tab) throw new Error(`Missing tab: ${name}`)
  await tab.trigger('click')
  await flushPromises()
}

describe('L4RuleForm WireGuard egress', () => {
  beforeEach(() => {
    mocks.createMutateAsync.mockReset()
    mocks.updateMutateAsync.mockReset()
    mocks.createMutateAsync.mockResolvedValue({})
    mocks.updateMutateAsync.mockResolvedValue({})
  })

  it('disables WireGuard URI egress for WireGuard inbound rules', async () => {
    const wrapper = mountForm()

    await selectByLabel(wrapper, '监听端口').setValue('1080')
    await switchTab(wrapper, '协议与监听')
    await selectByLabel(wrapper, '监听模式').setValue('wireguard')
    await selectByLabel(wrapper, '出口模式').setValue('wireguard')
    await flushPromises()

    expect(wrapper.text()).toContain('WireGuard 配置')
    expect(wrapper.text()).not.toContain('WireGuard 出口来源')
    expect(wrapper.find('input[placeholder="wireguard://user:pass@host:51820"]').exists()).toBe(false)

    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(mocks.createMutateAsync).toHaveBeenCalledTimes(1)
    expect(mocks.createMutateAsync.mock.calls[0][0]).toMatchObject({
      protocol: 'tcp',
      listen_mode: 'wireguard',
      proxy_egress_mode: 'wireguard',
      wireguard_inbound_mode: 'transparent',
      wireguard_profile_id: 21
    })
    expect(mocks.createMutateAsync.mock.calls[0][0]).not.toHaveProperty('wireguard_egress_uri')
  })

  it('derives WireGuard address-mode listen host from the selected profile', async () => {
    const wrapper = mountForm()

    await selectByLabel(wrapper, '监听端口').setValue('8443')
    await switchTab(wrapper, '协议与监听')
    await selectByLabel(wrapper, '监听模式').setValue('wireguard')
    await selectByLabel(wrapper, 'WireGuard 入站模式').setValue('address')
    await flushPromises()
    await switchTab(wrapper, '基础配置')
    const backend = wrapper.get('input[placeholder="IP:端口 或 域名:端口"]')
    await backend.setValue('upstream.local:9000')

    expect(wrapper.text()).not.toContain('WireGuard Listen Host')

    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(mocks.createMutateAsync).toHaveBeenCalledTimes(1)
    expect(mocks.createMutateAsync.mock.calls[0][0]).toMatchObject({
      listen_mode: 'wireguard',
      wireguard_inbound_mode: 'address',
      wireguard_profile_id: 21
    })
    expect(mocks.createMutateAsync.mock.calls[0][0]).not.toHaveProperty('wireguard_listen_host')
  })

  it('requires a selected profile for WireGuard transparent inbound rules', async () => {
    const wrapper = mountForm()

    await switchTab(wrapper, '协议与监听')
    await selectByLabel(wrapper, '监听模式').setValue('wireguard')
    await flushPromises()

    expect(selectByLabel(wrapper, 'WireGuard 配置').element.value).toBe('21')

    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(mocks.createMutateAsync).toHaveBeenCalledTimes(1)
    expect(mocks.createMutateAsync.mock.calls[0][0]).toMatchObject({
      listen_mode: 'wireguard',
      wireguard_inbound_mode: 'transparent',
      wireguard_profile_id: 21
    })
  })

  it('allows port 0 for WireGuard transparent inbound rules', async () => {
    const wrapper = mountForm()

    await selectByLabel(wrapper, '监听端口').setValue('0')
    await switchTab(wrapper, '协议与监听')
    await selectByLabel(wrapper, '监听模式').setValue('wireguard')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(wrapper.text()).not.toContain('监听端口必须在 1-65535 之间')
    expect(mocks.createMutateAsync).toHaveBeenCalledTimes(1)
    expect(mocks.createMutateAsync.mock.calls[0][0]).toMatchObject({
      protocol: 'tcp',
      listen_port: 0,
      listen_mode: 'wireguard',
      proxy_egress_mode: '',
      wireguard_inbound_mode: 'transparent',
      wireguard_profile_id: 21,
      backends: []
    })
  })

  it('allows relay egress for WireGuard transparent inbound rules', async () => {
    const wrapper = mountForm()

    await selectByLabel(wrapper, '监听端口').setValue('0')
    await switchTab(wrapper, '协议与监听')
    await selectByLabel(wrapper, '监听模式').setValue('wireguard')
    await selectByLabel(wrapper, '出口模式').setValue('relay')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(wrapper.text()).not.toContain('监听端口必须在 1-65535 之间')
    expect(wrapper.text()).not.toContain('至少需要一个有效的后端服务器')
    expect(mocks.createMutateAsync).toHaveBeenCalledTimes(1)
    expect(mocks.createMutateAsync.mock.calls[0][0]).toMatchObject({
      protocol: 'tcp',
      listen_port: 0,
      listen_mode: 'wireguard',
      proxy_egress_mode: 'relay',
      wireguard_inbound_mode: 'transparent',
      wireguard_profile_id: 21,
      backends: []
    })
  })

  it('rejects port 0 outside WireGuard transparent inbound rules', async () => {
    const wrapper = mountForm()

    await selectByLabel(wrapper, '监听端口').setValue('0')
    await wrapper.get('input[placeholder="IP:端口 或 域名:端口"]').setValue('upstream.local:9000')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(wrapper.text()).toContain('监听端口必须在 1-65535 之间')
    expect(mocks.createMutateAsync).not.toHaveBeenCalled()
  })

  it('does not offer entry authentication for WireGuard proxy egress', async () => {
    const wrapper = mountForm()

    await switchTab(wrapper, '协议与监听')
    await selectByLabel(wrapper, '监听模式').setValue('wireguard')
    await selectByLabel(wrapper, '出口模式').setValue('proxy')
    await flushPromises()

    expect(wrapper.text()).not.toContain('启用入口认证')
  })

  it('shows proxy URL input for WireGuard transparent proxy egress', async () => {
    const wrapper = mountForm()

    await selectByLabel(wrapper, '监听端口').setValue('0')
    await switchTab(wrapper, '协议与监听')
    await selectByLabel(wrapper, '监听模式').setValue('wireguard')
    await selectByLabel(wrapper, '出口模式').setValue('proxy')
    await flushPromises()

    const proxyURL = wrapper.get('input[placeholder="socks://user:pass@127.0.0.1:1080"]')
    await proxyURL.setValue('socks5://127.0.0.1:1080')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(mocks.createMutateAsync).toHaveBeenCalledTimes(1)
    expect(mocks.createMutateAsync.mock.calls[0][0]).toMatchObject({
      listen_mode: 'wireguard',
      wireguard_inbound_mode: 'transparent',
      proxy_egress_mode: 'proxy',
      proxy_egress_url: 'socks5://127.0.0.1:1080',
      wireguard_profile_id: 21
    })
  })

  it('rejects UDP proxy entry without same-port TCP SOCKS5 rule', async () => {
    const wrapper = mountFormWithRules([
      {
        id: 2,
        protocol: 'tcp',
        listen_mode: 'proxy',
        listen_host: '0.0.0.0',
        listen_port: 2080
      }
    ])

    await selectByLabel(wrapper, '协议').setValue('udp')
    await selectByLabel(wrapper, '监听端口').setValue('1080')
    await switchTab(wrapper, '协议与监听')
    await selectByLabel(wrapper, '监听模式').setValue('proxy')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(wrapper.text()).toContain('需要先维护同端口 TCP SOCKS5 入口规则')
    expect(mocks.createMutateAsync).not.toHaveBeenCalled()
  })

  it('allows UDP proxy entry when a same-port TCP SOCKS5 rule exists', async () => {
    const wrapper = mountFormWithRules([
      {
        id: 3,
        protocol: 'tcp',
        listen_mode: 'proxy',
        listen_host: '0.0.0.0',
        listen_port: 1080
      }
    ])

    await selectByLabel(wrapper, '协议').setValue('udp')
    await selectByLabel(wrapper, '监听端口').setValue('1080')
    await switchTab(wrapper, '协议与监听')
    await selectByLabel(wrapper, '监听模式').setValue('proxy')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(wrapper.text()).not.toContain('需要先维护同端口 TCP SOCKS5 入口规则')
    expect(mocks.createMutateAsync).toHaveBeenCalledTimes(1)
    expect(mocks.createMutateAsync.mock.calls[0][0]).toMatchObject({
      protocol: 'udp',
      listen_mode: 'proxy',
      listen_host: '0.0.0.0',
      listen_port: 1080
    })
  })

  it('does not duplicate UDP direct mode auto tags', async () => {
    const wrapper = mountForm()

    await selectByLabel(wrapper, '协议').setValue('udp')
    await flushPromises()
    await selectByLabel(wrapper, '监听端口').setValue('5353')
    await wrapper.get('input[placeholder="IP:端口 或 域名:端口"]').setValue('upstream.local:5353')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(mocks.createMutateAsync).toHaveBeenCalledTimes(1)
    const tags = mocks.createMutateAsync.mock.calls[0][0].tags
    expect(tags.filter((tag) => tag === 'UDP转发')).toHaveLength(1)
  })

  it('allows saving an existing UDP transparent WireGuard rule without backends', async () => {
    const wrapper = mountEditForm({
      id: 7,
      protocol: 'udp',
      listen_host: '0.0.0.0',
      listen_port: 51820,
      listen_mode: 'wireguard',
      proxy_egress_mode: '',
      wireguard_inbound_mode: 'transparent',
      wireguard_profile_id: 21,
      backends: []
    })

    await flushPromises()
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(wrapper.text()).not.toContain('至少需要一个有效的后端服务器')
    expect(mocks.updateMutateAsync).toHaveBeenCalledTimes(1)
    expect(mocks.updateMutateAsync.mock.calls[0][0]).toMatchObject({
      id: 7,
      protocol: 'udp',
      listen_mode: 'wireguard',
      proxy_egress_mode: '',
      wireguard_inbound_mode: 'transparent',
      wireguard_profile_id: 21,
      backends: []
    })
  })

  it('allows selecting transparent mode for new UDP WireGuard rules', async () => {
    const wrapper = mountForm()

    await selectByLabel(wrapper, '协议').setValue('udp')
    await switchTab(wrapper, '协议与监听')
    await selectByLabel(wrapper, '监听模式').setValue('wireguard')
    await selectByLabel(wrapper, 'WireGuard 入站模式').setValue('address')
    await selectByLabel(wrapper, 'WireGuard 入站模式').setValue('transparent')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(wrapper.text()).not.toContain('至少需要一个有效的后端服务器')
    expect(mocks.createMutateAsync).toHaveBeenCalledTimes(1)
    expect(mocks.createMutateAsync.mock.calls[0][0]).toMatchObject({
      protocol: 'udp',
      listen_mode: 'wireguard',
      proxy_egress_mode: '',
      wireguard_inbound_mode: 'transparent',
      wireguard_profile_id: 21,
      backends: []
    })
  })
})

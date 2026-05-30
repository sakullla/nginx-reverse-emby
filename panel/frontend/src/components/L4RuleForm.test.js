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
    data: {
      value: [
        { id: 7, name: 'relay-a', agent_id: 'local', transport_mode: 'tls_tcp' },
        { id: 8, name: 'relay-b', agent_id: 'local', transport_mode: 'tls_tcp' },
        { id: 9, name: 'relay-c', agent_id: 'local', transport_mode: 'tls_tcp' }
      ]
    }
  })
}))

vi.mock('../hooks/useWireGuardProfiles', () => ({
  useWireGuardProfiles: () => ({
    data: { value: [{ id: 21, name: 'wg-default', enabled: true }] }
  })
}))

vi.mock('../hooks/useEgressProfiles', () => ({
  useEgressProfiles: () => ({
    data: {
      value: [
        { id: 31, name: 'tcp http proxy', type: 'http', enabled: true },
        { id: 32, name: 'udp socks proxy', type: 'socks', enabled: true },
        { id: 33, name: 'disabled socks', type: 'socks', enabled: false }
      ]
    }
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

describe('L4RuleForm egress profile and relay path', () => {
  beforeEach(() => {
    mocks.createMutateAsync.mockReset()
    mocks.updateMutateAsync.mockReset()
    mocks.createMutateAsync.mockResolvedValue({})
    mocks.updateMutateAsync.mockResolvedValue({})
  })

  it('keeps egress profile in the protocol tab and removes old egress mode controls', async () => {
    const wrapper = mountForm()

    expect(wrapper.get('select[name="egress-profile"]').exists()).toBe(true)
    await switchTab(wrapper, '协议与监听')

    expect(wrapper.text()).toContain('出口 Profile')
    expect(wrapper.text()).not.toContain('出口模式')
    expect(wrapper.find('input[placeholder="socks://user:pass@127.0.0.1:1080"]').exists()).toBe(false)
    expect(wrapper.find('input[placeholder="wireguard://user:pass@host:51820"]').exists()).toBe(false)
  })

  it('submits relay layers and egress profile id together', async () => {
    const wrapper = mountForm()

    await selectByLabel(wrapper, '监听端口').setValue('25565')
    await wrapper.get('input[placeholder="IP:端口 或 域名:端口"]').setValue('upstream.local:25565')
    await switchTab(wrapper, '协议与监听')
    await wrapper.get('select[name="egress-profile"]').setValue('32')
    await wrapper.findComponent({ name: 'RelayChainInput' }).vm.$emit('update:modelValue', [[7], [8, 9]])
    await flushPromises()
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(mocks.createMutateAsync).toHaveBeenCalledTimes(1)
    const payload = mocks.createMutateAsync.mock.calls[0][0]
    expect(payload).toMatchObject({
      protocol: 'tcp',
      egress_profile_id: 32,
      relay_layers: [[7], [8, 9]]
    })
    expect(payload).not.toHaveProperty('proxy_egress_mode')
    expect(payload).not.toHaveProperty('proxy_egress_url')
    expect(payload).not.toHaveProperty('wireguard_egress_uri')
    expect(payload).not.toHaveProperty('wireguard_profile_override')
  })

  it('preserves relay layers and egress profile id when editing proxy entry rules', async () => {
    const wrapper = mountEditForm({
      id: 9,
      protocol: 'tcp',
      listen_host: '0.0.0.0',
      listen_port: 1080,
      listen_mode: 'proxy',
      egress_profile_id: 32,
      relay_layers: [[7], [8, 9]],
      backends: []
    })

    await flushPromises()
    await switchTab(wrapper, '协议与监听')
    expect(wrapper.text()).not.toContain('出口模式')
    expect(wrapper.get('select[name="egress-profile"]').element.value).toBe('32')

    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(mocks.updateMutateAsync).toHaveBeenCalledTimes(1)
    const payload = mocks.updateMutateAsync.mock.calls[0][0]
    expect(payload).toMatchObject({
      id: 9,
      listen_mode: 'proxy',
      egress_profile_id: 32,
      relay_layers: [[7], [8, 9]]
    })
    expect(payload).not.toHaveProperty('proxy_egress_mode')
    expect(payload).not.toHaveProperty('proxy_egress_url')
    expect(payload).not.toHaveProperty('wireguard_egress_uri')
  })

  it('sends explicit clear value when direct egress is selected while editing', async () => {
    const wrapper = mountEditForm({
      id: 10,
      protocol: 'tcp',
      listen_host: '0.0.0.0',
      listen_port: 9443,
      listen_mode: 'tcp',
      egress_profile_id: 32,
      backends: [{ host: '127.0.0.1', port: 443 }]
    })

    await flushPromises()
    await switchTab(wrapper, '协议与监听')
    await wrapper.get('select[name="egress-profile"]').setValue('0')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(mocks.updateMutateAsync).toHaveBeenCalledTimes(1)
    expect(mocks.updateMutateAsync.mock.calls[0][0]).toMatchObject({
      id: 10,
      egress_profile_id: 0
    })
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

  it('allows port 0 for WireGuard transparent inbound rules without old egress mode', async () => {
    const wrapper = mountForm()

    await selectByLabel(wrapper, '监听端口').setValue('0')
    await switchTab(wrapper, '协议与监听')
    await selectByLabel(wrapper, '监听模式').setValue('wireguard')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(wrapper.text()).not.toContain('监听端口必须在 1-65535 之间')
    expect(mocks.createMutateAsync).toHaveBeenCalledTimes(1)
    const payload = mocks.createMutateAsync.mock.calls[0][0]
    expect(payload).toMatchObject({
      protocol: 'tcp',
      listen_port: 0,
      listen_mode: 'wireguard',
      wireguard_inbound_mode: 'transparent',
      wireguard_profile_id: 21,
      backends: []
    })
    expect(payload).not.toHaveProperty('proxy_egress_mode')
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
      wireguard_inbound_mode: 'transparent',
      wireguard_profile_id: 21,
      backends: []
    })

    await flushPromises()
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(wrapper.text()).not.toContain('至少需要一个有效的后端服务器')
    expect(mocks.updateMutateAsync).toHaveBeenCalledTimes(1)
    const payload = mocks.updateMutateAsync.mock.calls[0][0]
    expect(payload).toMatchObject({
      id: 7,
      protocol: 'udp',
      listen_mode: 'wireguard',
      wireguard_inbound_mode: 'transparent',
      wireguard_profile_id: 21,
      backends: []
    })
    expect(payload).not.toHaveProperty('proxy_egress_mode')
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
    const payload = mocks.createMutateAsync.mock.calls[0][0]
    expect(payload).toMatchObject({
      protocol: 'udp',
      listen_mode: 'wireguard',
      wireguard_inbound_mode: 'transparent',
      wireguard_profile_id: 21,
      backends: []
    })
    expect(payload).not.toHaveProperty('proxy_egress_mode')
  })

  it('filters HTTP egress profiles from UDP rules', async () => {
    const wrapper = mountForm()

    await selectByLabel(wrapper, '协议').setValue('udp')
    await switchTab(wrapper, '协议与监听')

    const options = wrapper
      .get('select[name="egress-profile"]')
      .findAll('option')
      .map((option) => option.text())

    expect(options).toContain('Direct')
    expect(options.join('\n')).toContain('udp socks proxy')
    expect(options.join('\n')).not.toContain('tcp http proxy')
    expect(options.join('\n')).not.toContain('disabled socks')
  })
})

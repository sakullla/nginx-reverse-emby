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

function selectByLabel(wrapper, labelText) {
  const group = wrapper
    .findAll('.form-group')
    .find((item) => item.find('.form-label').exists() && item.find('.form-label').text() === labelText)
  if (!group) throw new Error(`Missing form group: ${labelText}`)
  return group.find('select').exists() ? group.get('select') : group.get('input')
}

describe('L4RuleForm WireGuard egress', () => {
  beforeEach(() => {
    mocks.createMutateAsync.mockReset()
    mocks.updateMutateAsync.mockReset()
    mocks.createMutateAsync.mockResolvedValue({})
    mocks.updateMutateAsync.mockResolvedValue({})
  })

  it('allows WireGuard inbound rules to egress through a WireGuard URI', async () => {
    const wrapper = mountForm()

    await wrapper.findAll('.form-tabs__btn')[1].trigger('click')
    await selectByLabel(wrapper, '监听模式').setValue('wireguard')
    await selectByLabel(wrapper, '出口模式').setValue('wireguard')
    await flushPromises()

    const uriInput = wrapper.get('input[placeholder="wireguard://user:pass@host:51820"]')
    await uriInput.setValue('wireguard://endpoint.example.test?profile=egress')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(mocks.createMutateAsync).toHaveBeenCalledTimes(1)
    expect(mocks.createMutateAsync.mock.calls[0][0]).toMatchObject({
      protocol: 'tcp',
      listen_mode: 'wireguard',
      proxy_egress_mode: 'wireguard',
      wireguard_inbound_mode: 'transparent',
      wireguard_profile_id: 21,
      wireguard_egress_uri: 'wireguard://endpoint.example.test?profile=egress'
    })
  })

  it('derives WireGuard address-mode listen host from the selected profile', async () => {
    const wrapper = mountForm()

    await wrapper.findAll('.form-tabs__btn')[1].trigger('click')
    await selectByLabel(wrapper, '监听模式').setValue('wireguard')
    await selectByLabel(wrapper, 'WireGuard 入站模式').setValue('address')
    await flushPromises()

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

    await wrapper.findAll('.form-tabs__btn')[1].trigger('click')
    await selectByLabel(wrapper, '监听模式').setValue('wireguard')
    await flushPromises()

    expect(selectByLabel(wrapper, 'WireGuard Profile').element.value).toBe('21')

    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(mocks.createMutateAsync).toHaveBeenCalledTimes(1)
    expect(mocks.createMutateAsync.mock.calls[0][0]).toMatchObject({
      listen_mode: 'wireguard',
      wireguard_inbound_mode: 'transparent',
      wireguard_profile_id: 21
    })
  })

  it('does not offer entry authentication for WireGuard proxy egress', async () => {
    const wrapper = mountForm()

    await wrapper.findAll('.form-tabs__btn')[1].trigger('click')
    await selectByLabel(wrapper, '监听模式').setValue('wireguard')
    await selectByLabel(wrapper, '出口模式').setValue('proxy')
    await flushPromises()

    expect(wrapper.text()).not.toContain('启用入口认证')
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
    await wrapper.findAll('.form-tabs__btn')[1].trigger('click')
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
    await wrapper.findAll('.form-tabs__btn')[1].trigger('click')
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
})

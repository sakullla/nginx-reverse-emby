import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import RuleForm from './RuleForm.vue'

const mocks = vi.hoisted(() => ({
  createMutateAsync: vi.fn(),
  updateMutateAsync: vi.fn()
}))

vi.mock('../hooks/useRules', () => ({
  useCreateRule: () => ({
    isPending: { value: false },
    mutateAsync: mocks.createMutateAsync
  }),
  useUpdateRule: () => ({
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

vi.mock('../hooks/useEgressProfiles', () => ({
  useEgressProfiles: () => ({
    data: {
      value: [
        { id: 17, name: 'office socks', type: 'socks', enabled: true },
        { id: 18, name: 'disabled http', type: 'http', enabled: false }
      ]
    }
  })
}))

vi.mock('../context/AgentContext', () => ({
  useAgent: () => ({
    systemInfo: { value: {} }
  })
}))

function mountForm() {
  return mount(RuleForm, {
    props: { agentId: 'local' },
    global: {
      stubs: {
        RouterLink: true,
        RelayChainInput: true
      }
    }
  })
}

function mountEditForm(initialData) {
  return mount(RuleForm, {
    props: { agentId: 'local', initialData },
    global: {
      stubs: {
        RouterLink: true,
        RelayChainInput: true
      }
    }
  })
}

describe('RuleForm WireGuard entry', () => {
  beforeEach(() => {
    mocks.createMutateAsync.mockReset()
    mocks.updateMutateAsync.mockReset()
    mocks.createMutateAsync.mockResolvedValue({})
    mocks.updateMutateAsync.mockResolvedValue({})
  })

  it('derives WireGuard entry host from the selected profile', async () => {
    const wrapper = mountForm()

    await wrapper.get('#frontend-url').setValue('https://app.example.test')
    await wrapper.get('#backend-url').setValue('http://origin.example.test:8096')
    await wrapper.findAll('.form-tabs__btn')[1].trigger('click')
    const wireGuardToggle = wrapper.findAll('label.toggle')
      .find((item) => item.text().includes('启用内网 IP 访问入口'))
    expect(wireGuardToggle).toBeTruthy()
    await wireGuardToggle.get('input').setValue(true)
    await flushPromises()

    expect(wrapper.find('#wireguard-entry-listen-host').exists()).toBe(false)

    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(mocks.createMutateAsync).toHaveBeenCalledTimes(1)
    expect(mocks.createMutateAsync.mock.calls[0][0]).toMatchObject({
      frontend_url: 'https://app.example.test',
      wireguard_entry_enabled: true,
      wireguard_profile_id: 21
    })
    expect(mocks.createMutateAsync.mock.calls[0][0]).not.toHaveProperty('wireguard_entry_listen_host')
    expect(mocks.createMutateAsync.mock.calls[0][0]).not.toHaveProperty('wireguard_entry_listen_port')
  })

  it('submits selected HTTP egress profile id', async () => {
    const wrapper = mountForm()

    await wrapper.get('#frontend-url').setValue('https://app.example.test')
    await wrapper.get('#backend-url').setValue('http://origin.example.test:8096')
    await wrapper.get('select[name="egress-profile"]').setValue('17')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(mocks.createMutateAsync).toHaveBeenCalledTimes(1)
    expect(mocks.createMutateAsync.mock.calls[0][0]).toMatchObject({
      frontend_url: 'https://app.example.test',
      egress_profile_id: 17
    })
  })

  it('submits direct HTTP egress without profile id', async () => {
    const wrapper = mountForm()

    await wrapper.get('#frontend-url').setValue('https://app.example.test')
    await wrapper.get('#backend-url').setValue('http://origin.example.test:8096')
    await wrapper.get('select[name="egress-profile"]').setValue('0')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(mocks.createMutateAsync).toHaveBeenCalledTimes(1)
    expect(mocks.createMutateAsync.mock.calls[0][0]).not.toHaveProperty('egress_profile_id')
  })

  it('sends explicit clear value when direct egress is selected while editing', async () => {
    const wrapper = mountEditForm({
      id: 9,
      frontend_url: 'https://app.example.test',
      backends: [{ url: 'http://origin.example.test:8096' }],
      egress_profile_id: 17,
      enabled: true
    })

    await wrapper.get('select[name="egress-profile"]').setValue('0')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(mocks.updateMutateAsync).toHaveBeenCalledTimes(1)
    expect(mocks.updateMutateAsync.mock.calls[0][0]).toMatchObject({
      id: 9,
      egress_profile_id: 0
    })
  })
})

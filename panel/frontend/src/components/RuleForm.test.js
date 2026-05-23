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
})

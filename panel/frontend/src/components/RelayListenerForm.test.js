import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import RelayListenerForm from './RelayListenerForm.vue'

const mocks = vi.hoisted(() => ({
  createMutateAsync: vi.fn(),
  updateMutateAsync: vi.fn(),
  wireGuardProfiles: [
    { id: 21, name: 'wg-default', enabled: true },
    { id: 22, name: 'wg-override', enabled: true },
    { id: 23, name: 'wg-disabled', enabled: false }
  ]
}))

vi.mock('../hooks/useRelayListeners', () => ({
  useCreateRelayListener: () => ({
    isPending: { value: false },
    mutateAsync: mocks.createMutateAsync
  }),
  useUpdateRelayListener: () => ({
    isPending: { value: false },
    mutateAsync: mocks.updateMutateAsync
  })
}))

vi.mock('../hooks/useCertificates', () => ({
  useCertificates: () => ({
    data: { value: [] }
  })
}))

vi.mock('../hooks/useWireGuardProfiles', () => ({
  useWireGuardProfiles: () => ({
    data: { value: mocks.wireGuardProfiles }
  })
}))

function mountForm(props = {}) {
  return mount(RelayListenerForm, {
    props: {
      agentId: 'local',
      ...props
    }
  })
}

function selectByLabel(wrapper, labelText) {
  const group = wrapper
    .findAll('.form-group')
    .find((item) => item.find('.form-label').exists() && item.find('.form-label').text() === labelText)
  if (!group) throw new Error(`Missing form group: ${labelText}`)
  return group.get('select')
}

async function fillValidWireGuardForm(wrapper) {
  await wrapper.get('input[placeholder="relay-a"]').setValue('relay-wg')
  await wrapper.get('input[type="number"]').setValue(7443)
  await selectByLabel(wrapper, 'Relay Transport').setValue('wireguard')
}

async function submit(wrapper) {
  await wrapper.get('form').trigger('submit')
  await flushPromises()
}

function baseInitialData(overrides = {}) {
  return {
    id: 7,
    name: 'relay-existing',
    bind_hosts: ['0.0.0.0'],
    listen_port: 7443,
    transport_mode: 'wireguard',
    enabled: true,
    certificate_source: 'auto_relay_ca',
    trust_mode_source: 'auto',
    ...overrides
  }
}

describe('RelayListenerForm WireGuard profile override', () => {
  beforeEach(() => {
    mocks.createMutateAsync.mockReset()
    mocks.updateMutateAsync.mockReset()
    mocks.createMutateAsync.mockResolvedValue({})
    mocks.updateMutateAsync.mockResolvedValue({})
  })

  it('omits wireguard_profile_id for ordinary WireGuard submissions without an advanced override', async () => {
    const wrapper = mountForm()

    await fillValidWireGuardForm(wrapper)
    await submit(wrapper)

    expect(mocks.createMutateAsync).toHaveBeenCalledTimes(1)
    expect(mocks.createMutateAsync.mock.calls[0][0]).toMatchObject({
      name: 'relay-wg',
      transport_mode: 'wireguard',
      allow_transport_fallback: false,
      obfs_mode: 'off'
    })
    expect(mocks.createMutateAsync.mock.calls[0][0]).not.toHaveProperty('wireguard_profile_id')
  })

  it('hides WireGuard public endpoint and bind host inputs in the ordinary flow', async () => {
    const wrapper = mountForm()

    await fillValidWireGuardForm(wrapper)

    expect(wrapper.text()).not.toContain('WireGuard 配置 Endpoint（可选）')
    expect(wrapper.text()).not.toContain('绑定地址（每行一个）')
    expect(wrapper.text()).not.toContain('作为公网 UDP 入口')
    expect(wrapper.text()).not.toContain('默认使用 TLS/TCP；如需更低握手耗时')
  })

  it('omits WireGuard bind hosts and relay public endpoint fields from submissions', async () => {
    const wrapper = mountForm()

    await fillValidWireGuardForm(wrapper)
    await submit(wrapper)

    expect(mocks.createMutateAsync).toHaveBeenCalledTimes(1)
    const payload = mocks.createMutateAsync.mock.calls[0][0]
    expect(payload.transport_mode).toBe('wireguard')
    expect(payload).not.toHaveProperty('bind_hosts')
    expect(payload).not.toHaveProperty('public_host')
    expect(payload).not.toHaveProperty('public_port')
  })

  it('includes wireguard_profile_id when the advanced WireGuard override is selected', async () => {
    const wrapper = mountForm()

    await fillValidWireGuardForm(wrapper)
    await wrapper.get('.advanced-toggle').trigger('click')
    await selectByLabel(wrapper, 'WireGuard 配置').setValue('22')
    await submit(wrapper)

    expect(mocks.createMutateAsync).toHaveBeenCalledTimes(1)
    expect(mocks.createMutateAsync.mock.calls[0][0]).toMatchObject({
      transport_mode: 'wireguard',
      wireguard_profile_id: 22
    })
  })

  it('opens the advanced panel with the explicit existing WireGuard profile selected while editing', () => {
    const wrapper = mountForm({
      initialData: baseInitialData({ wireguard_profile_id: 22 })
    })

    expect(wrapper.find('.advanced-panel').exists()).toBe(true)
    expect(selectByLabel(wrapper, 'WireGuard 配置').element.value).toBe('22')
  })

  it('keeps blank existing WireGuard profile IDs on the ordinary automatic profile path while editing', () => {
    const wrapper = mountForm({
      initialData: baseInitialData({ wireguard_profile_id: '' })
    })

    expect(wrapper.find('.advanced-panel').exists()).toBe(false)

    return wrapper.get('.advanced-toggle').trigger('click').then(() => {
      expect(selectByLabel(wrapper, 'WireGuard 配置').element.value).toBe('')
    })
  })
})

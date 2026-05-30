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

describe('RelayListenerForm WireGuard transport', () => {
  beforeEach(() => {
    mocks.createMutateAsync.mockReset()
    mocks.updateMutateAsync.mockReset()
    mocks.createMutateAsync.mockResolvedValue({})
    mocks.updateMutateAsync.mockResolvedValue({})
  })

  it('submits WireGuard transport on the automatic profile path', async () => {
    const wrapper = mountForm()

    await fillValidWireGuardForm(wrapper)
    await submit(wrapper)

    expect(mocks.createMutateAsync).toHaveBeenCalledTimes(1)
    expect(mocks.createMutateAsync.mock.calls[0][0]).toMatchObject({
      name: 'relay-wg',
      transport_mode: 'wireguard',
      allow_transport_fallback: false,
      obfs_mode: 'off',
      certificate_source: 'auto_relay_ca',
      trust_mode_source: 'auto',
      tls_mode: 'pin_and_ca'
    })
    expect(mocks.createMutateAsync.mock.calls[0][0]).not.toHaveProperty('wireguard_profile_id')
  })

  it('keeps Relay bind host and public endpoint inputs visible for WireGuard transport', async () => {
    const wrapper = mountForm()

    await fillValidWireGuardForm(wrapper)

    expect(wrapper.text()).toContain('绑定地址（每行一个）')
    expect(wrapper.text()).toContain('公网入口（可选）')
    expect(wrapper.text()).toContain('支持空值、host、host:port')
    expect(wrapper.text()).not.toContain('默认使用 TLS/TCP；如需更低握手耗时')
  })

  it('states that WireGuard relay still uses Relay TLS authentication', async () => {
    const wrapper = mountForm()

    await fillValidWireGuardForm(wrapper)

    expect(wrapper.text()).toContain('Relay TLS')
    expect(wrapper.text()).toContain('证书 / Pin')
  })

  it('maps Relay bind hosts, listen port, and public endpoint to WireGuard submissions', async () => {
    const wrapper = mountForm()

    await fillValidWireGuardForm(wrapper)
    await wrapper.get('textarea').setValue('0.0.0.0\n127.0.0.1')
    await wrapper.get('input[placeholder="relay.example.com:7443"]').setValue('relay.example.com:7443')
    await submit(wrapper)

    expect(mocks.createMutateAsync).toHaveBeenCalledTimes(1)
    const payload = mocks.createMutateAsync.mock.calls[0][0]
    expect(payload.transport_mode).toBe('wireguard')
    expect(payload.listen_port).toBe(7443)
    expect(payload.bind_hosts).toEqual(['0.0.0.0', '127.0.0.1'])
    expect(payload.public_host).toBe('relay.example.com')
    expect(payload.public_port).toBe(7443)
    expect(payload).not.toHaveProperty('wireguard_profile_id')
  })

  it('does not expose WireGuard profile selection in advanced settings', async () => {
    const wrapper = mountForm({
      initialData: baseInitialData({ wireguard_profile_id: 22 })
    })

    await wrapper.get('.advanced-toggle').trigger('click')

    expect(wrapper.text()).not.toContain('WireGuard 配置')
    expect(wrapper.find('.advanced-panel').exists()).toBe(true)
  })
})

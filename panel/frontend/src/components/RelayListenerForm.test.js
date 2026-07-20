import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import RelayListenerForm from './RelayListenerForm.vue'

const mocks = vi.hoisted(() => ({
  createMutateAsync: vi.fn(),
  updateMutateAsync: vi.fn()
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

async function fillRequiredFields(wrapper) {
  await wrapper.get('input[placeholder="relay-a"]').setValue('relay-main')
  await wrapper.get('input[type="number"]').setValue(7443)
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
    transport_mode: 'tls_tcp',
    enabled: true,
    certificate_source: 'auto_relay_ca',
    trust_mode_source: 'auto',
    ...overrides
  }
}

describe('RelayListenerForm transport behavior', () => {
  beforeEach(() => {
    mocks.createMutateAsync.mockReset()
    mocks.updateMutateAsync.mockReset()
    mocks.createMutateAsync.mockResolvedValue({})
    mocks.updateMutateAsync.mockResolvedValue({})
  })

  it('submits the default TLS/TCP transport with automatic trust', async () => {
    const wrapper = mountForm()

    await fillRequiredFields(wrapper)
    await submit(wrapper)

    expect(mocks.createMutateAsync).toHaveBeenCalledTimes(1)
    expect(mocks.createMutateAsync.mock.calls[0][0]).toMatchObject({
      name: 'relay-main',
      transport_mode: 'tls_tcp',
      allow_transport_fallback: true,
      obfs_mode: 'off',
      certificate_source: 'auto_relay_ca',
      trust_mode_source: 'auto',
      tls_mode: 'pin_and_ca'
    })
  })

  it('offers only TLS/TCP and QUIC transports', () => {
    const wrapper = mountForm()

    expect(selectByLabel(wrapper, 'Relay Transport').findAll('option').map((option) => option.element.value)).toEqual([
      'tls_tcp',
      'quic'
    ])
  })

  it('maps bind hosts and the public endpoint into QUIC submissions', async () => {
    const wrapper = mountForm()

    await fillRequiredFields(wrapper)
    await selectByLabel(wrapper, 'Relay Transport').setValue('quic')
    await wrapper.get('textarea').setValue('0.0.0.0\n127.0.0.1')
    await wrapper.get('input[placeholder="relay.example.com:7443"]').setValue('relay.example.com:7443')
    await submit(wrapper)

    expect(mocks.createMutateAsync).toHaveBeenCalledTimes(1)
    expect(mocks.createMutateAsync.mock.calls[0][0]).toMatchObject({
      transport_mode: 'quic',
      allow_transport_fallback: true,
      obfs_mode: 'off',
      listen_port: 7443,
      bind_hosts: ['0.0.0.0', '127.0.0.1'],
      public_host: 'relay.example.com',
      public_port: 7443
    })
  })

  it('preserves an explicitly disabled QUIC fallback while editing', async () => {
    const wrapper = mountForm({
      initialData: baseInitialData({
        transport_mode: 'quic',
        allow_transport_fallback: false
      })
    })

    await submit(wrapper)

    expect(mocks.updateMutateAsync).toHaveBeenCalledTimes(1)
    expect(mocks.updateMutateAsync.mock.calls[0][0]).toMatchObject({
      id: 7,
      transport_mode: 'quic',
      allow_transport_fallback: false,
      obfs_mode: 'off'
    })
  })
})

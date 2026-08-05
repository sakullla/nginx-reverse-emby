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
  await wrapper.get('input[placeholder="例如 hk-edge-1"]').setValue('relay-main')
  await wrapper.get('input[type="number"]').setValue(7443)
  await wrapper.get('input[placeholder="relay.example.com:7443"]').setValue('relay.example.com:7443')
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
    public_host: 'relay.example.com',
    public_port: 7443,
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

  it('starts with an empty listen port instead of zero', () => {
    const wrapper = mountForm()
    const portInput = wrapper.get('input[type="number"]')
    expect(portInput.element.value).toBe('')
    expect(wrapper.find('.settings-card').exists()).toBe(true)
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
    await wrapper.get('textarea.textarea--hosts').setValue('0.0.0.0\n127.0.0.1')
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

  it('rejects empty bind hosts and zero ports before submit', async () => {
    const wrapper = mountForm()

    await wrapper.get('input[placeholder="例如 hk-edge-1"]').setValue('relay-main')
    await wrapper.get('textarea.textarea--hosts').setValue('   \n  ')
    await wrapper.get('input[type="number"]').setValue(0)
    await submit(wrapper)

    expect(mocks.createMutateAsync).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('请至少填写一个绑定地址')
    expect(wrapper.text()).toContain('监听端口必须在 1-65535 之间')
  })

  it('requires a public endpoint when every bind host is unspecified', async () => {
    const wrapper = mountForm()

    await wrapper.get('input[placeholder="例如 hk-edge-1"]').setValue('relay-main')
    await wrapper.get('input[type="number"]').setValue(7443)
    await submit(wrapper)

    expect(mocks.createMutateAsync).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('使用通配绑定地址时必须填写公网入口')
  })

  it('allows a concrete bind host to serve as the certificate endpoint', async () => {
    const wrapper = mountForm()

    await wrapper.get('input[placeholder="例如 hk-edge-1"]').setValue('relay-main')
    await wrapper.get('input[type="number"]').setValue(7443)
    await wrapper.get('textarea.textarea--hosts').setValue('127.0.0.1')
    await submit(wrapper)

    expect(mocks.createMutateAsync).toHaveBeenCalledTimes(1)
    expect(mocks.createMutateAsync.mock.calls[0][0]).toMatchObject({ bind_hosts: ['127.0.0.1'] })
    expect(mocks.createMutateAsync.mock.calls[0][0]).not.toHaveProperty('public_host')
  })

  it('rejects an unspecified public endpoint', async () => {
    const wrapper = mountForm()

    await wrapper.get('input[placeholder="例如 hk-edge-1"]').setValue('relay-main')
    await wrapper.get('input[type="number"]').setValue(7443)
    await wrapper.get('input[placeholder="relay.example.com:7443"]').setValue('0.0.0.0:7443')
    await submit(wrapper)

    expect(mocks.createMutateAsync).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('公网入口必须是具体的 DNS 名称或 IP 地址')
  })

  it.each(['*', 'bad..name', '-bad.example.test', 'bad_.example.test'])(
    'rejects invalid certificate endpoint %s',
    async (endpoint) => {
      const wrapper = mountForm()

      await wrapper.get('input[placeholder="例如 hk-edge-1"]').setValue('relay-main')
      await wrapper.get('input[type="number"]').setValue(7443)
      await wrapper.get('input[placeholder="relay.example.com:7443"]').setValue(endpoint)
      await submit(wrapper)

      expect(mocks.createMutateAsync).not.toHaveBeenCalled()
      expect(wrapper.text()).toContain('公网入口必须是具体的 DNS 名称或 IP 地址')
    }
  )

  it('expands advanced settings when switching trust mode to custom', async () => {
    const wrapper = mountForm()

    expect(wrapper.find('.advanced-panel').exists()).toBe(false)
    await selectByLabel(wrapper, '信任策略').setValue('custom')
    await flushPromises()

    expect(wrapper.find('.advanced-panel').exists()).toBe(true)
    expect(wrapper.text()).toContain('自定义信任')
  })
})

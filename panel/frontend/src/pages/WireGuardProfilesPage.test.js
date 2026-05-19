import { flushPromises, mount } from '@vue/test-utils'
import { ref } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import WireGuardProfilesPage from './WireGuardProfilesPage.vue'
import * as api from '../api'

const mocks = vi.hoisted(() => ({
  updateMutate: vi.fn(),
  deleteClientMutate: vi.fn(),
  qrCodeToDataURL: vi.fn(),
  routeMock: { query: { agentId: 'local' }, params: {} },
  routerReplace: vi.fn(),
  routerPush: vi.fn()
}))

vi.mock('vue-router', () => ({
  useRoute: () => mocks.routeMock,
  useRouter: () => ({ replace: mocks.routerReplace, push: mocks.routerPush })
}))

vi.mock('@tanstack/vue-query', () => ({
  useQuery: () => ({
    data: ref([
      {
        id: 1,
        name: 'phone',
        address: '10.8.0.2/32',
        public_key: 'client-public-key',
        enabled: true
      },
      {
        id: 2,
        name: 'tablet',
        address: '10.8.0.3/32',
        public_key: 'second-client-public-key',
        enabled: true
      }
    ]),
    isLoading: ref(false)
  })
}))

vi.mock('../context/AgentContext', () => ({
  useAgent: () => ({ selectedAgentId: ref('local') })
}))

vi.mock('../hooks/useAgents', () => ({
  useAgents: () => ({
    data: ref([{ id: 'local', name: 'local' }])
  })
}))

vi.mock('../api', () => ({
  fetchWireGuardClients: vi.fn(),
  fetchWireGuardClientConfig: vi.fn(),
  fetchWireGuardClientURI: vi.fn()
}))

vi.mock('qrcode', () => ({
  default: {
    toDataURL: mocks.qrCodeToDataURL
  }
}))

vi.mock('../hooks/useWireGuardProfiles', () => ({
  useWireGuardProfiles: () => ({
    data: ref([
      {
        id: 7,
        name: 'wg-main',
        enabled: true,
        listen_port: 51820,
        addresses: ['10.8.0.1/24'],
        peers: [],
        public_endpoint: 'wg.example.com:51820'
      },
      {
        id: 8,
        name: 'wg-backup',
        enabled: true,
        listen_port: 51821,
        addresses: ['10.9.0.1/24'],
        peers: [],
        public_endpoint: 'wg-backup.example.com:51821'
      }
    ]),
    isLoading: ref(false)
  }),
  useCreateWireGuardProfile: () => ({ isPending: ref(false), mutateAsync: vi.fn() }),
  useUpdateWireGuardProfile: () => ({ isPending: ref(false), mutateAsync: vi.fn() }),
  useDeleteWireGuardProfile: () => ({ isPending: ref(false), mutate: vi.fn() }),
  useCreateWireGuardClient: () => ({ isPending: ref(false), mutateAsync: vi.fn() }),
  useUpdateWireGuardClient: () => ({ isPending: ref(false), mutate: mocks.updateMutate, mutateAsync: mocks.updateMutate }),
  useDeleteWireGuardClient: () => ({ isPending: ref(false), mutate: mocks.deleteClientMutate, mutateAsync: mocks.deleteClientMutate })
}))

function mountPage() {
  return mount(WireGuardProfilesPage, {
    global: {
      stubs: {
        QuickAgentSelect: true,
        RouterLink: true,
        BaseModal: {
          props: ['modelValue', 'title'],
          emits: ['update:modelValue'],
          template: `
            <section v-if="modelValue" class="base-modal-stub" :data-title="title">
              <button class="base-modal-stub__close" @click="$emit('update:modelValue', false)">close</button>
              <slot />
            </section>
          `
        },
        DeleteConfirmDialog: true
      }
    }
  })
}

function deferred() {
  let resolve
  let reject
  const promise = new Promise((done, fail) => {
    resolve = done
    reject = fail
  })
  return { promise, resolve, reject }
}

async function openClients(wrapper) {
  wrapper.vm.selectedProfileId = 7
  await wrapper.vm.$nextTick()
  await flushPromises()
}

function clientActionButtons(wrapper, rowIndex = 0) {
  return wrapper.findAll('.client-row').at(rowIndex).findAll('.client-row__actions .base-icon-button')
}

function clientActionButton(wrapper, title, rowIndex = 0) {
  const button = clientActionButtons(wrapper, rowIndex).find((item) => item.attributes('title') === title)
  if (!button) throw new Error(`Missing client action button: ${title}`)
  return button
}

describe('WireGuardProfilesPage client row actions', () => {
  beforeEach(() => {
    mocks.updateMutate.mockReset()
    mocks.deleteClientMutate.mockReset()
    mocks.qrCodeToDataURL.mockReset()
    mocks.qrCodeToDataURL.mockResolvedValue('data:image/png;base64,qr')
    api.fetchWireGuardClientConfig.mockReset()
    api.fetchWireGuardClientURI.mockReset()
    mocks.routeMock.params = {}
    mocks.routerReplace.mockReset()
    mocks.routerPush.mockReset()
  })

  it('disables client row actions while a client toggle is pending', async () => {
    mocks.updateMutate.mockReturnValue(new Promise(() => {}))
    const wrapper = mountPage()

    await openClients(wrapper)
    const buttons = clientActionButtons(wrapper)
    expect(buttons.map((button) => button.attributes('title'))).toEqual([
      '编辑', '停用', '下载配置', '二维码', '复制 URI', '删除'
    ])
    expect(buttons.every((button) => button.attributes('disabled') === undefined)).toBe(true)

    await clientActionButton(wrapper, '停用').trigger('click')

    expect(mocks.updateMutate.mock.calls[0][0]).toEqual({ clientId: 1, enabled: false })
    expect(clientActionButtons(wrapper).every((button) => button.attributes('disabled') !== undefined)).toBe(true)
    expect(clientActionButtons(wrapper, 1).every((button) => button.attributes('disabled') === undefined)).toBe(true)
  })

  it('keeps a pending client row scoped to the selected profile', async () => {
    mocks.updateMutate.mockReturnValue(new Promise(() => {}))
    const wrapper = mountPage()

    await openClients(wrapper)
    await clientActionButton(wrapper, '停用').trigger('click')
    expect(clientActionButtons(wrapper).every((button) => button.attributes('disabled') !== undefined)).toBe(true)

    // Switch to second profile
    wrapper.vm.selectedProfileId = 8
    await wrapper.vm.$nextTick()
    await flushPromises()

    expect(wrapper.text()).toContain('wg-backup')
    expect(wrapper.findAll('.client-row__actions .base-icon-button').every((button) => button.attributes('disabled') === undefined)).toBe(true)
  })

  it('disables client row actions while a client delete is pending', async () => {
    mocks.deleteClientMutate.mockReturnValue(new Promise(() => {}))
    const wrapper = mountPage()

    await openClients(wrapper)
    await clientActionButton(wrapper, '删除').trigger('click')

    expect(mocks.deleteClientMutate.mock.calls[0][0]).toBe(1)
    expect(clientActionButtons(wrapper).every((button) => button.attributes('disabled') !== undefined)).toBe(true)
    expect(clientActionButtons(wrapper, 1).every((button) => button.attributes('disabled') === undefined)).toBe(true)
  })

  it('clears pending state for overlapping client row operations when both settle', async () => {
    const deferreds = []
    mocks.updateMutate.mockImplementation(() => {
      let resolve
      const promise = new Promise((done) => {
        resolve = done
      })
      deferreds.push({ promise, resolve })
      return promise
    })
    const wrapper = mountPage()

    await openClients(wrapper)
    await clientActionButton(wrapper, '停用').trigger('click')
    await clientActionButton(wrapper, '停用', 1).trigger('click')

    expect(mocks.updateMutate.mock.calls[0][0]).toEqual({ clientId: 1, enabled: false })
    expect(mocks.updateMutate.mock.calls[1][0]).toEqual({ clientId: 2, enabled: false })
    expect(clientActionButtons(wrapper).every((button) => button.attributes('disabled') !== undefined)).toBe(true)
    expect(clientActionButtons(wrapper, 1).every((button) => button.attributes('disabled') !== undefined)).toBe(true)

    deferreds[1].resolve()
    await deferreds[1].promise
    await wrapper.vm.$nextTick()

    expect(clientActionButtons(wrapper).every((button) => button.attributes('disabled') !== undefined)).toBe(true)
    expect(clientActionButtons(wrapper, 1).every((button) => button.attributes('disabled') === undefined)).toBe(true)

    deferreds[0].resolve()
    await deferreds[0].promise
    await wrapper.vm.$nextTick()

    expect(clientActionButtons(wrapper).every((button) => button.attributes('disabled') === undefined)).toBe(true)
    expect(clientActionButtons(wrapper, 1).every((button) => button.attributes('disabled') === undefined)).toBe(true)
  })

  it('opens the QR modal by fetching client config and rendering a QR image', async () => {
    api.fetchWireGuardClientConfig.mockResolvedValue('[Interface]\nAddress = 10.8.0.2/32\n')
    const wrapper = mountPage()

    await openClients(wrapper)
    await clientActionButton(wrapper, '二维码').trigger('click')
    await flushPromises()

    expect(api.fetchWireGuardClientConfig).toHaveBeenCalledWith('local', 7, 1)
    expect(mocks.qrCodeToDataURL).toHaveBeenCalledWith('[Interface]\nAddress = 10.8.0.2/32\n', {
      errorCorrectionLevel: 'M',
      margin: 2,
      width: 280
    })
    expect(wrapper.get('.base-modal-stub[data-title="phone QR"]').exists()).toBe(true)
    expect(wrapper.get('.client-qr__image').attributes('src')).toBe('data:image/png;base64,qr')
    expect(wrapper.get('.client-qr__config').element.value).toBe('[Interface]\nAddress = 10.8.0.2/32\n')
  })

  it('keeps config text fallback visible when QR image generation fails', async () => {
    api.fetchWireGuardClientConfig.mockResolvedValue('[Interface]\nAddress = 10.8.0.2/32\n')
    mocks.qrCodeToDataURL.mockRejectedValue(new Error('QR too large'))
    const wrapper = mountPage()

    await openClients(wrapper)
    await clientActionButton(wrapper, '二维码').trigger('click')
    await flushPromises()

    expect(wrapper.get('.base-modal-stub[data-title="phone QR"]').exists()).toBe(true)
    expect(wrapper.find('.client-qr__image').exists()).toBe(false)
    expect(wrapper.get('.client-qr__config').element.value).toBe('[Interface]\nAddress = 10.8.0.2/32\n')
    expect(wrapper.text()).toContain('二维码生成失败，请使用配置文本。')
  })

  it('does not allow an older QR request to overwrite the latest modal state', async () => {
    const firstRequest = deferred()
    api.fetchWireGuardClientConfig
      .mockReturnValueOnce(firstRequest.promise)
      .mockResolvedValueOnce('[Interface]\nAddress = 10.8.0.3/32\n')
    mocks.qrCodeToDataURL.mockResolvedValueOnce('data:image/png;base64,tablet-qr')
    const wrapper = mountPage()

    await openClients(wrapper)
    await clientActionButton(wrapper, '二维码').trigger('click')
    await clientActionButton(wrapper, '二维码', 1).trigger('click')
    await flushPromises()

    expect(wrapper.get('.base-modal-stub[data-title="tablet QR"]').exists()).toBe(true)
    expect(wrapper.get('.client-qr__config').element.value).toBe('[Interface]\nAddress = 10.8.0.3/32\n')

    firstRequest.resolve('[Interface]\nAddress = 10.8.0.2/32\n')
    await flushPromises()

    expect(wrapper.get('.base-modal-stub[data-title="tablet QR"]').exists()).toBe(true)
    expect(wrapper.get('.client-qr__config').element.value).toBe('[Interface]\nAddress = 10.8.0.3/32\n')
    expect(wrapper.get('.client-qr__image').attributes('src')).toBe('data:image/png;base64,tablet-qr')
  })
})

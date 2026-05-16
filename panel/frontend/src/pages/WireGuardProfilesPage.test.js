import { mount } from '@vue/test-utils'
import { ref } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import WireGuardProfilesPage from './WireGuardProfilesPage.vue'

const mocks = vi.hoisted(() => ({
  updateMutate: vi.fn(),
  deleteClientMutate: vi.fn()
}))

vi.mock('vue-router', () => ({
  useRoute: () => ({ query: { agentId: 'local' } }),
  useRouter: () => ({ replace: vi.fn() })
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
  fetchWireGuardClientConfig: vi.fn()
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
        BaseModal: true,
        DeleteConfirmDialog: true
      }
    }
  })
}

describe('WireGuardProfilesPage client row actions', () => {
  beforeEach(() => {
    mocks.updateMutate.mockReset()
    mocks.deleteClientMutate.mockReset()
  })

  it('disables toggle, download, and delete while a client toggle is pending', async () => {
    mocks.updateMutate.mockReturnValue(new Promise(() => {}))
    const wrapper = mountPage()

    await wrapper.get('.profile-card__actions .btn').trigger('click')
    const buttons = wrapper.findAll('.client-row').at(0).findAll('.client-row__actions .btn')
    expect(buttons).toHaveLength(3)
    expect(buttons.every((button) => button.attributes('disabled') === undefined)).toBe(true)

    await buttons[0].trigger('click')

    expect(mocks.updateMutate.mock.calls[0][0]).toEqual({ clientId: 1, enabled: false })
    expect(wrapper.findAll('.client-row').at(0).findAll('.client-row__actions .btn').every((button) => button.attributes('disabled') !== undefined)).toBe(true)
    expect(wrapper.findAll('.client-row').at(1).findAll('.client-row__actions .btn').every((button) => button.attributes('disabled') === undefined)).toBe(true)
  })

  it('keeps a pending client row scoped to the selected profile', async () => {
    mocks.updateMutate.mockReturnValue(new Promise(() => {}))
    const wrapper = mountPage()

    const profileButtons = wrapper.findAll('.profile-card__actions .btn')
    await profileButtons[0].trigger('click')
    await wrapper.findAll('.client-row').at(0).findAll('.client-row__actions .btn')[0].trigger('click')
    expect(wrapper.findAll('.client-row').at(0).findAll('.client-row__actions .btn').every((button) => button.attributes('disabled') !== undefined)).toBe(true)

    await profileButtons[3].trigger('click')

    expect(wrapper.text()).toContain('wg-backup Clients')
    expect(wrapper.findAll('.client-row__actions .btn').every((button) => button.attributes('disabled') === undefined)).toBe(true)
  })

  it('disables toggle, download, and delete while a client delete is pending', async () => {
    mocks.deleteClientMutate.mockReturnValue(new Promise(() => {}))
    const wrapper = mountPage()

    await wrapper.get('.profile-card__actions .btn').trigger('click')
    const buttons = wrapper.findAll('.client-row').at(0).findAll('.client-row__actions .btn')
    await buttons[2].trigger('click')

    expect(mocks.deleteClientMutate.mock.calls[0][0]).toBe(1)
    expect(wrapper.findAll('.client-row').at(0).findAll('.client-row__actions .btn').every((button) => button.attributes('disabled') !== undefined)).toBe(true)
    expect(wrapper.findAll('.client-row').at(1).findAll('.client-row__actions .btn').every((button) => button.attributes('disabled') === undefined)).toBe(true)
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

    await wrapper.get('.profile-card__actions .btn').trigger('click')
    const clientRows = wrapper.findAll('.client-row')
    await clientRows.at(0).findAll('.client-row__actions .btn')[0].trigger('click')
    await clientRows.at(1).findAll('.client-row__actions .btn')[0].trigger('click')

    expect(mocks.updateMutate.mock.calls[0][0]).toEqual({ clientId: 1, enabled: false })
    expect(mocks.updateMutate.mock.calls[1][0]).toEqual({ clientId: 2, enabled: false })
    expect(wrapper.findAll('.client-row').at(0).findAll('.client-row__actions .btn').every((button) => button.attributes('disabled') !== undefined)).toBe(true)
    expect(wrapper.findAll('.client-row').at(1).findAll('.client-row__actions .btn').every((button) => button.attributes('disabled') !== undefined)).toBe(true)

    deferreds[1].resolve()
    await deferreds[1].promise
    await wrapper.vm.$nextTick()

    expect(wrapper.findAll('.client-row').at(0).findAll('.client-row__actions .btn').every((button) => button.attributes('disabled') !== undefined)).toBe(true)
    expect(wrapper.findAll('.client-row').at(1).findAll('.client-row__actions .btn').every((button) => button.attributes('disabled') === undefined)).toBe(true)

    deferreds[0].resolve()
    await deferreds[0].promise
    await wrapper.vm.$nextTick()

    expect(wrapper.findAll('.client-row').at(0).findAll('.client-row__actions .btn').every((button) => button.attributes('disabled') === undefined)).toBe(true)
    expect(wrapper.findAll('.client-row').at(1).findAll('.client-row__actions .btn').every((button) => button.attributes('disabled') === undefined)).toBe(true)
  })
})

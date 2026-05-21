import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import WireGuardProfileCard from './WireGuardProfileCard.vue'

const mocks = vi.hoisted(() => ({
  route: { query: { agentId: 'edge-2' } },
  push: vi.fn()
}))

vi.mock('vue-router', () => ({
  useRoute: () => mocks.route,
  useRouter: () => ({ push: mocks.push })
}))

function mountCard() {
  return mount(WireGuardProfileCard, {
    props: {
      profile: {
        id: 7,
        name: 'wg-main',
        enabled: true,
        addresses: ['10.8.0.1/24'],
        tags: []
      },
      clientCount: 2
    },
    global: {
      stubs: {
        BaseListCard: {
          name: 'BaseListCard',
          emits: ['click'],
          template: '<div class="base-list-card" @click="$emit(\'click\')"><slot /><slot name="header-left" /><slot name="header-right" /><slot name="footer" /></div>'
        },
        BaseBadge: {
          template: '<span><slot /></span>'
        },
        BaseIconButton: {
          props: ['title'],
          emits: ['click'],
          template: '<button class="base-icon-button" :title="title" @click="$emit(\'click\', $event)"><slot /></button>'
        }
      }
    }
  })
}

describe('WireGuardProfileCard', () => {
  beforeEach(() => {
    mocks.push.mockReset()
    mocks.route.query = { agentId: 'edge-2' }
  })

  it('preserves the selected agent query when navigating to profile clients', async () => {
    const wrapper = mountCard()

    await wrapper.find('.base-list-card').trigger('click')

    expect(mocks.push).toHaveBeenCalledWith({
      path: '/wireguard-profiles/7',
      query: { agentId: 'edge-2' }
    })
  })

  it('omits the query when no agent filter is selected', async () => {
    mocks.route.query = {}
    const wrapper = mountCard()

    await wrapper.find('.base-list-card').trigger('click')

    expect(mocks.push).toHaveBeenCalledWith({
      path: '/wireguard-profiles/7'
    })
  })
})

import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import WireGuardProfileCard from './WireGuardProfileCard.vue'

const mocks = vi.hoisted(() => ({
  route: { query: { agentId: 'edge-2' } },
  push: vi.fn()
}))

vi.mock('vue-router', () => ({
  useRoute: () => mocks.route,
  useRouter: () => ({ push: mocks.push })
}))

function mountCard(profileOverrides = {}, options = {}) {
  return mount(WireGuardProfileCard, {
    props: {
      profile: {
        id: 7,
        name: 'wg-main',
        enabled: true,
        addresses: ['0.0.0.0'],
        interface_addresses: ['10.8.0.1/24'],
        tags: [],
        ...profileOverrides,
      },
      clientCount: 2
    },
    attachTo: options.attachTo ? document.body : undefined,
    global: {
      stubs: options.useRealShell
        ? {}
        : {
            BaseListCard: {
              name: 'BaseListCard',
              props: ['title', 'status', 'disabled', 'clickable'],
              emits: ['click'],
              template: '<div class="base-list-card" :data-status="status" :data-title="title" @click="$emit(\'click\')"><div v-if="title" class="base-list-card__title">{{ title }}</div><slot /><slot name="header-left" /><slot name="header-right" /><slot name="footer" /></div>'
            },
            BaseBadge: {
              template: '<span><slot /></span>'
            },
            BaseIconButton: {
              props: ['title'],
              emits: ['click'],
              template: '<button class="base-icon-button" :title="title" @click.stop="$emit(\'click\', $event)"><slot /></button>'
            },
            BaseActionMenu: {
              props: ['items'],
              emits: ['select'],
              template: `
                <div class="base-action-menu" v-if="items && items.length">
                  <button class="base-action-menu__trigger" title="更多操作" @click.stop="open = !open">more</button>
                  <div v-if="open" data-testid="base-action-menu-panel">
                    <button
                      v-for="item in items"
                      :key="item.id"
                      :data-testid="'base-action-menu-item-' + item.id"
                      @click.stop="$emit('select', item); open = false"
                    >{{ item.label }}</button>
                  </div>
                </div>
              `,
              data() { return { open: false } },
            },
            AgentBadge: {
              props: ['item'],
              template: '<span class="agent-badge-stub">{{ item?.agent_name || item?.agent_id || "" }}</span>',
            },
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

describe('WireGuardProfileCard agent ownership badge', () => {
  it('renders profile agent_name without page agent', () => {
    const wrapper = mountCard({ agent_name: 'wg-node', agent_id: 'w1' })
    expect(wrapper.text()).toContain('wg-node')
  })
})

describe('WireGuardProfileCard redesign', () => {
  it('uses name as title and 生效中 / 已禁用 labels', () => {
    const on = mountCard({ name: 'wg-main', enabled: true })
    expect(on.find('.base-list-card__title').text()).toBe('wg-main')
    expect(on.text()).toContain('生效中')

    const off = mountCard({ enabled: false })
    expect(off.text()).toContain('已禁用')
  })

  it('falls back title to Profile id when name missing', () => {
    const wrapper = mountCard({ name: '' })
    expect(wrapper.find('.base-list-card__title').text()).toBe('Profile 7')
  })

  it('exposes toggle and manage clients; edit/delete in more menu', async () => {
    const wrapper = mountCard()
    expect(wrapper.find('button[title="停用"]').exists()).toBe(true)
    expect(wrapper.find('button[title="管理客户端"]').exists()).toBe(true)
    expect(wrapper.find('button[title="编辑"]').exists()).toBe(false)
    expect(wrapper.find('button[title="删除"]').exists()).toBe(false)

    await wrapper.find('.base-action-menu__trigger').trigger('click')
    await nextTick()
    await wrapper.find('[data-testid="base-action-menu-item-edit"]').trigger('click')
    await nextTick()
    expect(wrapper.emitted('edit')).toBeTruthy()

    await wrapper.find('.base-action-menu__trigger').trigger('click')
    await nextTick()
    await wrapper.find('[data-testid="base-action-menu-item-delete"]').trigger('click')
    await nextTick()
    expect(wrapper.emitted('delete')).toBeTruthy()
  })

  it('does not navigate when header toggle is clicked', async () => {
    const wrapper = mountCard()
    mocks.push.mockClear()
    await wrapper.find('button[title="停用"]').trigger('click')
    expect(wrapper.emitted('toggle')).toBeTruthy()
    expect(mocks.push).not.toHaveBeenCalled()
  })
})

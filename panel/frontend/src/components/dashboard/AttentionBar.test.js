import { describe, expect, it } from 'vitest'
import { mount, RouterLinkStub } from '@vue/test-utils'
import AttentionBar from './AttentionBar.vue'

function mountBar(props) {
  return mount(AttentionBar, {
    props,
    global: {
      stubs: { RouterLink: RouterLinkStub }
    }
  })
}

const emptyAttention = {
  offline: { count: 0, agent_ids: [] },
  blocked: { count: 0, agent_ids: [] },
  expiring_certs: { count: 0, items: [] },
  sync_failed: { count: 0, agent_ids: [] }
}

describe('AttentionBar', () => {
  it('renders clickable chips for each active signal with jump targets', () => {
    const wrapper = mountBar({
      attention: {
        offline: { count: 2, agent_ids: ['edge-1', 'edge-2'] },
        blocked: { count: 1, agent_ids: ['edge-b'] },
        expiring_certs: { count: 3, items: [] },
        sync_failed: { count: 2, agent_ids: ['edge-1', 'edge-3'] }
      }
    })

    const links = wrapper.findAllComponents(RouterLinkStub)
    expect(links).toHaveLength(4)

    const offline = wrapper.find('[data-testid="attention-offline"]')
    expect(offline.text()).toContain('2')
    expect(offline.text()).toContain('离线')
    expect(wrapper.findComponent('[data-testid="attention-offline"]').props('to')).toBe('/agents?status=offline')

    const blocked = wrapper.find('[data-testid="attention-blocked"]')
    expect(blocked.text()).toContain('1')
    expect(blocked.text()).toContain('阻断')
    expect(wrapper.findComponent('[data-testid="attention-blocked"]').props('to')).toBe('/agents/edge-b')

    const certs = wrapper.find('[data-testid="attention-certs"]')
    expect(certs.text()).toContain('3')
    expect(certs.text()).toContain('证书')
    expect(wrapper.findComponent('[data-testid="attention-certs"]').props('to')).toBe('/certs')

    const sync = wrapper.find('[data-testid="attention-sync"]')
    expect(sync.text()).toContain('2')
    expect(sync.text()).toContain('同步失败')
    expect(wrapper.findComponent('[data-testid="attention-sync"]').props('to')).toBe('/agents')
  })

  it('links a single-agent signal directly to the agent detail page', () => {
    const wrapper = mountBar({
      attention: {
        ...emptyAttention,
        sync_failed: { count: 1, agent_ids: ['edge-9'] }
      }
    })
    expect(wrapper.findComponent('[data-testid="attention-sync"]').props('to')).toBe('/agents/edge-9')
  })

  it('shows the all-clear state when no signal is active', () => {
    const wrapper = mountBar({ attention: emptyAttention })
    expect(wrapper.find('[data-testid="attention-ok"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('一切正常')
    expect(wrapper.findAllComponents(RouterLinkStub)).toHaveLength(0)
  })

  it('renders nothing conclusive while attention data is missing', () => {
    const wrapper = mountBar({ attention: null })
    expect(wrapper.find('[data-testid="attention-ok"]').exists()).toBe(false)
    expect(wrapper.findAllComponents(RouterLinkStub)).toHaveLength(0)
  })
})

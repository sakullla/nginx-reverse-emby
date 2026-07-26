import { describe, expect, it } from 'vitest'
import { mount, RouterLinkStub } from '@vue/test-utils'
import AgentStatusTiles from './AgentStatusTiles.vue'

function mountTiles(agents) {
  return mount(AgentStatusTiles, {
    props: { agents },
    global: {
      stubs: { RouterLink: RouterLinkStub }
    }
  })
}

describe('AgentStatusTiles', () => {
  it('renders one linked tile per agent with sync state labels', () => {
    const wrapper = mountTiles([
      { id: 'edge-ok', name: '节点甲', status: 'online', last_apply_status: 'success', desired_revision: 3, current_revision: 3, last_apply_revision: 3, version: '1.2.3' },
      { id: 'edge-off', name: '节点乙', status: 'offline' },
      { id: 'edge-fail', name: '节点丙', status: 'online', last_apply_status: 'error', desired_revision: 3, current_revision: 3, last_apply_revision: 3 },
      { id: 'edge-pending', name: '节点丁', status: 'online', last_apply_status: 'success', desired_revision: 5, current_revision: 3, last_apply_revision: 3 }
    ])

    const links = wrapper.findAllComponents(RouterLinkStub)
    expect(links).toHaveLength(4)
    expect(links[0].props('to')).toBe('/agents/edge-ok')

    const tiles = wrapper.findAll('[data-testid="agent-tile"]')
    expect(tiles).toHaveLength(4)
    expect(tiles[0].text()).toContain('节点甲')
    expect(tiles[0].text()).toContain('已同步')
    expect(tiles[1].text()).toContain('离线')
    expect(tiles[2].text()).toContain('同步失败')
    expect(tiles[3].text()).toContain('待同步')
  })

  it('marks offline and failed tiles with alert styling hooks', () => {
    const wrapper = mountTiles([
      { id: 'edge-off', name: '节点乙', status: 'offline' },
      { id: 'edge-fail', name: '节点丙', status: 'online', last_apply_status: 'error' }
    ])
    const tiles = wrapper.findAll('[data-testid="agent-tile"]')
    expect(tiles[0].classes()).toContain('agent-tile--offline')
    expect(tiles[1].classes()).toContain('agent-tile--failed')
  })
})

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

  it('显示节点入口地址:优先 DDNS 域名,其次 agent_url 主机名 / last_seen IP', () => {
    const wrapper = mountTiles([
      { id: 'edge-ddns', name: '节点甲', status: 'online', ddns_domain: 'hk.example.com', agent_url: 'http://1.2.3.4:8080' },
      { id: 'edge-url', name: '节点乙', status: 'online', agent_url: 'http://vps-b.example.com:8080', last_seen_ip: '5.6.7.8' },
      { id: 'edge-ip', name: '节点丙', status: 'online', last_seen_ip: '5.6.7.8' },
      { id: 'edge-none', name: '节点丁', status: 'online' }
    ])
    const tiles = wrapper.findAll('[data-testid="agent-tile"]')
    expect(tiles[0].text()).toContain('hk.example.com')
    expect(tiles[0].text()).not.toContain('1.2.3.4')
    expect(tiles[1].text()).toContain('vps-b.example.com')
    expect(tiles[2].text()).toContain('5.6.7.8')
    expect(tiles[3].find('.agent-tile__endpoint').exists()).toBe(false)
  })

  it('detailed 模式下切为整行布局并展示规则数', () => {    const wrapper = mount(AgentStatusTiles, {
      props: {
        agents: [
          { id: 'local', name: 'local', status: 'online', last_apply_status: 'success', version: '1.0.0', http_rules_count: 3, l4_rules_count: 2 }
        ],
        detailed: true
      },
      global: { stubs: { RouterLink: RouterLinkStub } }
    })
    expect(wrapper.get('.agent-tiles').classes()).toContain('agent-tiles--detailed')
    const tile = wrapper.get('[data-testid="agent-tile"]')
    expect(tile.text()).toContain('HTTP 3')
    expect(tile.text()).toContain('L4 2')
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

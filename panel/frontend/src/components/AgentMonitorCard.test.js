import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import AgentMonitorCard from './AgentMonitorCard.vue'

describe('AgentMonitorCard', () => {
  it('does not display DDNS status in the agent list', () => {
    const wrapper = mount(AgentMonitorCard, {
      props: {
        agent: {
          id: 'edge-1',
          name: 'edge-1',
          status: 'online',
          last_seen_ip: '203.0.113.10',
          last_seen_at: '2026-07-22T00:00:00Z',
          ddns_status: { status: 'idle' }
        }
      }
    })

    expect(wrapper.get('[data-testid="monitor-card-endpoint"]').text()).toBe('203.0.113.10')
    expect(wrapper.find('[data-testid="monitor-card-ddns-status"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('待解析')
  })

  it('prefers configured ddns_domain over agent_url IP hostname', () => {
    const wrapper = mount(AgentMonitorCard, {
      props: {
        agent: {
          id: 'edge-1',
          name: 'edge-1',
          status: 'online',
          agent_url: 'http://45.143.128.115:8080',
          ddns_domain: 'nosla-sjc.example.com',
          last_seen_ip: '45.143.128.115',
          last_seen_at: '2026-07-22T00:00:00Z'
        }
      }
    })

    expect(wrapper.get('[data-testid="monitor-card-endpoint"]').text()).toBe('nosla-sjc.example.com')
  })

  it('shows full network rates without truncating KiB/s values', () => {
    const wrapper = mount(AgentMonitorCard, {
      props: {
        agent: {
          id: 'agent-42',
          name: 'agent-42',
          status: 'online',
          last_seen_ip: '203.0.113.42',
          last_seen_at: '2026-07-22T00:00:00Z',
          metrics: {
            network: {
              rx_bytes_per_second: 838.1 * 1024,
              tx_bytes_per_second: 869.9 * 1024
            }
          }
        }
      }
    })

    const network = wrapper.get('[data-testid="monitor-card-network"]')
    const down = network.get('[data-testid="agent-metric-tile-network-down"]').text()
    const up = network.get('[data-testid="agent-metric-tile-network-up"]').text()

    expect(down).toContain('838.1 KiB/s')
    expect(up).toContain('869.9 KiB/s')
    expect(down).not.toMatch(/…|\.\.\./)
    expect(up).not.toMatch(/…|\.\.\./)

    // Stacked layout keeps each rate on its own row so long values fit.
    expect(network.attributes('data-display-mode')).toBe('network')
    const networkRoot = network.find('.agent-metric-tile__network')
    expect(networkRoot.classes()).toContain('agent-metric-tile__network--stacked')
  })

  it('unifies list metrics as compact rings with full memory/disk byte pairs', () => {
    const gib = 1024 * 1024 * 1024
    const wrapper = mount(AgentMonitorCard, {
      props: {
        agent: {
          id: 'agent-7',
          name: 'agent-7',
          status: 'online',
          last_seen_ip: '203.0.113.7',
          last_seen_at: '2026-07-22T00:00:00Z',
          metrics: {
            cpu_used_cores: 1.4,
            cpu_total_cores: 8,
            cpu_usage_percent: 18,
            memory_used_bytes: 4 * gib,
            memory_total_bytes: 16 * gib,
            memory_usage_percent: 42,
            disk_used_bytes: 80 * gib,
            disk_total_bytes: 512 * gib,
            disk_usage_percent: 35,
            network: {
              rx_bytes_per_second: 8 * 1024,
              tx_bytes_per_second: 3 * 1024
            }
          }
        }
      }
    })

    for (const key of ['cpu', 'memory', 'disk']) {
      const tile = wrapper.get(`[data-testid="monitor-card-${key}"]`)
      expect(tile.attributes('data-display-mode')).toBe('ring')
      expect(tile.attributes('data-variant')).toBe('compact')
      expect(tile.find('[data-testid="agent-metric-tile-metric-ring"]').exists()).toBe(true)
      expect(tile.find('[data-testid="agent-metric-tile-metric-bar"]').exists()).toBe(false)
    }

    const cpuValue = wrapper
      .get('[data-testid="monitor-card-cpu"]')
      .get('[data-testid="agent-metric-tile-ring-value"]')
      .text()
    expect(cpuValue).toBe('1.4/8核')

    const memoryValue = wrapper
      .get('[data-testid="monitor-card-memory"]')
      .get('[data-testid="agent-metric-tile-ring-value"]')
      .text()
    expect(memoryValue).toBe('4.00/16.0 GiB')

    const diskValue = wrapper
      .get('[data-testid="monitor-card-disk"]')
      .get('[data-testid="agent-metric-tile-ring-value"]')
      .text()
    expect(diskValue).toBe('80.0/512.0 GiB')

    const network = wrapper.get('[data-testid="monitor-card-network"]')
    expect(network.attributes('data-display-mode')).toBe('network')
    expect(network.attributes('data-variant')).toBe('compact')
    expect(network.get('[data-testid="agent-metric-tile-network-down"]').text()).toContain('8.00 KiB/s')
    expect(network.get('[data-testid="agent-metric-tile-network-up"]').text()).toContain('3.00 KiB/s')
  })
})

import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import AgentMonitorCard from './AgentMonitorCard.vue'

function mountCard(agent = {}) {
  return mount(AgentMonitorCard, {
    props: {
      agent: {
        id: 'edge-1',
        name: 'Edge 1',
        status: 'online',
        mode: 'master',
        last_seen_ip: '203.0.113.9',
        last_seen_at: new Date().toISOString(),
        tags: ['edge'],
        monitor: {
          metrics: {
            cpu_usage_percent: 12.4,
            cpu_used_cores: 1,
            cpu_total_cores: 8,
            memory_usage_percent: 63.8,
            memory_used_bytes: 1024 * 1024 * 1024 * 10,
            memory_total_bytes: 1024 * 1024 * 1024 * 16,
            disk_usage_percent: 77,
            disk_used_bytes: 1024 * 1024 * 1024 * 398,
            disk_total_bytes: 1024 * 1024 * 1024 * 512,
            network: {
              rx_bytes: 1024 * 1024 * 4,
              tx_bytes: 1024 * 1024,
              rx_bytes_per_second: 2048,
              tx_bytes_per_second: 1024
            }
          }
        },
        ...agent
      }
    }
  })
}

describe('AgentMonitorCard', () => {
  it('renders node name and monitor metrics', () => {
    const wrapper = mountCard()

    expect(wrapper.find('[data-testid="monitor-card-name"]').text()).toBe('Edge 1')

    expect(wrapper.find('[data-testid="monitor-card-cpu-value"]').text()).toContain('1.0 / 8 核')
    expect(wrapper.find('[data-testid="monitor-card-cpu-percent"]').text()).toContain('12.4%')

    expect(wrapper.find('[data-testid="monitor-card-memory-value"]').text()).toContain('10.0 GiB / 16.0 GiB')
    expect(wrapper.find('[data-testid="monitor-card-memory-percent"]').text()).toContain('63.8%')

    expect(wrapper.find('[data-testid="monitor-card-disk-value"]').text()).toContain('398.0 GiB / 512.0 GiB')
    expect(wrapper.find('[data-testid="monitor-card-disk-percent"]').text()).toContain('77.0%')

    expect(wrapper.find('[data-testid="monitor-card-network-down"]').text()).toContain('2.00 KiB/s')
    expect(wrapper.find('[data-testid="monitor-card-network-up"]').text()).toContain('1.00 KiB/s')
  })

  it('emits details event when clicking detail button or card', async () => {
    const wrapper = mountCard()

    await wrapper.find('[title="查看详情"]').trigger('click')
    expect(wrapper.emitted('details')).toHaveLength(1)

    await wrapper.find('.base-list-card').trigger('click')
    expect(wrapper.emitted('details')).toHaveLength(2)

    expect(wrapper.emitted('rename')).toBeUndefined()
    expect(wrapper.emitted('delete')).toBeUndefined()
  })

  it('uses placeholders for missing metrics', () => {
    const wrapper = mountCard({
      monitor: {
        metrics: {
          cpu_usage_percent: null,
          memory_usage_percent: null,
          disk_usage_percent: null,
          network: {
            rx_bytes: null,
            tx_bytes: null,
            rx_bytes_per_second: null,
            tx_bytes_per_second: null
          }
        }
      }
    })

    expect(wrapper.find('[data-testid="monitor-card-cpu-percent"]').text()).toContain('—')
    expect(wrapper.find('[data-testid="monitor-card-memory-percent"]').text()).toContain('—')
    expect(wrapper.find('[data-testid="monitor-card-disk-percent"]').text()).toContain('—')
    expect(wrapper.find('[data-testid="monitor-card-network-down"]').text()).toContain('—')
    expect(wrapper.find('[data-testid="monitor-card-network-up"]').text()).toContain('—')

    expect(wrapper.text()).not.toContain('0%')
    expect(wrapper.text()).not.toContain('0 B/s')
  })

  it('renders online status badge', () => {
    const wrapper = mountCard({ status: 'online' })
    expect(wrapper.find('.agent-monitor-card__status').text()).toContain('在线')
  })

  it('renders offline status badge', () => {
    const wrapper = mountCard({ status: 'offline' })
    expect(wrapper.find('.agent-monitor-card__status').text()).toContain('离线')
  })

  it('renders failed status badge', () => {
    const wrapper = mountCard({
      status: 'online',
      desired_revision: 2,
      current_revision: 1,
      last_apply_status: 'failed',
      last_apply_revision: 2
    })
    expect(wrapper.find('.agent-monitor-card__status').text()).toContain('失败')
  })

  it('renders pending status badge', () => {
    const wrapper = mountCard({
      status: 'online',
      desired_revision: 2,
      current_revision: 1
    })
    expect(wrapper.find('.agent-monitor-card__status').text()).toContain('同步中')
  })
})

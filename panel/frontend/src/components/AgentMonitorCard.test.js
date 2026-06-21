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
  it('renders monitor metrics and emits details only', async () => {
    const wrapper = mountCard()

    expect(wrapper.text()).toContain('CPU')
    expect(wrapper.text()).toContain('1.0 / 8 核')
    expect(wrapper.text()).toContain('12.4%')
    expect(wrapper.text()).toContain('内存')
    expect(wrapper.text()).toContain('10.0 GB / 16.0 GB')
    expect(wrapper.text()).toContain('63.8%')
    expect(wrapper.text()).toContain('398.0 GB / 512.0 GB')
    expect(wrapper.text()).toContain('77.0%')
    expect(wrapper.text()).toContain('下行速率')
    expect(wrapper.text()).toContain('2.0 KB/s')
    expect(wrapper.text()).not.toContain('重命名')
    expect(wrapper.text()).not.toContain('删除')

    await wrapper.find('[title="查看详情"]').trigger('click')
    expect(wrapper.emitted('details')).toHaveLength(1)
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

    expect(wrapper.text()).toContain('—')
    expect(wrapper.text()).not.toContain('0%')
    expect(wrapper.text()).not.toContain('0 B/s')
  })
})

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
            memory_usage_percent: 63.8,
            disk_usage_percent: 77,
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
    expect(wrapper.text()).toContain('12%')
    expect(wrapper.text()).toContain('内存')
    expect(wrapper.text()).toContain('64%')
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
    const wrapper = mountCard({ monitor: { metrics: {} } })

    expect(wrapper.text()).toContain('—')
  })
})

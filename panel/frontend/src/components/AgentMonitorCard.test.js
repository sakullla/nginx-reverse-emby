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
})

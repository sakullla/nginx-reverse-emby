import { describe, expect, it } from 'vitest'
import { mount, RouterLinkStub } from '@vue/test-utils'
import ClusterMetricsCard from './ClusterMetricsCard.vue'

function mountCard(props = {}) {
  return mount(ClusterMetricsCard, {
    props: {
      agents: [
        { id: 'a', status: 'online', http_rules_count: 10, l4_rules_count: 2 },
        { id: 'b', status: 'online', http_rules_count: 6, l4_rules_count: 3 },
        { id: 'c', status: 'offline', http_rules_count: 0, l4_rules_count: 0 }
      ],
      certsTotal: 3,
      certsExpiring: 1,
      defaultAgentId: 'a',
      ...props
    },
    global: {
      stubs: { RouterLink: RouterLinkStub }
    }
  })
}

describe('ClusterMetricsCard', () => {
  it('shows node health ring with online ratio', () => {
    const wrapper = mountCard()
    const ring = wrapper.find('[data-testid="metric-nodes"]')
    expect(ring.text()).toContain('2')
    expect(ring.text()).toContain('3')
    expect(wrapper.find('[data-testid="metric-ring"]').exists()).toBe(true)
  })

  it('shows http/l4/cert metric rows with totals and links', () => {
    const wrapper = mountCard()

    const http = wrapper.find('[data-testid="metric-http"]')
    expect(http.text()).toContain('16')
    expect(wrapper.findComponent('[data-testid="metric-http"]').props('to')).toBe('/rules')

    const l4 = wrapper.find('[data-testid="metric-l4"]')
    expect(l4.text()).toContain('5')
    expect(wrapper.findComponent('[data-testid="metric-l4"]').props('to')).toBe('/l4')

    const certs = wrapper.find('[data-testid="metric-certs"]')
    expect(certs.text()).toContain('3')
    expect(certs.text()).toContain('1 个即将过期')
    expect(wrapper.findComponent('[data-testid="metric-certs"]').props('to')).toBe('/certs?agentId=a')
  })

  it('marks cert row healthy when nothing expires', () => {
    const wrapper = mountCard({ certsExpiring: 0 })
    const certs = wrapper.find('[data-testid="metric-certs"]')
    expect(certs.text()).toContain('证书正常')
    expect(certs.classes()).not.toContain('cluster-metrics__row--danger')
  })

  it('renders per-agent distribution bars for rule rows', () => {
    const wrapper = mountCard()
    expect(wrapper.findAll('[data-testid="metric-http"] .cluster-metrics__bar-seg').length).toBe(2)
  })
})

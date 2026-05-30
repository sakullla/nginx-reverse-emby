import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import L4RuleItem from './L4RuleItem.vue'

function mountItem(overrides = {}) {
  return mount(L4RuleItem, {
    props: {
      rule: {
        id: 1,
        protocol: 'tcp',
        listen_mode: 'tcp',
        listen_host: '0.0.0.0',
        listen_port: 9000,
        backends: [{ host: '127.0.0.1', port: 9001 }],
        load_balancing: { strategy: 'adaptive' },
        enabled: true,
        tags: [],
        ...overrides
      }
    }
  })
}

describe('L4RuleItem', () => {
  it('omits redundant default TCP and UDP forwarding badges', () => {
    const tcp = mountItem({ protocol: 'tcp', listen_mode: 'tcp' })
    const udp = mountItem({ protocol: 'udp', listen_mode: 'udp' })

    expect(tcp.text()).not.toContain('TCP转发')
    expect(udp.text()).not.toContain('UDP转发')
  })

  it('keeps non-default listen mode badges', () => {
    const wrapper = mountItem({ listen_mode: 'proxy' })

    expect(wrapper.text()).toContain('代理')
  })
})

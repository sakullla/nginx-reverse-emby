import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import PluginOperationTimeline from './PluginOperationTimeline.vue'

describe('PluginOperationTimeline', () => {
  it('renders lifecycle audit and Agent results through the redaction boundary', () => {
    const wrapper = mount(PluginOperationTimeline, {
      props: { operations: [{ id: 'op-1', kind: 'rollback', status: 'failed', actor_id: 'admin', target_revision: 9, error: 'password=hunter2', agent_results: { edge: { state: 'failed', secret: 'raw-secret' } } }] }
    })
    expect(wrapper.text()).toMatch(/rollback|failed|admin|revision 9/)
    expect(wrapper.text()).not.toContain('hunter2')
    expect(wrapper.text()).not.toContain('raw-secret')
    expect(wrapper.text()).toContain('[REDACTED]')
  })
})

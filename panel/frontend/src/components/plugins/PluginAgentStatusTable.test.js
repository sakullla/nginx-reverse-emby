import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import PluginAgentStatusTable from './PluginAgentStatusTable.vue'

describe('PluginAgentStatusTable', () => {
  it('shows per-Agent runtime, budget and crash state without credential material', () => {
    const wrapper = mount(PluginAgentStatusTable, {
      props: {
        statuses: [{
          instance_id: 'waf-a', agent_id: 'edge-a', target_scope: 'active', runtime_state: 'degraded',
          operation_kind: 'configure', operation_status: 'failed', desired_revision: 8, current_revision: 7,
          runtime_error_code: 'budget_exceeded', last_apply_message: 'authorization=raw-token',
          runtime_budget: { memory_bytes: 1024 }, runtime_details: { crash_count: 2, token: 'runtime-token', restart_count: 3 }
        }]
      }
    })
    expect(wrapper.text()).toMatch(/edge-a|degraded|budget_exceeded|崩溃与重试/)
    expect(wrapper.text()).not.toContain('raw-token')
    expect(wrapper.text()).not.toContain('runtime-token')
    expect(wrapper.text()).toContain('[REDACTED]')
  })

  it('offers an explicit revision retry only for actionable failed targets', async () => {
    const wrapper = mount(PluginAgentStatusTable, { props: { actionable: true, statuses: [{ instance_id: 'rpc', agent_id: 'edge', runtime_state: 'failed', desired_revision: 8, target_revision: 9 }] } })
    expect(wrapper.text()).toContain('0 / 9')
    await wrapper.get('button').trigger('click')
    expect(wrapper.emitted('retry')[0][0]).toMatchObject({ agent_id: 'edge', desired_revision: 8, target_revision: 9 })
  })
})

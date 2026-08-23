import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import PluginAgentStatusTable from './PluginAgentStatusTable.vue'

describe('PluginAgentStatusTable', () => {
  it('shows the agent name instead of the raw id when a matching agent is provided', () => {
    const wrapper = mount(PluginAgentStatusTable, {
      props: {
        agents: [{ id: 'a4ae8a0d7d39f1f94d0dd862196784a8', name: 'nosla-hk' }],
        statuses: [{
          face_id: 'agent-execution', instance_id: 'waf-a', agent_id: 'a4ae8a0d7d39f1f94d0dd862196784a8', target_scope: 'active', runtime_state: 'active',
          operation_kind: 'configure', operation_status: 'succeeded'
        }]
      }
    })
    expect(wrapper.text()).toContain('nosla-hk')
    expect(wrapper.text()).toContain('Agent 执行面')
    expect(wrapper.find('.agent-status-card__name').text()).not.toContain('a4ae8a0d7d39f1f94d0dd862196784a8')
  })

  it('shows per-Agent runtime, budget and crash state without credential material', () => {
    const wrapper = mount(PluginAgentStatusTable, {
      props: {
        statuses: [{
          face_id: 'agent-execution', instance_id: 'waf-a', agent_id: 'edge-a', target_scope: 'active', runtime_state: 'degraded',
          operation_kind: 'configure', operation_status: 'failed', desired_revision: 8, current_revision: 7,
          generation_id: 'generation-edge-a-8',
          runtime_error_code: 'budget_exceeded', last_apply_message: 'authorization=raw-token',
          runtime_budget: { memory_bytes: 1024 }, runtime_details: { crash_count: 2, token: 'runtime-token', restart_count: 3 }
        }]
      }
    })
    expect(wrapper.text()).toMatch(/edge-a|执行降级|budget_exceeded|崩溃与重试/)
    expect(wrapper.text()).toContain('generation-edge-a-8')
    expect(wrapper.text()).toContain('同步或启动失败')
    expect(wrapper.text()).toContain('Agent 执行面：')
    expect(wrapper.text()).not.toContain('raw-token')
    expect(wrapper.text()).not.toContain('runtime-token')
    expect(wrapper.text()).toContain('[REDACTED]')
  })

  it('offers an explicit revision retry only for actionable failed targets', async () => {
    const wrapper = mount(PluginAgentStatusTable, { props: { actionable: true, statuses: [{ face_id: 'agent-execution', instance_id: 'rpc', agent_id: 'edge', runtime_state: 'failed', desired_revision: 8, target_revision: 9 }] } })
    expect(wrapper.text()).toContain('0 / 9')
    await wrapper.get('button').trigger('click')
    expect(wrapper.emitted('retry')[0][0]).toMatchObject({ agent_id: 'edge', desired_revision: 8, target_revision: 9 })
  })

  it('attributes offline and unsynced state to the Agent execution face', () => {
    const wrapper = mount(PluginAgentStatusTable, {
      props: {
        agents: [
          { id: 'edge-offline', name: 'Edge Offline', status: 'offline' },
          { id: 'edge-syncing', name: 'Edge Syncing', status: 'online' }
        ],
        statuses: [
          { face_id: 'agent-execution', instance_id: 'rpc', agent_id: 'edge-offline', available: true, current_revision: 4, desired_revision: 4 },
          { face_id: 'agent-execution', instance_id: 'rpc', agent_id: 'edge-syncing', available: true, runtime_state: 'active', current_revision: 3, desired_revision: 5 }
        ]
      }
    })
    const cards = wrapper.findAll('[data-test="plugin-agent-execution-status"]')
    expect(cards).toHaveLength(2)
    expect(cards[0].text()).toContain('Agent 离线')
    expect(cards[1].text()).toContain('Generation 未同步')
    expect(wrapper.text()).not.toContain('本地管理面失败')
  })

  it('treats prepare and activation error states as execution failures', () => {
    const wrapper = mount(PluginAgentStatusTable, {
      props: {
        actionable: true,
        statuses: [{
          face_id: 'agent-execution', instance_id: 'rpc', agent_id: 'edge', runtime_state: 'activation_failed',
          current_revision: 6, desired_revision: 6, generation_id: 'generation-6'
        }]
      }
    })
    expect(wrapper.text()).toContain('activation_failed')
    expect(wrapper.text()).toContain('同步或启动失败')
    expect(wrapper.get('button').text()).toContain('重试此 Agent revision')
  })
})

import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const hooks = vi.hoisted(() => ({
  tracked: vi.fn(),
  agentsData: { value: [] }
}))
vi.mock('../../hooks/useOperationStatus', () => ({
  useOperationStatus: (operationID) => {
    hooks.tracked(operationID)
    return {}
  }
}))
vi.mock('../../hooks/useAgents', () => ({
  useAgents: () => ({ data: hooks.agentsData })
}))

import { recordAcceptedOperation, resetOperations, useOperationsStore } from '../../stores/operations'
import OperationStatusList from './OperationStatusList.vue'

describe('OperationStatusList', () => {
  beforeEach(() => {
    resetOperations()
    hooks.tracked.mockReset()
    hooks.agentsData.value = []
  })

  it('tracks every nonterminal operation even when only five are visible', async () => {
    for (let index = 1; index <= 6; index += 1) {
      recordAcceptedOperation({
        operation_id: `op-${index}`,
        status_url: `/panel-api/operations/op-${index}`,
        apply_status: 'pending'
      })
    }

    const wrapper = mount(OperationStatusList)
    await nextTick()

    expect(wrapper.findAll('.operation-status')).toHaveLength(5)
    expect(hooks.tracked.mock.calls.map(([operationID]) => operationID.value).sort()).toEqual([
      'op-1', 'op-2', 'op-3', 'op-4', 'op-5', 'op-6'
    ])
    wrapper.unmount()
  })

  it('hides successfully completed operations but keeps failures actionable', async () => {
    recordAcceptedOperation({
      operation_id: 'op-drained',
      agent_id: 'edge-drained',
      apply_status: 'applied',
      agents: [{ agent_id: 'edge-drained', apply_status: 'applied', drain_status: 'drained' }]
    })
    recordAcceptedOperation({
      operation_id: 'op-failed',
      agent_id: 'edge-failed',
      apply_status: 'failed',
      error_message: 'apply failed'
    })
    recordAcceptedOperation({
      operation_id: 'op-pending',
      agent_id: 'edge-pending',
      status_url: '/panel-api/operations/op-pending',
      apply_status: 'pending'
    })

    const wrapper = mount(OperationStatusList)
    await nextTick()

    expect(wrapper.findAll('.operation-status')).toHaveLength(2)
    expect(wrapper.text()).toContain('edge-pending')
    expect(wrapper.text()).toContain('edge-failed')
    expect(wrapper.text()).not.toContain('edge-drained')
    expect(hooks.tracked.mock.calls.map(([operationID]) => operationID.value)).toEqual(['op-pending'])
    wrapper.unmount()
  })

  it('shows the agent name in operation metadata and falls back to the id', async () => {
    hooks.agentsData.value = [{ id: 'opaque-agent-id', name: '新加坡节点' }]
    recordAcceptedOperation({
      operation_id: 'op-named',
      agent_id: 'opaque-agent-id',
      desired_revision: 390,
      apply_status: 'applying'
    })
    recordAcceptedOperation({
      operation_id: 'op-fallback',
      agent_id: 'unknown-agent-id',
      apply_status: 'pending'
    })

    const wrapper = mount(OperationStatusList)
    await nextTick()

    expect(wrapper.text()).toContain('新加坡节点')
    expect(wrapper.text()).not.toContain('opaque-agent-id')
    expect(wrapper.text()).toContain('unknown-agent-id')
    wrapper.unmount()
  })

  it('only renders operations for the selected agent', async () => {
    recordAcceptedOperation({
      operation_id: 'op-edge-a',
      agent_id: 'edge-a',
      apply_status: 'applying'
    })
    recordAcceptedOperation({
      operation_id: 'op-edge-b',
      agent_id: 'edge-b',
      apply_status: 'pending'
    })

    const wrapper = mount(OperationStatusList, { props: { agentId: 'edge-a' } })
    await nextTick()

    expect(wrapper.text()).toContain('edge-a')
    expect(wrapper.text()).not.toContain('edge-b')
    wrapper.unmount()
  })

  it('hides a completed apply banner while continuing to track its drain', async () => {
    recordAcceptedOperation({
      operation_id: 'op-draining',
      status_url: '/panel-api/operations/op-draining',
      agent_id: 'edge-a',
      apply_status: 'applied',
      completed_at: '2026-07-23T00:12:15Z',
      agents: [{ agent_id: 'edge-a', apply_status: 'applied', drain_status: 'draining' }]
    })

    const wrapper = mount(OperationStatusList)
    await nextTick()

    expect(wrapper.find('.operation-status').exists()).toBe(false)
    expect(hooks.tracked.mock.calls.map(([operationID]) => operationID.value)).toContain('op-draining')
    wrapper.unmount()
  })

  it('sends a progress banner dismissal through the operation store', async () => {
    recordAcceptedOperation({
      operation_id: 'op-dismiss',
      status_url: '/panel-api/operations/op-dismiss',
      agent_id: 'edge-test-1',
      desired_revision: 8,
      apply_status: 'applying'
    })
    const dismiss = vi.spyOn(useOperationsStore(), 'dismiss').mockResolvedValue({
      operation_id: 'op-dismiss', dismissed: true, terminal: true
    })
    const wrapper = mount(OperationStatusList)

    await wrapper.find('[data-action="dismiss"]').trigger('click')

    expect(dismiss).toHaveBeenCalledWith('op-dismiss')
    dismiss.mockRestore()
    wrapper.unmount()
  })
})

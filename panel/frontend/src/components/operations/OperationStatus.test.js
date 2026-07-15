import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import OperationStatus from './OperationStatus.vue'

describe('OperationStatus', () => {
  it('renders saved, draining and accessible failure actions', async () => {
    const saved = mount(OperationStatus, { props: { operation: { operation_id: 'op-1', ui_status: 'pending' } } })
    expect(saved.text()).toContain('已保存，等待生效')

    const draining = mount(OperationStatus, { props: { operation: { operation_id: 'op-2', ui_status: 'draining' } } })
    expect(draining.text()).toContain('旧连接排空中')

    const failed = mount(OperationStatus, {
      props: { operation: { operation_id: 'op-3', ui_status: 'failed', error_code: 'bind_failed', error_message: 'address in use' } }
    })
    expect(failed.text()).toContain('bind_failed: address in use')
    expect(failed.findAll('button')).toHaveLength(2)
    await failed.findAll('button')[0].trigger('click')
    expect(failed.emitted('retry')).toEqual([[{ operationID: 'op-3', agentID: '', revision: 0 }]])
  })

  it('renders recovery actions and errors for every failed member of a degraded operation', async () => {
    const wrapper = mount(OperationStatus, {
      props: {
        operation: {
          operation_id: 'op-degraded',
          ui_status: 'degraded',
          agents: [
            { agent_id: 'edge-a', desired_revision: 7, apply_status: 'failed', attempt_count: 2, error_message: 'bind failed' },
            { agent_id: 'edge-b', desired_revision: 8, apply_status: 'failed', attempt_count: 3, error_message: 'timeout' }
          ]
        }
      }
    })

    expect(wrapper.text()).toContain('edge-a')
    expect(wrapper.text()).toContain('edge-b')
    expect(wrapper.text()).toContain('bind failed')
    expect(wrapper.text()).toContain('timeout')
    expect(wrapper.findAll('button')).toHaveLength(4)
    await wrapper.findAll('button')[2].trigger('click')
    expect(wrapper.emitted('retry')).toEqual([[{
      operationID: 'op-degraded', agentID: 'edge-b', revision: 8
    }]])
  })
})

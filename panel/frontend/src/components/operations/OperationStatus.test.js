import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import OperationStatus from './OperationStatus.vue'

function mountStatus(operation, props = {}) {
  return mount(OperationStatus, {
    props: {
      operation,
      agentNameById: new Map([['edge-test-1', 'edge-test-1']]),
      ...props
    }
  })
}

describe('OperationStatus', () => {
  it('collapses a single-node failure into one block without repeating details', () => {
    const wrapper = mountStatus({
      operation_id: 'op-1',
      ui_status: 'failed',
      agent_id: 'edge-test-1',
      desired_revision: 21,
      error_code: 'apply_failed',
      error_message: 'address already in use',
      agents: [{
        agent_id: 'edge-test-1',
        apply_status: 'failed',
        desired_revision: 21,
        attempt_count: 5,
        error_code: 'apply_failed',
        error_message: 'address already in use'
      }]
    })

    const text = wrapper.text()
    expect(text).toContain('生效失败')
    expect(text).toContain('edge-test-1')
    expect(text).toContain('revision 21')
    expect(text).toContain('第 5 次尝试')
    expect(text).toContain('apply_failed: address already in use')
    expect(wrapper.findAll('.operation-status__error')).toHaveLength(1)
    expect(wrapper.find('.operation-status__agents').exists()).toBe(false)
    expect(wrapper.findAll('.operation-status__btn')).toHaveLength(3)
    expect(text.match(/edge-test-1/g)).toHaveLength(1)
    expect(text.match(/revision 21/g)).toHaveLength(1)
    expect(text.match(/第 5 次尝试/g)).toHaveLength(1)
    expect(text.match(/address already in use/g)).toHaveLength(1)
  })

  it('emits recovery for the failed agent when actions are in the header', async () => {
    const wrapper = mountStatus({
      operation_id: 'op-1',
      ui_status: 'failed',
      agent_id: 'edge-test-1',
      desired_revision: 21,
      agents: [{
        agent_id: 'edge-test-1',
        apply_status: 'failed',
        desired_revision: 21,
        attempt_count: 2,
        error_message: 'boom'
      }]
    })

    await wrapper.find('.operation-status__btn--solid').trigger('click')
    expect(wrapper.emitted('retry')).toEqual([[{
      operationID: 'op-1',
      agentID: 'edge-test-1',
      revision: 21
    }]])
  })

  it('keeps a per-agent list when multiple nodes fail', () => {
    const wrapper = mountStatus({
      operation_id: 'op-2',
      ui_status: 'degraded',
      agent_id: 'edge-a',
      desired_revision: 9,
      error_message: 'should stay out of header',
      agents: [
        {
          agent_id: 'edge-a',
          apply_status: 'failed',
          desired_revision: 9,
          attempt_count: 1,
          error_message: 'a failed'
        },
        {
          agent_id: 'edge-b',
          apply_status: 'failed',
          desired_revision: 9,
          attempt_count: 3,
          error_message: 'b failed'
        }
      ]
    }, {
      agentNameById: new Map([
        ['edge-a', '节点A'],
        ['edge-b', '节点B']
      ])
    })

    expect(wrapper.find('.operation-status__agents').exists()).toBe(true)
    expect(wrapper.findAll('.operation-status__agent')).toHaveLength(2)
    expect(wrapper.text()).toContain('节点A')
    expect(wrapper.text()).toContain('节点B')
    expect(wrapper.text()).toContain('a failed')
    expect(wrapper.text()).toContain('b failed')
    expect(wrapper.text()).not.toContain('should stay out of header')
    // Header keeps the status label; meta/error live on agent rows.
    expect(wrapper.find('.operation-status__main .operation-status__meta').exists()).toBe(false)
    expect(wrapper.findAll('.operation-status__main .operation-status__btn')).toHaveLength(1)
  })

  it('shows compact progress metadata without agent list chrome', () => {
    const wrapper = mountStatus({
      operation_id: 'op-3',
      ui_status: 'applying',
      agent_id: 'edge-test-1',
      desired_revision: 12,
      agents: []
    })

    expect(wrapper.text()).toContain('正在生效')
    expect(wrapper.text()).toContain('edge-test-1')
    expect(wrapper.text()).toContain('revision 12')
    expect(wrapper.find('.operation-status__agents').exists()).toBe(false)
    expect(wrapper.findAll('.operation-status__btn')).toHaveLength(1)
  })

  it('emits a dismissal without changing the operation itself', async () => {
    const wrapper = mountStatus({
      operation_id: 'op-progress',
      ui_status: 'applying',
      agent_id: 'edge-test-1',
      desired_revision: 8,
      agents: []
    })

    await wrapper.find('[data-action="dismiss"]').trigger('click')

    expect(wrapper.emitted('dismiss')).toEqual([['op-progress']])
  })
})

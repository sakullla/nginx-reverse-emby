import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const hooks = vi.hoisted(() => ({ tracked: vi.fn() }))
vi.mock('../../hooks/useOperationStatus', () => ({
  useOperationStatus: (operationID) => {
    hooks.tracked(operationID)
    return {}
  }
}))

import { recordAcceptedOperation, resetOperations } from '../../stores/operations'
import OperationStatusList from './OperationStatusList.vue'

describe('OperationStatusList', () => {
  beforeEach(() => {
    resetOperations()
    hooks.tracked.mockReset()
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
})

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
    expect(failed.emitted('retry')).toEqual([['op-3']])
  })
})

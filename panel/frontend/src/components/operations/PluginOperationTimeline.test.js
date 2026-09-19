import { afterEach, describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import PluginOperationTimeline from './PluginOperationTimeline.vue'
import { setPanelTimeZone } from '../../utils/panelDateTime.js'

describe('PluginOperationTimeline', () => {
  afterEach(() => setPanelTimeZone('UTC'))

  it('renders lifecycle audit and Agent results through the redaction boundary', () => {
    const wrapper = mount(PluginOperationTimeline, {
      props: { operations: [{ id: 'op-1', kind: 'rollback', status: 'failed', actor_id: 'admin', target_revision: 9, error: 'password=hunter2', agent_results: { edge: { state: 'failed', secret: 'raw-secret' } } }] }
    })
    expect(wrapper.text()).toMatch(/rollback|failed|admin|revision 9/)
    expect(wrapper.text()).not.toContain('hunter2')
    expect(wrapper.text()).not.toContain('raw-secret')
    expect(wrapper.text()).toContain('[REDACTED]')
  })

  it('shows only the five newest lifecycle operations', () => {
    const operations = Array.from({ length: 7 }, (_, index) => ({
      id: `op-${index + 1}`,
      kind: `kind-${index + 1}`,
      status: 'succeeded',
      created_at: `2026-08-${String(index + 1).padStart(2, '0')}T00:00:00Z`
    }))
    const wrapper = mount(PluginOperationTimeline, { props: { operations } })
    const items = wrapper.findAll('li')
    expect(items).toHaveLength(5)
    expect(items.map((item) => item.text())).toEqual([
      expect.stringContaining('kind-7'),
      expect.stringContaining('kind-6'),
      expect.stringContaining('kind-5'),
      expect.stringContaining('kind-4'),
      expect.stringContaining('kind-3')
    ])
    expect(wrapper.text()).not.toContain('kind-2')
  })

  it('formats operation times in the panel timezone after /info loads NRE_TIMEZONE', async () => {
    setPanelTimeZone('UTC')
    const wrapper = mount(PluginOperationTimeline, {
      props: {
        operations: [{ id: 'op-1', kind: 'configure', status: 'succeeded', created_at: '2026-08-24T06:55:53Z' }]
      }
    })
    expect(wrapper.get('time').text()).toBe('2026/08/24 06:55:53')
    setPanelTimeZone('Asia/Shanghai')
    await wrapper.vm.$nextTick()
    expect(wrapper.get('time').text()).toBe('2026/08/24 14:55:53')
    expect(wrapper.get('time').attributes('data-timezone')).toBe('Asia/Shanghai')
  })
})

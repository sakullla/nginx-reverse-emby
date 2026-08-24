import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import PluginLogViewer from './PluginLogViewer.vue'
import { setPanelTimeZone } from '../../utils/panelDateTime.js'

const mocks = vi.hoisted(() => ({ fetchPluginLogs: vi.fn() }))
vi.mock('../../api/plugins', () => ({ fetchPluginLogs: mocks.fetchPluginLogs }))

describe('PluginLogViewer', () => {
  beforeEach(() => {
    mocks.fetchPluginLogs.mockReset()
    setPanelTimeZone('Asia/Shanghai')
  })
  afterEach(() => setPanelTimeZone('UTC'))

  it('filters and shows only five newest host logs without credentials', async () => {
    mocks.fetchPluginLogs
      .mockResolvedValueOnce({ entries: Array.from({ length: 7 }, (_, index) => ({ agent_id: 'edge-a', level: index ? 'info' : 'warning', message: index ? `log-${index + 1}` : 'token=[REDACTED]', truncated: index === 0, created_at: `2026-08-${String(index + 1).padStart(2, '0')}T00:00:00Z` })), next_cursor: 'cursor-2' })
      .mockResolvedValueOnce({ entries: [], next_cursor: '' })
    const wrapper = mount(PluginLogViewer, { props: { pluginId: 'official.rpc', instanceId: 'rpc-a', agents: ['edge-a'] } })
    await flushPromises()
    expect(mocks.fetchPluginLogs).toHaveBeenCalledWith('official.rpc', 'rpc-a', expect.objectContaining({ agentID: '', cursor: '', limit: 5 }))
    expect(wrapper.findAll('li')).toHaveLength(5)
    expect(wrapper.findAll('li')[0].text()).toContain('log-7')
    expect(wrapper.text()).not.toContain('token=[REDACTED]')
    expect(wrapper.text()).not.toContain('plaintext-credential')
    expect(wrapper.find('button').exists()).toBe(false)
    await wrapper.get('select').setValue('edge-a')
    await flushPromises()
		expect(mocks.fetchPluginLogs).toHaveBeenLastCalledWith('official.rpc', 'rpc-a', expect.objectContaining({ agentID: 'edge-a', cursor: '', limit: 5 }))
	})

	it('discards a deferred stale response after the selected instance changes', async () => {
		let resolveFirst
		const first = new Promise((resolve) => { resolveFirst = resolve })
		mocks.fetchPluginLogs.mockReturnValueOnce(first).mockResolvedValueOnce({ entries: [{ agent_id: 'edge-b', level: 'info', message: 'current', created_at: 'now' }], next_cursor: '' })
		const wrapper = mount(PluginLogViewer, { props: { pluginId: 'official.rpc', instanceId: 'rpc-a', agents: [] } })
		await wrapper.setProps({ instanceId: 'rpc-b' })
		await flushPromises()
		expect(wrapper.text()).toContain('current')
		resolveFirst({ entries: [{ agent_id: 'edge-a', level: 'error', message: 'stale', created_at: 'old' }], next_cursor: 'stale-cursor' })
		await flushPromises()
		expect(wrapper.text()).not.toContain('stale')
		expect(wrapper.find('button').exists()).toBe(false)
	})

  it('shows agent names in the body and filter and still filters by agent id', async () => {
    mocks.fetchPluginLogs.mockResolvedValue({
      entries: [{ agent_id: 'edge-a', level: 'info', message: 'hello', created_at: '2026-08-01T00:00:00Z' }],
      next_cursor: ''
    })
    const wrapper = mount(PluginLogViewer, {
      props: {
        pluginId: 'official.rpc',
        instanceId: 'rpc-a',
        agents: [
          { id: 'edge-a', name: 'Edge A' },
          { id: 'edge-b', name: '' },
          { id: 'edge-c' }
        ]
      }
    })
    await flushPromises()
    const options = wrapper.findAll('select option')
    expect(options.map((option) => option.text())).toEqual(['全部可见 Agent', 'Edge A', 'edge-b', 'edge-c', '控制面'])
    expect(options.map((option) => option.element.value)).toEqual(['', 'edge-a', 'edge-b', 'edge-c', 'control-plane'])
    expect(wrapper.get('li strong').text()).toBe('Edge A')
    await wrapper.get('select').setValue('edge-a')
    await flushPromises()
    expect(mocks.fetchPluginLogs).toHaveBeenLastCalledWith('official.rpc', 'rpc-a', expect.objectContaining({ agentID: 'edge-a', cursor: '', limit: 5 }))
  })

  it('falls back to the agent id when the name is missing', async () => {
    mocks.fetchPluginLogs.mockResolvedValue({
      entries: [{ agent_id: 'edge-z', level: 'info', message: 'solo', created_at: '2026-08-01T00:00:00Z' }],
      next_cursor: ''
    })
    const wrapper = mount(PluginLogViewer, {
      props: {
        pluginId: 'official.rpc',
        instanceId: 'rpc-a',
        agents: [{ id: 'edge-z' }]
      }
    })
    await flushPromises()
    expect(wrapper.get('li strong').text()).toBe('edge-z')
    expect(wrapper.get('select option[value="edge-z"]').text()).toBe('edge-z')
  })

  it('lets the filter select control-plane logs by name and agent id', async () => {
    mocks.fetchPluginLogs.mockResolvedValue({
      entries: [{ agent_id: 'control-plane', level: 'info', message: 'http: Accept error', created_at: '2026-08-24T06:55:53Z' }],
      next_cursor: ''
    })
    const wrapper = mount(PluginLogViewer, {
      props: {
        pluginId: 'official.rpc',
        instanceId: 'rpc-a',
        agents: [{ id: '903d5dedb9b03336d0b37ce394a0e31b', name: 'zouter-hk' }]
      }
    })
    await flushPromises()
    expect(wrapper.get('select option[value="903d5dedb9b03336d0b37ce394a0e31b"]').text()).toBe('zouter-hk')
    expect(wrapper.get('select option[value="control-plane"]').text()).toBe('控制面')
    expect(wrapper.get('li strong').text()).toBe('控制面')
    expect(wrapper.get('time').text()).toBe('2026/08/24 14:55:53')
    await wrapper.get('select').setValue('control-plane')
    await flushPromises()
    expect(mocks.fetchPluginLogs).toHaveBeenLastCalledWith('official.rpc', 'rpc-a', expect.objectContaining({ agentID: 'control-plane', cursor: '', limit: 5 }))
  })

  it('re-renders host log stamps after /info applies NRE_TIMEZONE', async () => {
    setPanelTimeZone('UTC')
    mocks.fetchPluginLogs.mockResolvedValue({
      entries: [{ agent_id: 'control-plane', level: 'info', message: '2026/08/24 06:55:53 http: Accept error', created_at: '2026-08-24T06:55:53Z' }],
      next_cursor: ''
    })
    const wrapper = mount(PluginLogViewer, {
      props: { pluginId: 'official.rpc', instanceId: 'rpc-a', agents: [] }
    })
    await flushPromises()
    expect(wrapper.get('time').text()).toBe('2026/08/24 06:55:53')
    expect(wrapper.get('li > span').text()).toBe('http: Accept error')
    setPanelTimeZone('Asia/Shanghai')
    await wrapper.vm.$nextTick()
    expect(wrapper.get('time').text()).toBe('2026/08/24 14:55:53')
    expect(wrapper.get('time').attributes('data-timezone')).toBe('Asia/Shanghai')
  })
})

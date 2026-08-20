import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import PluginLogViewer from './PluginLogViewer.vue'

const mocks = vi.hoisted(() => ({ fetchPluginLogs: vi.fn() }))
vi.mock('../../api/plugins', () => ({ fetchPluginLogs: mocks.fetchPluginLogs }))

describe('PluginLogViewer', () => {
  beforeEach(() => mocks.fetchPluginLogs.mockReset())

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
})

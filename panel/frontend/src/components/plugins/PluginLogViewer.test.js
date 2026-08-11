import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import PluginLogViewer from './PluginLogViewer.vue'

const mocks = vi.hoisted(() => ({ fetchPluginLogs: vi.fn() }))
vi.mock('../../api/plugins', () => ({ fetchPluginLogs: mocks.fetchPluginLogs }))

describe('PluginLogViewer', () => {
  beforeEach(() => mocks.fetchPluginLogs.mockReset())

  it('filters, paginates and shows host truncation without credentials', async () => {
    mocks.fetchPluginLogs
      .mockResolvedValueOnce({ entries: [{ agent_id: 'edge-a', level: 'warning', message: 'token=[REDACTED]', truncated: true, created_at: '2026-08-11T00:00:00Z' }], next_cursor: 'cursor-2' })
      .mockResolvedValueOnce({ entries: [{ agent_id: 'edge-a', level: 'info', message: 'older', truncated: false, created_at: '2026-08-10T00:00:00Z' }], next_cursor: '' })
      .mockResolvedValueOnce({ entries: [], next_cursor: '' })
    const wrapper = mount(PluginLogViewer, { props: { pluginId: 'official.rpc', instanceId: 'rpc-a', agents: ['edge-a'] } })
    await flushPromises()
    expect(wrapper.text()).toContain('token=[REDACTED]')
    expect(wrapper.text()).toContain('已截断')
    expect(wrapper.text()).not.toContain('plaintext-credential')
    await wrapper.get('button').trigger('click')
    await flushPromises()
		expect(mocks.fetchPluginLogs).toHaveBeenNthCalledWith(2, 'official.rpc', 'rpc-a', expect.objectContaining({ agentID: '', cursor: 'cursor-2', limit: 50 }))
    expect(wrapper.text()).toContain('older')
    await wrapper.get('select').setValue('edge-a')
    await flushPromises()
		expect(mocks.fetchPluginLogs).toHaveBeenLastCalledWith('official.rpc', 'rpc-a', expect.objectContaining({ agentID: 'edge-a', cursor: '', limit: 50 }))
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

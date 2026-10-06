// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import PluginLogViewer from './PluginLogViewer.vue'
import { setPanelTimeZone } from '../../utils/panelDateTime.js'

const mocks = vi.hoisted(() => ({ fetchPluginLogs: vi.fn() }))
vi.mock('../../api/plugins', () => ({ fetchPluginLogs: mocks.fetchPluginLogs }))

const agentFilter = () => '[data-test="plugin-log-agent-filter"]'

describe('PluginLogViewer', () => {
  beforeEach(() => {
    mocks.fetchPluginLogs.mockReset()
    setPanelTimeZone('Asia/Shanghai')
  })
  afterEach(() => {
    setPanelTimeZone('UTC')
    vi.useRealTimers()
  })

  it('filters and keeps the fifty newest host logs without credentials', async () => {
    mocks.fetchPluginLogs
      .mockResolvedValueOnce({
        entries: Array.from({ length: 52 }, (_, index) => ({
          agent_id: 'edge-a',
          level: index ? 'info' : 'warning',
          message: index ? `log-${index + 1}` : 'token=[REDACTED]',
          truncated: index === 0,
          created_at: `2026-08-01T00:${String(index).padStart(2, '0')}:00Z`
        })),
        next_cursor: 'cursor-2'
      })
      .mockResolvedValueOnce({ entries: [], next_cursor: '' })
    const wrapper = mount(PluginLogViewer, { props: { pluginId: 'official.rpc', instanceId: 'rpc-a', agents: ['edge-a'] } })
    await flushPromises()
    expect(mocks.fetchPluginLogs).toHaveBeenCalledWith('official.rpc', 'rpc-a', expect.objectContaining({ agentID: '', limit: 50 }))
    expect(wrapper.findAll('li')).toHaveLength(50)
    expect(wrapper.findAll('li')[0].text()).toContain('log-52')
    expect(wrapper.text()).not.toContain('token=[REDACTED]')
    expect(wrapper.text()).not.toContain('plaintext-credential')
    expect(wrapper.find('select option[value="control-plane"]').exists()).toBe(false)
    await wrapper.get(agentFilter()).setValue('edge-a')
    await flushPromises()
    expect(mocks.fetchPluginLogs).toHaveBeenLastCalledWith('official.rpc', 'rpc-a', expect.objectContaining({ agentID: 'edge-a', limit: 50 }))
  })

  it('resets the agent filter and batch ids when the instance changes', async () => {
    mocks.fetchPluginLogs
      .mockResolvedValueOnce({
        entries: [
          { agent_id: 'edge-a', level: 'info', message: 'from-a', created_at: '2026-08-02T00:00:00Z' },
          { agent_id: 'batch-old', level: 'info', message: 'old-batch', created_at: '2026-08-01T00:00:00Z' }
        ],
        next_cursor: ''
      })
      .mockResolvedValueOnce({
        entries: [{ agent_id: 'edge-a', level: 'info', message: 'filtered-a', created_at: '2026-08-02T00:00:00Z' }],
        next_cursor: ''
      })
    let resolveNext
    const next = new Promise((resolve) => { resolveNext = resolve })
    mocks.fetchPluginLogs.mockReturnValueOnce(next)

    const wrapper = mount(PluginLogViewer, {
      props: {
        pluginId: 'official.rpc',
        instanceId: 'rpc-a',
        agents: [{ id: 'edge-a', name: 'Edge A' }]
      }
    })
    await flushPromises()
    expect(wrapper.find(`${agentFilter()} option[value="batch-old"]`).exists()).toBe(true)
    await wrapper.get(agentFilter()).setValue('edge-a')
    await flushPromises()
    expect(mocks.fetchPluginLogs).toHaveBeenLastCalledWith('official.rpc', 'rpc-a', expect.objectContaining({ agentID: 'edge-a', limit: 50 }))

    await wrapper.setProps({
      instanceId: 'rpc-b',
      agents: [{ id: 'edge-b', name: 'Edge B' }]
    })
    expect(mocks.fetchPluginLogs).toHaveBeenLastCalledWith('official.rpc', 'rpc-b', expect.objectContaining({ agentID: '', limit: 50 }))
    expect(wrapper.get(agentFilter()).element.value).toBe('')
    expect(wrapper.findAll(`${agentFilter()} option`).map((option) => option.element.value)).toEqual(['', 'edge-b'])
    expect(wrapper.find(`${agentFilter()} option[value="edge-a"]`).exists()).toBe(false)
    expect(wrapper.find(`${agentFilter()} option[value="batch-old"]`).exists()).toBe(false)
    expect(wrapper.text()).not.toContain('from-a')
    expect(wrapper.text()).not.toContain('old-batch')
    expect(wrapper.text()).not.toContain('filtered-a')

    resolveNext({
      entries: [{ agent_id: 'edge-b', level: 'info', message: 'from-b', created_at: '2026-08-03T00:00:00Z' }],
      next_cursor: ''
    })
    await flushPromises()
    expect(wrapper.get(agentFilter()).element.value).toBe('')
    expect(wrapper.findAll(`${agentFilter()} option`).map((option) => option.element.value)).toEqual(['', 'edge-b'])
    expect(wrapper.text()).toContain('from-b')
    expect(wrapper.get('li strong').text()).toBe('Edge B')
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
  })

  it('shows instance-related agent names plus this-batch ids without a default control-plane option', async () => {
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
    const options = wrapper.findAll(`${agentFilter()} option`)
    expect(options.map((option) => option.text())).toEqual(['全部可见 Agent', 'Edge A', 'edge-b', 'edge-c'])
    expect(options.map((option) => option.element.value)).toEqual(['', 'edge-a', 'edge-b', 'edge-c'])
    expect(wrapper.find(`${agentFilter()} option[value="control-plane"]`).exists()).toBe(false)
    expect(wrapper.get('li strong').text()).toBe('Edge A')
    await wrapper.get(agentFilter()).setValue('edge-a')
    await flushPromises()
    expect(mocks.fetchPluginLogs).toHaveBeenLastCalledWith('official.rpc', 'rpc-a', expect.objectContaining({ agentID: 'edge-a', limit: 50 }))
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
    expect(wrapper.get(`${agentFilter()} option[value="edge-z"]`).text()).toBe('edge-z')
    expect(wrapper.find(`${agentFilter()} option[value="control-plane"]`).exists()).toBe(false)
  })

  it('adds control-plane only when it is a target or appears in this batch', async () => {
    mocks.fetchPluginLogs.mockResolvedValue({ entries: [], next_cursor: '' })
    const unrelated = mount(PluginLogViewer, {
      props: { pluginId: 'official.rpc', instanceId: 'rpc-a', agents: [{ id: 'edge-a', name: 'Edge A' }] }
    })
    await flushPromises()
    expect(unrelated.find(`${agentFilter()} option[value="control-plane"]`).exists()).toBe(false)

    const asTarget = mount(PluginLogViewer, {
      props: { pluginId: 'official.rpc', instanceId: 'rpc-a', agents: [{ id: 'control-plane' }] }
    })
    await flushPromises()
    expect(asTarget.get(`${agentFilter()} option[value="control-plane"]`).text()).toBe('控制面')
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
    expect(wrapper.get(`${agentFilter()} option[value="903d5dedb9b03336d0b37ce394a0e31b"]`).text()).toBe('zouter-hk')
    expect(wrapper.get(`${agentFilter()} option[value="control-plane"]`).text()).toBe('控制面')
    expect(wrapper.get('li strong').text()).toBe('控制面')
    expect(wrapper.get('time').text()).toBe('2026/08/24 14:55:53')
    await wrapper.get(agentFilter()).setValue('control-plane')
    await flushPromises()
    expect(mocks.fetchPluginLogs).toHaveBeenLastCalledWith('official.rpc', 'rpc-a', expect.objectContaining({ agentID: 'control-plane', limit: 50 }))
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

  it('shows an explicit empty-body label when a log has no display message', async () => {
    mocks.fetchPluginLogs.mockResolvedValue({
      entries: [
        { agent_id: 'edge-a', level: 'info', message: '', created_at: '2026-08-03T00:00:00Z' },
        { agent_id: 'edge-a', level: 'warning', message: '   ', created_at: '2026-08-02T00:00:00Z' },
        { agent_id: 'edge-a', level: 'error', message: '2026/08/24 06:55:53 ', created_at: '2026-08-01T00:00:00Z' }
      ],
      next_cursor: ''
    })
    const wrapper = mount(PluginLogViewer, {
      props: { pluginId: 'official.rpc', instanceId: 'rpc-a', agents: [{ id: 'edge-a', name: 'Edge A' }] }
    })
    await flushPromises()
    const rows = wrapper.findAll('li')
    expect(rows).toHaveLength(3)
    expect(rows.map((row) => row.get('header + span').text())).toEqual(['无日志正文', '无日志正文', '无日志正文'])
    expect(rows[0].text()).toContain('info')
    expect(rows[0].get('strong').text()).toBe('Edge A')
    expect(wrapper.text()).not.toContain('暂无宿主持久化运行日志。')
  })

  it('keeps the empty-state copy when there are no host logs', async () => {
    mocks.fetchPluginLogs.mockResolvedValue({ entries: [], next_cursor: '' })
    const wrapper = mount(PluginLogViewer, {
      props: { pluginId: 'official.rpc', instanceId: 'rpc-a', agents: [{ id: 'edge-a' }] }
    })
    await flushPromises()
    expect(wrapper.text()).toContain('暂无宿主持久化运行日志。')
    expect(wrapper.find('li').exists()).toBe(false)
    expect(wrapper.find(`${agentFilter()} option[value="control-plane"]`).exists()).toBe(false)
  })

  it('filters loaded entries by level group without refetching', async () => {
    mocks.fetchPluginLogs.mockResolvedValue({
      entries: [
        { agent_id: 'edge-a', level: 'error', message: 'bootstrap failed', created_at: '2026-10-07T08:00:00Z' },
        { agent_id: 'edge-a', level: 'warn', message: 'retry scheduled', created_at: '2026-10-07T07:30:00Z' },
        { agent_id: 'edge-a', level: 'info', message: 'instance became ready', created_at: '2026-10-07T07:00:00Z' }
      ],
      next_cursor: ''
    })
    const wrapper = mount(PluginLogViewer, { props: { pluginId: 'official.rpc', instanceId: 'rpc-a', agents: [{ id: 'edge-a', name: 'Edge A' }] } })
    await flushPromises()
    const callsBefore = mocks.fetchPluginLogs.mock.calls.length
    await wrapper.get('[data-test="plugin-log-level-filter"]').setValue('error')
    expect(wrapper.findAll('li')).toHaveLength(1)
    expect(wrapper.text()).toContain('bootstrap failed')
    await wrapper.get('[data-test="plugin-log-level-filter"]').setValue('warning')
    expect(wrapper.findAll('li')).toHaveLength(1)
    expect(wrapper.text()).toContain('retry scheduled')
    await wrapper.get('[data-test="plugin-log-level-filter"]').setValue('info')
    expect(wrapper.findAll('li')).toHaveLength(1)
    expect(wrapper.text()).toContain('instance became ready')
    await wrapper.get('[data-test="plugin-log-level-filter"]').setValue('')
    expect(wrapper.findAll('li')).toHaveLength(3)
    expect(mocks.fetchPluginLogs.mock.calls.length).toBe(callsBefore)
  })

  it('filters loaded entries by text across message and agent name', async () => {
    mocks.fetchPluginLogs.mockResolvedValue({
      entries: [
        { agent_id: 'edge-a', level: 'info', message: 'bootstrap failed', created_at: '2026-10-07T08:00:00Z' },
        { agent_id: 'edge-b', level: 'info', message: 'retry scheduled', created_at: '2026-10-07T07:30:00Z' }
      ],
      next_cursor: ''
    })
    const wrapper = mount(PluginLogViewer, {
      props: { pluginId: 'official.rpc', instanceId: 'rpc-a', agents: [{ id: 'edge-a', name: 'Edge A' }, { id: 'edge-b' }] }
    })
    await flushPromises()
    await wrapper.get('[data-test="plugin-log-text-filter"]').setValue('edge a')
    expect(wrapper.findAll('li')).toHaveLength(1)
    expect(wrapper.text()).toContain('bootstrap failed')
    await wrapper.get('[data-test="plugin-log-text-filter"]').setValue('retry')
    expect(wrapper.findAll('li')).toHaveLength(1)
    expect(wrapper.text()).toContain('retry scheduled')
    await wrapper.get('[data-test="plugin-log-text-filter"]').setValue('no-such-text')
    expect(wrapper.find('.plugin-log-list').exists()).toBe(false)
    expect(wrapper.text()).toContain('没有匹配的日志条目')
  })

  it('reloads automatically while auto refresh is enabled and stops after it is turned off', async () => {
    vi.useFakeTimers()
    mocks.fetchPluginLogs.mockResolvedValue({ entries: [], next_cursor: '' })
    const wrapper = mount(PluginLogViewer, { props: { pluginId: 'official.rpc', instanceId: 'rpc-a', agents: [] } })
    await flushPromises()
    expect(mocks.fetchPluginLogs).toHaveBeenCalledTimes(1)
    await wrapper.get('[data-test="plugin-log-auto-refresh"]').setValue(true)
    await vi.advanceTimersByTimeAsync(10000)
    await flushPromises()
    expect(mocks.fetchPluginLogs).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(20000)
    await flushPromises()
    expect(mocks.fetchPluginLogs).toHaveBeenCalledTimes(4)
    await wrapper.get('[data-test="plugin-log-auto-refresh"]').setValue(false)
    await vi.advanceTimersByTimeAsync(30000)
    await flushPromises()
    expect(mocks.fetchPluginLogs).toHaveBeenCalledTimes(4)
  })

  it('shows a sanitized error when loading fails', async () => {
    mocks.fetchPluginLogs.mockRejectedValue(new Error('log backend unreachable'))
    const wrapper = mount(PluginLogViewer, { props: { pluginId: 'official.rpc', instanceId: 'rpc-a', agents: [] } })
    await flushPromises()
    expect(wrapper.get('.plugin-log-viewer__error').attributes('role')).toBe('alert')
    expect(wrapper.text()).toContain('log backend unreachable')
  })
})

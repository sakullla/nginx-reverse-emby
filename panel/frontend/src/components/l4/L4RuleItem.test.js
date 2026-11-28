import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import L4RuleItem from './L4RuleItem.vue'
import L4RuleTable from './L4RuleTable.vue'
import { messageStore } from '../../stores/messages.js'

const tcpRule = {
  id: 7,
  agent_id: 'a1',
  protocol: 'tcp',
  listen_host: '0.0.0.0',
  listen_port: 9000,
  enabled: true,
  backends: [{ host: '10.0.0.1', port: 80 }],
  tags: ['one', 'two', 'three', 'four']
}
const udpRule = { ...tcpRule, id: 8, protocol: 'udp', tags: [] }

const wrappers = []
function mountAttached(component, props) {
  const wrapper = mount(component, { props, attachTo: document.body })
  wrappers.push(wrapper)
  return wrapper
}

afterEach(() => {
  while (wrappers.length) wrappers.pop().unmount()
  vi.restoreAllMocks()
  document.body.innerHTML = ''
})

function menuItem(id) {
  return document.body.querySelector(`[data-testid="base-action-menu-item-${id}"]`)
}

async function openMenu(wrapper) {
  await wrapper.get('.base-action-menu__trigger').trigger('click')
  await nextTick()
  await nextTick()
}

describe('L4RuleItem actions', () => {
  it('shows only toggle and edit as visible buttons', () => {
    const wrapper = mountAttached(L4RuleItem, { rule: tcpRule })
    const visibleTitles = wrapper
      .findAll('.base-list-card__header-right > button')
      .map((b) => b.attributes('title'))
    expect(visibleTitles).toEqual(['停用', '编辑'])
  })

  it.each([
    ['copy', 'copy'],
    ['diagnose', 'diagnose'],
    ['delete', 'delete']
  ])('emits %s from the more menu', async (itemId, eventName) => {
    const wrapper = mountAttached(L4RuleItem, { rule: tcpRule })
    await openMenu(wrapper)
    menuItem(itemId).click()
    await nextTick()
    expect(wrapper.emitted(eventName)?.[0]).toEqual([tcpRule])
    expect(wrapper.emitted('edit')).toBeUndefined()
  })

  it('offers diagnose only for TCP rules', async () => {
    const wrapper = mountAttached(L4RuleItem, { rule: udpRule })
    await openMenu(wrapper)
    expect(menuItem('copy')).not.toBeNull()
    expect(menuItem('delete')).not.toBeNull()
    expect(menuItem('diagnose')).toBeNull()
  })

  it('emits select instead of edit when selectable and guards actions & copy buttons', async () => {
    const wrapper = mountAttached(L4RuleItem, { rule: tcpRule, selectable: true, selected: false })
    const toggleBtn = wrapper.get('[data-action="toggle"]')
    const editBtn = wrapper.get('[data-action="edit"]')
    expect(toggleBtn.attributes('disabled')).toBeDefined()
    expect(editBtn.attributes('disabled')).toBeDefined()
    expect(wrapper.find('.base-action-menu__trigger').exists()).toBe(false)
    expect(wrapper.find('[data-copy-target="listen"]').exists()).toBe(false)
    expect(wrapper.find('[data-copy-target="backend"]').exists()).toBe(false)

    await toggleBtn.trigger('click')
    await editBtn.trigger('click')
    expect(wrapper.emitted('toggle')).toBeUndefined()
    expect(wrapper.emitted('edit')).toBeUndefined()

    await wrapper.trigger('click')
    expect(wrapper.emitted('select')?.[0]).toEqual([tcpRule])
    expect(wrapper.emitted('edit')).toBeUndefined()
  })

  it('copies listen address and primary backend address without triggering edit', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText },
      configurable: true
    })
    const successSpy = vi.spyOn(messageStore, 'success').mockImplementation(() => {})
    const multiBackendRule = {
      ...tcpRule,
      backends: [{ host: '10.0.0.1', port: 80 }, { host: '10.0.0.2', port: 81 }]
    }

    const wrapper = mountAttached(L4RuleItem, { rule: multiBackendRule })
    await wrapper.get('[data-copy-target="listen"]').trigger('click')
    await flushPromises()
    expect(writeText).toHaveBeenCalledWith('0.0.0.0:9000')

    await wrapper.get('[data-copy-target="backend"]').trigger('click')
    await flushPromises()
    expect(writeText).toHaveBeenCalledWith('10.0.0.1:80')
    expect(wrapper.emitted('edit')).toBeUndefined()
    expect(successSpy).toHaveBeenCalledWith('已复制监听地址')
    expect(successSpy).toHaveBeenCalledWith('已复制后端地址')
  })

  it('hides copy buttons when listen address or backend is empty', () => {
    const emptyRule = {
      id: 9,
      agent_id: 'a1',
      protocol: 'tcp',
      listen_host: '',
      listen_port: '',
      enabled: true,
      backends: [],
      tags: []
    }
    const wrapper = mountAttached(L4RuleItem, { rule: emptyRule })
    expect(wrapper.find('[data-copy-target="listen"]').exists()).toBe(false)
    expect(wrapper.find('[data-copy-target="backend"]').exists()).toBe(false)
  })

  it('toggles selection instead of opening traffic trend when clicking TrafficBar in bulk mode', async () => {
    const traffic = { accounted_bytes: 2048, rx_bytes: 1024, tx_bytes: 1024 }
    const normal = mountAttached(L4RuleItem, { rule: tcpRule, traffic, agentNodeTotal: 4096, selectable: false })
    await normal.get('.traffic-bar').trigger('click')
    expect(normal.emitted('traffic-click')).toEqual([[tcpRule]])
    expect(normal.emitted('select')).toBeUndefined()

    const bulk = mountAttached(L4RuleItem, { rule: tcpRule, traffic, agentNodeTotal: 4096, selectable: true, selected: false })
    await bulk.get('.traffic-bar').trigger('click')
    expect(bulk.emitted('select')).toEqual([[tcpRule]])
    expect(bulk.emitted('traffic-click')).toBeUndefined()
    expect(bulk.emitted('edit')).toBeUndefined()
  })
})

describe('L4RuleTable', () => {
  it('renders a traffic column from trafficFor', () => {
    const wrapper = mountAttached(L4RuleTable, {
      rules: [tcpRule],
      trafficFor: () => ({ accounted_bytes: 2048, rx_bytes: 1024, tx_bytes: 1024 })
    })
    expect(wrapper.findAll('th').map((th) => th.text())).toContain('流量')
    expect(wrapper.find('.traffic-bar--compact').exists()).toBe(true)
  })

  it('offers the same action set as the card', async () => {
    const wrapper = mountAttached(L4RuleTable, { rules: [tcpRule] })
    await wrapper.get('button[title="编辑"]').trigger('click')
    expect(wrapper.emitted('edit')?.[0]).toEqual([tcpRule])
    for (const id of ['copy', 'diagnose', 'delete']) {
      await openMenu(wrapper)
      menuItem(id).click()
      await nextTick()
      expect(wrapper.emitted(id)?.[0]).toEqual([tcpRule])
    }
  })

  it('supports row selection and select-all in selectable mode', async () => {
    const wrapper = mountAttached(L4RuleTable, {
      rules: [tcpRule, udpRule],
      selectable: true,
      selectedKeys: ['a1:7']
    })
    expect(wrapper.findAll('tbody tr')[0].attributes('aria-selected')).toBe('true')
    await wrapper.findAll('tbody tr')[1].trigger('click')
    expect(wrapper.emitted('select')?.[0]).toEqual([udpRule])
    await wrapper.get('thead input[type="checkbox"]').trigger('change')
    expect(wrapper.emitted('select-all')).toHaveLength(1)
  })
})

import { mount } from '@vue/test-utils'
import { afterEach, describe, expect, it } from 'vitest'
import { nextTick } from 'vue'
import L4RuleItem from './L4RuleItem.vue'
import L4RuleTable from './L4RuleTable.vue'

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

  it('emits select instead of edit when selectable', async () => {
    const wrapper = mountAttached(L4RuleItem, { rule: tcpRule, selectable: true, selected: false })
    await wrapper.trigger('click')
    expect(wrapper.emitted('select')?.[0]).toEqual([tcpRule])
    expect(wrapper.emitted('edit')).toBeUndefined()
  })
})

describe('L4RuleTable', () => {
  it('shows at most two tags plus a +N badge with the full list in title', () => {
    const wrapper = mountAttached(L4RuleTable, { rules: [tcpRule] })
    const tags = wrapper.get('.rules-table__tags')
    expect(tags.findAll('.tag:not(.tag--more)').map((t) => t.text())).toEqual(['one', 'two'])
    expect(tags.get('[data-testid="l4-tags-more"]').text()).toBe('+2')
    expect(tags.attributes('title')).toBe('one, two, three, four')
  })

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

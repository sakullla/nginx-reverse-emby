import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import L4RuleTable, { fitTagCount } from './L4RuleTable.vue'
import { messageStore } from '../../stores/messages.js'

const baseRule = {
  id: 7,
  agent_id: 'a1',
  protocol: 'tcp',
  listen_host: '0.0.0.0',
  listen_port: 9000,
  enabled: true,
  backends: [{ host: '10.0.0.1', port: 80 }],
  tags: ['one', 'two', 'three', 'four']
}

const wrappers = []

afterEach(() => {
  while (wrappers.length) wrappers.pop().unmount()
  vi.restoreAllMocks()
  document.body.innerHTML = ''
})

// jsdom has no layout: tags get a width proportional to their text and the
// tags cell a configurable width, so the fold logic runs against real elements.
function mockLayout(tagsWidth, moreWidth = 24) {
  vi.spyOn(Element.prototype, 'getBoundingClientRect').mockImplementation(function () {
    let width = 0
    if (this.classList.contains('rules-table__tags')) width = tagsWidth
    else if (this.classList.contains('tag--more')) width = moreWidth
    else if (this.hasAttribute('data-tag')) width = this.textContent.trim().length * 10 + 10
    return { width, height: 20, top: 0, left: 0, right: width, bottom: 20, x: 0, y: 0 }
  })
  vi.spyOn(window, 'getComputedStyle').mockImplementation(() => ({ columnGap: '0px' }))
}

async function mountMeasured(rules, tagsWidth, moreWidth, props = {}) {
  mockLayout(tagsWidth, moreWidth)
  const wrapper = mount(L4RuleTable, { props: { rules, ...props }, attachTo: document.body })
  wrappers.push(wrapper)
  await nextTick()
  await nextTick()
  return wrapper
}

function shownTags(cell) {
  return cell.findAll('[data-tag]:not(.tag--overflow)').map((tag) => tag.text())
}

describe('L4RuleTable tags', () => {
  it('folds tags that do not fit the cell into +N with the full list in title', async () => {
    const wrapper = await mountMeasured([baseRule], 120)
    const tags = wrapper.get('.rules-table__tags')
    // one(40) + two(40) + "+N" reserve(28) = 108 fits; three(60) does not.
    expect(shownTags(tags)).toEqual(['one', 'two'])
    expect(tags.get('[data-testid="l4-tags-more"]').text()).toBe('+2')
    expect(tags.attributes('title')).toBe('one, two, three, four')
    for (const hidden of tags.findAll('.tag--overflow')) {
      expect(hidden.attributes('aria-hidden')).toBe('true')
    }
  })

  it('shows all tags when they fit and never renders a cut-off tag', async () => {
    const wide = await mountMeasured([baseRule], 400)
    expect(shownTags(wide.get('.rules-table__tags'))).toEqual(['one', 'two', 'three', 'four'])
    expect(wide.find('[data-testid="l4-tags-more"]').exists()).toBe(false)

    const longRule = { ...baseRule, id: 8, tags: ['averyveryverylongtag'] }
    const narrow = await mountMeasured([longRule], 120)
    expect(shownTags(narrow.get('.rules-table__tags'))).toEqual([])
    expect(narrow.get('[data-testid="l4-tags-more"]').text()).toBe('+1')
  })

  it('refits when the rendered +N badge is wider than the space reserved for it', async () => {
    const wrapper = await mountMeasured([baseRule], 120, 50)
    for (let i = 0; i < 4; i += 1) await nextTick()
    const tags = wrapper.get('.rules-table__tags')
    // First pass reserves 28 (one + two fit); the real badge is 50, so two folds too.
    expect(shownTags(tags)).toEqual(['one'])
    expect(tags.get('[data-testid="l4-tags-more"]').text()).toBe('+3')
  })

  it('marks bulk mode on the table so the layout reserves the select column', async () => {
    const wrapper = await mountMeasured([baseRule], 400, 24, { selectable: true })
    expect(wrapper.get('table').classes()).toEqual(expect.arrayContaining(['rules-table--selectable', 'rules-table--with-agent']))
    expect(wrapper.find('th.rules-table__col-listen').exists()).toBe(true)
    expect(wrapper.find('tbody td.rules-table__cell-select input').exists()).toBe(true)
  })

  it('refits when the rules change', async () => {
    const wrapper = await mountMeasured([baseRule], 120)
    await wrapper.setProps({ rules: [{ ...baseRule, tags: ['a', 'b', 'c'] }] })
    await nextTick()
    await nextTick()
    expect(shownTags(wrapper.get('.rules-table__tags'))).toEqual(['a', 'b', 'c'])
    expect(wrapper.find('[data-testid="l4-tags-more"]').exists()).toBe(false)
  })

  it('fitTagCount keeps whole tags and reserves the +N badge', () => {
    expect(fitTagCount([40, 40], 84, 4, 28)).toBe(2)
    expect(fitTagCount([40, 40, 40], 110, 4, 28)).toBe(1)
    expect(fitTagCount([200], 120, 4, 28)).toBe(0)
  })
})

describe('L4RuleTable copy and bulk guards', () => {
  it('copies listen address and primary backend address in normal mode without extra suffixes', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText },
      configurable: true
    })
    const successSpy = vi.spyOn(messageStore, 'success').mockImplementation(() => {})
    const list = [
      {
        ...baseRule,
        backends: [{ host: '10.0.0.1', port: 80 }, { host: '10.0.0.2', port: 81 }]
      },
      {
        id: 9,
        agent_id: 'a1',
        protocol: 'tcp',
        listen_host: '',
        listen_port: '',
        enabled: true,
        backends: [],
        tags: []
      }
    ]

    const wrapper = mount(L4RuleTable, { props: { rules: list }, attachTo: document.body })
    wrappers.push(wrapper)
    const rows = wrapper.findAll('tbody .rules-table__row')
    expect(rows[0].get('.rules-table__url--backend').text()).toBe('10.0.0.1:80 +1')

    await rows[0].get('[data-copy-target="listen"]').trigger('click')
    await flushPromises()
    expect(writeText).toHaveBeenCalledWith('0.0.0.0:9000')

    await rows[0].get('[data-copy-target="backend"]').trigger('click')
    await flushPromises()
    expect(writeText).toHaveBeenCalledWith('10.0.0.1:80')

    expect(rows[1].find('[data-copy-target="listen"]').exists()).toBe(false)
    expect(rows[1].find('[data-copy-target="backend"]').exists()).toBe(false)
    expect(wrapper.emitted('select')).toBeUndefined()
    expect(wrapper.emitted('edit')).toBeUndefined()
    expect(successSpy).toHaveBeenCalledWith('已复制监听地址')
    expect(successSpy).toHaveBeenCalledWith('已复制后端地址')
  })

  it('guards row actions and hides copy buttons and action menu in bulk mode', async () => {
    const wrapper = mount(L4RuleTable, {
      props: {
        rules: [baseRule],
        selectable: true,
        selectedKeys: [],
        trafficFor: () => ({ accounted_bytes: 2048, rx_bytes: 1024, tx_bytes: 1024 }),
        nodeTotalFor: () => 4096
      },
      attachTo: document.body
    })
    wrappers.push(wrapper)

    const toggleBtn = wrapper.get('.toggle')
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

    await wrapper.get('.traffic-bar--compact').trigger('click')
    expect(wrapper.emitted('select')).toEqual([[baseRule]])
    expect(wrapper.emitted('traffic-click')).toBeUndefined()
  })
})

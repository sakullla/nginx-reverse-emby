import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import RuleTable, { fitTagCount } from './RuleTable.vue'
import { messageStore } from '../../stores/messages.js'

const rules = [
  { id: 1, agent_id: 'a', frontend_url: 'https://one.example.com', enabled: true, tags: ['emby', 'web', 'media', 'cdn'], backends: [{ url: 'http://10.0.0.1:8096' }] },
  { id: 2, agent_id: 'a', frontend_url: 'http://two.example.com', enabled: false, tags: ['solo'], backends: [{ url: 'http://10.0.0.2' }] }
]

const stubs = { RouterLink: { props: ['to'], template: '<a><slot /></a>' } }

afterEach(() => {
  vi.restoreAllMocks()
  document.body.innerHTML = ''
})

// jsdom has no layout: give tags a width proportional to their text, and the
// tags cell a configurable width, so the fold logic runs against real elements.
function mockLayout(tagsWidth, moreWidth = 24) {
  vi.spyOn(Element.prototype, 'getBoundingClientRect').mockImplementation(function () {
    let width = 0
    if (this.classList.contains('rules-table__tags')) width = tagsWidth
    else if (this.classList.contains('tag--more')) width = moreWidth
    else if (this.classList.contains('rules-table__service-link')) width = 50
    else if (this.hasAttribute('data-tag')) width = this.textContent.trim().length * 10 + 10
    return { width, height: 20, top: 0, left: 0, right: width, bottom: 20, x: 0, y: 0 }
  })
  vi.spyOn(window, 'getComputedStyle').mockImplementation(() => ({ columnGap: '0px' }))
}

async function mountMeasured({ rules: list, tagsWidth, moreWidth }) {
  mockLayout(tagsWidth, moreWidth)
  const wrapper = mount(RuleTable, { props: { rules: list }, global: { stubs } })
  await nextTick()
  await nextTick()
  return wrapper
}

function shownTags(cell) {
  return cell.findAll('[data-tag]:not(.tag--overflow)').map((tag) => tag.text())
}

describe('RuleTable', () => {
  it('folds tags that do not fit the cell width into a +N badge whose title lists every tag', async () => {
    const wrapper = await mountMeasured({ rules, tagsWidth: 120 })
    const rows = wrapper.findAll('tbody .rules-table__row')
    const firstTags = rows[0].get('.rules-table__tags')
    // emby(50) + web(40) + "+N" reserve fit in 120; media(60) does not.
    expect(shownTags(firstTags)).toEqual(['emby', 'web'])
    expect(firstTags.get('[data-testid="rule-table-tags-more"]').text()).toBe('+2')
    expect(firstTags.attributes('title')).toBe('emby, web, media, cdn')
    expect(shownTags(rows[1].get('.rules-table__tags'))).toEqual(['solo'])
    expect(rows[1].find('[data-testid="rule-table-tags-more"]').exists()).toBe(false)
  })

  it('shows every tag when the cell is wide enough and never cuts a tag that is too long', async () => {
    const wide = await mountMeasured({ rules: [rules[0]], tagsWidth: 400 })
    expect(shownTags(wide.get('.rules-table__tags'))).toEqual(['emby', 'web', 'media', 'cdn'])
    expect(wide.find('[data-testid="rule-table-tags-more"]').exists()).toBe(false)

    const longRule = { ...rules[0], id: 3, tags: ['averyveryverylongtag', 'x'] }
    const narrow = await mountMeasured({ rules: [longRule], tagsWidth: 120 })
    expect(shownTags(narrow.get('.rules-table__tags'))).toEqual([])
    expect(narrow.get('[data-testid="rule-table-tags-more"]').text()).toBe('+2')
    expect(narrow.get('.rules-table__tags').attributes('title')).toBe('averyveryverylongtag, x')
  })

  it('refits when the rendered +N badge is wider than the space reserved for it', async () => {
    const wrapper = await mountMeasured({ rules: [rules[0]], tagsWidth: 120, moreWidth: 50 })
    for (let i = 0; i < 4; i += 1) await nextTick()
    const tags = wrapper.get('.rules-table__tags')
    // First pass reserves 28 (emby + web fit); the real badge is 50, so
    // emby(50) + web(40) + 50 > 120 and web folds too.
    expect(shownTags(tags)).toEqual(['emby'])
    expect(tags.get('[data-testid="rule-table-tags-more"]').text()).toBe('+3')
  })

  it('reserves room for the service link before fitting tags', async () => {
    const linked = { ...rules[0], id: 4, tags: ['emby', 'web', 'nre-access-service:s1'] }
    const wrapper = await mountMeasured({ rules: [linked], tagsWidth: 150 })
    const tags = wrapper.get('.rules-table__tags')
    expect(tags.find('.rules-table__service-link').exists()).toBe(true)
    // 150 - link(50) = 100: emby(50) + "+N" reserve fits; adding web(40) would not.
    expect(shownTags(tags)).toEqual(['emby'])
    expect(tags.get('[data-testid="rule-table-tags-more"]').text()).toBe('+2')
  })

  it('fitTagCount keeps whole tags and reserves the +N badge', () => {
    expect(fitTagCount([], 100, 4, 28)).toBe(0)
    expect(fitTagCount([40, 40], 84, 4, 28)).toBe(2)
    expect(fitTagCount([40, 40, 40], 110, 4, 28)).toBe(1)
    expect(fitTagCount([40, 40, 40], 116, 4, 28)).toBe(2)
    expect(fitTagCount([200], 120, 4, 28)).toBe(0)
  })

  it('renders a traffic column from trafficFor and emits traffic-click', async () => {
    const wrapper = mount(RuleTable, {
      props: {
        rules,
        trafficFor: (rule) => (rule.id === 1 ? { accounted_bytes: 2048, rx_bytes: 1024, tx_bytes: 1024 } : null),
        nodeTotalFor: () => 4096
      },
      global: { stubs }
    })
    expect(wrapper.find('th.rules-table__col-traffic').text()).toBe('流量')
    const cells = wrapper.findAll('.rules-table__cell-traffic')
    expect(cells[0].find('.traffic-bar--compact').exists()).toBe(true)
    expect(cells[1].text()).toBe('—')
    await cells[0].get('.traffic-bar--compact').trigger('click')
    expect(wrapper.emitted('traffic-click')?.[0]).toEqual([rules[0]])
  })

  it('hides the traffic column when traffic stats are off', () => {
    const wrapper = mount(RuleTable, { props: { rules, showTraffic: false }, global: { stubs } })
    expect(wrapper.find('th.rules-table__col-traffic').exists()).toBe(false)
  })

  it('offers edit plus copy/diagnose/delete in the more menu, same as the card', async () => {
    const wrapper = mount(RuleTable, { props: { rules: [rules[0]] }, global: { stubs }, attachTo: document.body })
    await wrapper.get('[data-action="edit"]').trigger('click')
    expect(wrapper.emitted('edit')?.[0]).toEqual([rules[0]])
    for (const id of ['copy', 'diagnose', 'delete']) {
      await wrapper.get('.base-action-menu__trigger').trigger('click')
      document.body.querySelector(`[data-testid="base-action-menu-item-${id}"]`).click()
      await nextTick()
      expect(wrapper.emitted(id)?.[0]).toEqual([rules[0]])
    }
    wrapper.unmount()
  })

  it('keeps the switch out of the faded cells on disabled rows', () => {
    const wrapper = mount(RuleTable, { props: { rules }, global: { stubs } })
    const disabledRow = wrapper.findAll('tbody .rules-table__row')[1]
    expect(disabledRow.classes()).toContain('rules-table__row--disabled')
    expect(disabledRow.get('.rules-table__cell-toggle').classes()).not.toContain('rules-table__dim')
    expect(disabledRow.get('.toggle').attributes('aria-checked')).toBe('false')
  })

  it('adds a selection column with page select-all and guards row actions in bulk mode', async () => {
    const wrapper = mount(RuleTable, {
      props: { rules, selectable: true, selectedKeys: ['a:1'], indeterminate: true },
      global: { stubs }
    })
    const selectAll = wrapper.get('[data-testid="rule-table-select-all"]')
    expect(selectAll.element.indeterminate).toBe(true)
    const rows = wrapper.findAll('tbody .rules-table__row')
    expect(rows[0].classes()).toContain('rules-table__row--selected')
    expect(rows[0].get('.rules-table__cell-select input').element.checked).toBe(true)

    await selectAll.trigger('change')
    expect(wrapper.emitted('select-all')).toHaveLength(1)
    await rows[1].trigger('click')
    await rows[1].get('.rules-table__cell-select input').trigger('change')
    expect(wrapper.emitted('select')).toEqual([[rules[1]], [rules[1]]])

    // Bulk mode disables toggle and edit and hides action menus & copy buttons.
    const toggleBtn = rows[1].get('.toggle')
    const editBtn = rows[1].get('[data-action="edit"]')
    expect(toggleBtn.attributes('disabled')).toBeDefined()
    expect(editBtn.attributes('disabled')).toBeDefined()
    expect(wrapper.find('.base-action-menu__trigger').exists()).toBe(false)
    expect(wrapper.find('[data-copy-target="frontend"]').exists()).toBe(false)
    expect(wrapper.find('[data-copy-target="backend"]').exists()).toBe(false)

    await toggleBtn.trigger('click')
    await editBtn.trigger('click')
    expect(wrapper.emitted('toggle')).toBeUndefined()
    expect(wrapper.emitted('edit')).toBeUndefined()
    expect(wrapper.emitted('select')).toHaveLength(2)
  })

  it('copies frontend URL and primary backend URL in normal mode without extra suffixes', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText },
      configurable: true
    })
    const successSpy = vi.spyOn(messageStore, 'success')
    const multiRules = [
      {
        ...rules[0],
        backends: [{ url: 'http://10.0.0.1:8096' }, { url: 'http://10.0.0.9:8096' }]
      },
      {
        id: 3,
        agent_id: 'a',
        frontend_url: 'https://provider.example.com',
        enabled: true,
        tags: [],
        backends: [{ kind: 'plugin_provider', plugin_provider: { instance_id: 'inst-1', provider_id: 'default' } }]
      }
    ]

    const wrapper = mount(RuleTable, { props: { rules: multiRules }, global: { stubs } })
    const rows = wrapper.findAll('tbody .rules-table__row')
    expect(rows[0].get('.rules-table__url--backend').text()).toBe('http://10.0.0.1:8096 +1')

    await rows[0].get('[data-copy-target="frontend"]').trigger('click')
    await flushPromises()
    expect(writeText).toHaveBeenCalledWith('https://one.example.com')

    await rows[0].get('[data-copy-target="backend"]').trigger('click')
    await flushPromises()
    expect(writeText).toHaveBeenCalledWith('http://10.0.0.1:8096')

    expect(rows[1].find('[data-copy-target="frontend"]').exists()).toBe(true)
    expect(rows[1].find('[data-copy-target="backend"]').exists()).toBe(false)
    expect(wrapper.emitted('select')).toBeUndefined()
    expect(wrapper.emitted('edit')).toBeUndefined()
    expect(successSpy).toHaveBeenCalledWith('已复制入口 URL')
    expect(successSpy).toHaveBeenCalledWith('已复制后端地址')
    successSpy.mockRestore()
  })

  it('shows an ellipsizing node column only when no agent is selected', () => {
    const all = mount(RuleTable, { props: { rules }, global: { stubs } })
    expect(all.get('table').classes()).toContain('rules-table--with-agent')
    expect(all.findAll('tbody .rules-table__cell-agent')).toHaveLength(2)
    const one = mount(RuleTable, { props: { rules, agent: { id: 'a', name: 'A' } }, global: { stubs } })
    expect(one.get('table').classes()).not.toContain('rules-table--with-agent')
    expect(one.find('.rules-table__cell-agent').exists()).toBe(false)
  })
})

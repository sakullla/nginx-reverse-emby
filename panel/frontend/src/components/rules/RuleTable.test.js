import { mount } from '@vue/test-utils'
import { afterEach, describe, expect, it } from 'vitest'
import { nextTick } from 'vue'
import RuleTable from './RuleTable.vue'

const rules = [
  { id: 1, agent_id: 'a', frontend_url: 'https://one.example.com', enabled: true, tags: ['emby', 'web', 'media', 'cdn'], backends: [{ url: 'http://10.0.0.1:8096' }] },
  { id: 2, agent_id: 'a', frontend_url: 'http://two.example.com', enabled: false, tags: ['solo'], backends: [{ url: 'http://10.0.0.2' }] }
]

const stubs = { RouterLink: { props: ['to'], template: '<a><slot /></a>' } }

afterEach(() => {
  document.body.innerHTML = ''
})

describe('RuleTable', () => {
  it('shows at most two tags plus a +N badge whose title lists every tag', () => {
    const wrapper = mount(RuleTable, { props: { rules }, global: { stubs } })
    const rows = wrapper.findAll('tbody .rules-table__row')
    const firstTags = rows[0].get('.rules-table__tags')
    expect(firstTags.findAll('.tag:not(.tag--more)').map((tag) => tag.text())).toEqual(['emby', 'web'])
    expect(firstTags.get('[data-testid="rule-table-tags-more"]').text()).toBe('+2')
    expect(firstTags.attributes('title')).toBe('emby, web, media, cdn')
    expect(rows[1].find('[data-testid="rule-table-tags-more"]').exists()).toBe(false)
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

  it('adds a selection column with page select-all in bulk mode', async () => {
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
    // Clicking the switch in bulk mode still toggles, without changing selection.
    await rows[1].get('.toggle').trigger('click')
    expect(wrapper.emitted('toggle')?.[0]).toEqual([rules[1]])
    expect(wrapper.emitted('select')).toHaveLength(2)
  })
})

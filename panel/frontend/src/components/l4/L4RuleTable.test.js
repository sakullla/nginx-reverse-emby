import { mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import L4RuleTable, { fitTagCount } from './L4RuleTable.vue'

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
function mockLayout(tagsWidth) {
  vi.spyOn(Element.prototype, 'getBoundingClientRect').mockImplementation(function () {
    let width = 0
    if (this.classList.contains('rules-table__tags')) width = tagsWidth
    else if (this.classList.contains('tag--more')) width = 24
    else if (this.hasAttribute('data-tag')) width = this.textContent.trim().length * 10 + 10
    return { width, height: 20, top: 0, left: 0, right: width, bottom: 20, x: 0, y: 0 }
  })
  vi.spyOn(window, 'getComputedStyle').mockImplementation(() => ({ columnGap: '0px' }))
}

async function mountMeasured(rules, tagsWidth) {
  mockLayout(tagsWidth)
  const wrapper = mount(L4RuleTable, { props: { rules }, attachTo: document.body })
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

import { afterEach, describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import AgentFilterBar from './AgentFilterBar.vue'

const baseProps = {
  view: 'monitor',
  searchQuery: '',
  statusFilter: '',
  modeFilter: '',
  tagFilter: '',
  sortField: 'last_seen_at',
  sortOrder: 'desc',
  availableTags: ['edge', 'hk']
}

let wrapper

function mountBar(props = {}) {
  wrapper = mount(AgentFilterBar, {
    props: { ...baseProps, ...props },
    attachTo: document.body
  })
  return wrapper
}

async function openPanel(w) {
  await w.get('.resource-list-filter-bar__filter-trigger').trigger('click')
  await nextTick()
  return document.body.querySelector('.resource-list-filter-bar__panel')
}

function fieldButton(panel, key, value) {
  return panel.querySelector(`[data-field-key="${key}"] button[data-value="${value}"]`)
}

afterEach(() => {
  wrapper?.unmount()
  wrapper = null
  document.body.innerHTML = ''
})

describe('AgentFilterBar', () => {
  it('renders search first without an agent selector and emits search updates', async () => {
    const w = mountBar({ availableTags: [] })

    const fields = w.findAll('.resource-list-filter-bar__toolbar > .resource-list-filter-bar__field')
    expect(fields[0].classes()).toContain('resource-list-filter-bar__field--search')
    expect(w.find('.resource-list-filter-bar__field--agent').exists()).toBe(false)

    await w.get('input[type="search"]').setValue('edge')
    expect(w.emitted('update:searchQuery')?.at(-1)).toEqual(['edge'])
  })

  it('sets status, mode and tag through the filter panel with unchanged emits', async () => {
    const w = mountBar()
    const panel = await openPanel(w)
    expect(panel).not.toBeNull()

    fieldButton(panel, 'status', 'offline').click()
    fieldButton(panel, 'mode', 'pull').click()
    fieldButton(panel, 'tag', 'hk').click()
    await nextTick()

    expect(w.emitted('update:statusFilter')?.at(-1)).toEqual(['offline'])
    expect(w.emitted('update:modeFilter')?.at(-1)).toEqual(['pull'])
    expect(w.emitted('update:tagFilter')?.at(-1)).toEqual(['hk'])
  })

  it('shows active filters as removable condition chips', async () => {
    const w = mountBar({ statusFilter: 'online', tagFilter: 'edge' })

    const conditions = w.findAll('.resource-list-filter-bar__condition')
    expect(conditions.map((c) => c.text())).toEqual(['状态: 在线', '标签: edge'])
    expect(w.get('.resource-list-filter-bar__filter-badge').text()).toBe('2')

    await conditions[0].trigger('click')
    expect(w.emitted('update:statusFilter')?.at(-1)).toEqual([''])

    await w.get('.resource-list-filter-bar__reset--inline').trigger('click')
    expect(w.emitted('update:tagFilter')?.at(-1)).toEqual([''])
  })

  it('omits the tag field when no tags exist', async () => {
    const w = mountBar({ availableTags: [] })
    const panel = await openPanel(w)
    expect(panel.querySelector('[data-field-key="tag"]')).toBeNull()
    expect(panel.querySelector('[data-field-key="status"]')).not.toBeNull()
  })

  it('keeps sort field, sort direction and view toggle controls', async () => {
    const w = mountBar()

    await w.get('button[aria-label="排序字段"]').trigger('click')
    const nameOption = w.findAll('[role="option"]').find((option) => option.text() === '名称')
    await nameOption.trigger('click')
    expect(w.emitted('update:sortField')?.at(-1)).toEqual(['name'])

    await w.get('.sort-order-btn').trigger('click')
    expect(w.emitted('toggle-sort-order')).toHaveLength(1)

    await w.get('button[aria-label="列表视图"]').trigger('click')
    expect(w.emitted('update:view')?.at(-1)).toEqual(['list'])
  })
})

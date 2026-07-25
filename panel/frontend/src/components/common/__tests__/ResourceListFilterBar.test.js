import { afterEach, describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import ResourceListFilterBar from '../ResourceListFilterBar.vue'

const AGENTS = [
  { id: 1, name: 'master' },
  { id: 2, name: 'edge-1' }
]

const FILTER_FIELDS = [
  {
    key: 'enabled',
    label: '启用状态',
    type: 'chip',
    options: [
      { value: '', label: '全部' },
      { value: '1', label: '启用' },
      { value: '0', label: '停用' }
    ]
  },
  {
    key: 'sync',
    label: '同步状态',
    type: 'chip',
    options: [
      { value: '', label: '全部' },
      { value: 'applied', label: '已同步' },
      { value: 'pending', label: '待同步' }
    ]
  },
  {
    key: 'certificate_id',
    label: '证书',
    type: 'select',
    options: [
      { value: '', label: '全部' },
      { value: '7', label: 'example.com' }
    ]
  },
  {
    key: 'tags',
    label: '标签',
    type: 'multi',
    options: [
      { value: 'emby', label: 'emby' },
      { value: 'web', label: 'web' },
      { value: 'media', label: 'media' },
      { value: 'archive', label: 'archive' }
    ]
  }
]

const mountedWrappers = []

function mountBar(props = {}) {
  const wrapper = mount(ResourceListFilterBar, {
    attachTo: document.body,
    props: {
      agentId: '',
      agents: AGENTS,
      q: '',
      filterFields: FILTER_FIELDS,
      filterValues: {},
      ...props
    },
    global: {
      stubs: {
        // Keep panel open/close synchronous in jsdom (no CSS transitionend).
        // Teleport stays real so the panel mounts on document.body.
        Transition: true,
        AgentSearchSelect: {
          props: ['modelValue', 'agents'],
          template: '<div class="agent-search-select-stub" />'
        }
      }
    }
  })
  mountedWrappers.push(wrapper)
  return wrapper
}

function panelRoot() {
  return document.body.querySelector('.resource-list-filter-bar__panel')
}

function panelQuery(selector) {
  const root = panelRoot()
  return root ? root.querySelector(selector) : null
}

function panelQueryAll(selector) {
  const root = panelRoot()
  return root ? Array.from(root.querySelectorAll(selector)) : []
}

async function openPanel(wrapper) {
  await wrapper.find('.resource-list-filter-bar__filter-trigger').trigger('click')
  await nextTick()
  await nextTick()
}

afterEach(() => {
  while (mountedWrappers.length) {
    const wrapper = mountedWrappers.pop()
    wrapper.unmount()
  }
  document.body.innerHTML = ''
})

describe('ResourceListFilterBar', () => {
  it('exposes status fields via the filter panel and emits their values', async () => {
    const wrapper = mountBar({ filterFields: FILTER_FIELDS.filter((f) => f.type === 'chip') })
    await openPanel(wrapper)
    panelQuery('[data-field-key="enabled"] .resource-list-filter-bar__segment[data-value="1"]').click()
    await nextTick()
    expect(wrapper.emitted('update:filter')).toEqual([[{ key: 'enabled', value: '1' }]])
  })

  it('clears a status field when the active segment is clicked again', async () => {
    const wrapper = mountBar({ filterValues: { enabled: '1' } })
    await openPanel(wrapper)
    panelQuery('[data-field-key="enabled"] .resource-list-filter-bar__segment[data-value="1"]').click()
    await nextTick()
    expect(wrapper.emitted('update:filter')).toEqual([[{ key: 'enabled', value: '' }]])
  })

  it('emits select changes from the panel', async () => {
    const wrapper = mountBar()
    await openPanel(wrapper)
    const certSelect = panelQueryAll('.resource-list-filter-bar__select')
      .find((node) => node.getAttribute('aria-label') === '证书')
    certSelect.value = '7'
    certSelect.dispatchEvent(new Event('change'))
    await nextTick()
    expect(wrapper.emitted('update:filter')).toEqual([[{ key: 'certificate_id', value: '7' }]])
  })

  it('toggles multi options and emits arrays', async () => {
    const wrapper = mountBar({ filterValues: { tags: ['emby'] } })
    await openPanel(wrapper)
    const candidates = panelQueryAll('.resource-list-filter-bar__multi-candidates .resource-list-filter-bar__chip')
    candidates.find((chip) => chip.textContent.trim() === 'web').click()
    await nextTick()
    expect(wrapper.emitted('update:filter')).toEqual([[{ key: 'tags', value: ['emby', 'web'] }]])
  })

  it('removes a selected multi value from the selected strip', async () => {
    const wrapper = mountBar({ filterValues: { tags: ['emby', 'web'] } })
    await openPanel(wrapper)
    const selected = panelQueryAll('.resource-list-filter-bar__multi-selected .resource-list-filter-bar__chip')
    expect(selected.map((chip) => chip.textContent.replace(/\s+/g, ''))).toEqual(
      expect.arrayContaining(['emby', 'web'].map((label) => expect.stringContaining(label)))
    )
    selected.find((chip) => chip.textContent.includes('emby')).click()
    await nextTick()
    expect(wrapper.emitted('update:filter')).toEqual([[{ key: 'tags', value: ['web'] }]])
  })

  it('filters multi candidates by search without mixing selected into the candidate list', async () => {
    const wrapper = mountBar({ filterValues: { tags: ['archive'] } })
    await openPanel(wrapper)
    const search = panelQuery('.resource-list-filter-bar__multi-search')
    search.value = 'web'
    search.dispatchEvent(new Event('input'))
    await nextTick()

    const selectedLabels = panelQueryAll('.resource-list-filter-bar__multi-selected .resource-list-filter-bar__chip')
      .map((chip) => chip.textContent)
    expect(selectedLabels.some((text) => text.includes('archive'))).toBe(true)

    const candidateLabels = panelQueryAll('.resource-list-filter-bar__multi-candidates .resource-list-filter-bar__chip')
      .map((chip) => chip.textContent.trim())
    expect(candidateLabels).toEqual(['web'])
  })

  it('shows no multi candidates when search has no match', async () => {
    mountBar()
    const wrapper = mountedWrappers[mountedWrappers.length - 1]
    await openPanel(wrapper)
    const search = panelQuery('.resource-list-filter-bar__multi-search')
    search.value = 'zzz-no-match'
    search.dispatchEvent(new Event('input'))
    await nextTick()
    expect(
      panelQueryAll('.resource-list-filter-bar__multi-candidates .resource-list-filter-bar__chip')
    ).toHaveLength(0)
  })

  it('resolves condition tags from active filters and agents', () => {
    const wrapper = mountBar({
      agentId: '2',
      agentBaseline: '',
      filterValues: { enabled: '1', tags: ['emby', 'web'] }
    })
    const texts = wrapper.findAll('.resource-list-filter-bar__condition').map((tag) => tag.text())
    expect(texts.some((text) => text.includes('节点: edge-1'))).toBe(true)
    expect(texts.some((text) => text.includes('启用状态: 启用'))).toBe(true)
    expect(texts.some((text) => text.includes('标签: emby'))).toBe(true)
    expect(texts.some((text) => text.includes('标签: web'))).toBe(true)
    expect(texts.some((text) => text.includes('emby、web'))).toBe(false)
  })

  it('removes a single-value condition tag by emitting its baseline', async () => {
    const wrapper = mountBar({ filterValues: { enabled: '1' } })
    await wrapper.find('.resource-list-filter-bar__condition').trigger('click')
    expect(wrapper.emitted('update:filter')).toEqual([[{ key: 'enabled', value: '' }]])
  })

  it('removes one multi condition tag without clearing other selected values', async () => {
    const wrapper = mountBar({ filterValues: { tags: ['emby', 'web'] } })
    const tags = wrapper.findAll('.resource-list-filter-bar__condition')
    const embyTag = tags.find((tag) => tag.text().includes('标签: emby'))
    await embyTag.trigger('click')
    expect(wrapper.emitted('update:filter')).toEqual([[{ key: 'tags', value: ['web'] }]])
  })

  it('removes the agent condition by emitting the agent baseline', async () => {
    const wrapper = mountBar({ agentId: '2', agentBaseline: '__all__' })
    await wrapper.find('.resource-list-filter-bar__condition').trigger('click')
    expect(wrapper.emitted('update:agentId')).toEqual([['__all__']])
  })

  it('does not emit an agent condition at baseline', () => {
    const wrapper = mountBar({ agentId: '__all__', agentBaseline: '__all__' })
    expect(wrapper.find('.resource-list-filter-bar__condition').exists()).toBe(false)
  })

  it('resets all active filters to their baselines', async () => {
    const wrapper = mountBar({ filterValues: { enabled: '1', tags: ['emby'], certificate_id: '7' } })
    await wrapper.find('.resource-list-filter-bar__reset--inline').trigger('click')
    const emitted = wrapper.emitted('update:filter')
    expect(emitted).toContainEqual([{ key: 'enabled', value: '' }])
    expect(emitted).toContainEqual([{ key: 'tags', value: [] }])
    expect(emitted).toContainEqual([{ key: 'certificate_id', value: '' }])
    expect(emitted).toHaveLength(3)
  })

  it('emits search updates and clears the query', async () => {
    const wrapper = mountBar({ q: 'emby' })
    await wrapper.find('.resource-list-filter-bar__input').setValue('web')
    expect(wrapper.emitted('update:q')).toEqual([['web']])

    await wrapper.find('.resource-list-filter-bar__clear').trigger('click')
    expect(wrapper.emitted('update:q')[1]).toEqual([''])
  })

  it('opens a grouped filter panel and closes it from the header control', async () => {
    const wrapper = mountBar()
    expect(panelRoot()).toBeNull()

    await openPanel(wrapper)
    const panel = panelRoot()
    expect(panel).not.toBeNull()
    expect(panel.getAttribute('aria-label')).toBe('筛选条件')
    expect(panelQuery('[data-group="status"]')).not.toBeNull()
    expect(panelQuery('[data-group="resource"]')).not.toBeNull()
    expect(panelQuery('[data-group="tags"]')).not.toBeNull()

    panelQuery('.resource-list-filter-bar__panel-close').click()
    await nextTick()
    expect(panelRoot()).toBeNull()
    expect(wrapper.find('.resource-list-filter-bar__filter-trigger').attributes('aria-expanded')).toBe('false')
  })
})

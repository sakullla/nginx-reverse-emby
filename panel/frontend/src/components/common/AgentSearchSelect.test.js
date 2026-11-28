import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import AgentSearchSelect from './AgentSearchSelect.vue'
import BaseModal from '../base/BaseModal.vue'
import { ALL_AGENTS_FILTER } from '../../utils/agentFilter.js'

const AGENTS = [
  {
    id: 'edge-hk',
    name: '香港入口节点',
    status: 'online',
    last_seen_at: '2026-11-28T08:00:00Z',
  },
  {
    id: 'origin-nas',
    name: '家庭源站',
    status: 'online',
    last_seen_at: '2026-11-28T08:15:00Z',
  },
  {
    id: 'backup-jp',
    name: '东京备用',
    status: 'offline',
    last_seen_at: '2026-11-27T12:00:00Z',
  },
]

let wrapper

function mountSelect(props = {}) {
  wrapper = mount(AgentSearchSelect, {
    attachTo: document.body,
    props: {
      agents: AGENTS,
      ...props,
    },
  })
  return wrapper
}

function getDropdown() {
  return document.body.querySelector('.agent-search-select__dropdown')
}

function getOptions() {
  return Array.from(document.body.querySelectorAll('.agent-search-select__option'))
}

function getSearchInput() {
  return document.body.querySelector('.agent-search-select__search-input')
}

async function openByClick(w) {
  await w.get('.agent-search-select__trigger').trigger('click')
  await nextTick()
}

afterEach(() => {
  wrapper?.unmount()
  wrapper = null
  document.body.innerHTML = ''
  vi.restoreAllMocks()
})

describe('AgentSearchSelect', () => {
  it('preserves default list filter mode with all-agents option', async () => {
    const w = mountSelect()
    const trigger = w.get('.agent-search-select__trigger')

    expect(trigger.text()).toContain('全部节点')
    expect(trigger.attributes('aria-expanded')).toBe('false')
    expect(getDropdown()).toBeNull()

    await openByClick(w)
    expect(trigger.attributes('aria-expanded')).toBe('true')
    const dropdown = getDropdown()
    expect(dropdown).not.toBeNull()
    expect(dropdown.getAttribute('role')).toBe('listbox')

    const options = getOptions()
    expect(options).toHaveLength(4)
    expect(options[0].textContent).toContain('全部节点')
    expect(options[0].getAttribute('aria-selected')).toBe('true')

    // Select an agent
    options[1].click()
    await nextTick()
    expect(w.emitted('update:modelValue')).toEqual([['origin-nas']])
    expect(getDropdown()).toBeNull()

    // Select all agents again when an agent is currently selected
    await w.setProps({ modelValue: 'origin-nas' })
    expect(trigger.text()).toContain('家庭源站')
    await openByClick(w)
    getOptions()[0].click()
    await nextTick()
    expect(w.emitted('update:modelValue').at(-1)).toEqual([ALL_AGENTS_FILTER])
  })

  it('supports form single-select mode with placeholder, missing node label, and accessibility attributes', async () => {
    const w = mountSelect({
      showAllOption: false,
      modelValue: '',
      id: 'service-agent',
      placeholder: '请选择服务侧节点',
      missingLabel: '节点已移除',
      invalid: true,
      ariaDescribedby: 'service-agent-error',
      ariaLabel: '服务侧节点',
    })

    const trigger = w.get('#service-agent')
    expect(trigger.attributes('aria-invalid')).toBe('true')
    expect(trigger.attributes('aria-describedby')).toBe('service-agent-error')
    expect(trigger.attributes('aria-label')).toBe('服务侧节点')
    expect(trigger.classes()).toContain('agent-search-select__trigger--invalid')
    expect(trigger.text()).toContain('请选择服务侧节点')
    expect(trigger.find('.agent-search-select__status-dot').exists()).toBe(false)

    // Opening hides "全部节点"
    await openByClick(w)
    const options = getOptions()
    expect(options).toHaveLength(3)
    expect(options.some((opt) => opt.textContent.includes('全部节点'))).toBe(false)

    // Close and set a missing agent ID: displays missingLabel and does not clear modelValue
    await openByClick(w)
    await w.setProps({ modelValue: 'deleted-node' })
    expect(trigger.text()).toContain('节点已移除')
    expect(trigger.find('.agent-search-select__label--missing').exists()).toBe(true)
    expect(w.emitted('update:modelValue')).toBeUndefined()

    // Set a valid agent ID: displays name + status dot
    await w.setProps({ modelValue: 'edge-hk', invalid: false })
    expect(trigger.text()).toContain('香港入口节点')
    expect(trigger.attributes('aria-invalid')).toBeUndefined()
    expect(trigger.find('.agent-search-select__status-dot--online').exists()).toBe(true)
  })

  it('prevents opening when disabled and closes if disabled while open', async () => {
    const w = mountSelect({ showAllOption: false, modelValue: 'edge-hk', disabled: true })
    const trigger = w.get('.agent-search-select__trigger')
    expect(trigger.attributes('disabled')).toBeDefined()

    await trigger.trigger('click')
    await trigger.trigger('keydown', { key: 'ArrowDown' })
    await nextTick()
    expect(getDropdown()).toBeNull()

    await w.setProps({ disabled: false })
    await openByClick(w)
    expect(getDropdown()).not.toBeNull()

    await w.setProps({ disabled: true })
    await nextTick()
    expect(getDropdown()).toBeNull()
  })

  it('searches by both agent name and agent id, filters by status, sorts, and renders empty state', async () => {
    const w = mountSelect({ showAllOption: false, modelValue: '' })
    await openByClick(w)

    const input = getSearchInput()
    expect(document.activeElement).toBe(input)

    // Search by agent ID
    input.value = 'edge-hk'
    input.dispatchEvent(new Event('input', { bubbles: true }))
    await nextTick()
    expect(getOptions()).toHaveLength(1)
    expect(getOptions()[0].textContent).toContain('香港入口节点')

    // Clear search button
    const clearBtn = document.body.querySelector('.agent-search-select__search-clear')
    expect(clearBtn).not.toBeNull()
    clearBtn.click()
    await nextTick()
    expect(getOptions()).toHaveLength(3)

    // Filter offline
    const offlineBtn = document.body.querySelector('.agent-search-select__filter-btn--offline')
    offlineBtn.click()
    await nextTick()
    expect(getOptions()).toHaveLength(1)
    expect(getOptions()[0].textContent).toContain('东京备用')

    // Unmatched search shows empty message
    input.value = 'does-not-exist'
    input.dispatchEvent(new Event('input', { bubbles: true }))
    await nextTick()
    expect(getOptions()).toHaveLength(0)
    expect(document.body.querySelector('.agent-search-select__empty')?.textContent).toContain('没有匹配的节点')

    // Reset filter and test sort by name vs last_seen
    document.body.querySelector('.agent-search-select__filter-btn--all').click()
    input.value = ''
    input.dispatchEvent(new Event('input', { bubbles: true }))
    await nextTick()
    // Default last_seen order: origin-nas (08:15), edge-hk (08:00), backup-jp (prev day)
    expect(getOptions().map((el) => el.dataset.agentId)).toEqual(['origin-nas', 'edge-hk', 'backup-jp'])

    document.body.querySelector('.agent-search-select__sort-btn--name').click()
    await nextTick()
    const nameSorted = getOptions().map((el) => el.dataset.agentId)
    expect(nameSorted).toHaveLength(3)
    expect(new Set(nameSorted)).toEqual(new Set(['origin-nas', 'edge-hk', 'backup-jp']))
  })

  it('supports keyboard navigation, selection, and two-stage Escape restoring focus', async () => {
    const w = mountSelect({ showAllOption: false, modelValue: '' })
    const trigger = w.get('.agent-search-select__trigger')

    // ArrowDown opens dropdown with first option highlighted
    await trigger.trigger('keydown', { key: 'ArrowDown' })
    await nextTick()
    expect(getDropdown()).not.toBeNull()
    expect(getOptions()[0].classList.contains('agent-search-select__option--highlighted')).toBe(true)

    const input = getSearchInput()
    // Move highlight down to second option and select with Enter
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }))
    await nextTick()
    expect(getOptions()[1].classList.contains('agent-search-select__option--highlighted')).toBe(true)

    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }))
    await nextTick()
    expect(w.emitted('update:modelValue')).toEqual([['edge-hk']])
    expect(getDropdown()).toBeNull()
    expect(document.activeElement).toBe(trigger.element)

    // Reopen with Space, type search, Escape clears search first, second Escape closes and restores focus
    await trigger.trigger('keydown', { key: ' ' })
    await nextTick()
    const input2 = getSearchInput()
    input2.value = '东京'
    input2.dispatchEvent(new Event('input', { bubbles: true }))
    await nextTick()
    expect(getOptions()).toHaveLength(1)

    input2.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await nextTick()
    expect(getDropdown()).not.toBeNull()
    expect(input2.value).toBe('')
    expect(getOptions()).toHaveLength(3)

    input2.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await nextTick()
    expect(getDropdown()).toBeNull()
    expect(document.activeElement).toBe(trigger.element)
  })

  it('positions teleported dropdown with fixed viewport clamping, flips above near bottom, and handles outside clicks', async () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1200 })
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 700 })

    const w = mountSelect({ showAllOption: false, modelValue: '' })
    const triggerEl = w.get('.agent-search-select__trigger').element
    vi.spyOn(triggerEl, 'getBoundingClientRect').mockReturnValue({
      top: 620,
      bottom: 658,
      left: 1000,
      right: 1180,
      width: 180,
      height: 38,
    })

    await openByClick(w)
    const dropdown = getDropdown()
    expect(dropdown.style.position).toBe('fixed')
    expect(dropdown.style.zIndex).toBe('var(--z-popover, 600)')
    expect(dropdown.classList.contains('agent-search-select__dropdown--above')).toBe(true)
    expect(Number.parseInt(dropdown.style.top, 10)).toBeLessThan(620)
    // Clamped inside right edge of viewport (1200 - 8 - 320 = 872)
    expect(Number.parseInt(dropdown.style.left, 10)).toBeLessThanOrEqual(872)

    // Clicking inside teleported dropdown keeps it open
    dropdown.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }))
    await nextTick()
    expect(getDropdown()).not.toBeNull()

    // Clicking outside closes it without emitting
    document.body.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }))
    await nextTick()
    expect(getDropdown()).toBeNull()
    expect(w.emitted('update:modelValue')).toBeUndefined()
  })

  it('works inside BaseModal (default dialog & alertdialog role) and Escape closes dropdown without closing modal', async () => {
    const defaultModal = mount(BaseModal, {
      attachTo: document.body,
      props: { modelValue: true, title: '默认弹窗' },
    })
    await nextTick()
    expect(document.body.querySelector('.modal')?.getAttribute('role')).toBe('dialog')
    defaultModal.unmount()

    const onModalUpdate = vi.fn()
    wrapper = mount(
      {
        components: { BaseModal, AgentSearchSelect },
        template: `
          <BaseModal :model-value="true" title="节点表单" role="alertdialog" @update:model-value="onUpdate">
            <AgentSearchSelect
              :agents="agents"
              :show-all-option="false"
              model-value=""
              id="modal-agent"
            />
          </BaseModal>
        `,
        data: () => ({ agents: AGENTS }),
        methods: { onUpdate: onModalUpdate },
      },
      { attachTo: document.body },
    )

    await nextTick()
    const dialog = document.body.querySelector('.modal')
    expect(dialog?.getAttribute('role')).toBe('alertdialog')

    const trigger = document.body.querySelector('#modal-agent')
    trigger.click()
    await nextTick()
    expect(getDropdown()).not.toBeNull()

    // Pressing Escape inside dropdown closes dropdown only, not BaseModal
    const input = getSearchInput()
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await nextTick()
    expect(getDropdown()).toBeNull()
    expect(onModalUpdate).not.toHaveBeenCalled()
  })

  it('handles empty agent list gracefully without throwing', async () => {
    const w = mountSelect({ showAllOption: false, modelValue: '', agents: [] })
    await openByClick(w)
    expect(getOptions()).toHaveLength(0)
    expect(document.body.querySelector('.agent-search-select__empty')?.textContent).toContain('没有匹配的节点')

    const input = getSearchInput()
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }))
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }))
    await nextTick()
    expect(w.emitted('update:modelValue')).toBeUndefined()
  })
})

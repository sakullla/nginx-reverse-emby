import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import { nextTick } from 'vue'
import RuleCard from './RuleCard.vue'

function baseRule(overrides = {}) {
  return {
    id: 3,
    frontend_url: 'https://media.example.com',
    backends: [{ url: 'http://127.0.0.1:8096' }],
    enabled: true,
    tags: [],
    ...overrides,
  }
}

function mountRule(overrides = {}, extra = {}) {
  return mount(RuleCard, {
    props: {
      rule: baseRule(overrides),
      ...extra,
    },
    attachTo: document.body,
  })
}

describe('RuleCard redesign', () => {
  it('uses frontend_url as BaseListCard title', () => {
    const wrapper = mountRule()
    expect(wrapper.find('.base-list-card__title').text()).toBe('https://media.example.com')
  })

  it('shows 生效中 for active enabled rules', () => {
    const wrapper = mountRule({ enabled: true })
    expect(wrapper.text()).toContain('生效中')
    expect(wrapper.find('.base-list-card').attributes('data-status')).toBe('success')
  })

  it('shows 已禁用 when disabled', () => {
    const wrapper = mountRule({ enabled: false })
    expect(wrapper.text()).toContain('已禁用')
    expect(wrapper.find('.base-list-card--disabled').exists()).toBe(true)
  })

  it('exposes toggle and edit as primary actions', () => {
    const wrapper = mountRule()
    expect(wrapper.find('button[title="停用"]').exists()).toBe(true)
    expect(wrapper.find('button[title="编辑"]').exists()).toBe(true)
    expect(wrapper.find('button[title="复制"]').exists()).toBe(false)
    expect(wrapper.find('button[title="删除"]').exists()).toBe(false)
  })

  it('puts copy/diagnose/delete in more menu and emits original events', async () => {
    const wrapper = mountRule()
    await wrapper.find('.base-action-menu__trigger').trigger('click')
    await nextTick()
    await wrapper.find('[data-testid="base-action-menu-item-copy"]').trigger('click')
    await nextTick()
    expect(wrapper.emitted('copy')).toBeTruthy()

    await wrapper.find('.base-action-menu__trigger').trigger('click')
    await nextTick()
    await wrapper.find('[data-testid="base-action-menu-item-diagnose"]').trigger('click')
    await nextTick()
    expect(wrapper.emitted('diagnose')).toBeTruthy()

    await wrapper.find('.base-action-menu__trigger').trigger('click')
    await nextTick()
    await wrapper.find('[data-testid="base-action-menu-item-delete"]').trigger('click')
    await nextTick()
    expect(wrapper.emitted('delete')).toBeTruthy()
  })
})

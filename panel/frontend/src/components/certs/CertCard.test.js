import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import CertCard from './CertCard.vue'

function baseCert(overrides = {}) {
  return {
    id: 101,
    domain: 'example.com',
    scope: 'domain',
    status: 'active',
    enabled: true,
    usage: 'https',
    certificate_type: 'acme',
    tags: [],
    ...overrides,
  }
}

function mountCert(overrides = {}) {
  return mount(CertCard, { props: { cert: baseCert(overrides) } })
}

describe('CertCard issuing/error display (R3)', () => {
  it('shows a disabled spinner and 签发中 label while issuing, with no issue button', () => {
    const wrapper = mountCert({ status: 'issuing' })
    expect(wrapper.find('.cert-card__spin').exists()).toBe(true)
    const spinner = wrapper.find('button[title="签发中"]')
    expect(spinner.exists()).toBe(true)
    expect(spinner.attributes('disabled')).toBeDefined()
    expect(wrapper.find('button[title="签发"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('签发中')
  })

  it('offers an issue button on error certs that emits issue', async () => {
    const wrapper = mountCert({ status: 'error', last_error: 'dns fail', next_retry_at_unix: 0 })
    const issueBtn = wrapper.find('button[title="签发"]')
    expect(issueBtn.exists()).toBe(true)
    await issueBtn.trigger('click')
    expect(wrapper.emitted('issue')).toBeTruthy()
    expect(wrapper.text()).toContain('签发失败')
    expect(wrapper.text()).toContain('dns fail')
  })

  it('offers an issue button on pending certs that emits issue', async () => {
    const wrapper = mountCert({ status: 'pending' })
    const issueBtn = wrapper.find('button[title="签发"]')
    expect(issueBtn.exists()).toBe(true)
    await issueBtn.trigger('click')
    expect(wrapper.emitted('issue')).toBeTruthy()
    expect(wrapper.text()).toContain('待签发')
  })

  it('shows neither spinner nor issue button on active certs', () => {
    const wrapper = mountCert({ status: 'active' })
    expect(wrapper.find('.cert-card__spin').exists()).toBe(false)
    expect(wrapper.find('button[title="签发"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('生效中')
  })

  it('renders the 已禁用 label when disabled, regardless of underlying status', () => {
    const wrapper = mountCert({ status: 'issuing', enabled: false })
    expect(wrapper.text()).toContain('已禁用')
  })

  it('formats next-retry with the count suffix when both fields are set', () => {
    const ts = Math.floor(Date.now() / 1000) + 600
    const wrapper = mountCert({ status: 'error', last_error: 'boom', next_retry_at_unix: ts, retry_count: 3 })
    expect(wrapper.text()).toContain('下次重试')
    expect(wrapper.text()).toContain('（第 3 次）')
  })

  it('omits the next-retry line when next_retry_at_unix is missing or non-positive', () => {
    const wrapper = mountCert({ status: 'error', last_error: 'boom', next_retry_at_unix: 0, retry_count: 3 })
    expect(wrapper.text()).not.toContain('下次重试')
  })

  it('omits the count suffix when retry_count is zero', () => {
    const ts = Math.floor(Date.now() / 1000) + 600
    const wrapper = mountCert({ status: 'error', last_error: 'boom', next_retry_at_unix: ts, retry_count: 0 })
    expect(wrapper.text()).toContain('下次重试')
    expect(wrapper.text()).not.toContain('第')
  })
})

describe('CertCard agent ownership badge', () => {
  it('renders agent_name without page agent prop', () => {
    const wrapper = mountCert({ agent_name: 'edge-a', agent_id: 'a1' })
    expect(wrapper.text()).toContain('edge-a')
  })

  it('falls back to agent_id when agent_name is absent', () => {
    const wrapper = mountCert({ agent_id: 'node-7' })
    expect(wrapper.text()).toContain('node-7')
  })
})

describe('CertCard redesign', () => {
  it('uses domain as title', () => {
    const wrapper = mountCert({ domain: 'cdn.example.com' })
    expect(wrapper.find('.base-list-card__title').text()).toBe('cdn.example.com')
  })

  it('maps issuing data-status to warning while badge says 签发中', () => {
    const wrapper = mountCert({ status: 'issuing' })
    expect(wrapper.find('.base-list-card').attributes('data-status')).toBe('warning')
    expect(wrapper.text()).toContain('签发中')
  })

  it('keeps delete in more menu for non-system certs', async () => {
    document.body
      .querySelectorAll('[data-testid="base-action-menu-panel"]')
      .forEach((el) => {
        el.style.display = 'none'
        el.setAttribute('aria-hidden', 'true')
      })
    const wrapper = mount(CertCard, {
      props: { cert: baseCert() },
      attachTo: document.body,
    })
    expect(wrapper.find('button[title="删除"]').exists()).toBe(false)
    await wrapper.find('.base-action-menu__trigger').trigger('click')
    await Promise.resolve()
    await Promise.resolve()
    const panel = document.body.querySelector(
      '[data-testid="base-action-menu-panel"]:not([aria-hidden="true"])',
    )
    const item = panel?.querySelector('[data-testid="base-action-menu-item-delete"]')
    expect(item).toBeTruthy()
    item.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await Promise.resolve()
    await Promise.resolve()
    expect(wrapper.emitted('delete')).toBeTruthy()
  })

  it('hides delete menu for system Relay CA', () => {
    const wrapper = mountCert({
      domain: 'relay-ca',
      tags: ['system:relay-ca'],
    })
    expect(wrapper.find('.base-action-menu').exists()).toBe(false)
    expect(wrapper.find('button[title="删除"]').exists()).toBe(false)
  })
})

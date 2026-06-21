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

import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import CertTable from './CertTable.vue'

function cert(overrides = {}) {
  return {
    id: 1,
    domain: 'a.example.com',
    status: 'active',
    enabled: true,
    usage: 'https',
    certificate_type: 'acme',
    tags: [],
    ...overrides,
  }
}

function mountTable(certificates) {
  return mount(CertTable, { props: { certificates } })
}

describe('CertTable status + failure detail (R3)', () => {
  it('renders the 签发中 badge for issuing certificates', () => {
    const wrapper = mountTable([cert({ id: 1, domain: 'a.example.com', status: 'issuing' })])
    expect(wrapper.text()).toContain('签发中')
  })

  it('renders the error reason and next-retry detail for failed certificates', () => {
    const ts = Math.floor(Date.now() / 1000) + 600
    const wrapper = mountTable([cert({
      id: 2, domain: 'b.example.com', status: 'error',
      last_error: 'rate limited', next_retry_at_unix: ts, retry_count: 2,
    })])
    expect(wrapper.text()).toContain('签发失败')
    expect(wrapper.text()).toContain('rate limited')
    expect(wrapper.text()).toContain('下次重试')
    expect(wrapper.text()).toContain('（第 2 次）')
  })

  it('omits the next-retry line when next_retry_at_unix is non-positive', () => {
    const wrapper = mountTable([cert({
      id: 3, domain: 'c.example.com', status: 'error',
      last_error: 'boom', next_retry_at_unix: 0, retry_count: 2,
    })])
    expect(wrapper.text()).not.toContain('下次重试')
  })

  it('shows the empty state when there are no certificates', () => {
    const wrapper = mountTable([])
    expect(wrapper.text()).toContain('暂无数据')
  })

  it('emits edit and delete from the action buttons', async () => {
    const wrapper = mountTable([cert({ id: 4, domain: 'd.example.com', status: 'active' })])
    await wrapper.find('button[title="编辑"]').trigger('click')
    expect(wrapper.emitted('edit')).toBeTruthy()
    await wrapper.find('button[title="删除"]').trigger('click')
    expect(wrapper.emitted('delete')).toBeTruthy()
  })
})

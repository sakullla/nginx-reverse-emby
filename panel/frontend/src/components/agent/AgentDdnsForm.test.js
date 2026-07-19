import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import AgentDdnsForm from './AgentDdnsForm.vue'

function mountForm(modelValue) {
  return mount(AgentDdnsForm, {
    props: { modelValue }
  })
}

function ddnsConfig(overrides = {}) {
  return {
    enabled: true,
    domain: 'media.example.com',
    ipv4: { enabled: true, source: 'public_api', interface: '' },
    ipv6: { enabled: false, source: 'public_api', interface: '' },
    ...overrides
  }
}

describe('AgentDdnsForm', () => {
  it('blocks enabled interface sources without an interface name', () => {
    const wrapper = mountForm(ddnsConfig({
      ipv4: { enabled: true, source: 'interface', interface: ' ' }
    }))

    expect(wrapper.get('[data-testid="agent-ddns-form-save"]').element.disabled).toBe(true)
    expect(wrapper.get('[data-testid="agent-ddns-form-ipv4-interface-required"]').exists()).toBe(true)
  })

  it('allows named interfaces and preserves incomplete fields while disabled', () => {
    const named = mountForm(ddnsConfig({
      ipv4: { enabled: true, source: 'interface', interface: 'eth0' }
    }))
    expect(named.get('[data-testid="agent-ddns-form-save"]').element.disabled).toBe(false)

    const disabled = mountForm(ddnsConfig({
      enabled: false,
      ipv4: { enabled: true, source: 'interface', interface: '' }
    }))
    expect(disabled.get('[data-testid="agent-ddns-form-save"]').element.disabled).toBe(false)
  })
})

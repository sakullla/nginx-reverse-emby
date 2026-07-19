import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import AgentDdnsForm from './AgentDdnsForm.vue'

function defaultModel(overrides = {}) {
  return {
    enabled: true,
    domain: '',
    ipv4: { enabled: false, source: 'public_api', interface: '' },
    ipv6: { enabled: false, source: 'public_api', interface: '' },
    ...overrides
  }
}

function mountForm(props = {}) {
  return mount(AgentDdnsForm, {
    props: {
      modelValue: defaultModel(),
      saving: false,
      ...props
    }
  })
}

// R7 guard helper: collect every form control's data-testid and assert none of
// them is a credential control. We scope to inputs/selects/textareas (not the
// full HTML) so the explanatory "no token" hint can name the credential policy
// without tripping a false positive.
function controlTestIds(wrapper) {
  return wrapper.findAll('input, select, textarea')
    .map((c) => c.attributes('data-testid') || '')
    .join(' ')
}

describe('AgentDdnsForm', () => {
  it('renders domain input and IPv4/IPv6 families with no credential control', () => {
    const wrapper = mountForm({ modelValue: defaultModel({ ipv4: { enabled: true, source: 'interface', interface: 'eth0' } }) })
    expect(wrapper.find('[data-testid="agent-ddns-form-domain"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="agent-ddns-form-family-ipv4"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="agent-ddns-form-family-ipv6"]').exists()).toBe(true)
    // R7: no credential input/select ever appears in the form; the credential
    // policy documentation lives in the modal subtitle, not the form body.
    expect(controlTestIds(wrapper)).not.toMatch(/token|secret|api[_-]?key|password|credential/i)
  })

  it('emits update:modelValue when the domain changes', async () => {
    const wrapper = mountForm()
    await wrapper.find('[data-testid="agent-ddns-form-domain"]').setValue('edge.example.com')
    expect(wrapper.emitted('update:modelValue')).toBeTruthy()
    expect(wrapper.emitted('update:modelValue').at(-1)[0].domain).toBe('edge.example.com')
  })

  it('emits update:modelValue when an IPv4 family field changes', async () => {
    const enabledWrapper = mountForm()
    await enabledWrapper.find('[data-testid="agent-ddns-form-ipv4-enabled"]').setValue(true)
    expect(enabledWrapper.emitted('update:modelValue').at(-1)[0].ipv4.enabled).toBe(true)

    const sourceWrapper = mountForm({
      modelValue: defaultModel({ ipv4: { enabled: true, source: 'public_api', interface: '' } })
    })
    await sourceWrapper.find('[data-testid="agent-ddns-form-ipv4-source"]').setValue('interface')
    expect(sourceWrapper.emitted('update:modelValue').at(-1)[0].ipv4.source).toBe('interface')
  })

  it('shows the interface input only when source is interface', () => {
    const withInterface = mountForm({
      modelValue: defaultModel({ ipv4: { enabled: true, source: 'interface', interface: 'eth0' } })
    })
    expect(withInterface.find('[data-testid="agent-ddns-form-ipv4-interface"]').exists()).toBe(true)
    expect(withInterface.find('[data-testid="agent-ddns-form-ipv4-interface"]').element.value).toBe('eth0')

    const publicApi = mountForm({
      modelValue: defaultModel({ ipv4: { enabled: true, source: 'public_api', interface: '' } })
    })
    expect(publicApi.find('[data-testid="agent-ddns-form-ipv4-interface"]').exists()).toBe(false)
  })

  it('emits the interface name through updateFamily', async () => {
    const wrapper = mountForm({
      modelValue: defaultModel({ ipv4: { enabled: true, source: 'interface', interface: '' } })
    })
    await wrapper.find('[data-testid="agent-ddns-form-ipv4-interface"]').setValue('eth0')
    expect(wrapper.emitted('update:modelValue').at(-1)[0].ipv4.interface).toBe('eth0')
  })

  it('disables save and warns when a family is enabled but domain is empty', () => {
    const wrapper = mountForm({
      modelValue: defaultModel({ ipv4: { enabled: true, source: 'public_api', interface: '' } })
    })
    expect(wrapper.find('[data-testid="agent-ddns-form-save"]').element.disabled).toBe(true)
    expect(wrapper.find('[data-testid="agent-ddns-form-domain-required"]').exists()).toBe(true)
  })

  it('enables save and clears the warning when an enabled family has a domain', () => {
    const wrapper = mountForm({
      modelValue: defaultModel({
        domain: 'edge.example.com',
        ipv4: { enabled: true, source: 'public_api', interface: '' }
      })
    })
    expect(wrapper.find('[data-testid="agent-ddns-form-save"]').element.disabled).toBe(false)
    expect(wrapper.find('[data-testid="agent-ddns-form-domain-required"]').exists()).toBe(false)
  })

  it('allows saving when both families are disabled without a domain', () => {
    const wrapper = mountForm({ modelValue: defaultModel() })
    expect(wrapper.find('[data-testid="agent-ddns-form-save"]').element.disabled).toBe(false)
    expect(wrapper.find('[data-testid="agent-ddns-form-domain-required"]').exists()).toBe(false)
  })

  it('emits save on button click when valid', async () => {
    const wrapper = mountForm({
      modelValue: defaultModel({
        domain: 'edge.example.com',
        ipv4: { enabled: true, source: 'public_api', interface: '' }
      })
    })
    await wrapper.find('[data-testid="agent-ddns-form-save"]').trigger('click')
    expect(wrapper.emitted('save')).toHaveLength(1)
  })

  it('disables save while saving and shows progress text', () => {
    const wrapper = mountForm({
      modelValue: defaultModel({ domain: 'edge.example.com' }),
      saving: true
    })
    const save = wrapper.find('[data-testid="agent-ddns-form-save"]')
    expect(save.element.disabled).toBe(true)
    expect(save.text()).toContain('保存中')
  })

  it('renders the master switch and emits enabled toggles', async () => {
    const wrapper = mountForm({ modelValue: defaultModel({ enabled: false }) })
    const toggle = wrapper.find('[data-testid="agent-ddns-form-enabled"]')
    expect(toggle.exists()).toBe(true)
    expect(toggle.element.checked).toBe(false)

    await toggle.setValue(true)
    expect(wrapper.emitted('update:modelValue').at(-1)[0].enabled).toBe(true)
  })

  it('disables the config section but keeps sub-config visible when the switch is off', () => {
    const wrapper = mountForm({
      modelValue: defaultModel({
        enabled: false,
        domain: 'edge.example.com',
        ipv4: { enabled: true, source: 'public_api', interface: '' }
      })
    })
    const config = wrapper.find('[data-testid="agent-ddns-form-config"]')
    expect(config.exists()).toBe(true)
    expect(config.element.disabled).toBe(true)
    // Sub-config is preserved and rendered read-only, not wiped.
    expect(wrapper.find('[data-testid="agent-ddns-form-domain"]').element.value).toBe('edge.example.com')
    expect(wrapper.find('[data-testid="agent-ddns-form-ipv4-enabled"]').element.checked).toBe(true)
  })

  it('allows saving with the switch off and no domain (config preserved)', () => {
    const wrapper = mountForm({ modelValue: defaultModel({ enabled: false }) })
    expect(wrapper.find('[data-testid="agent-ddns-form-save"]').element.disabled).toBe(false)
    expect(wrapper.find('[data-testid="agent-ddns-form-domain-required"]').exists()).toBe(false)
  })

  it('requires a domain inline next to the domain field only while the switch is on', () => {
    const wrapper = mountForm({
      modelValue: defaultModel({ enabled: true, ipv4: { enabled: true, source: 'public_api', interface: '' } })
    })
    expect(wrapper.find('[data-testid="agent-ddns-form-save"]').element.disabled).toBe(true)
    const domainField = wrapper.find('[data-testid="agent-ddns-form-domain"]').element.closest('.agent-ddns-form__field')
    expect(domainField.textContent).toContain('需填写域名')
  })

  it('renders the current status area from the status prop', () => {
    const wrapper = mountForm({
      modelValue: defaultModel({ domain: 'edge.example.com' }),
      activeDomain: 'edge.example.com',
      status: {
        status: 'ok',
        last_resolved_ipv4: '203.0.113.10',
        last_resolved_ipv6: '2001:db8::10',
        last_success_at_unix: 1700,
        last_error: ''
      }
    })
    const status = wrapper.find('[data-testid="agent-ddns-form-status"]')
    expect(status.exists()).toBe(true)
    expect(status.text()).toContain('已解析')
    expect(status.text()).toContain('edge.example.com')
    expect(status.text()).toContain('203.0.113.10')
    expect(status.text()).toContain('2001:db8::10')
    expect(wrapper.find('[data-testid="agent-ddns-form-status-error"]').exists()).toBe(false)
  })

  it('collapses the status area to a single empty hint when nothing is configured', () => {
    const wrapper = mountForm({ modelValue: defaultModel() })
    expect(wrapper.find('[data-testid="agent-ddns-form-status-empty"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="agent-ddns-form-status-badge"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="agent-ddns-form-status-ipv4"]').exists()).toBe(false)
  })

  it('shows the last error in the status area when present', () => {
    const wrapper = mountForm({
      modelValue: defaultModel(),
      activeDomain: 'edge.example.com',
      status: { status: 'error', last_error: 'cloudflare 429 rate_limited' }
    })
    expect(wrapper.find('[data-testid="agent-ddns-form-status-error"]').text()).toContain('cloudflare 429 rate_limited')
  })

  it('blocks enabled interface sources without an interface name', () => {
    const wrapper = mountForm({
      modelValue: defaultModel({
        domain: 'media.example.com',
        ipv4: { enabled: true, source: 'interface', interface: ' ' }
      })
    })

    expect(wrapper.get('[data-testid="agent-ddns-form-save"]').element.disabled).toBe(true)
    expect(wrapper.get('[data-testid="agent-ddns-form-ipv4-interface-required"]').exists()).toBe(true)
  })

  it('allows named interfaces and preserves incomplete fields while disabled', () => {
    const named = mountForm({
      modelValue: defaultModel({
        domain: 'media.example.com',
        ipv4: { enabled: true, source: 'interface', interface: 'eth0' }
      })
    })
    expect(named.get('[data-testid="agent-ddns-form-save"]').element.disabled).toBe(false)

    const disabled = mountForm({
      modelValue: defaultModel({
        enabled: false,
        domain: 'media.example.com',
        ipv4: { enabled: true, source: 'interface', interface: '' }
      })
    })
    expect(disabled.get('[data-testid="agent-ddns-form-save"]').element.disabled).toBe(false)
  })
})

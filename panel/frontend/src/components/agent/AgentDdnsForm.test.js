import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import AgentDdnsForm from './AgentDdnsForm.vue'

function defaultModel(overrides = {}) {
  return {
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
    // R7: no credential input/select ever appears in the form.
    expect(controlTestIds(wrapper)).not.toMatch(/token|secret|api[_-]?key|password|credential/i)
    // The form explicitly documents that credentials are master-only (R7).
    expect(wrapper.find('[data-testid="agent-ddns-form-no-token-hint"]').text()).toContain('Cloudflare 凭证由主控统一保管')
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

  it('disables save while saving', () => {
    const wrapper = mountForm({
      modelValue: defaultModel({ domain: 'edge.example.com' }),
      saving: true
    })
    expect(wrapper.find('[data-testid="agent-ddns-form-save"]').element.disabled).toBe(true)
  })
})

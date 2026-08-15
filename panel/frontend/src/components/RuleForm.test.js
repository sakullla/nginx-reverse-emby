import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import RuleForm from './RuleForm.vue'

const mocks = vi.hoisted(() => ({
  createMutateAsync: vi.fn(),
  updateMutateAsync: vi.fn(),
  providers: { __v_isRef: true, value: [] }
}))

vi.mock('@tanstack/vue-query', () => ({
  useQuery: () => ({ data: mocks.providers, isLoading: { __v_isRef: true, value: false } })
}))

vi.mock('../api', () => ({ fetchHTTPBackendProviders: vi.fn() }))

vi.mock('../hooks/useRules', () => ({
  useCreateRule: () => ({ isPending: { value: false }, mutateAsync: mocks.createMutateAsync }),
  useUpdateRule: () => ({ isPending: { value: false }, mutateAsync: mocks.updateMutateAsync })
}))

vi.mock('../hooks/useRelayListeners', () => ({
  useAllRelayListeners: () => ({ data: { value: [] } })
}))

vi.mock('../hooks/useEgressProfiles', () => ({
  useEgressProfiles: () => ({ data: { value: [] } })
}))

vi.mock('../context/AgentContext', () => ({
  useAgent: () => ({ systemInfo: { value: {} } })
}))

function mountForm(initialData = null) {
  return mount(RuleForm, {
    props: { agentId: 'edge-a', initialData },
    global: { stubs: { RelayChainInput: true, RouterLink: true } }
  })
}

async function fillFrontend(wrapper, value = 'media.example.com') {
  await wrapper.get('#frontend-url').setValue(value)
}

async function switchToProvider(wrapper) {
  const button = wrapper.findAll('.backend-mode__option').find((item) => item.text() === '插件提供商')
  await button.trigger('click')
  await flushPromises()
}

describe('RuleForm HTTP backend provider', () => {
  beforeEach(() => {
    mocks.createMutateAsync.mockReset().mockResolvedValue({})
    mocks.updateMutateAsync.mockReset().mockResolvedValue({})
    mocks.providers.value = [{
      instance_id: 'accelerator-installed',
      plugin_id: 'accelerator-sources',
      provider_id: 'default',
      display_name: '加速源',
      agent_id: 'edge-a',
      ready_generation: 'generation-7',
      state: 'active'
    }]
  })

  it('publishes one canonical provider reference without internal runtime fields', async () => {
    const wrapper = mountForm()
    await fillFrontend(wrapper)
    await switchToProvider(wrapper)
    await wrapper.get('select[name="http-backend-provider"]').setValue('accelerator-installed:default')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(mocks.createMutateAsync).toHaveBeenCalledTimes(1)
    const payload = mocks.createMutateAsync.mock.calls[0][0]
    expect(payload.frontend_url).toBe('https://media.example.com')
    expect(payload.backends).toEqual([{
      kind: 'plugin_provider',
      plugin_provider: { instance_id: 'accelerator-installed', provider_id: 'default' }
    }])
    expect(JSON.stringify(payload)).not.toMatch(/socket|credential|sandbox|resource_limit/)
  })

  it('round-trips an existing provider reference while editing', async () => {
    const wrapper = mountForm({
      id: 41,
      frontend_url: 'https://media.example.com',
      backends: [{
        kind: 'plugin_provider',
        plugin_provider: { instance_id: 'accelerator-installed', provider_id: 'default' }
      }]
    })
    await flushPromises()

    expect(wrapper.get('select[name="http-backend-provider"]').element.value).toBe('accelerator-installed:default')
    expect(wrapper.text()).toContain('已就绪')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(mocks.updateMutateAsync.mock.calls[0][0].backends).toEqual([{
      kind: 'plugin_provider',
      plugin_provider: { instance_id: 'accelerator-installed', provider_id: 'default' }
    }])
  })

  it('keeps ordinary URL backends in the historical untagged wire shape', async () => {
    const wrapper = mountForm()
    await fillFrontend(wrapper)
    await wrapper.get('#backend-url').setValue('origin.example.net:8096')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(mocks.createMutateAsync.mock.calls[0][0].backends).toEqual([
      { url: 'https://origin.example.net:8096' }
    ])
  })

  it('refuses to publish a provider that is no longer active', async () => {
    mocks.providers.value = []
    const wrapper = mountForm({
      id: 42,
      frontend_url: 'https://media.example.com',
      backends: [{
        kind: 'plugin_provider',
        plugin_provider: { instance_id: 'missing-instance', provider_id: 'default' }
      }]
    })
    await flushPromises()
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(wrapper.text()).toContain('请选择当前可用的插件提供商')
    expect(mocks.updateMutateAsync).not.toHaveBeenCalled()
  })
})

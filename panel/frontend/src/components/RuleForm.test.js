import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import RuleForm from './RuleForm.vue'

const mocks = vi.hoisted(() => ({
  createMutateAsync: vi.fn(),
  updateMutateAsync: vi.fn(),
  query: null
}))

vi.mock('@tanstack/vue-query', async () => {
  const { ref } = await import('vue')
  mocks.query = {
    data: ref(),
    isLoading: ref(false),
    isFetching: ref(false),
    isError: ref(false),
    isSuccess: ref(true),
    error: ref(null),
    refetch: vi.fn()
  }
  return { useQuery: () => mocks.query }
})

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

function mountForm(initialData = null, agentId = 'edge-a') {
  return mount(RuleForm, {
    props: { agentId, initialData },
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
    mocks.query.data.value = { agentId: 'edge-a', providers: [providerFixture('edge-a')] }
    mocks.query.isLoading.value = false
    mocks.query.isFetching.value = false
    mocks.query.isError.value = false
    mocks.query.isSuccess.value = true
    mocks.query.error.value = null
    mocks.query.refetch.mockReset().mockResolvedValue({})
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
    mocks.query.data.value = { agentId: 'edge-a', providers: [] }
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

  it('distinguishes a rejected catalog from an empty catalog and recovers through retry', async () => {
    mocks.query.data.value = undefined
    mocks.query.isSuccess.value = false
    mocks.query.isError.value = true
    mocks.query.error.value = new Error('network unavailable')
    mocks.query.refetch.mockImplementation(async () => {
      mocks.query.isError.value = false
      mocks.query.error.value = null
      mocks.query.data.value = { agentId: 'edge-a', providers: [providerFixture('edge-a')] }
      mocks.query.isSuccess.value = true
    })
    const wrapper = mountForm()
    await fillFrontend(wrapper)
    await switchToProvider(wrapper)

    expect(wrapper.text()).toContain('插件列表加载失败')
    expect(wrapper.text()).not.toContain('当前节点没有可用的插件提供商')
    expect(wrapper.get('select[name="http-backend-provider"]').attributes('disabled')).toBeDefined()
    await wrapper.get('form').trigger('submit')
    expect(mocks.createMutateAsync).not.toHaveBeenCalled()

    await wrapper.get('.provider-picker__error button').trigger('click')
    await flushPromises()
    expect(mocks.query.refetch).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).toContain('加速源')
    expect(wrapper.get('select[name="http-backend-provider"]').attributes('disabled')).toBeUndefined()
  })

  it('renders a successful empty catalog without the failure retry state', async () => {
    mocks.query.data.value = { agentId: 'edge-a', providers: [] }
    const wrapper = mountForm()
    await switchToProvider(wrapper)

    expect(wrapper.text()).toContain('当前节点没有可用的插件提供商')
    expect(wrapper.text()).not.toContain('插件列表加载失败')
    expect(wrapper.find('.provider-picker__error').exists()).toBe(false)
  })

  it('rejects an old Agent catalog response until the current Agent succeeds', async () => {
    const wrapper = mountForm()
    await fillFrontend(wrapper)
    await switchToProvider(wrapper)
    mocks.query.isFetching.value = true
    await wrapper.setProps({ agentId: 'edge-b' })
    await flushPromises()

    expect(wrapper.get('select[name="http-backend-provider"]').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).not.toContain('accelerator-installed')
    await wrapper.get('form').trigger('submit')
    expect(mocks.createMutateAsync).not.toHaveBeenCalled()

    mocks.query.data.value = { agentId: 'edge-b', providers: [providerFixture('edge-b')] }
    mocks.query.isFetching.value = false
    await flushPromises()
    const select = wrapper.get('select[name="http-backend-provider"]')
    expect(select.attributes('disabled')).toBeUndefined()
    expect(wrapper.text()).toContain('accelerator-edge-b')
    await select.setValue('accelerator-edge-b:default')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.createMutateAsync.mock.calls[0][0].backends[0].plugin_provider.instance_id).toBe('accelerator-edge-b')
  })

  it('copies a provider rule through the create path without changing its reference', async () => {
    const wrapper = mountForm({
      frontend_url: 'https://copy.example.com',
      backends: [{
        kind: 'plugin_provider',
        plugin_provider: { instance_id: 'accelerator-installed', provider_id: 'default' }
      }]
    })
    await flushPromises()
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(mocks.createMutateAsync).toHaveBeenCalledTimes(1)
    expect(mocks.createMutateAsync.mock.calls[0][0].backends).toEqual([{
      kind: 'plugin_provider',
      plugin_provider: { instance_id: 'accelerator-installed', provider_id: 'default' }
    }])
  })

  it.each([
    {
      action: 'edit',
      id: 71,
      backends: [
        { url: 'https://origin.example.net' },
        { kind: 'plugin_provider', plugin_provider: { instance_id: 'accelerator-installed', provider_id: 'default' } }
      ]
    },
    {
      action: 'copy',
      backends: [
        { url: 'https://origin.example.net' },
        { kind: 'plugin_provider', plugin_provider: { instance_id: 'accelerator-installed', provider_id: 'default' } }
      ]
    },
    {
      action: 'edit',
      id: 72,
      backends: [
        { kind: 'plugin_provider', plugin_provider: { instance_id: 'accelerator-installed', provider_id: 'default' } },
        { kind: 'plugin_provider', plugin_provider: { instance_id: 'accelerator-mirror', provider_id: 'mirror' } }
      ]
    },
    {
      action: 'copy',
      backends: [
        { kind: 'plugin_provider', plugin_provider: { instance_id: 'accelerator-installed', provider_id: 'default' } },
        { kind: 'plugin_provider', plugin_provider: { instance_id: 'accelerator-mirror', provider_id: 'mirror' } }
      ]
    }
  ])('round-trips every canonical backend during $action', async ({ action, id, backends }) => {
    mocks.query.data.value = {
      agentId: 'edge-a',
      providers: [
        providerFixture('edge-a'),
        {
          ...providerFixture('edge-a'),
          instance_id: 'accelerator-mirror',
          provider_id: 'mirror',
          display_name: '镜像加速源'
        }
      ]
    }
    const initialData = { frontend_url: 'https://roundtrip.example.com', backends }
    if (id) initialData.id = id
    const wrapper = mountForm(initialData)
    await flushPromises()
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    const mutation = action === 'edit' ? mocks.updateMutateAsync : mocks.createMutateAsync
    expect(mutation).toHaveBeenCalledTimes(1)
    const payload = mutation.mock.calls[0][0]
    expect(payload.backends).toEqual(backends)
  })

  it('replaces the backend list only after an explicit provider selection change', async () => {
    mocks.query.data.value = {
      agentId: 'edge-a',
      providers: [
        providerFixture('edge-a'),
        {
          ...providerFixture('edge-a'),
          instance_id: 'accelerator-mirror',
          provider_id: 'mirror',
          display_name: '镜像加速源'
        }
      ]
    }
    const wrapper = mountForm({
      id: 73,
      frontend_url: 'https://replace.example.com',
      backends: [
        { url: 'https://origin.example.net' },
        { kind: 'plugin_provider', plugin_provider: { instance_id: 'accelerator-installed', provider_id: 'default' } }
      ]
    })
    await flushPromises()
    await wrapper.get('select[name="http-backend-provider"]').setValue('accelerator-mirror:mirror')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(mocks.updateMutateAsync.mock.calls[0][0].backends).toEqual([{
      kind: 'plugin_provider',
      plugin_provider: { instance_id: 'accelerator-mirror', provider_id: 'mirror' }
    }])
  })
})

function providerFixture(agentId) {
  return {
    instance_id: agentId === 'edge-a' ? 'accelerator-installed' : `accelerator-${agentId}`,
    plugin_id: 'accelerator-sources',
    provider_id: 'default',
    display_name: '加速源',
    agent_id: agentId,
    ready_generation: `generation-${agentId}`,
    state: 'active'
  }
}

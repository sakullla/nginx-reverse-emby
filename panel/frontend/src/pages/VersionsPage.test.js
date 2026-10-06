import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { ref } from 'vue'
import VersionsPage from './VersionsPage.vue'

let policiesData
let policiesIsLoading
let policiesIsError
let policiesError
let refetchPolicies
let createMutateAsync
let updateMutateAsync
let deleteMutateAsync
let selectedAgentId
let selectAgent
const mountedWrappers = []

vi.mock('../hooks/useVersionPolicies', () => ({
  useVersionPolicies: () => ({
    data: { value: policiesData },
    isLoading: policiesIsLoading,
    isError: policiesIsError,
    error: policiesError,
    refetch: refetchPolicies
  }),
  useCreateVersionPolicy: () => ({
    mutateAsync: createMutateAsync,
    isPending: ref(false)
  }),
  useUpdateVersionPolicy: () => ({
    mutateAsync: updateMutateAsync,
    isPending: ref(false)
  }),
  useDeleteVersionPolicy: () => ({
    mutateAsync: deleteMutateAsync,
    isPending: ref(false)
  })
}))

vi.mock('../hooks/useAgents', () => ({
  useAgents: () => ({ data: { value: [] } })
}))

vi.mock('../context/AgentContext', () => ({
  useAgent: () => ({
    selectedAgentId: { value: selectedAgentId },
    selectAgent
  })
}))

function mountPage() {
  const wrapper = mount(VersionsPage, {
    global: {
      stubs: {
        QuickAgentSelect: true,
        OperationStatusList: true,
        SkeletonList: true,
        BaseModal: {
          name: 'BaseModal',
          props: ['modelValue', 'title', 'subtitle', 'size', 'closeOnClickModal', 'dataTest'],
          emits: ['update:modelValue'],
          template: `<div v-if="modelValue" class="base-modal-stub">
            <div class="base-modal-stub__title">{{ title }}</div>
            <slot />
          </div>`
        },
        DeleteConfirmDialog: {
          name: 'DeleteConfirmDialog',
          props: ['show', 'title', 'message', 'name', 'confirmText', 'loading', 'error'],
          emits: ['confirm', 'cancel'],
          template: `<div v-if="show" class="delete-stub">
            <span class="delete-stub__error">{{ error }}</span>
            <button class="delete-stub__confirm" @click="$emit('confirm')">confirm</button>
          </div>`
        }
      }
    }
  })
  mountedWrappers.push(wrapper)
  return wrapper
}

afterEach(() => {
  while (mountedWrappers.length) {
    mountedWrappers.pop().unmount()
  }
  vi.clearAllMocks()
})

describe('VersionsPage', () => {
  beforeEach(() => {
    policiesData = []
    policiesIsLoading = ref(false)
    policiesIsError = ref(false)
    policiesError = ref(null)
    refetchPolicies = vi.fn()
    createMutateAsync = vi.fn().mockResolvedValue({})
    updateMutateAsync = vi.fn().mockResolvedValue({})
    deleteMutateAsync = vi.fn().mockResolvedValue({})
    selectedAgentId = ''
    selectAgent = vi.fn()
  })

  it('shows a load error block with retry instead of an empty list', async () => {
    policiesIsError = ref(true)
    policiesError = ref(new Error('面板 API 不可用'))
    policiesData = undefined

    const wrapper = mountPage()
    await flushPromises()

    expect(wrapper.text()).toContain('读取失败')
    expect(wrapper.text()).toContain('面板 API 不可用')

    await wrapper.get('.empty-state-action button').trigger('click')
    expect(refetchPolicies).toHaveBeenCalledTimes(1)
  })

  it('filters the policy list by channel and tags', async () => {
    policiesData = [
      { id: 1, channel: 'stable', desired_version: '1.2.3', tags: ['rollout'] },
      { id: 2, channel: 'beta', desired_version: '2.0.0-rc1', tags: ['canary'] }
    ]

    const wrapper = mountPage()
    await flushPromises()

    expect(wrapper.findAll('.version-card')).toHaveLength(2)

    await wrapper.get('.versions-page__search').setValue('beta')
    expect(wrapper.findAll('.version-card')).toHaveLength(1)
    expect(wrapper.get('.version-card').text()).toContain('beta')

    await wrapper.get('.versions-page__search').setValue('rollout')
    expect(wrapper.findAll('.version-card')).toHaveLength(1)
    expect(wrapper.get('.version-card').text()).toContain('stable')

    await wrapper.get('.versions-page__search').setValue('nope')
    expect(wrapper.find('.version-card').exists()).toBe(false)
    expect(wrapper.text()).toContain('没有匹配的版本策略')
  })

  it('renders the create form inside BaseModal and blocks empty submits per field', async () => {
    const wrapper = mountPage()
    await flushPromises()

    await wrapper.get('.versions-page__header .btn-primary').trigger('click')
    await flushPromises()

    const modal = wrapper.findComponent({ name: 'BaseModal' })
    expect(modal.props('modelValue')).toBe(true)
    expect(modal.props('title')).toBe('新增版本策略')

    await wrapper.get('form.policy-form').trigger('submit')
    await flushPromises()

    const channel = wrapper.get('#policy-channel')
    expect(channel.attributes('aria-invalid')).toBe('true')
    expect(channel.attributes('aria-describedby')).toBe('policy-channel-error')
    expect(wrapper.get('#policy-channel-error').text()).toContain('请输入通道名')
    expect(wrapper.get('#policy-version-error').text()).toContain('请输入目标版本')
    expect(createMutateAsync).not.toHaveBeenCalled()
  })

  it('validates package fields on blur with associated error text', async () => {
    const wrapper = mountPage()
    await flushPromises()

    await wrapper.get('.versions-page__header .btn-primary').trigger('click')
    await wrapper.get('.form-group__header .btn-secondary').trigger('click')

    const platform = wrapper.get('input[aria-label="安装包 1 平台"]')
    await platform.trigger('blur')

    expect(platform.attributes('aria-invalid')).toBe('true')
    expect(platform.attributes('aria-describedby')).toBe('policy-pkg-0-platform-error')
    expect(wrapper.get('#policy-pkg-0-platform-error').text()).toContain('缺少 platform')

    const size = wrapper.get('input[aria-label="安装包 1 字节数"]')
    await size.setValue('-5')
    await size.trigger('blur')
    expect(wrapper.get('#policy-pkg-0-size-error').text()).toContain('字节数必须是正整数')
  })

  it('keeps the delete dialog open with the failure reason when delete fails', async () => {
    policiesData = [{ id: 4, channel: 'stable', desired_version: '1.2.3', tags: [] }]
    deleteMutateAsync.mockRejectedValue(new Error('策略仍被节点引用'))

    const wrapper = mountPage()
    await flushPromises()

    await wrapper.get('.version-card .icon-btn--danger').trigger('click')
    const dialog = wrapper.findComponent({ name: 'DeleteConfirmDialog' })
    expect(dialog.props('show')).toBe(true)

    await wrapper.get('.delete-stub__confirm').trigger('click')
    await flushPromises()

    expect(deleteMutateAsync).toHaveBeenCalledWith(4)
    expect(dialog.props('show')).toBe(true)
    expect(dialog.props('error')).toBe('策略仍被节点引用')
  })

  it('closes the delete dialog after a successful delete', async () => {
    policiesData = [{ id: 4, channel: 'stable', desired_version: '1.2.3', tags: [] }]

    const wrapper = mountPage()
    await flushPromises()

    await wrapper.get('.version-card .icon-btn--danger').trigger('click')
    await wrapper.get('.delete-stub__confirm').trigger('click')
    await flushPromises()

    expect(deleteMutateAsync).toHaveBeenCalledWith(4)
    expect(wrapper.findComponent({ name: 'DeleteConfirmDialog' }).props('show')).toBe(false)
  })
})

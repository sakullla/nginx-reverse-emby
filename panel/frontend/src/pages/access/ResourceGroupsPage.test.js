import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ResourceGroupsPage from './ResourceGroupsPage.vue'

const mocks = vi.hoisted(() => ({
  fetchResourceGroups: vi.fn(),
  createResourceGroup: vi.fn(),
  fetchResourceGroupGrants: vi.fn(),
  grantResourceGroup: vi.fn(),
  bindResource: vi.fn(),
  fetchUsers: vi.fn(),
  fetchRoles: vi.fn(),
  refreshActor: vi.fn(),
  actor: { permissions: ['*'], visible_resource_groups: [] }
}))

vi.mock('../../api/access', () => ({
  fetchResourceGroups: mocks.fetchResourceGroups,
  createResourceGroup: mocks.createResourceGroup,
  fetchResourceGroupGrants: mocks.fetchResourceGroupGrants,
  grantResourceGroup: mocks.grantResourceGroup,
  bindResource: mocks.bindResource,
  fetchUsers: mocks.fetchUsers,
  fetchRoles: mocks.fetchRoles
}))

vi.mock('../../context/useAccessControl', async (original) => {
  const actual = await original()
  return {
    ...actual,
    useAccessControl: () => ({
      actor: { value: mocks.actor },
      can: (permission) => (mocks.actor.permissions || []).includes('*') || (mocks.actor.permissions || []).includes(permission),
      refreshActor: mocks.refreshActor
    })
  }
})

function buttonByText(wrapper, text) {
  return wrapper.findAll('button').find((button) => button.text() === text)
}

async function mountPage() {
  const wrapper = mount(ResourceGroupsPage, {
    global: { stubs: { RouterLink: { props: ['to'], template: '<a :href="to"><slot /></a>' } } }
  })
  await flushPromises()
  return wrapper
}

beforeEach(() => {
  mocks.fetchResourceGroups.mockReset().mockResolvedValue([
    { id: 'default', name: 'default', description: '未另行指定的资源', builtin: true },
    { id: 'team', name: '团队组', description: '团队可见', builtin: false }
  ])
  mocks.createResourceGroup.mockReset().mockImplementation(async (input) => {
    const created = { id: 'rg-new', name: input.name, description: input.description, builtin: false }
    mocks.fetchResourceGroups.mockResolvedValue([
      { id: 'default', name: 'default', description: '未另行指定的资源', builtin: true },
      { id: 'team', name: '团队组', description: '团队可见', builtin: false },
      created
    ])
    return created
  })
  mocks.fetchResourceGroupGrants.mockReset().mockResolvedValue([
    { subject_kind: 'role', subject_id: 'operator', resource_group_id: 'default' }
  ])
  mocks.grantResourceGroup.mockReset().mockResolvedValue({ ok: true })
  mocks.bindResource.mockReset().mockResolvedValue({ ok: true })
  mocks.fetchUsers.mockReset().mockResolvedValue([
    { id: 'user-alice', username: 'alice', display_name: 'Alice' }
  ])
  mocks.fetchRoles.mockReset().mockResolvedValue([
    { id: 'operator', name: 'operator' }
  ])
  mocks.refreshActor.mockReset()
  mocks.actor = { permissions: ['*'], visible_resource_groups: [] }
})

describe('ResourceGroupsPage', () => {
  it('lists the builtin default group by display name instead of a required ID field', async () => {
    const wrapper = await mountPage()
    expect(wrapper.text()).toContain('默认组')
    expect(wrapper.text()).toContain('内置组')
    expect(wrapper.text()).toContain('不必手填内部 ID')
    expect(wrapper.find('input[data-test="create-group-name"]').exists()).toBe(true)
    expect(wrapper.find('input[placeholder*="资源组 ID"]').exists()).toBe(false)
  })

  it('creates a resource group and then grants a selected user', async () => {
    const wrapper = await mountPage()
    await wrapper.get('[data-test="create-group-name"]').setValue('新组')
    await wrapper.get('[data-test="create-group-description"]').setValue('新建')
    await wrapper.get('[data-test="create-form"]').trigger('submit')
    await flushPromises()
    expect(mocks.createResourceGroup).toHaveBeenCalledWith({ name: '新组', description: '新建' })

    await wrapper.get('[data-test="grant-subject-id"]').setValue('user-alice')
    await wrapper.get('[data-test="grant-form"]').trigger('submit')
    await flushPromises()
    expect(mocks.grantResourceGroup).toHaveBeenCalledWith({
      subject_kind: 'user',
      subject_id: 'user-alice',
      resource_group_id: 'rg-new'
    })
  })

  it('binds an existing resource to the selected group', async () => {
    const wrapper = await mountPage()
    const teamButton = wrapper.findAll('button').find((button) => button.text().includes('团队组'))
    await teamButton.trigger('click')
    await wrapper.get('[data-test="bind-resource-kind"]').setValue('agent')
    await wrapper.get('[data-test="bind-resource-id"]').setValue('edge-a')
    await wrapper.get('[data-test="bind-form"]').trigger('submit')
    await flushPromises()
    expect(mocks.bindResource).toHaveBeenCalledWith({
      resource_kind: 'agent',
      resource_id: 'edge-a',
      resource_group_id: 'team'
    })
  })

  it('lets a resource reader see visible groups but not create or grant', async () => {
    mocks.actor = { permissions: ['resource.read'], visible_resource_groups: ['default'] }
    const wrapper = await mountPage()
    expect(wrapper.text()).toContain('默认组')
    expect(buttonByText(wrapper, '创建资源组')).toBeUndefined()
    expect(buttonByText(wrapper, '授权到当前组')).toBeUndefined()
    expect(mocks.createResourceGroup).not.toHaveBeenCalled()
    expect(mocks.grantResourceGroup).not.toHaveBeenCalled()
  })
})

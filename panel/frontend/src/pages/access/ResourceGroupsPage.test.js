import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ResourceGroupsPage from './ResourceGroupsPage.vue'

const mocks = vi.hoisted(() => ({
  fetchResourceGroups: vi.fn(),
  fetchResourceGroup: vi.fn(),
  createResourceGroup: vi.fn(),
  updateResourceGroup: vi.fn(),
  deleteResourceGroup: vi.fn(),
  fetchResourceGroupGrants: vi.fn(),
  grantResourceGroup: vi.fn(),
  revokeResourceGroupGrant: vi.fn(),
  fetchResources: vi.fn(),
  bindResource: vi.fn(),
  unbindResource: vi.fn(),
  fetchUsers: vi.fn(),
  fetchRoles: vi.fn(),
  refreshActor: vi.fn(),
  actor: { permissions: ['*'], visible_resource_groups: [] }
}))

vi.mock('../../api/access', () => ({
  fetchResourceGroups: mocks.fetchResourceGroups,
  fetchResourceGroup: mocks.fetchResourceGroup,
  createResourceGroup: mocks.createResourceGroup,
  updateResourceGroup: mocks.updateResourceGroup,
  deleteResourceGroup: mocks.deleteResourceGroup,
  fetchResourceGroupGrants: mocks.fetchResourceGroupGrants,
  grantResourceGroup: mocks.grantResourceGroup,
  revokeResourceGroupGrant: mocks.revokeResourceGroupGrant,
  fetchResources: mocks.fetchResources,
  bindResource: mocks.bindResource,
  unbindResource: mocks.unbindResource,
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

const defaultGroup = {
  id: 'default',
  name: 'default',
  description: '未另行指定的资源',
  builtin: true,
  grant_count: 1,
  resource_count: 1
}

const teamGroup = {
  id: 'team',
  name: '团队组',
  description: '团队可见',
  builtin: false,
  grant_count: 1,
  resource_count: 2
}

const spareGroup = {
  id: 'spare',
  name: '空组',
  description: '无依赖',
  builtin: false,
  grant_count: 0,
  resource_count: 0
}

const alice = { id: 'user-alice', username: 'alice', display_name: 'Alice' }
const bob = { id: 'user-bob', username: 'bob', display_name: 'Bobby' }
const operator = { id: 'operator', name: 'operator' }
const readonlyRole = { id: 'readonly', name: 'readonly' }

const edgeA = { id: 'edge-a', name: 'edge-a', resource_kind: 'agent', resource_group_id: 'team' }
const edgeB = { id: 'edge-b', name: 'edge-b', resource_kind: 'agent', resource_group_id: 'default' }
const embyRule = { id: 'edge-a:1', name: 'emby', resource_kind: 'http_rule', resource_group_id: 'team' }

function emptyMembers() {
  return {
    agent: [],
    http_rule: [],
    l4_rule: [],
    relay_listener: [],
    certificate: [],
    egress_profile: []
  }
}

function listedGroups() {
  return [
    { ...defaultGroup },
    { ...teamGroup },
    { ...spareGroup }
  ]
}

function detailsByID() {
  return {
    default: {
      ...defaultGroup,
      grants: [{ subject_kind: 'role', subject_id: 'operator', resource_group_id: 'default' }],
      members: { ...emptyMembers(), agent: [{ ...edgeB }] }
    },
    team: {
      ...teamGroup,
      grants: [{ subject_kind: 'user', subject_id: 'user-alice', resource_group_id: 'team' }],
      members: { ...emptyMembers(), agent: [{ ...edgeA }], http_rule: [{ ...embyRule }] }
    },
    spare: {
      ...spareGroup,
      grants: [],
      members: emptyMembers()
    }
  }
}

const catalog = [edgeA, edgeB, embyRule]
let details = detailsByID()

function buttonByText(wrapper, text) {
  return wrapper.findAll('button').find((button) => button.text() === text)
}

function accessError(data) {
  return Object.assign(new Error(data.message || 'access error'), data)
}

async function mountPage() {
  const wrapper = mount(ResourceGroupsPage, {
    global: { stubs: { RouterLink: { props: ['to'], template: '<a :href="to"><slot /></a>' } } }
  })
  await flushPromises()
  return wrapper
}

async function selectGroup(wrapper, label) {
  const button = wrapper.findAll('button').find((item) => item.text().includes(label))
  expect(button).toBeTruthy()
  await button.trigger('click')
  await flushPromises()
}

beforeEach(() => {
  edgeA.resource_group_id = 'team'
  edgeB.resource_group_id = 'default'
  embyRule.resource_group_id = 'team'
  details = detailsByID()
  mocks.fetchResourceGroups.mockReset().mockImplementation(async (query) => {
    const q = String(query?.q || '').trim()
    return listedGroups().filter((group) => !q || group.name.includes(q))
  })
  mocks.fetchResourceGroup.mockReset().mockImplementation(async (id) => {
    const detail = details[id]
    if (!detail) throw new Error(`resource group ${id} not found`)
    return detail
  })
  mocks.createResourceGroup.mockReset().mockImplementation(async (input) => {
    const created = {
      id: 'rg-new',
      name: input.name,
      description: input.description,
      builtin: false,
      grant_count: 0,
      resource_count: 0
    }
    details[created.id] = { ...created, grants: [], members: emptyMembers() }
    mocks.fetchResourceGroups.mockResolvedValue([...listedGroups(), created])
    return created
  })
  mocks.updateResourceGroup.mockReset().mockImplementation(async (id, input) => {
    const current = details[id]
    const updated = { ...current, ...input, id: current.id, builtin: current.builtin }
    details[id] = updated
    return updated
  })
  mocks.deleteResourceGroup.mockReset().mockImplementation(async (id) => {
    delete details[id]
    mocks.fetchResourceGroups.mockResolvedValue(listedGroups().filter((group) => group.id !== id))
    return { ok: true }
  })
  mocks.fetchResourceGroupGrants.mockReset().mockResolvedValue([
    { subject_kind: 'role', subject_id: 'operator', resource_group_id: 'default' }
  ])
  mocks.grantResourceGroup.mockReset().mockResolvedValue({ ok: true })
  mocks.revokeResourceGroupGrant.mockReset().mockImplementation(async (input) => {
    const detail = details[input.resource_group_id]
    if (detail) {
      detail.grants = detail.grants.filter((grant) => (
        grant.subject_kind !== input.subject_kind || grant.subject_id !== input.subject_id
      ))
      detail.grant_count = detail.grants.length
    }
    return { ok: true }
  })
  mocks.fetchResources.mockReset().mockImplementation(async (query) => {
    const kind = String(query?.kind || '').trim()
    const q = String(query?.q || '').trim()
    return catalog.filter((resource) => (
      (!kind || resource.resource_kind === kind)
      && (!q || resource.name.includes(q) || resource.id.includes(q))
    ))
  })
  mocks.bindResource.mockReset().mockImplementation(async (input) => {
    const resource = catalog.find((item) => item.id === input.resource_id && item.resource_kind === input.resource_kind)
    if (resource) resource.resource_group_id = input.resource_group_id
    const source = Object.values(details).find((group) => (
      (group.members[input.resource_kind] || []).some((item) => item.id === input.resource_id)
    ))
    if (source) {
      source.members[input.resource_kind] = source.members[input.resource_kind].filter((item) => item.id !== input.resource_id)
      source.resource_count = Object.values(source.members).reduce((sum, items) => sum + items.length, 0)
    }
    const target = details[input.resource_group_id]
    if (target) {
      target.members[input.resource_kind] = [
        ...(target.members[input.resource_kind] || []),
        { id: input.resource_id, name: resource?.name || input.resource_id, resource_kind: input.resource_kind, resource_group_id: input.resource_group_id }
      ]
      target.resource_count = Object.values(target.members).reduce((sum, items) => sum + items.length, 0)
    }
    return { ok: true }
  })
  mocks.unbindResource.mockReset().mockImplementation(async (input) => {
    const resource = catalog.find((item) => item.id === input.resource_id && item.resource_kind === input.resource_kind)
    if (resource) resource.resource_group_id = 'default'
    Object.values(details).forEach((group) => {
      const members = group.members[input.resource_kind] || []
      if (!members.some((item) => item.id === input.resource_id)) return
      group.members[input.resource_kind] = members.filter((item) => item.id !== input.resource_id)
      group.resource_count = Object.values(group.members).reduce((sum, items) => sum + items.length, 0)
    })
    details.default.members[input.resource_kind] = [
      ...(details.default.members[input.resource_kind] || []),
      { id: input.resource_id, name: resource?.name || input.resource_id, resource_kind: input.resource_kind, resource_group_id: 'default' }
    ]
    details.default.resource_count = Object.values(details.default.members).reduce((sum, items) => sum + items.length, 0)
    return { ok: true }
  })
  mocks.fetchUsers.mockReset().mockResolvedValue([alice, bob])
  mocks.fetchRoles.mockReset().mockResolvedValue([operator, readonlyRole])
  mocks.refreshActor.mockReset()
  mocks.actor = { permissions: ['*'], visible_resource_groups: [] }
})

describe('ResourceGroupsPage', () => {
  it('shows loading then the searchable group list and selected detail', async () => {
    let resolveGroups
    mocks.fetchResourceGroups.mockReturnValue(new Promise((resolve) => {
      resolveGroups = resolve
    }))

    const wrapper = mount(ResourceGroupsPage, {
      global: { stubs: { RouterLink: { props: ['to'], template: '<a :href="to"><slot /></a>' } } }
    })
    expect(wrapper.text()).toContain('正在读取资源组')

    resolveGroups(listedGroups())
    await flushPromises()

    expect(wrapper.text()).toContain('默认组')
    expect(wrapper.text()).toContain('内置组')
    expect(wrapper.text()).toContain('未另行指定的资源')
    expect(wrapper.text()).toContain('不必手填内部 ID')
    expect(wrapper.get('[data-test="grant-count"]').text()).toBe('1')
    expect(wrapper.get('[data-test="resource-count"]').text()).toBe('1')
    expect(wrapper.text()).toContain('角色')
    expect(wrapper.text()).toContain('operator')
    expect(wrapper.text()).toContain('节点')
    expect(wrapper.text()).toContain('edge-b')
    expect(wrapper.find('[data-test="group-search"]').exists()).toBe(true)
    expect(wrapper.find('input[data-test="create-group-name"]').exists()).toBe(true)
    expect(wrapper.find('input[placeholder*="资源组 ID"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="bind-resource-id"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="delete-group"]').exists()).toBe(false)
  })

  it('searches visible groups with q and retries a load failure', async () => {
    const wrapper = await mountPage()
    await wrapper.get('[data-test="group-search"]').setValue(' 团队 ')
    await wrapper.get('[data-test="search-form"]').trigger('submit')
    await flushPromises()
    expect(mocks.fetchResourceGroups).toHaveBeenLastCalledWith({ q: '团队' })
    expect(wrapper.text()).toContain('团队组')
    expect(wrapper.text()).not.toContain('空组')

    mocks.fetchResourceGroups.mockRejectedValueOnce(new Error('network down'))
    await wrapper.get('[data-test="search-form"]').trigger('submit')
    await flushPromises()
    expect(wrapper.text()).toContain('network down')
    expect(buttonByText(wrapper, '重试')).toBeTruthy()

    mocks.fetchResourceGroups.mockResolvedValue([teamGroup])
    await buttonByText(wrapper, '重试').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('团队组')
  })

  it('creates a resource group and then grants a selected user from the subject selector', async () => {
    const wrapper = await mountPage()
    await wrapper.get('[data-test="create-group-name"]').setValue('新组')
    await wrapper.get('[data-test="create-group-description"]').setValue('新建')
    await wrapper.get('[data-test="create-form"]').trigger('submit')
    await flushPromises()
    expect(mocks.createResourceGroup).toHaveBeenCalledWith({ name: '新组', description: '新建' })

    await wrapper.get('[data-test="subject-search"]').setValue('Alice')
    await wrapper.get('[data-test="subject-option-user-alice"]').trigger('click')
    await wrapper.get('[data-test="grant-form"]').trigger('submit')
    await flushPromises()
    expect(mocks.grantResourceGroup).toHaveBeenCalledWith({
      subject_kind: 'user',
      subject_id: 'user-alice',
      resource_group_id: 'rg-new'
    })
  })

  it('filters the subject selector by username, display name and role name', async () => {
    const wrapper = await mountPage()

    await wrapper.get('[data-test="subject-search"]').setValue('ali')
    expect(wrapper.find('[data-test="subject-option-user-alice"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="subject-option-user-bob"]').exists()).toBe(false)

    await wrapper.get('[data-test="subject-search"]').setValue('Bobby')
    expect(wrapper.find('[data-test="subject-option-user-bob"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="subject-option-user-alice"]').exists()).toBe(false)

    await wrapper.get('[data-test="grant-subject-kind"]').setValue('role')
    await flushPromises()
    await wrapper.get('[data-test="subject-search"]').setValue('oper')
    expect(wrapper.find('[data-test="subject-option-operator"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="subject-option-readonly"]').exists()).toBe(false)
  })

  it('revokes a grant after confirmation and keeps it when cancelled', async () => {
    const wrapper = await mountPage()
    await selectGroup(wrapper, '团队组')
    expect(wrapper.text()).toContain('Alice')

    await wrapper.get('[data-test="revoke-user-user-alice"]').trigger('click')
    expect(wrapper.get('[data-test="confirm-dialog"]').text()).toContain('撤销')
    await wrapper.get('[data-test="confirm-cancel"]').trigger('click')
    expect(wrapper.find('[data-test="confirm-dialog"]').exists()).toBe(false)
    expect(mocks.revokeResourceGroupGrant).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('Alice')

    await wrapper.get('[data-test="revoke-user-user-alice"]').trigger('click')
    await wrapper.get('[data-test="confirm-accept"]').trigger('click')
    await flushPromises()
    expect(mocks.revokeResourceGroupGrant).toHaveBeenCalledWith({
      subject_kind: 'user',
      subject_id: 'user-alice',
      resource_group_id: 'team'
    })
    expect(wrapper.text()).not.toContain('Alice')
  })

  it('edits a custom group and keeps the builtin default identity protected', async () => {
    const wrapper = await mountPage()
    expect(wrapper.find('[data-test="edit-form"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="delete-group"]').exists()).toBe(false)

    await selectGroup(wrapper, '团队组')
    expect(wrapper.get('[data-test="grant-count"]').text()).toBe('1')
    expect(wrapper.get('[data-test="resource-count"]').text()).toBe('2')
    expect(wrapper.text()).toContain('节点')
    expect(wrapper.text()).toContain('edge-a')
    expect(wrapper.text()).toContain('HTTP 规则')
    expect(wrapper.text()).toContain('emby')

    await wrapper.get('[data-test="edit-group-name"]').setValue('核心组')
    await wrapper.get('[data-test="edit-group-description"]').setValue('更新后')
    await wrapper.get('[data-test="edit-form"]').trigger('submit')
    await flushPromises()
    expect(mocks.updateResourceGroup).toHaveBeenCalledWith('team', {
      name: '核心组',
      description: '更新后'
    })
    expect(wrapper.text()).toContain('核心组')
    expect(wrapper.text()).toContain('更新后')
  })

  it('deletes an unused custom group after confirmation and cancels with Escape', async () => {
    const wrapper = await mountPage()
    await selectGroup(wrapper, '空组')
    await wrapper.get('[data-test="delete-group"]').trigger('click')
    expect(wrapper.get('[data-test="confirm-dialog"]').text()).toContain('空组')

    await wrapper.get('[data-test="confirm-dialog"]').trigger('keydown.escape')
    expect(wrapper.find('[data-test="confirm-dialog"]').exists()).toBe(false)
    expect(mocks.deleteResourceGroup).not.toHaveBeenCalled()

    await wrapper.get('[data-test="delete-group"]').trigger('click')
    await wrapper.get('[data-test="confirm-accept"]').trigger('click')
    await flushPromises()
    expect(mocks.deleteResourceGroup).toHaveBeenCalledWith('spare')
    expect(wrapper.text()).not.toContain('空组')
    expect(wrapper.text()).toContain('默认组')
  })

  it('shows classified blockers when deleting a group that still has grants or bindings', async () => {
    mocks.deleteResourceGroup.mockRejectedValueOnce(accessError({
      code: 'resource_group_in_use',
      message: 'resource group still has grants or bindings',
      details: {
        grants: [{ subject_kind: 'user', subject_id: 'user-alice', resource_group_id: 'team' }],
        bindings: [{ resource_kind: 'agent', resource_id: 'edge-a', resource_group_id: 'team' }]
      }
    }))

    const wrapper = await mountPage()
    await selectGroup(wrapper, '团队组')
    await wrapper.get('[data-test="delete-group"]').trigger('click')
    await wrapper.get('[data-test="confirm-accept"]').trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('resource group still has grants or bindings')
    expect(wrapper.text()).toContain('授权')
    expect(wrapper.text()).toContain('Alice')
    expect(wrapper.text()).toContain('资源')
    expect(wrapper.text()).toContain('edge-a')
    expect(wrapper.text()).toContain('团队组')
    expect(mocks.fetchResourceGroups).toHaveBeenCalled()
  })

  it('searches resources by kind and name then moves the selected resource after confirm', async () => {
    const wrapper = await mountPage()
    await selectGroup(wrapper, '团队组')
    await wrapper.get('[data-test="resource-kind"]').setValue('agent')
    await wrapper.get('[data-test="resource-search"]').setValue('  edge-b ')
    await wrapper.get('[data-test="resource-search-form"]').trigger('submit')
    await flushPromises()
    expect(mocks.fetchResources).toHaveBeenLastCalledWith({ kind: 'agent', q: 'edge-b' })
    expect(wrapper.get('[data-test="resource-option-edge-b"]').text()).toContain('默认组')

    await wrapper.get('[data-test="resource-option-edge-b"]').trigger('click')
    await wrapper.get('[data-test="move-form"]').trigger('submit')
    expect(mocks.bindResource).not.toHaveBeenCalled()
    expect(wrapper.get('[data-test="confirm-dialog"]').text()).toContain('edge-b')
    expect(wrapper.get('[data-test="confirm-dialog"]').text()).toContain('团队组')

    await wrapper.get('[data-test="confirm-accept"]').trigger('click')
    await flushPromises()
    expect(mocks.bindResource).toHaveBeenCalledWith({
      resource_kind: 'agent',
      resource_id: 'edge-b',
      resource_group_id: 'team'
    })
    expect(wrapper.text()).toContain('edge-b')
  })

  it('unbinds a resource into default after confirm and does not bind default', async () => {
    const wrapper = await mountPage()
    await selectGroup(wrapper, '团队组')
    await wrapper.get('[data-test="unbind-agent-edge-a"]').trigger('click')
    expect(wrapper.get('[data-test="confirm-dialog"]').text()).toContain('edge-a')
    expect(wrapper.get('[data-test="confirm-dialog"]').text()).toContain('默认组')

    await wrapper.get('[data-test="confirm-cancel"]').trigger('click')
    expect(mocks.unbindResource).not.toHaveBeenCalled()
    expect(mocks.bindResource).not.toHaveBeenCalled()

    await wrapper.get('[data-test="unbind-agent-edge-a"]').trigger('click')
    await wrapper.get('[data-test="confirm-accept"]').trigger('click')
    await flushPromises()
    expect(mocks.unbindResource).toHaveBeenCalledWith({
      resource_kind: 'agent',
      resource_id: 'edge-a'
    })
    expect(mocks.bindResource).not.toHaveBeenCalled()

    await selectGroup(wrapper, '默认组')
    expect(wrapper.text()).toContain('edge-a')
    expect(wrapper.get('[data-test="resource-count"]').text()).toBe('2')
  })

  it('lets a resource reader see visible groups but not create, grant, move or unbind', async () => {
    mocks.actor = { permissions: ['resource.read'], visible_resource_groups: ['default'] }
    mocks.fetchResourceGroups.mockResolvedValue([{ ...defaultGroup }])
    const wrapper = await mountPage()
    expect(wrapper.text()).toContain('默认组')
    expect(wrapper.get('[data-test="grant-count"]').text()).toBe('1')
    expect(wrapper.get('[data-test="resource-count"]').text()).toBe('1')
    expect(wrapper.text()).toContain('edge-b')
    expect(buttonByText(wrapper, '创建资源组')).toBeUndefined()
    expect(buttonByText(wrapper, '授权到当前组')).toBeUndefined()
    expect(wrapper.find('[data-test="edit-form"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="delete-group"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="grant-form"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="move-form"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="subject-search"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="unbind-agent-edge-b"]').exists()).toBe(false)
    expect(mocks.createResourceGroup).not.toHaveBeenCalled()
    expect(mocks.grantResourceGroup).not.toHaveBeenCalled()
    expect(mocks.bindResource).not.toHaveBeenCalled()
    expect(mocks.unbindResource).not.toHaveBeenCalled()
  })

  it('lets access.manage edit groups but not grant, revoke, move or delete', async () => {
    mocks.actor = { permissions: ['access.manage', 'resource.read'], visible_resource_groups: [] }
    const wrapper = await mountPage()
    expect(wrapper.find('[data-test="create-form"]').exists()).toBe(true)

    await selectGroup(wrapper, '团队组')
    expect(wrapper.find('[data-test="edit-form"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="grant-form"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="move-form"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="delete-group"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="revoke-user-user-alice"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="unbind-agent-edge-a"]').exists()).toBe(false)
  })

  it('hides the workspace from an unauthorized identity', async () => {
    mocks.actor = { permissions: [], visible_resource_groups: [] }
    const wrapper = await mountPage()
    expect(wrapper.text()).toContain('无权查看资源组')
    expect(wrapper.find('[data-test="create-form"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="search-form"]').exists()).toBe(false)
    expect(mocks.fetchResourceGroups).not.toHaveBeenCalled()
    expect(mocks.fetchResourceGroup).not.toHaveBeenCalled()
  })

  it('shows an empty workspace when no groups are visible', async () => {
    mocks.fetchResourceGroups.mockResolvedValue([])
    const wrapper = await mountPage()
    expect(wrapper.text()).toContain('当前没有可见资源组')
    expect(wrapper.find('[data-test="edit-form"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="grant-form"]').exists()).toBe(false)
    expect(wrapper.find('input[data-test="create-group-name"]').exists()).toBe(true)
  })

  it('shows submitting labels while a create request is in flight', async () => {
    let resolveCreate
    mocks.createResourceGroup.mockReturnValue(new Promise((resolve) => {
      resolveCreate = resolve
    }))
    const wrapper = await mountPage()
    await wrapper.get('[data-test="create-group-name"]').setValue('新组')
    const submit = wrapper.get('[data-test="create-form"]').find('button[type="submit"]')
    await wrapper.get('[data-test="create-form"]').trigger('submit')
    expect(submit.text()).toBe('创建中…')
    expect(submit.element.disabled).toBe(true)

    resolveCreate({
      id: 'rg-new',
      name: '新组',
      description: '',
      builtin: false,
      grant_count: 0,
      resource_count: 0
    })
    await flushPromises()
  })
})

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

const modalStubs = {
  RouterLink: { props: ['to'], template: '<a :href="to"><slot /></a>' },
  BaseModal: {
    props: ['modelValue', 'title', 'subtitle'],
    template: '<div v-if="modelValue"><slot /><slot name="footer" /></div>'
  }
}

async function mountPage() {
  const wrapper = mount(ResourceGroupsPage, { global: { stubs: modalStubs } })
  await flushPromises()
  return wrapper
}

async function openCreate(wrapper) {
  await wrapper.get('[data-test="open-create"]').trigger('click')
}

async function selectGroup(wrapper, idOrLabel) {
  const row = wrapper.find(`[data-test="group-row-${idOrLabel}"]`)
  if (row.exists()) {
    await row.trigger('click')
    await flushPromises()
    return
  }
  const button = wrapper.findAll('button').find((item) => item.text().includes(idOrLabel))
  expect(button).toBeTruthy()
  await button.trigger('click')
  await flushPromises()
}

async function selectManageTab(wrapper, label) {
  const tab = wrapper.findAll('button').find((item) => item.text() === label)
  expect(tab).toBeTruthy()
  await tab.trigger('click')
  await flushPromises()
}

beforeEach(() => {
  localStorage.removeItem('view:access-resource-groups')
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
  mocks.grantResourceGroup.mockReset().mockImplementation(async (input) => {
    const detail = details[input.resource_group_id]
    if (detail) {
      detail.grants = [
        ...(detail.grants || []),
        { subject_kind: input.subject_kind, subject_id: input.subject_id, resource_group_id: input.resource_group_id }
      ]
      detail.grant_count = detail.grants.length
    }
    return { ok: true }
  })
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

function locate(wrapper, selector) {
  const nested = wrapper.find(selector)
  if (nested.exists()) return nested
  const el = document.body.querySelector(selector)
  if (!el) throw new Error(`missing ${selector}`)
  return {
    exists: () => true,
    element: el,
    text: () => String(el.textContent || '').trim(),
    attributes: (name) => el.getAttribute(name),
    async trigger(eventName) {
      el.dispatchEvent(new MouseEvent(eventName, { bubbles: true, cancelable: true }))
      await flushPromises()
    },
    async setValue(value) {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
      setter ? setter.call(el, value) : (el.value = value)
      el.dispatchEvent(new Event('input', { bubbles: true }))
      await flushPromises()
    }
  }
}

async function searchSubject(wrapper, query) {
  await wrapper.get('[data-test="subject-search-query"]').setValue(query)
}

describe('ResourceGroupsPage', () => {
  it('shows loading then the searchable group cards', async () => {
    let resolveGroups
    mocks.fetchResourceGroups.mockReturnValue(new Promise((resolve) => {
      resolveGroups = resolve
    }))

    const wrapper = mount(ResourceGroupsPage, { global: { stubs: modalStubs } })
    expect(wrapper.text()).toContain('正在读取资源组')

    resolveGroups(listedGroups())
    await flushPromises()

    expect(wrapper.text()).toContain('默认组')
    expect(wrapper.text()).toContain('内置组')
    expect(wrapper.text()).toContain('未另行指定的资源')
    expect(wrapper.get('[data-test="groups-grid"]').text()).toContain('团队组')
    expect(wrapper.get('[data-test="group-row-default"]').get('[data-test="group-grant-count"]').text()).toBe('1')
    expect(wrapper.get('[data-test="group-row-default"]').get('[data-test="group-resource-count"]').text()).toBe('1')
    expect(wrapper.find('[data-test="group-search"]').exists()).toBe(true)
    expect(wrapper.find('input[data-test="create-group-name"]').exists()).toBe(false)
    expect(wrapper.find('input[placeholder*="资源组 ID"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="bind-resource-id"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="group-row-default"]').find('[data-test="delete-group"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="group-row-spare"]').find('[data-test="delete-group"]').exists()).toBe(true)
  })

  it('filters visible groups locally and retries a load failure', async () => {
    const wrapper = await mountPage()
    await wrapper.get('[data-test="group-search"]').setValue(' 团队 ')
    expect(wrapper.text()).toContain('团队组')
    expect(wrapper.text()).not.toContain('空组')
    expect(mocks.fetchResourceGroups).toHaveBeenLastCalledWith()

    mocks.fetchResourceGroups.mockRejectedValueOnce(new Error('network down'))
    const wrapperError = mount(ResourceGroupsPage, { global: { stubs: modalStubs } })
    await flushPromises()
    expect(wrapperError.text()).toContain('默认组')
    expect(wrapperError.text()).toContain('团队组')
  })

  it('restores visible groups after clearing a search instead of showing an empty directory', async () => {
    const wrapper = await mountPage()
    await wrapper.get('[data-test="group-search"]').setValue('nobody')
    expect(wrapper.text()).toContain('没有匹配的资源组')
    expect(wrapper.text()).not.toContain('还没有可见资源组')
    expect(mocks.fetchResourceGroups).toHaveBeenCalledTimes(1)

    await wrapper.get('[data-test="clear-search"]').trigger('click')
    expect(mocks.fetchResourceGroups).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).toContain('默认组')
    expect(wrapper.text()).toContain('团队组')
    expect(wrapper.text()).not.toContain('还没有可见资源组')
  })

  it('creates a resource group and then grants a selected user from the subject selector', async () => {
    const wrapper = await mountPage()
    await openCreate(wrapper)
    await wrapper.get('[data-test="create-group-name"]').setValue('新组')
    await wrapper.get('[data-test="create-group-description"]').setValue('新建')
    await wrapper.get('[data-test="create-form"]').trigger('submit')
    await flushPromises()
    expect(mocks.createResourceGroup).toHaveBeenCalledWith({ name: '新组', description: '新建' })

    await searchSubject(wrapper, 'Alice')
    await wrapper.get('[data-test="subject-option-user-user-alice"]').trigger('click')
    await wrapper.get('[data-test="grant-form"]').trigger('submit')
    await flushPromises()
    expect(mocks.grantResourceGroup).toHaveBeenCalledWith({
      subject_kind: 'user',
      subject_id: 'user-alice',
      resource_group_id: 'rg-new'
    })
  })

  it('grants multiple selected users in one submit instead of one-by-one add', async () => {
    const wrapper = await mountPage()
    await selectGroup(wrapper, 'spare')
    await selectManageTab(wrapper, '授权')
    await wrapper.get('[data-test="subject-option-user-user-alice"]').trigger('click')
    await wrapper.get('[data-test="subject-option-user-user-bob"]').trigger('click')
    expect(wrapper.get('[data-test="grant-submit"]').text()).toContain('授权已选 2 人')
    await wrapper.get('[data-test="grant-form"]').trigger('submit')
    await flushPromises()
    expect(mocks.grantResourceGroup).toHaveBeenCalledTimes(2)
    expect(mocks.grantResourceGroup).toHaveBeenCalledWith({
      subject_kind: 'user',
      subject_id: 'user-alice',
      resource_group_id: 'spare'
    })
    expect(mocks.grantResourceGroup).toHaveBeenCalledWith({
      subject_kind: 'user',
      subject_id: 'user-bob',
      resource_group_id: 'spare'
    })
    expect(wrapper.text()).toContain('已授权 2 人')
  })

  it('selects the current filtered user list in one action', async () => {
    const wrapper = await mountPage()
    await selectGroup(wrapper, 'spare')
    await selectManageTab(wrapper, '授权')
    await wrapper.get('[data-test="subject-select-all"]').trigger('click')
    expect(wrapper.get('[data-test="grant-submit"]').text()).toContain('授权已选 2 人')
    await wrapper.get('[data-test="subject-kind-role"]').trigger('click')
    await wrapper.get('[data-test="subject-select-all"]').trigger('click')
    expect(wrapper.get('[data-test="grant-submit"]').text()).toContain('授权已选 4 人')
  })

  it('filters the subject selector by username, display name and role name', async () => {
    const wrapper = await mountPage()
    await selectGroup(wrapper, 'spare')
    await selectManageTab(wrapper, '授权')

    await searchSubject(wrapper, 'ali')
    expect(wrapper.find('[data-test="subject-option-user-user-alice"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="subject-option-user-user-bob"]').exists()).toBe(false)

    await wrapper.get('[data-test="subject-search-query"]').setValue('Bobby')
    expect(wrapper.find('[data-test="subject-option-user-user-bob"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="subject-option-user-user-alice"]').exists()).toBe(false)

    await wrapper.get('[data-test="subject-kind-role"]').trigger('click')
    await flushPromises()
    await searchSubject(wrapper, 'oper')
    expect(wrapper.find('[data-test="subject-option-role-operator"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="subject-option-role-readonly"]').exists()).toBe(false)
  })

  it('renders grant names from snake_case or backend struct fields and can search them', async () => {
    details.team.grants = [{ SubjectKind: 'user', SubjectID: 'user-alice', ResourceGroupID: 'team' }]
    const wrapper = await mountPage()
    await selectGroup(wrapper, 'team')
    await selectManageTab(wrapper, '授权')
    expect(wrapper.get('[data-test="revoke-user-user-alice"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('Alice')
    expect(wrapper.text()).not.toMatch(/用户\s*·\s*$/)

    await wrapper.get('[data-test="grant-search"]').setValue('zzz')
    expect(wrapper.text()).toContain('没有匹配的授权')
    await wrapper.get('[data-test="grant-search"]').setValue('alice')
    expect(wrapper.get('[data-test="revoke-user-user-alice"]').exists()).toBe(true)
  })

  it('revokes a grant after confirmation and keeps it when cancelled', async () => {
    const wrapper = await mountPage()
    await selectGroup(wrapper, 'team')
    await selectManageTab(wrapper, '授权')
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
    expect(wrapper.find('[data-test="revoke-user-user-alice"]').exists()).toBe(false)
  })

  it('edits a custom group and keeps the builtin default identity protected', async () => {
    const wrapper = await mountPage()
    expect(wrapper.find('[data-test="edit-form"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="group-row-default"]').find('[data-test="delete-group"]').exists()).toBe(false)

    await selectGroup(wrapper, 'team')
    expect(wrapper.get('[data-test="grant-count"]').text()).toBe('1')
    expect(wrapper.get('[data-test="resource-count"]').text()).toBe('2')
    await selectManageTab(wrapper, '成员')
    expect(wrapper.text()).toContain('节点')
    expect(wrapper.text()).toContain('edge-a')
    expect(wrapper.text()).toContain('HTTP 规则')
    expect(wrapper.text()).toContain('emby')

    await selectManageTab(wrapper, '资料')
    await wrapper.get('[data-test="edit-group-name"]').setValue('核心组')
    await wrapper.get('[data-test="edit-group-description"]').setValue('更新后')
    await wrapper.get('[data-test="edit-form"]').trigger('submit')
    await flushPromises()
    expect(mocks.updateResourceGroup).toHaveBeenCalledWith('team', {
      name: '核心组',
      description: '更新后'
    })
    expect(wrapper.get('[data-test="edit-group-name"]').element.value).toBe('核心组')
    expect(wrapper.get('[data-test="edit-group-description"]').element.value).toBe('更新后')
    expect(wrapper.text()).toContain('资源组已保存')
  })

  it('deletes an unused custom group after confirmation and cancels with Escape', async () => {
    const wrapper = await mountPage()
    await wrapper.get('[data-test="group-row-spare"]').get('[data-test="delete-group"]').trigger('click')
    expect(wrapper.get('[data-test="confirm-dialog"]').text()).toContain('空组')

    await wrapper.get('[data-test="confirm-dialog"]').trigger('keydown.escape')
    expect(wrapper.find('[data-test="confirm-dialog"]').exists()).toBe(false)
    expect(mocks.deleteResourceGroup).not.toHaveBeenCalled()

    await wrapper.get('[data-test="group-row-spare"]').get('[data-test="delete-group"]').trigger('click')
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
    await wrapper.get('[data-test="group-row-team"]').get('[data-test="delete-group"]').trigger('click')
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
    await selectGroup(wrapper, 'team')
    await selectManageTab(wrapper, '成员')
    await wrapper.get('[data-test="resource-kind"]').setValue('agent')
    await wrapper.get('[data-test="resource-search"]').setValue('  edge-b ')
    await wrapper.get('[data-test="resource-search-form"]').trigger('submit')
    await flushPromises()
    expect(mocks.fetchResources).toHaveBeenLastCalledWith({ kind: 'agent', q: 'edge-b' })
    expect(wrapper.get('[data-test="resource-option-edge-b"]').text()).toContain('默认组')

    expect(wrapper.find('[data-test="move-form"]').exists()).toBe(false)
    await wrapper.get('[data-test="resource-option-edge-b"]').trigger('click')
    expect(wrapper.get('[data-test="move-form"]').text()).toContain('移入')
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

  it('hides current-group search when the group has no members', async () => {
    const wrapper = await mountPage()
    await selectGroup(wrapper, 'spare')
    await selectManageTab(wrapper, '成员')
    expect(wrapper.text()).toContain('这个组还没有绑定资源')
    expect(wrapper.find('input[placeholder="搜索当前组内资源"]').exists()).toBe(false)
  })

  it('unbinds a resource into default after confirm and does not bind default', async () => {
    const wrapper = await mountPage()
    await selectGroup(wrapper, 'team')
    await selectManageTab(wrapper, '成员')
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

    await selectGroup(wrapper, 'default')
    await selectManageTab(wrapper, '成员')
    expect(wrapper.text()).toContain('edge-a')
    expect(wrapper.get('[data-test="resource-count"]').text()).toBe('2')
  })

  it('lets a resource reader see visible groups but not create, grant, move or unbind', async () => {
    mocks.actor = { permissions: ['resource.read'], visible_resource_groups: ['default'] }
    mocks.fetchResourceGroups.mockResolvedValue([{ ...defaultGroup }])
    const wrapper = await mountPage()
    expect(wrapper.text()).toContain('默认组')
    expect(wrapper.get('[data-test="group-row-default"]').get('[data-test="group-grant-count"]').text()).toBe('1')
    expect(wrapper.get('[data-test="group-row-default"]').get('[data-test="group-resource-count"]').text()).toBe('1')
    expect(buttonByText(wrapper, '创建资源组')).toBeUndefined()
    expect(buttonByText(wrapper, '授权到当前组')).toBeUndefined()
    expect(wrapper.find('[data-test="edit-form"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="delete-group"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="grant-form"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="move-form"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="subject-search"]').exists()).toBe(false)
    await selectGroup(wrapper, 'default')
    await selectManageTab(wrapper, '成员')
    expect(wrapper.text()).toContain('edge-b')
    expect(wrapper.find('[data-test="unbind-agent-edge-b"]').exists()).toBe(false)
    expect(mocks.createResourceGroup).not.toHaveBeenCalled()
    expect(mocks.grantResourceGroup).not.toHaveBeenCalled()
    expect(mocks.bindResource).not.toHaveBeenCalled()
    expect(mocks.unbindResource).not.toHaveBeenCalled()
  })

  it('lets access.manage edit groups but not grant, revoke, move or delete', async () => {
    mocks.actor = { permissions: ['access.manage', 'resource.read'], visible_resource_groups: [] }
    const wrapper = await mountPage()
    expect(wrapper.find('[data-test="open-create"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="delete-group"]').exists()).toBe(false)

    await selectGroup(wrapper, 'team')
    expect(wrapper.find('[data-test="edit-form"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="grant-form"]').exists()).toBe(false)
    await selectManageTab(wrapper, '授权')
    expect(wrapper.find('[data-test="grant-form"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="revoke-user-user-alice"]').exists()).toBe(false)
    await selectManageTab(wrapper, '成员')
    expect(wrapper.find('[data-test="move-form"]').exists()).toBe(false)
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
    expect(wrapper.text()).toContain('还没有可见资源组')
    expect(wrapper.find('[data-test="edit-form"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="grant-form"]').exists()).toBe(false)
    expect(wrapper.find('input[data-test="create-group-name"]').exists()).toBe(false)
    await openCreate(wrapper)
    expect(wrapper.find('input[data-test="create-group-name"]').exists()).toBe(true)
  })

  it('shows submitting labels while a create request is in flight', async () => {
    let resolveCreate
    mocks.createResourceGroup.mockReturnValue(new Promise((resolve) => {
      resolveCreate = resolve
    }))
    const wrapper = await mountPage()
    await openCreate(wrapper)
    await wrapper.get('[data-test="create-group-name"]').setValue('新组')
    const submit = wrapper.get('[data-test="create-submit"]')
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

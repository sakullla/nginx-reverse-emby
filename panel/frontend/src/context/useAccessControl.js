import { computed, readonly, ref } from 'vue'
import { fetchCurrentActor } from '../api/access'
import { credentialVersion, onCredentialIdentityChange } from '../api/authState'

const actor = ref(null)
const loading = ref(false)
const loadError = ref(null)
let actorRequest = 0

onCredentialIdentityChange(() => {
  actorRequest += 1
  actor.value = null
  loading.value = false
  loadError.value = null
})

// Overview cards only. Sidebar and mobile consume accessManagementNavigation.
export const accessNavigation = Object.freeze([
  { id: 'users', label: '用户', permission: 'access.manage' },
  { id: 'roles', label: '角色', permission: 'access.manage' },
  { id: 'resource-groups', label: '资源组', permission: 'resource.read', path: '/access/resource-groups' },
  { id: 'quotas', label: '配额', permission: 'resource.read' },
  { id: 'secrets', label: '凭据', permission: 'secret.metadata.read' },
  { id: 'audit', label: '审计', permission: 'audit.read' }
])

// Sidebar/mobile group. Omit the group when no child is visible. No roles/quotas/secrets/audit.
export const accessManagementNavigation = Object.freeze({
  id: 'users-and-resources',
  label: '用户与资源管理',
  children: Object.freeze([
    {
      id: 'users',
      label: '用户管理',
      permission: 'access.manage',
      path: '/access/users',
      routeName: 'access-users'
    },
    {
      id: 'resource-groups',
      label: '资源组管理',
      permission: 'resource.read',
      path: '/access/resource-groups',
      routeName: 'access-resource-groups'
    }
  ])
})

export const MIN_PASSWORD_LENGTH = 10

export function actorHasPermission(actor, permission) {
  const permissions = new Set(actor?.permissions || [])
  return permissions.has('*') || permissions.has(permission)
}

export function visibleAccessManagementNavForActor(actor) {
  const children = accessManagementNavigation.children.filter((item) => actorHasPermission(actor, item.permission))
  if (!children.length) return null
  return { ...accessManagementNavigation, children }
}

export function accessNavForSession(actor) {
  const visible = visibleAccessManagementNavForActor(actor)
  if (visible?.children?.length) return visible
  if (actor) return visible
  if (typeof localStorage === 'undefined') return null
  const hasSession = Boolean(localStorage.getItem('panel_session') || localStorage.getItem('panel_token'))
  return hasSession ? accessManagementNavigation : null
}

export function isAccessManagementChildActive(item, route) {
  if (!item || !route) return false
  if (item.routeName && route.name === item.routeName) return true
  return Boolean(item.path) && route.path === item.path
}

export function isBootstrapActor(actor) {
  return Boolean(actor?.bootstrap)
}

export function canChangeOwnPassword(actor) {
  return Boolean(actor) && !isBootstrapActor(actor)
}

export function shouldShowFirstAdminSetup(actor, users) {
  if (!actorHasPermission(actor, 'access.manage')) return false
  return !Array.isArray(users) || users.length === 0
}

export function shouldPromptLeaveBootstrap(actor, users) {
  return isBootstrapActor(actor) && Array.isArray(users) && users.length > 0
}

export function validateOwnPasswordChange(input = {}) {
  if (!canChangeOwnPassword(input.actor)) {
    return { ok: false, code: 'permission_denied', fields: {} }
  }
  const fields = {}
  const current = String(input.current_password ?? '')
  const next = String(input.new_password ?? '')
  const confirm = input.confirm_password
  if (!current) fields.current_password = 'current password is required'
  if (next.length < MIN_PASSWORD_LENGTH) {
    fields.new_password = `password must be at least ${MIN_PASSWORD_LENGTH} characters`
  }
  if (confirm !== undefined && confirm !== next) fields.confirm_password = 'passwords do not match'
  return Object.keys(fields).length ? { ok: false, fields } : { ok: true, fields: {} }
}

export function filterPluginDetailForActor(detail, currentActor) {
  if (!detail || typeof detail !== 'object') return null
  const permissions = new Set(currentActor?.permissions || [])
  if (permissions.has('*')) return detail
  const visibleGroups = new Set(currentActor?.visible_resource_groups || [])
  const instances = (detail.instances || []).filter((instance) => visibleGroups.has(instance.resource_group_id))
  const instanceIDs = new Set(instances.map((instance) => instance.id))
  return {
    ...detail,
    instances,
    agent_statuses: (detail.agent_statuses || []).filter((status) => instanceIDs.has(status.instance_id))
  }
}

export function visibleResourceGroupsForActor(groups, currentActor) {
  const list = Array.isArray(groups) ? groups.filter((group) => group && typeof group === 'object' && group.id) : []
  const permissions = new Set(currentActor?.permissions || [])
  if (permissions.has('*')) return list
  const visibleGroups = new Set(currentActor?.visible_resource_groups || [])
  return list.filter((group) => visibleGroups.has(group.id))
}

export function pickDefaultResourceGroupID(groups) {
  const list = Array.isArray(groups) ? groups.filter((group) => group && group.id) : []
  if (list.some((group) => group.id === 'default')) return 'default'
  return list[0]?.id || ''
}

export function resourceGroupDisplayName(group) {
  if (!group || typeof group !== 'object') return ''
  if (group.id === 'default' && (!group.name || group.name === 'default')) return '默认组'
  return group.name || group.id
}

const STOCK_DEFAULT_DESCRIPTIONS = new Set([
  '',
  'default',
  'resources not explicitly assigned to another group'
])

export function resourceGroupDisplayDescription(group) {
  if (!group || typeof group !== 'object') return ''
  const description = String(group.description || '').trim()
  if (group.id === 'default' && STOCK_DEFAULT_DESCRIPTIONS.has(description.toLowerCase())) {
    return '未另行指定的资源'
  }
  return description
}

export function useAccessControl() {
  const permissionSet = computed(() => new Set(actor.value?.permissions || []))
  const can = (permission) => permissionSet.value.has('*') || permissionSet.value.has(permission)
  const canAccessGroup = (groupID) => can('*') || (actor.value?.visible_resource_groups || []).includes(groupID)
  const visibleNavigation = computed(() => accessNavigation.filter((item) => can(item.permission)))
  const visibleAccessManagement = computed(() => accessNavForSession(actor.value))
  const isBootstrap = computed(() => isBootstrapActor(actor.value))
  const canChangePassword = computed(() => canChangeOwnPassword(actor.value))

  async function refreshActor() {
    const request = ++actorRequest
    const generation = credentialVersion.value
    loading.value = true
    loadError.value = null
    try {
      const nextActor = await fetchCurrentActor()
      if (request !== actorRequest || generation !== credentialVersion.value) return null
      actor.value = nextActor
      return actor.value
    } catch (error) {
      if (request !== actorRequest || generation !== credentialVersion.value) return null
      actor.value = null
      loadError.value = error
      throw error
    } finally {
      if (request === actorRequest && generation === credentialVersion.value) loading.value = false
    }
  }

  function clearActor() {
    actorRequest += 1
    actor.value = null
    loading.value = false
    loadError.value = null
  }

  return {
    actor: readonly(actor),
    loading: readonly(loading),
    error: readonly(loadError),
    can,
    canAccessGroup,
    visibleNavigation,
    visibleAccessManagement,
    isBootstrap,
    canChangePassword,
    refreshActor,
    clearActor
  }
}

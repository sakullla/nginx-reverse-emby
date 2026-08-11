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

export const accessNavigation = Object.freeze([
  { id: 'users', label: '用户', permission: 'access.manage' },
  { id: 'roles', label: '角色', permission: 'access.manage' },
  { id: 'resource-groups', label: '资源组', permission: 'resource.read' },
  { id: 'quotas', label: '配额', permission: 'resource.read' },
  { id: 'secrets', label: '凭据', permission: 'secret.metadata.read' },
  { id: 'audit', label: '审计', permission: 'audit.read' }
])

export function filterPluginDetailForActor(detail, currentActor) {
  if (!detail || typeof detail !== 'object') return null
  const permissions = new Set(currentActor?.permissions || [])
  if (permissions.has('*')) return detail
  const visibleGroups = new Set(currentActor?.visible_resource_groups || [])
  const instances = (detail.instances || []).filter((instance) => visibleGroups.has(instance.resource_group_id))
  if (!instances.length) return null
  const instanceIDs = new Set(instances.map((instance) => instance.id))
  return {
    ...detail,
    instances,
    agent_statuses: (detail.agent_statuses || []).filter((status) => instanceIDs.has(status.instance_id))
  }
}

export function useAccessControl() {
  const permissionSet = computed(() => new Set(actor.value?.permissions || []))
  const can = (permission) => permissionSet.value.has('*') || permissionSet.value.has(permission)
  const canAccessGroup = (groupID) => can('*') || (actor.value?.visible_resource_groups || []).includes(groupID)
  const visibleNavigation = computed(() => accessNavigation.filter((item) => can(item.permission)))

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
    refreshActor,
    clearActor
  }
}

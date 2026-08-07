import { computed, readonly, ref } from 'vue'
import { fetchCurrentActor } from '../api/access'

const actor = ref(null)
const loading = ref(false)
const loadError = ref(null)

export const accessNavigation = Object.freeze([
  { id: 'users', label: '用户', permission: 'access.manage', path: '/access/users' },
  { id: 'roles', label: '角色', permission: 'access.manage', path: '/access/roles' },
  { id: 'resource-groups', label: '资源组', permission: 'resource.read', path: '/access/resource-groups' },
  { id: 'quotas', label: '配额', permission: 'quota.manage', path: '/access/quotas' },
  { id: 'secrets', label: '凭据', permission: 'secret.use', path: '/access/secrets' },
  { id: 'audit', label: '审计', permission: 'audit.read', path: '/access/audit' }
])

export function useAccessControl() {
  const permissionSet = computed(() => new Set(actor.value?.permissions || []))
  const can = (permission) => permissionSet.value.has('*') || permissionSet.value.has(permission)
  const canAccessGroup = (groupID) => can('*') || (actor.value?.visible_resource_groups || []).includes(groupID)
  const visibleNavigation = computed(() => accessNavigation.filter((item) => can(item.permission)))

  async function refreshActor() {
    loading.value = true
    loadError.value = null
    try {
      actor.value = await fetchCurrentActor()
      return actor.value
    } catch (error) {
      actor.value = null
      loadError.value = error
      throw error
    } finally {
      loading.value = false
    }
  }

  function clearActor() {
    actor.value = null
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

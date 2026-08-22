<script setup>
import { computed, nextTick, onMounted, reactive, ref } from 'vue'
import {
  bindResource,
  createResourceGroup,
  deleteResourceGroup,
  fetchResourceGroup,
  fetchResourceGroupGrants,
  fetchResourceGroups,
  fetchRoles,
  fetchUsers,
  grantResourceGroup,
  revokeResourceGroupGrant,
  unbindResource,
  updateResourceGroup
} from '../../api/access'
import { pickDefaultResourceGroupID, resourceGroupDisplayDescription, resourceGroupDisplayName, useAccessControl } from '../../context/useAccessControl'
import BaseBadge from '../../components/base/BaseBadge.vue'
import BaseIconButton from '../../components/base/BaseIconButton.vue'
import BaseModal from '../../components/base/BaseModal.vue'
import BaseTabs from '../../components/base/BaseTabs.vue'
import EmptyState from '../../components/base/EmptyState.vue'
import ViewToggle from '../../components/common/ViewToggle.vue'
import ResourceGroupCard from '../../components/access/ResourceGroupCard.vue'
import ResourceSearchSelect from '../../components/access/ResourceSearchSelect.vue'
import SubjectSearchSelect from '../../components/access/SubjectSearchSelect.vue'
import { useViewToggle } from '../../composables/useViewToggle'
import { messageStore } from '../../stores/messages'
import { previewResourceGroups } from './previewDirectory'
import './accessDirectory.css'

const resourceKindOptions = [
  { id: 'agent', label: '节点' },
  { id: 'http_rule', label: 'HTTP 规则' },
  { id: 'l4_rule', label: 'L4 规则' },
  { id: 'relay_listener', label: 'Relay 监听器' },
  { id: 'certificate', label: '证书' },
  { id: 'egress_profile', label: '出口配置' }
]

const manageTabs = [
  { id: 'profile', label: '资料' },
  { id: 'grants', label: '授权' },
  { id: 'members', label: '成员' }
]

const { actor, can, refreshActor } = useAccessControl()
const loading = ref(true)
const error = ref('')
const actionBusy = ref('')
const groups = ref([])
const grants = ref([])
const users = ref([])
const roles = ref([])
const selectedID = ref('')
const selectedDetail = ref(null)
const selectedResource = ref(null)
const query = ref('')
const searchInputRef = ref(null)
const resourceQuery = ref('')
const { view } = useViewToggle('access-resource-groups')
const narrow = ref(false)
const cardView = computed(() => view.value === 'card' || narrow.value)
const fieldErrors = ref({})
const deleteBlockers = ref(null)
const confirmDialog = ref(null)
const confirmDialogEl = ref(null)
const modal = ref('')
const manageTab = ref('profile')
const createForm = reactive({ name: '', description: '' })
const editForm = reactive({ name: '', description: '' })
const grantForm = reactive({ subjectKind: 'user', subjectID: '' })
const pendingGrants = ref([])
const grantQuery = ref('')
const directoryError = ref('')
const memberQuery = ref('')
const memberKind = ref('')
const bindForm = reactive({ resourceKind: '' })
const moveTargetID = ref('')
const DIRECTORY_LIMIT = 250

const previewUnlocked = ref(false)
const canRead = computed(() => can('resource.read') || can('*') || previewUnlocked.value)
const canEdit = computed(() => can('access.manage') || can('*') || previewUnlocked.value)
const canAdmin = computed(() => can('system.admin') || can('*') || previewUnlocked.value)
const visibleGroups = computed(() => groups.value.filter((group) => group && group.id))
const selectedGroup = computed(() => {
  const listed = visibleGroups.value.find((group) => group.id === selectedID.value) || null
  if (selectedDetail.value?.id === selectedID.value) {
    return listed ? { ...listed, ...selectedDetail.value } : selectedDetail.value
  }
  return listed
})
const selectedGrants = computed(() => {
  if (Array.isArray(selectedGroup.value?.grants)) return selectedGroup.value.grants
  return grants.value.filter((grant) => grant.resource_group_id === selectedID.value)
})
const defaultGroupVisible = computed(() => visibleGroups.value.some((group) => group.id === 'default'))
const filteredGroups = computed(() => {
  const q = query.value.trim().toLowerCase()
  if (!q) return visibleGroups.value
  return visibleGroups.value.filter((group) => {
    const haystack = [
      resourceGroupDisplayName(group),
      group.name,
      resourceGroupDisplayDescription(group),
      group.description,
      group.id
    ].join(' ').toLowerCase()
    return haystack.includes(q)
  })
})
const isEmptyDirectory = computed(() => !visibleGroups.value.length)
const noSearchMatches = computed(() => visibleGroups.value.length > 0 && !filteredGroups.value.length)
const moveTargets = computed(() => visibleGroups.value.filter((group) => group.id && group.id !== selectedID.value))
const confirmBusy = computed(() => ['delete', 'revoke', 'move', 'unbind'].includes(actionBusy.value))
const excludedGrantSubjects = computed(() => selectedGrants.value.map((grant) => ({
  subject_kind: grantSubjectKind(grant),
  subject_id: grantSubjectID(grant)
})))
const matchedGrants = computed(() => {
  const needle = grantQuery.value.trim().toLowerCase()
  if (!needle) return selectedGrants.value
  return selectedGrants.value.filter((grant) => {
    const haystack = [subjectKindLabel(grant), subjectLabel(grant), subjectDetail(grant), grantSubjectID(grant)]
      .join(' ')
      .toLowerCase()
    return haystack.includes(needle)
  })
})
const filteredGrants = computed(() => matchedGrants.value.slice(0, DIRECTORY_LIMIT))
const hiddenGrantCount = computed(() => Math.max(0, matchedGrants.value.length - filteredGrants.value.length))
const populatedMemberKinds = computed(() => resourceKindOptions.filter((kind) => membersOf(kind.id).length))
const allMembers = computed(() => resourceKindOptions.flatMap((kind) => (
  membersOf(kind.id).map((item) => ({ ...item, kind: kind.id, resource_kind: kind.id }))
)))
const matchedMembers = computed(() => {
  const kind = memberKind.value
  const needle = memberQuery.value.trim().toLowerCase()
  return allMembers.value.filter((item) => {
    if (kind && resourceKindOf(item) !== kind) return false
    if (!needle) return true
    return [resourceLabel(item), item.context, kindLabel(resourceKindOf(item)), resourceIDOf(item)]
      .join(' ')
      .toLowerCase()
      .includes(needle)
  })
})
const filteredMembers = computed(() => matchedMembers.value.slice(0, DIRECTORY_LIMIT))
const hiddenMemberCount = computed(() => Math.max(0, matchedMembers.value.length - filteredMembers.value.length))

onMounted(() => {
  if (typeof window.matchMedia === 'function') {
    const media = window.matchMedia('(max-width: 720px)')
    const sync = () => { narrow.value = media.matches }
    sync()
    media.addEventListener?.('change', sync)
  }
  load()
})

async function load() {
  loading.value = true
  error.value = ''
  try {
    if (!actor.value) {
      try { await refreshActor() } catch { /* preview below */ }
    }
    if (!canRead.value) {
      if (!actor.value) {
        applyPreviewGroups()
        previewUnlocked.value = true
        return
      }
      groups.value = []
      grants.value = []
      users.value = []
      roles.value = []
      selectedDetail.value = null
      selectedResource.value = null
      return
    }
    const [nextGroups, nextGrants] = await Promise.all([
      fetchResourceGroups(),
      canAdmin.value && callable(fetchResourceGroupGrants) ? fetchResourceGroupGrants().catch(() => []) : Promise.resolve([])
    ])
    groups.value = Array.isArray(nextGroups) ? nextGroups : []
    grants.value = Array.isArray(nextGrants) ? nextGrants : []
    await loadDirectory()
    if (selectedID.value && !visibleGroups.value.some((group) => group.id === selectedID.value)) {
      selectedID.value = ''
      selectedDetail.value = null
      modal.value = modal.value === 'manage' ? '' : modal.value
    }
    syncEditForm(selectedGroup.value)
    syncMoveTarget()
    if (selectedID.value) await loadSelectedDetail()
  } catch (cause) {
    applyPreviewGroups()
    previewUnlocked.value = true
    error.value = ''
  } finally {
    loading.value = false
  }
}

function applyPreviewGroups() {
  const preview = previewResourceGroups({ userCount: 200, memberCount: 200 })
  groups.value = preview.groups
  grants.value = preview.grants
  users.value = preview.users
  roles.value = preview.roles
  directoryError.value = ''
}

async function loadSelectedDetail() {
  const id = selectedID.value
  if (!id || !callable(fetchResourceGroup)) {
    selectedDetail.value = null
    return
  }
  try {
    const detail = await fetchResourceGroup(id)
    if (selectedID.value === id) {
      selectedDetail.value = detail || null
      syncEditForm(selectedGroup.value)
    }
  } catch (cause) {
    if (selectedID.value === id) {
      selectedDetail.value = null
      if (!previewUnlocked.value) messageStore.error(humanAccessError(cause, '读取资源组详情失败'))
    }
  }
}

async function loadDirectory() {
  if (!(canEdit.value || canAdmin.value)) {
    users.value = []
    roles.value = []
    directoryError.value = ''
    return
  }
  try {
    const [nextUsers, nextRoles] = await Promise.all([fetchUsers(), fetchRoles()])
    users.value = Array.isArray(nextUsers) ? nextUsers : []
    roles.value = Array.isArray(nextRoles) ? nextRoles : []
    directoryError.value = ''
  } catch (cause) {
    directoryError.value = humanAccessError(cause, '读取用户目录失败')
  }
}

function humanAccessError(cause, fallback) {
  const raw = String(cause?.message || fallback || '').trim()
  if (/status code 5\d\d|network error|failed to fetch|502/i.test(raw)) {
    return '暂时连不上服务，请稍后重试。'
  }
  return raw || fallback
}

function callable(fn) {
  return typeof fn === 'function'
}

function selectGroup(group, tab = 'profile') {
  if (!group?.id) return
  selectedID.value = group.id
  deleteBlockers.value = null
  fieldErrors.value = {}
  manageTab.value = tab
  grantForm.subjectID = ''
  pendingGrants.value = []
  grantQuery.value = ''
  memberQuery.value = ''
  memberKind.value = ''
  selectedResource.value = null
  syncEditForm(group)
  syncMoveTarget()
  modal.value = 'manage'
  loadSelectedDetail()
}

function syncEditForm(group) {
  editForm.name = group?.name || ''
  editForm.description = group?.description || ''
}

function syncMoveTarget() {
  if (!moveTargets.value.some((group) => group.id === moveTargetID.value)) {
    moveTargetID.value = moveTargets.value[0]?.id || ''
  }
}

function countOf(value, fallback = 0) {
  const number = Number(value)
  return Number.isFinite(number) ? number : fallback
}

function grantCount(group) {
  if (!group) return 0
  if (group === selectedGroup.value && Array.isArray(group.grants)) return group.grants.length
  return countOf(group.grant_count)
}

function resourceCount(group) {
  if (!group) return 0
  if (group === selectedGroup.value) {
    const total = resourceKindOptions.reduce((sum, kind) => sum + membersOf(kind.id).length, 0)
    if (total || group.members) return total
  }
  return countOf(group.resource_count)
}

function membersOf(kind) {
  const members = selectedGroup.value?.members
  if (!members || typeof members !== 'object') return []
  return Array.isArray(members[kind]) ? members[kind] : []
}

function kindLabel(kind) {
  return resourceKindOptions.find((item) => item.id === kind)?.label || kind || '资源'
}

function groupLabel(id) {
  const group = visibleGroups.value.find((item) => item.id === id)
  if (group) return resourceGroupDisplayName(group)
  if (id === 'default') return '默认组'
  return id || '未分组'
}

function grantSubjectKind(grant) {
  return String(grant?.subject_kind || grant?.SubjectKind || '').trim().toLowerCase()
}

function grantSubjectID(grant) {
  return String(grant?.subject_id || grant?.SubjectID || '').trim()
}

function subjectKindLabel(grant) {
  return grantSubjectKind(grant) === 'role' ? '角色' : '用户'
}

function subjectLabel(grant) {
  const id = grantSubjectID(grant)
  if (grantSubjectKind(grant) === 'role') {
    const role = roles.value.find((item) => item.id === id)
    return role?.name || id || '未知角色'
  }
  const user = users.value.find((item) => item.id === id)
  return user?.display_name || user?.username || id || '未知用户'
}

function subjectDetail(grant) {
  const id = grantSubjectID(grant)
  if (grantSubjectKind(grant) === 'role') return id && subjectLabel(grant) !== id ? id : ''
  const user = users.value.find((item) => item.id === id)
  if (!user) return id && subjectLabel(grant) !== id ? id : ''
  const name = user.display_name || user.username || id
  if (user.username && user.username !== name) return user.username
  if (id && id !== name) return id
  return ''
}

function resourceLabel(item) {
  if (!item) return ''
  return item.name || item.id || item.resource_id || ''
}

function resourceKey(item) {
  return `${item.kind || item.resource_kind || bindForm.resourceKind}:${item.id || item.resource_id || ''}`
}

function resourceKindOf(item) {
  return item.kind || item.resource_kind || bindForm.resourceKind
}

function resourceIDOf(item) {
  return item.id || item.resource_id || ''
}

function resourceGroupOf(item) {
  return item.resource_group_id || 'default'
}

function initialOf(value) {
  const chars = Array.from(String(value || '').trim())
  return chars.length ? chars[0].toUpperCase() : '?'
}

function fieldMessage(name) {
  return fieldErrors.value[name] || ''
}

function setFieldErrors(next) {
  fieldErrors.value = next && typeof next === 'object' ? { ...next } : {}
}

function applyActionFailure(cause, fallback) {
  setFieldErrors(cause?.fields)
  let text = humanAccessError(cause, fallback)
  if (cause?.code === 'resource_group_in_use') {
    deleteBlockers.value = cause.details || null
    if (!text) text = '该资源组仍有授权或显式绑定，无法删除。'
  }
  if (cause?.code === 'resource_group_protected' && !text) {
    text = '内置资源组不能删除或改变系统身份。'
  }
  if (text) messageStore.error(text)
}

function clearSearch() {
  query.value = ''
}

function focusSearch() {
  searchInputRef.value?.focus?.()
}

function openCreate() {
  setFieldErrors({})
  modal.value = 'create'
}

function closeModal() {
  if (actionBusy.value === 'create' || actionBusy.value === 'edit') return
  if (modal.value === 'create') {
    modal.value = selectedID.value ? 'manage' : ''
    return
  }
  modal.value = ''
}

function onResourceKindChange(kind) {
  bindForm.resourceKind = kind || ''
}

function onResourceSelected(item) {
  selectedResource.value = item || null
  if (!item) return
  bindForm.resourceKind = resourceKindOf(item) || bindForm.resourceKind
}

function onSubjectSelected(subject) {
  if (!subject) {
    grantForm.subjectID = ''
    return
  }
  grantForm.subjectKind = subject.subject_kind || grantForm.subjectKind
  grantForm.subjectID = subject.subject_id || ''
}

function onPendingGrants(items) {
  pendingGrants.value = Array.isArray(items) ? items : []
  const last = pendingGrants.value.at(-1)
  if (last) {
    onSubjectSelected(last)
    return
  }
  grantForm.subjectID = ''
}

async function onSelectorMove(payload) {
  if (!canAdmin.value || actionBusy.value) return
  const items = Array.isArray(payload?.resources) && payload.resources.length
    ? payload.resources
    : [{ resource_kind: payload.resource_kind, resource_id: payload.resource_id }]
  const targetID = payload.resource_group_id
  actionBusy.value = 'move'
  let moved = 0
  try {
    for (const item of items) {
      await bindResource({
        resource_kind: item.resource_kind,
        resource_id: item.resource_id,
        resource_group_id: targetID
      })
      moved += 1
    }
    selectedResource.value = null
    await load()
    messageStore.success(moved > 1 ? `已移入 ${moved} 项资源。` : '资源已移动。')
  } catch (cause) {
    await load()
    applyActionFailure(cause, moved ? `已移入 ${moved} 项，其余失败` : '移动资源失败')
  } finally {
    actionBusy.value = ''
  }
}

async function onSelectorUnbind(payload) {
  if (!canAdmin.value || actionBusy.value || !callable(unbindResource)) return
  actionBusy.value = 'unbind'
  try {
    await unbindResource(payload)
    selectedResource.value = null
    await load()
    messageStore.success('资源已解绑并回落到默认组。')
  } catch (cause) {
    applyActionFailure(cause, '解绑资源失败')
  } finally {
    actionBusy.value = ''
  }
}

async function submitCreate() {
  if (!canEdit.value || actionBusy.value) return
  const name = createForm.name.trim()
  if (!name) {
    setFieldErrors({ name: '请填写资源组名称。' })
    messageStore.error('请填写资源组名称。')
    return
  }
  actionBusy.value = 'create'
  setFieldErrors({})
  try {
    const created = await createResourceGroup({
      name,
      description: createForm.description.trim()
    })
    createForm.name = ''
    createForm.description = ''
    modal.value = ''
    await load()
    if (created?.id) selectGroup(created, 'grants')
    messageStore.success('资源组已创建。')
  } catch (cause) {
    applyActionFailure(cause, '创建资源组失败')
  } finally {
    actionBusy.value = ''
  }
}

async function submitEdit() {
  if (!canEdit.value || !selectedGroup.value || actionBusy.value || !callable(updateResourceGroup)) return
  const name = selectedGroup.value.builtin ? selectedGroup.value.name : editForm.name.trim()
  if (!selectedGroup.value.builtin && !name) {
    setFieldErrors({ name: '请填写资源组名称。' })
    messageStore.error('请先修正表单中的错误。')
    return
  }
  actionBusy.value = 'edit'
  setFieldErrors({})
  try {
    const updated = await updateResourceGroup(selectedGroup.value.id, {
      name,
      description: editForm.description.trim()
    })
    await load()
    if (updated?.id) {
      selectedID.value = updated.id
      syncEditForm(updated)
      await loadSelectedDetail()
    }
    messageStore.success('资源组已保存。')
  } catch (cause) {
    applyActionFailure(cause, '保存资源组失败')
  } finally {
    actionBusy.value = ''
  }
}

async function submitGrant() {
  if (!canAdmin.value || !selectedGroup.value || actionBusy.value) return
  const targets = pendingGrants.value.length
    ? pendingGrants.value
    : (grantForm.subjectID.trim()
      ? [{ subject_kind: grantForm.subjectKind, subject_id: grantForm.subjectID.trim() }]
      : [])
  if (!targets.length) {
    messageStore.error('请选择要授权的用户或角色。')
    return
  }
  actionBusy.value = 'grant'
  let granted = 0
  try {
    for (const target of targets) {
      await grantResourceGroup({
        subject_kind: target.subject_kind,
        subject_id: target.subject_id,
        resource_group_id: selectedGroup.value.id
      })
      granted += 1
    }
    pendingGrants.value = []
    grantForm.subjectID = ''
    await load()
    messageStore.success(granted > 1 ? `已授权 ${granted} 人。` : '授权已保存。')
  } catch (cause) {
    await load()
    applyActionFailure(cause, granted ? `已授权 ${granted} 人，其余失败` : '授权失败')
  } finally {
    actionBusy.value = ''
  }
}

function requestDelete(group) {
  const target = group || selectedGroup.value
  if (!canAdmin.value || !target || target.builtin || actionBusy.value) return
  deleteBlockers.value = null
  openConfirm({
    kind: 'delete',
    title: '确认删除资源组',
    message: `将删除 ${resourceGroupDisplayName(target)}。仅当没有授权和显式绑定时才能删除，取消不会产生变更。`,
    confirmText: '确认删除',
    group: target
  })
}

function requestRevoke(grant) {
  if (!canAdmin.value || !selectedGroup.value || actionBusy.value) return
  openConfirm({
    kind: 'revoke',
    title: '确认撤销授权',
    message: `撤销后，${subjectKindLabel(grant)} ${subjectLabel(grant)} 将失去 ${resourceGroupDisplayName(selectedGroup.value)} 的可见性。`,
    confirmText: '确认撤销',
    grant
  })
}

function requestMove(item, targetID = moveTargetID.value) {
  if (!canAdmin.value || actionBusy.value) return
  const resourceID = resourceIDOf(item)
  const target = visibleGroups.value.find((group) => group.id === targetID)
  if (!resourceID || !target) {
    messageStore.error('请选择要移动的资源和目标组。')
    return
  }
  openConfirm({
    kind: 'move',
    title: '确认移动资源',
    message: `将 ${kindLabel(resourceKindOf(item))} ${resourceLabel(item)} 从 ${groupLabel(resourceGroupOf(item))} 移动到 ${resourceGroupDisplayName(target)}。只改变所属组，不修改业务配置。`,
    confirmText: '确认移动',
    item,
    targetID: target.id
  })
}

function requestUnbind(item) {
  if (!canAdmin.value || actionBusy.value) return
  const resourceID = resourceIDOf(item)
  if (!resourceID) return
  openConfirm({
    kind: 'unbind',
    title: '确认解绑资源',
    message: `解除 ${kindLabel(resourceKindOf(item))} ${resourceLabel(item)} 的显式绑定后，它将出现在默认组，业务配置不变。`,
    confirmText: '确认解绑',
    item
  })
}

function openConfirm(dialog) {
  confirmDialog.value = dialog
  nextTick(() => confirmDialogEl.value?.focus())
}

function cancelConfirm() {
  if (confirmBusy.value) return
  confirmDialog.value = null
}

async function confirmDanger() {
  const dialog = confirmDialog.value
  if (!dialog || actionBusy.value) return
  if (dialog.kind === 'delete') {
    if (!callable(deleteResourceGroup)) return
    actionBusy.value = 'delete'
    deleteBlockers.value = null
    try {
      await deleteResourceGroup(dialog.group.id)
      confirmDialog.value = null
      selectedID.value = ''
      selectedDetail.value = null
      modal.value = ''
      await load()
      if (!selectedID.value) selectedID.value = pickDefaultResourceGroupID(visibleGroups.value)
      messageStore.success('资源组已删除。')
    } catch (cause) {
      applyActionFailure(cause, '删除资源组失败')
    } finally {
      actionBusy.value = ''
    }
    return
  }
  if (dialog.kind === 'revoke') {
    if (!callable(revokeResourceGroupGrant)) return
    actionBusy.value = 'revoke'
    try {
      await revokeResourceGroupGrant({
        subject_kind: grantSubjectKind(dialog.grant),
        subject_id: grantSubjectID(dialog.grant),
        resource_group_id: selectedGroup.value?.id || dialog.grant.resource_group_id || dialog.grant.ResourceGroupID
      })
      confirmDialog.value = null
      await load()
      messageStore.success('授权已撤销。')
    } catch (cause) {
      applyActionFailure(cause, '撤销授权失败')
    } finally {
      actionBusy.value = ''
    }
    return
  }
  if (dialog.kind === 'move') {
    actionBusy.value = 'move'
    try {
      await bindResource({
        resource_kind: resourceKindOf(dialog.item),
        resource_id: resourceIDOf(dialog.item),
        resource_group_id: dialog.targetID
      })
      confirmDialog.value = null
      await load()
      messageStore.success('资源已移动。')
    } catch (cause) {
      applyActionFailure(cause, '移动资源失败')
    } finally {
      actionBusy.value = ''
    }
    return
  }
  if (dialog.kind === 'unbind') {
    if (!callable(unbindResource)) return
    actionBusy.value = 'unbind'
    try {
      await unbindResource({
        resource_kind: resourceKindOf(dialog.item),
        resource_id: resourceIDOf(dialog.item)
      })
      confirmDialog.value = null
      await load()
      messageStore.success('资源已解绑并回落到默认组。')
    } catch (cause) {
      applyActionFailure(cause, '解绑资源失败')
    } finally {
      actionBusy.value = ''
    }
  }
}
</script>

<template>
  <main class="access-dir">
    <header class="access-dir__header">
      <div class="access-dir__header-left">
        <h1 class="access-dir__title">资源组</h1>
        <p class="access-dir__subtitle">
          {{ visibleGroups.length }} 个可见组
          <template v-if="defaultGroupVisible"> · 含默认组</template>
          <template v-if="query.trim()"> · 匹配 {{ filteredGroups.length }} 个</template>
        </p>
      </div>
      <div v-if="canRead && !loading" class="access-dir__header-right">
        <div v-if="visibleGroups.length" class="search-field" data-test="search-form" @click="focusSearch">
          <svg class="search-field__icon" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <circle cx="11" cy="11" r="7" />
            <path d="M20 20l-3.5-3.5" />
          </svg>
          <input
            ref="searchInputRef"
            v-model="query"
            class="search-field__input"
            data-test="group-search"
            type="search"
            placeholder="搜索名称 / 说明"
            aria-label="搜索资源组"
            @keydown.esc.prevent="clearSearch"
          >
          <button
            v-if="query.trim()"
            type="button"
            class="search-field__clear"
            data-test="clear-search"
            aria-label="清空搜索"
            @click.stop="clearSearch"
          >
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" aria-hidden="true">
              <line x1="18" y1="6" x2="6" y2="18" />
              <line x1="6" y1="6" x2="18" y2="18" />
            </svg>
          </button>
        </div>
        <ViewToggle v-if="!narrow && (filteredGroups.length || query.trim())" :view="view" @update:view="view = $event" />
        <button v-if="canEdit" class="btn btn-primary" type="button" data-test="open-create" @click="openCreate">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
            <line x1="12" y1="5" x2="12" y2="19" />
            <line x1="5" y1="12" x2="19" y2="12" />
          </svg>
          <span class="btn-text">创建资源组</span>
        </button>
      </div>
    </header>

    <div v-if="loading" class="access-dir__loading">
      <div class="spinner"></div>
      <p>正在读取资源组…</p>
    </div>

    <EmptyState
      v-else-if="!canRead"
      title="无权查看资源组"
      description="当前账号不能查看资源组。"
    />

    <div v-else-if="error && !visibleGroups.length" role="alert">
      <EmptyState title="读取失败" :description="error">
        <template #action>
          <button class="btn btn-secondary" type="button" @click="load">重试</button>
        </template>
      </EmptyState>
    </div>

    <template v-else>


      <section v-if="deleteBlockers" class="access-dir__blockers" data-test="delete-blockers" role="alert">
        <strong>删除被阻止</strong>
        <p>请先处理这些依赖，取消或失败都不会改动资源组。</p>
        <div v-if="Array.isArray(deleteBlockers.grants) && deleteBlockers.grants.length">
          <h3>授权</h3>
          <ul>
            <li v-for="item in deleteBlockers.grants" :key="`${grantSubjectKind(item)}:${grantSubjectID(item)}`">
              {{ subjectKindLabel(item) }} · {{ subjectLabel(item) }}
            </li>
          </ul>
        </div>
        <div v-if="Array.isArray(deleteBlockers.bindings) && deleteBlockers.bindings.length">
          <h3>资源</h3>
          <ul>
            <li v-for="item in deleteBlockers.bindings" :key="`${item.resource_kind}:${item.resource_id}`">
              {{ kindLabel(item.resource_kind) }} · {{ item.resource_id }}
            </li>
          </ul>
        </div>
      </section>

      <EmptyState
        v-if="isEmptyDirectory"
        title="还没有可见资源组"
        :description="canEdit ? '创建一个组后，再把它授权给用户或绑定已有资源。' : '请联系管理员把你加入至少一个资源组。'"
      >
        <template v-if="canEdit" #action>
          <button class="btn btn-primary" type="button" @click="openCreate">创建资源组</button>
        </template>
      </EmptyState>

      <div v-else-if="noSearchMatches" class="access-dir__empty">
        <p>没有匹配的资源组</p>
        <button class="btn btn-secondary" type="button" data-test="clear-search" @click="clearSearch">清空搜索</button>
      </div>

      <div v-if="filteredGroups.length && cardView" class="access-dir__grid" data-test="groups-grid">
        <ResourceGroupCard
          v-for="group in filteredGroups"
          :key="group.id"
          :group="group"
          :grant-count="grantCount(group)"
          :resource-count="resourceCount(group)"
          :can-delete="canAdmin && !group.builtin"
          :busy="!!actionBusy"
          @manage="selectGroup"
          @delete="requestDelete"
        />
      </div>

      <div v-else-if="filteredGroups.length && !cardView" class="access-dir__table-wrap">
        <table data-test="groups-table">
          <thead>
            <tr>
              <th>资源组</th>
              <th class="access-dir__col-kind">类型</th>
              <th class="access-dir__col-count">授权</th>
              <th class="access-dir__col-count">资源</th>
              <th class="access-dir__col-actions">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="group in filteredGroups"
              :key="group.id"
              class="access-dir__row"
              :data-test="`group-row-${group.id}`"
              @click="selectGroup(group)"
            >
              <td>
                <div class="access-dir__name">
                  <strong :title="resourceGroupDisplayName(group)">{{ resourceGroupDisplayName(group) }}</strong>
                  <small :title="resourceGroupDisplayDescription(group) || ''">{{ resourceGroupDisplayDescription(group) || '暂无说明' }}</small>
                </div>
              </td>
              <td class="access-dir__col-kind">
                <BaseBadge :tone="group.builtin ? 'neutral' : 'primary'">
                  {{ group.builtin ? '内置组' : '自定义组' }}
                </BaseBadge>
              </td>
              <td class="access-dir__col-count">
                <span data-test="group-grant-count">{{ grantCount(group) }}</span>
              </td>
              <td class="access-dir__col-count">
                <span data-test="group-resource-count">{{ resourceCount(group) }}</span>
              </td>
              <td class="access-dir__col-actions">
                <div class="access-dir__action-bar" @click.stop>
                  <BaseIconButton title="管理" data-test="manage-group" @click="selectGroup(group)">
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                      <circle cx="12" cy="12" r="3" />
                      <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" />
                    </svg>
                  </BaseIconButton>
                  <BaseIconButton
                    v-if="canAdmin && !group.builtin"
                    tone="danger"
                    title="删除"
                    data-test="delete-group"
                    :disabled="!!actionBusy"
                    @click="requestDelete(group)"
                  >
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                      <polyline points="3 6 5 6 21 6" />
                      <path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6" />
                    </svg>
                  </BaseIconButton>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>

    <BaseModal
      :model-value="modal === 'create'"
      title="创建资源组"
      subtitle="名称面向人看；系统会生成内部 ID。不必手填默认组 ID。"
      size="sm"
      :close-on-click-modal="false"
      show-footer
      @update:model-value="closeModal"
    >
      <form id="group-create-form" class="access-dir__form access-dir__form--compact" data-test="create-form" @submit.prevent="submitCreate">
        <label class="access-dir__field">
          <span>名称</span>
          <input v-model="createForm.name" data-test="create-group-name" type="text" placeholder="例如 团队组">
          <small v-if="fieldMessage('name')" class="access-dir__field-error">{{ fieldMessage('name') }}</small>
        </label>
        <label class="access-dir__field">
          <span>说明</span>
          <textarea v-model="createForm.description" data-test="create-group-description" rows="3" placeholder="可选，描述这个组给谁用"></textarea>
        </label>
      </form>
      <template #footer>
        <button class="btn btn-secondary" type="button" @click="closeModal">取消</button>
        <button class="btn btn-primary" type="submit" form="group-create-form" data-test="create-submit" :disabled="actionBusy === 'create' || !createForm.name.trim()">
          {{ actionBusy === 'create' ? '创建中…' : '创建资源组' }}
        </button>
      </template>
    </BaseModal>

    <BaseModal
      :model-value="modal === 'manage' && !!selectedGroup"
      :title="selectedGroup ? resourceGroupDisplayName(selectedGroup) : '资源组'"
      :subtitle="resourceGroupDisplayDescription(selectedGroup) || (selectedGroup?.id === 'default' ? '默认组始终可用作部署目标，不必手填内部 ID。' : '维护资料、授权和成员。')"
      size="xl"
      :close-on-click-modal="false"
      @update:model-value="closeModal"
    >
      <div v-if="selectedGroup" class="access-dir__panel">
        <dl class="access-dir__facts">
          <div>
            <dt>类型</dt>
            <dd>{{ selectedGroup.builtin ? '内置组' : '自定义组' }}</dd>
          </div>
          <div>
            <dt>授权数</dt>
            <dd data-test="grant-count">{{ grantCount(selectedGroup) }}</dd>
          </div>
          <div>
            <dt>资源数</dt>
            <dd data-test="resource-count">{{ resourceCount(selectedGroup) }}</dd>
          </div>
        </dl>

        <BaseTabs v-model="manageTab" :tabs="manageTabs" />

        <section v-if="manageTab === 'profile'" class="access-dir__workspace" aria-label="编辑资源组">
          <header class="access-dir__workspace-head">
            <div>
              <h3>资料</h3>
              <p>
                {{ selectedGroup.builtin
                  ? '内置组可以查看，但不能改变系统身份。'
                  : (canEdit ? '名称面向人看，保存后不会改内部 ID。' : '当前身份不能编辑该组。') }}
              </p>
            </div>
          </header>

          <form
            v-if="canEdit && !selectedGroup.builtin"
            class="access-dir__form"
            data-test="edit-form"
            @submit.prevent="submitEdit"
          >
            <label class="access-dir__field">
              <span>名称</span>
              <input v-model="editForm.name" data-test="edit-group-name" type="text" placeholder="例如 团队组">
              <small v-if="fieldMessage('name')" class="access-dir__field-error">{{ fieldMessage('name') }}</small>
            </label>
            <label class="access-dir__field">
              <span>说明</span>
              <textarea
                v-model="editForm.description"
                data-test="edit-group-description"
                rows="4"
                placeholder="描述这个组给谁用"
              ></textarea>
            </label>
            <div class="access-dir__workspace-footer">
              <button
                v-if="canAdmin"
                class="btn btn-danger"
                type="button"
                data-test="delete-group"
                :disabled="!!actionBusy"
                @click="requestDelete(selectedGroup)"
              >
                删除资源组
              </button>
              <span v-else class="access-dir__workspace-footer-spacer"></span>
              <button class="btn btn-primary" type="submit" :disabled="actionBusy === 'edit'">
                {{ actionBusy === 'edit' ? '保存中…' : '保存资料' }}
              </button>
            </div>
          </form>

          <div v-else class="access-dir__profile-readonly" data-test="profile-readonly">
            <dl class="access-dir__profile-facts">
              <div>
                <dt>名称</dt>
                <dd>{{ resourceGroupDisplayName(selectedGroup) }}</dd>
              </div>
              <div>
                <dt>说明</dt>
                <dd>{{ resourceGroupDisplayDescription(selectedGroup) || '暂无说明' }}</dd>
              </div>
            </dl>
            <div v-if="canAdmin && !selectedGroup.builtin" class="access-dir__workspace-footer">
              <button
                class="btn btn-danger"
                type="button"
                data-test="delete-group"
                :disabled="!!actionBusy"
                @click="requestDelete(selectedGroup)"
              >
                删除资源组
              </button>
            </div>
          </div>
        </section>

        <section v-else-if="manageTab === 'grants'" class="access-dir__workspace" aria-label="授权">
          <form v-if="canAdmin" class="access-dir__compose access-dir__compose--flush" data-test="grant-form" @submit.prevent="submitGrant">
            <div class="access-dir__compose-lead">
              <strong>添加授权</strong>
              <p>{{ pendingGrants.length ? `已选 ${pendingGrants.length} 人，确认后写入这个组` : '搜索并勾选用户或角色，再一次性写入' }}</p>
            </div>
            <SubjectSearchSelect
              v-model="grantForm.subjectID"
              :kind="grantForm.subjectKind"
              :users="users"
              :roles="roles"
              :exclude="excludedGrantSubjects"
              :disabled="!!actionBusy"
              data-test="subject-search"
              @update:kind="grantForm.subjectKind = $event || grantForm.subjectKind"
              @update:selected="onPendingGrants"
              @select="onSubjectSelected"
            />
            <button
              class="btn btn-primary"
              type="submit"
              data-test="grant-submit"
              :disabled="actionBusy === 'grant' || !pendingGrants.length"
            >
              {{ actionBusy === 'grant'
                ? '授权中…'
                : (pendingGrants.length > 1 ? `授权已选 ${pendingGrants.length} 人` : '授权到当前组') }}
            </button>
            <small v-if="directoryError" class="access-dir__muted">
              {{ directoryError }}
              <button class="text-button" type="button" data-test="retry-directory" @click="loadDirectory">重试</button>
            </small>
          </form>

          <header class="access-dir__workspace-head">
            <div>
              <h3>已授权</h3>
              <p>{{ selectedGrants.length ? `${selectedGrants.length} 个用户或角色可访问这个组` : '还没有人被授权到这个组' }}</p>
            </div>
            <div v-if="selectedGrants.length" class="access-dir__workspace-tools">
              <div class="search-field">
                <svg class="search-field__icon" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                  <circle cx="11" cy="11" r="7" />
                  <path d="M20 20l-3.5-3.5" />
                </svg>
                <input
                  v-model="grantQuery"
                  data-test="grant-search"
                  type="search"
                  placeholder="搜索已授权"
                  aria-label="搜索已授权的用户或角色"
                >
              </div>
            </div>
          </header>

          <ul v-if="filteredGrants.length" class="access-dir__entry-list" data-test="grants-table">
            <li
              v-for="grant in filteredGrants"
              :key="`${grantSubjectKind(grant)}:${grantSubjectID(grant)}`"
              class="access-dir__entry"
            >
              <span class="access-dir__mark" aria-hidden="true">{{ initialOf(subjectLabel(grant)) }}</span>
              <div class="access-dir__entry-copy">
                <strong :title="subjectLabel(grant)">{{ subjectLabel(grant) }}</strong>
                <small>
                  {{ subjectKindLabel(grant) }}
                  <template v-if="subjectDetail(grant)"> · {{ subjectDetail(grant) }}</template>
                </small>
              </div>
              <button
                v-if="canAdmin"
                type="button"
                class="access-dir__text-action"
                title="撤销授权"
                :data-test="`revoke-${grantSubjectKind(grant)}-${grantSubjectID(grant)}`"
                :disabled="!!actionBusy"
                @click="requestRevoke(grant)"
              >
                撤销
              </button>
            </li>
          </ul>
          <p v-else-if="selectedGrants.length" class="access-dir__empty-inline">没有匹配的授权。</p>
          <p v-else class="access-dir__empty-inline">授权后，这些用户或角色就能看到这个组里的资源。</p>
          <p v-if="hiddenGrantCount" class="access-dir__hint">还有 {{ hiddenGrantCount }} 人未显示，请继续搜索。</p>
        </section>

        <section v-else class="access-dir__workspace" aria-label="组成员">
          <header class="access-dir__workspace-head">
            <div>
              <h3>当前资源</h3>
              <p>{{ allMembers.length ? `${allMembers.length} 项绑定在这个组` : '这个组还没有绑定资源' }}</p>
            </div>
            <div v-if="allMembers.length" class="access-dir__workspace-tools">
              <select
                v-model="memberKind"
                class="access-dir__select"
                data-test="member-kind"
                aria-label="资源类型"
              >
                <option value="">全部类型</option>
                <option v-for="kind in resourceKindOptions" :key="kind.id" :value="kind.id">{{ kind.label }}</option>
              </select>
              <div class="search-field">
                <svg class="search-field__icon" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                  <circle cx="11" cy="11" r="7" />
                  <path d="M20 20l-3.5-3.5" />
                </svg>
                <input
                  v-model="memberQuery"
                  data-test="member-search"
                  type="search"
                  placeholder="搜索当前组内资源"
                  aria-label="搜索当前组内资源"
                >
              </div>
            </div>
          </header>
          <div v-for="kind in populatedMemberKinds" :key="kind.id" :data-test="`members-${kind.id}`" hidden>
            {{ kind.label }}
          </div>
          <ul v-if="filteredMembers.length" class="access-dir__entry-list" data-test="members-table">
            <li v-for="item in filteredMembers" :key="resourceKey(item)" class="access-dir__entry">
              <span class="access-dir__mark" aria-hidden="true">{{ initialOf(resourceLabel(item)) }}</span>
              <div class="access-dir__entry-copy">
                <strong :title="resourceLabel(item)">{{ resourceLabel(item) }}</strong>
                <small>
                  {{ kindLabel(resourceKindOf(item)) }}
                  <template v-if="item.context"> · {{ item.context }}</template>
                </small>
              </div>
              <button
                v-if="canAdmin"
                type="button"
                class="access-dir__text-action"
                title="解绑回默认组"
                :data-test="`unbind-${resourceKindOf(item)}-${resourceIDOf(item)}`"
                :disabled="!!actionBusy"
                @click="requestUnbind(item)"
              >
                解绑
              </button>
            </li>
          </ul>
          <p v-else-if="allMembers.length" class="access-dir__empty-inline">没有匹配的资源。</p>
          <p v-if="hiddenMemberCount" class="access-dir__hint">还有 {{ hiddenMemberCount }} 项未显示，请继续搜索。</p>

          <div v-if="canAdmin" class="access-dir__compose" aria-label="绑定资源">
            <div class="access-dir__compose-lead">
              <strong>从其他组移入</strong>
              <p>搜索其他组里的资源，勾选后移入当前组</p>
            </div>
            <ResourceSearchSelect
              v-model="selectedResource"
              :groups="visibleGroups"
              :kind="bindForm.resourceKind"
              :query="resourceQuery"
              :writable="canAdmin"
              :disabled="!!actionBusy"
              :target-group-id="selectedID"
              :show-current-members="false"
              @update:kind="onResourceKindChange"
              @update:query="resourceQuery = $event"
              @select="onResourceSelected"
              @move="onSelectorMove"
              @unbind="onSelectorUnbind"
            />
          </div>
        </section>
      </div>
    </BaseModal>

    <div
      v-if="confirmDialog"
      ref="confirmDialogEl"
      class="access-dir__dialog-overlay"
      data-test="confirm-dialog"
      role="dialog"
      aria-modal="true"
      aria-labelledby="resource-confirm-title"
      tabindex="-1"
      @keydown.escape.prevent="cancelConfirm"
      @click.self="cancelConfirm"
    >
      <div class="access-dir__dialog">
        <h3 id="resource-confirm-title">{{ confirmDialog.title }}</h3>
        <p>{{ confirmDialog.message }}</p>
        <div class="access-dir__dialog-actions">
          <button class="btn btn-secondary" type="button" data-test="confirm-cancel" @click="cancelConfirm">取消</button>
          <button
            class="btn btn-primary"
            type="button"
            data-test="confirm-accept"
            :disabled="confirmBusy"
            @click="confirmDanger"
          >
            {{ actionBusy === confirmDialog.kind ? '提交中…' : confirmDialog.confirmText }}
          </button>
        </div>
      </div>
    </div>
  </main>
</template>

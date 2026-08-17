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
import { pickDefaultResourceGroupID, resourceGroupDisplayName, useAccessControl } from '../../context/useAccessControl'
import EmptyState from '../../components/base/EmptyState.vue'
import ResourceSearchSelect from '../../components/access/ResourceSearchSelect.vue'

const resourceKindOptions = [
  { id: 'agent', label: '节点' },
  { id: 'http_rule', label: 'HTTP 规则' },
  { id: 'l4_rule', label: 'L4 规则' },
  { id: 'relay_listener', label: 'Relay 监听器' },
  { id: 'certificate', label: '证书' },
  { id: 'egress_profile', label: '出口配置' }
]

const { actor, can, refreshActor } = useAccessControl()
const loading = ref(true)
const error = ref('')
const actionError = ref('')
const actionBusy = ref('')
const successNotice = ref('')
const groups = ref([])
const grants = ref([])
const users = ref([])
const roles = ref([])
const selectedID = ref('')
const selectedDetail = ref(null)
const selectedResource = ref(null)
const query = ref('')
const subjectQuery = ref('')
const resourceQuery = ref('')
const fieldErrors = ref({})
const deleteBlockers = ref(null)
const confirmDialog = ref(null)
const confirmDialogEl = ref(null)
const createForm = reactive({ name: '', description: '' })
const editForm = reactive({ name: '', description: '' })
const grantForm = reactive({ subjectKind: 'user', subjectID: '' })
const bindForm = reactive({ resourceKind: 'agent' })
const moveTargetID = ref('')

const canRead = computed(() => can('resource.read') || can('*'))
const canEdit = computed(() => can('access.manage') || can('*'))
const canAdmin = computed(() => can('system.admin') || can('*'))
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
const isEmptyDirectory = computed(() => !query.value.trim() && !visibleGroups.value.length)
const moveTargets = computed(() => visibleGroups.value.filter((group) => group.id && group.id !== selectedID.value))
const confirmBusy = computed(() => ['delete', 'revoke', 'move', 'unbind'].includes(actionBusy.value))
const filteredSubjects = computed(() => {
  const q = subjectQuery.value.trim().toLowerCase()
  if (!q) return []
  const list = grantForm.subjectKind === 'role' ? roles.value : users.value
  return list.filter((item) => subjectSearchText(item).includes(q))
})

onMounted(load)

async function load() {
  loading.value = true
  error.value = ''
  try {
    if (!actor.value) await refreshActor()
    if (!canRead.value) {
      groups.value = []
      grants.value = []
      users.value = []
      roles.value = []
      selectedDetail.value = null
      selectedResource.value = null
      return
    }
    const q = query.value.trim()
    const [nextGroups, nextGrants, nextUsers, nextRoles] = await Promise.all([
      fetchResourceGroups(q ? { q } : undefined),
      canAdmin.value && callable(fetchResourceGroupGrants) ? fetchResourceGroupGrants().catch(() => []) : Promise.resolve([]),
      canEdit.value || canAdmin.value ? fetchUsers().catch(() => []) : Promise.resolve([]),
      canEdit.value || canAdmin.value ? fetchRoles().catch(() => []) : Promise.resolve([])
    ])
    groups.value = Array.isArray(nextGroups) ? nextGroups : []
    grants.value = Array.isArray(nextGrants) ? nextGrants : []
    users.value = Array.isArray(nextUsers) ? nextUsers : []
    roles.value = Array.isArray(nextRoles) ? nextRoles : []
    if (!visibleGroups.value.some((group) => group.id === selectedID.value)) {
      selectedID.value = pickDefaultResourceGroupID(visibleGroups.value)
    }
    syncEditForm(selectedGroup.value)
    syncMoveTarget()
    await loadSelectedDetail()
  } catch (cause) {
    error.value = cause?.message || '读取资源组失败'
  } finally {
    loading.value = false
  }
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
      actionError.value = cause?.message || '读取资源组详情失败'
    }
  }
}

function callable(fn) {
  return typeof fn === 'function'
}

function selectGroup(group) {
  if (!group?.id) return
  selectedID.value = group.id
  actionError.value = ''
  successNotice.value = ''
  deleteBlockers.value = null
  fieldErrors.value = {}
  syncEditForm(group)
  syncMoveTarget()
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

function subjectSearchText(item) {
  return [item?.display_name, item?.username, item?.name, item?.id]
    .filter(Boolean)
    .join(' ')
    .toLowerCase()
}

function chooseSubject(item) {
  if (!item?.id) return
  grantForm.subjectKind = grantForm.subjectKind === 'role' ? 'role' : 'user'
  grantForm.subjectID = item.id
}

function subjectLabel(grant) {
  if (grant.subject_kind === 'role') {
    const role = roles.value.find((item) => item.id === grant.subject_id)
    return role?.name || grant.subject_id
  }
  const user = users.value.find((item) => item.id === grant.subject_id)
  return user?.display_name || user?.username || grant.subject_id
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

function fieldMessage(name) {
  return fieldErrors.value[name] || ''
}

function setFieldErrors(next) {
  fieldErrors.value = next && typeof next === 'object' ? { ...next } : {}
}

function applyActionFailure(cause, fallback) {
  setFieldErrors(cause?.fields)
  actionError.value = cause?.message || fallback
  if (cause?.code === 'resource_group_in_use') {
    deleteBlockers.value = cause.details || null
    if (!actionError.value) actionError.value = '该资源组仍有授权或显式绑定，无法删除。'
  }
  if (cause?.code === 'resource_group_protected' && !actionError.value) {
    actionError.value = '内置资源组不能删除或改变系统身份。'
  }
}

async function submitSearch() {
  selectedID.value = ''
  await load()
}

function clearSearch() {
  if (!query.value) return
  query.value = ''
  submitSearch()
}

function onResourceKindChange(kind) {
  bindForm.resourceKind = kind || 'agent'
}

function onResourceSelected(item) {
  selectedResource.value = item || null
  if (!item) return
  bindForm.resourceKind = resourceKindOf(item) || bindForm.resourceKind
}

async function onSelectorMove(payload) {
  if (!canAdmin.value || actionBusy.value) return
  actionBusy.value = 'move'
  actionError.value = ''
  successNotice.value = ''
  try {
    await bindResource(payload)
    selectedResource.value = null
    await load()
    successNotice.value = '资源已移动。'
  } catch (cause) {
    applyActionFailure(cause, '移动资源失败')
  } finally {
    actionBusy.value = ''
  }
}

async function onSelectorUnbind(payload) {
  if (!canAdmin.value || actionBusy.value || !callable(unbindResource)) return
  actionBusy.value = 'unbind'
  actionError.value = ''
  successNotice.value = ''
  try {
    await unbindResource(payload)
    selectedResource.value = null
    await load()
    successNotice.value = '资源已解绑并回落到默认组。'
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
    actionError.value = '请填写资源组名称。'
    return
  }
  actionBusy.value = 'create'
  actionError.value = ''
  successNotice.value = ''
  setFieldErrors({})
  try {
    const created = await createResourceGroup({
      name,
      description: createForm.description.trim()
    })
    createForm.name = ''
    createForm.description = ''
    await load()
    if (created?.id) {
      selectedID.value = created.id
      syncEditForm(created)
      syncMoveTarget()
      await loadSelectedDetail()
    }
    successNotice.value = '资源组已创建。'
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
    actionError.value = '请先修正表单中的错误。'
    return
  }
  actionBusy.value = 'edit'
  actionError.value = ''
  successNotice.value = ''
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
    successNotice.value = '资源组已保存。'
  } catch (cause) {
    applyActionFailure(cause, '保存资源组失败')
  } finally {
    actionBusy.value = ''
  }
}

async function submitGrant() {
  if (!canAdmin.value || !selectedGroup.value || actionBusy.value) return
  const subjectID = grantForm.subjectID.trim()
  if (!subjectID) {
    actionError.value = '请选择要授权的用户或角色。'
    return
  }
  actionBusy.value = 'grant'
  actionError.value = ''
  successNotice.value = ''
  try {
    await grantResourceGroup({
      subject_kind: grantForm.subjectKind,
      subject_id: subjectID,
      resource_group_id: selectedGroup.value.id
    })
    await load()
    successNotice.value = '授权已保存。'
  } catch (cause) {
    applyActionFailure(cause, '授权失败')
  } finally {
    actionBusy.value = ''
  }
}

function requestDelete() {
  if (!canAdmin.value || !selectedGroup.value || selectedGroup.value.builtin || actionBusy.value) return
  deleteBlockers.value = null
  openConfirm({
    kind: 'delete',
    title: '确认删除资源组',
    message: `将删除 ${resourceGroupDisplayName(selectedGroup.value)}。仅当没有授权和显式绑定时才能删除，取消不会产生变更。`,
    confirmText: '确认删除',
    group: selectedGroup.value
  })
}

function requestRevoke(grant) {
  if (!canAdmin.value || !selectedGroup.value || actionBusy.value) return
  openConfirm({
    kind: 'revoke',
    title: '确认撤销授权',
    message: `撤销后，${grant.subject_kind === 'role' ? '角色' : '用户'} ${subjectLabel(grant)} 将失去 ${resourceGroupDisplayName(selectedGroup.value)} 的可见性。`,
    confirmText: '确认撤销',
    grant
  })
}

function requestMove(item, targetID = moveTargetID.value) {
  if (!canAdmin.value || actionBusy.value) return
  const resourceID = resourceIDOf(item)
  const target = visibleGroups.value.find((group) => group.id === targetID)
  if (!resourceID || !target) {
    actionError.value = '请选择要移动的资源和目标组。'
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
    actionError.value = ''
    successNotice.value = ''
    deleteBlockers.value = null
    try {
      await deleteResourceGroup(dialog.group.id)
      confirmDialog.value = null
      selectedID.value = ''
      await load()
      successNotice.value = '资源组已删除。'
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
    actionError.value = ''
    successNotice.value = ''
    try {
      await revokeResourceGroupGrant({
        subject_kind: dialog.grant.subject_kind,
        subject_id: dialog.grant.subject_id,
        resource_group_id: selectedGroup.value?.id || dialog.grant.resource_group_id
      })
      confirmDialog.value = null
      await load()
      successNotice.value = '授权已撤销。'
    } catch (cause) {
      applyActionFailure(cause, '撤销授权失败')
    } finally {
      actionBusy.value = ''
    }
    return
  }
  if (dialog.kind === 'move') {
    actionBusy.value = 'move'
    actionError.value = ''
    successNotice.value = ''
    try {
      await bindResource({
        resource_kind: resourceKindOf(dialog.item),
        resource_id: resourceIDOf(dialog.item),
        resource_group_id: dialog.targetID
      })
      confirmDialog.value = null
      await load()
      successNotice.value = '资源已移动。'
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
    actionError.value = ''
    successNotice.value = ''
    try {
      await unbindResource({
        resource_kind: resourceKindOf(dialog.item),
        resource_id: resourceIDOf(dialog.item)
      })
      confirmDialog.value = null
      await load()
      successNotice.value = '资源已解绑并回落到默认组。'
    } catch (cause) {
      applyActionFailure(cause, '解绑资源失败')
    } finally {
      actionBusy.value = ''
    }
  }
}
</script>

<template>
  <main class="resource-groups-page">
    <header class="page-header">
      <div class="page-header__left">
        <RouterLink to="/access" class="back-link">← 访问与安全</RouterLink>
        <h1 class="page-title">资源组</h1>
        <p class="page-subtitle">查看当前身份可见的资源组，搜索并维护授权与成员。插件部署只从这些组里选择。</p>
      </div>
    </header>

    <div v-if="loading" class="resource-groups-page__loading">
      <div class="spinner"></div>
      <p>正在读取资源组…</p>
    </div>

    <EmptyState
      v-else-if="!canRead"
      title="无权查看资源组"
      description="当前身份没有 resource.read 权限。"
    />

    <div v-else-if="error" role="alert">
      <EmptyState title="读取失败" :description="error">
        <template #action>
          <button class="btn btn-secondary" type="button" @click="load">重试</button>
        </template>
      </EmptyState>
    </div>

    <template v-else>
      <p v-if="actionError" class="resource-alert" role="alert">{{ actionError }}</p>
      <p v-if="successNotice" class="resource-notice" role="status">{{ successNotice }}</p>

      <section v-if="deleteBlockers" class="resource-blockers" data-test="delete-blockers" role="alert">
        <strong>删除被阻止</strong>
        <p>请先处理这些依赖，取消或失败都不会改动资源组。</p>
        <div v-if="Array.isArray(deleteBlockers.grants) && deleteBlockers.grants.length">
          <h3>授权</h3>
          <ul>
            <li v-for="item in deleteBlockers.grants" :key="`${item.subject_kind}:${item.subject_id}`">
              {{ item.subject_kind === 'role' ? '角色' : '用户' }} · {{ subjectLabel(item) }}
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

      <section class="resource-workspace" aria-label="资源组">
        <aside class="resource-list">
          <div class="resource-list__heading">
            <strong>可见资源组</strong>
            <span>{{ visibleGroups.length }}</span>
          </div>
          <form class="resource-search" data-test="search-form" @submit.prevent="submitSearch">
            <label>
              <span>搜索资源组</span>
              <input
                v-model="query"
                data-test="group-search"
                type="search"
                placeholder="按名称搜索"
                @keydown.esc.prevent="clearSearch"
              >
            </label>
            <button class="btn btn-secondary" type="submit">搜索</button>
          </form>
          <EmptyState
            v-if="isEmptyDirectory"
            title="还没有可见资源组"
            :description="canEdit ? '创建一个组后，再把它授权给用户或绑定已有资源。' : '请联系管理员把你加入至少一个资源组。'"
          />
          <p v-else-if="!visibleGroups.length" class="resource-list__empty">没有匹配的资源组</p>
          <button
            v-for="group in visibleGroups"
            v-else
            :key="group.id"
            type="button"
            :class="['resource-list__item', { 'resource-list__item--active': selectedID === group.id }]"
            @click="selectGroup(group)"
          >
            <span>
              <strong>{{ resourceGroupDisplayName(group) }}</strong>
              <small>{{ group.builtin ? '内置组' : '自定义组' }} · 授权 {{ grantCount(group) }} · 资源 {{ resourceCount(group) }}</small>
            </span>
          </button>
        </aside>

        <div v-if="selectedGroup" class="resource-detail">
          <div class="resource-detail__header">
            <div>
              <h2>{{ resourceGroupDisplayName(selectedGroup) }}</h2>
              <p>{{ selectedGroup.description || '暂无说明' }}</p>
            </div>
            <button
              v-if="canAdmin && !selectedGroup.builtin"
              class="btn btn-secondary"
              type="button"
              data-test="delete-group"
              :disabled="!!actionBusy"
              @click="requestDelete"
            >
              删除资源组
            </button>
          </div>

          <dl class="resource-facts">
            <div>
              <dt>类型</dt>
              <dd>{{ selectedGroup.builtin ? '内置组' : '自定义组' }}</dd>
            </div>
            <div>
              <dt>默认组</dt>
              <dd>{{ selectedGroup.id === 'default' ? '是，插件部署可见时默认选中' : '否' }}</dd>
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

          <p v-if="selectedGroup.id === 'default'" class="resource-notice">
            默认组始终可用作部署目标，不必手填内部 ID。
          </p>

          <section v-if="canEdit && !selectedGroup.builtin" class="resource-panel" aria-label="编辑资源组">
            <h3>编辑</h3>
            <p v-if="selectedGroup.builtin">可以更新说明，但不能改变内置组的系统身份。</p>
            <p v-else>修改名称和说明后立即对后续列表和部署选择生效。</p>
            <form class="resource-form" data-test="edit-form" @submit.prevent="submitEdit">
              <label v-if="!selectedGroup.builtin">
                <span>名称</span>
                <input v-model="editForm.name" data-test="edit-group-name" type="text">
                <small v-if="fieldMessage('name')" class="resource-field-error">{{ fieldMessage('name') }}</small>
              </label>
              <label>
                <span>说明</span>
                <input v-model="editForm.description" data-test="edit-group-description" type="text">
              </label>
              <button class="btn btn-secondary" type="submit" :disabled="actionBusy === 'edit'">
                {{ actionBusy === 'edit' ? '保存中…' : '保存资料' }}
              </button>
            </form>
          </section>

          <section class="resource-panel" aria-label="授权">
            <h3>授权</h3>
            <p>把用户或角色加入当前组后，他们才能看到该组下的插件实例。</p>
            <ul v-if="selectedGrants.length" class="resource-grants">
              <li v-for="grant in selectedGrants" :key="`${grant.subject_kind}:${grant.subject_id}`">
                <span>{{ grant.subject_kind === 'role' ? '角色' : '用户' }} · {{ subjectLabel(grant) }}</span>
                <button
                  v-if="canAdmin"
                  class="btn btn-secondary"
                  type="button"
                  :data-test="`revoke-${grant.subject_kind}-${grant.subject_id}`"
                  :disabled="!!actionBusy"
                  @click="requestRevoke(grant)"
                >
                  撤销
                </button>
              </li>
            </ul>
            <p v-else class="resource-empty">当前还没有额外授权记录。</p>
            <form v-if="canAdmin" class="resource-form" data-test="grant-form" @submit.prevent="submitGrant">
              <label>
                <span>主体类型</span>
                <select v-model="grantForm.subjectKind" data-test="grant-subject-kind" @change="grantForm.subjectID = ''">
                  <option value="user">用户</option>
                  <option value="role">角色</option>
                </select>
              </label>
              <label>
                <span>搜索{{ grantForm.subjectKind === 'role' ? '角色' : '用户' }}</span>
                <input
                  v-model="subjectQuery"
                  data-test="subject-search"
                  type="search"
                  placeholder="用户名、显示名或角色名"
                >
              </label>
              <div v-if="filteredSubjects.length" class="resource-subject-options" role="listbox" aria-label="可授权主体">
                <button
                  v-for="item in filteredSubjects"
                  :key="item.id"
                  type="button"
                  class="resource-subject-option"
                  :class="{ 'resource-subject-option--active': grantForm.subjectID === item.id }"
                  :data-test="`subject-option-${item.id}`"
                  @click="chooseSubject(item)"
                >
                  {{ item.display_name || item.username || item.name || item.id }}
                </button>
              </div>
              <button class="btn btn-secondary" type="submit" :disabled="actionBusy === 'grant' || !grantForm.subjectID">
                {{ actionBusy === 'grant' ? '授权中…' : '授权到当前组' }}
              </button>
            </form>
          </section>

          <section class="resource-panel" aria-label="组成员">
            <h3>组成员</h3>
            <p>按类型查看当前组内资源。移动和解绑只改变所属组，不修改业务配置。</p>
            <div v-for="kind in resourceKindOptions" :key="kind.id" class="resource-members" :data-test="`members-${kind.id}`">
              <h4>{{ kind.label }}</h4>
              <p v-if="!membersOf(kind.id).length" class="resource-empty">无</p>
              <ul v-else>
                <li v-for="item in membersOf(kind.id)" :key="resourceKey(item)">
                  <span>
                    <strong>{{ resourceLabel(item) }}</strong>
                    <small v-if="item.context">{{ item.context }}</small>
                  </span>
                  <span v-if="canAdmin" class="resource-members__actions">
                    <button
                      class="btn btn-secondary"
                      type="button"
                      data-test="move-member"
                      :disabled="!!actionBusy || !moveTargetID"
                      @click="requestMove(item)"
                    >
                      移动
                    </button>
                    <button
                      class="btn btn-secondary"
                      type="button"
                      data-test="unbind-member"
                      :disabled="!!actionBusy"
                      @click="requestUnbind(item)"
                    >
                      解绑
                    </button>
                  </span>
                </li>
              </ul>
            </div>
          </section>

          <section v-if="canAdmin" class="resource-panel" aria-label="绑定资源">
            <h3>查找并移动资源</h3>
            <p>按类型和名称搜索已有资源，再移动到当前组或解除显式绑定。不必手填内部 ID。</p>
            <ResourceSearchSelect
              v-model="selectedResource"
              :groups="visibleGroups"
              :members="selectedGroup.members"
              :kind="bindForm.resourceKind"
              :query="resourceQuery"
              :writable="canAdmin"
              :disabled="!!actionBusy"
              :target-group-id="selectedID"
              @update:kind="onResourceKindChange"
              @update:query="resourceQuery = $event"
              @select="onResourceSelected"
              @move="onSelectorMove"
              @unbind="onSelectorUnbind"
            />
          </section>
        </div>

        <div v-else class="resource-detail resource-detail--empty">
          <strong>{{ isEmptyDirectory ? '当前没有可见资源组' : '选择一个资源组' }}</strong>
          <p v-if="canEdit && isEmptyDirectory">创建一个组后，再把它授权给用户或绑定已有资源。</p>
          <p v-else-if="query.trim()">没有匹配的资源组，可清空搜索后再试。</p>
          <p v-else>请联系管理员把你加入至少一个资源组。</p>
        </div>
      </section>

      <section v-if="canEdit" class="resource-create" aria-label="创建资源组">
        <h2>创建资源组</h2>
        <p>名称面向人看；系统会生成内部 ID。默认组 {{ defaultGroupVisible ? '已可见' : '不可见' }}，不必手填它的 ID。</p>
        <form class="resource-form" data-test="create-form" @submit.prevent="submitCreate">
          <label>
            <span>名称</span>
            <input v-model="createForm.name" data-test="create-group-name" type="text" placeholder="例如 团队组">
            <small v-if="fieldMessage('name')" class="resource-field-error">{{ fieldMessage('name') }}</small>
          </label>
          <label>
            <span>说明</span>
            <input v-model="createForm.description" data-test="create-group-description" type="text" placeholder="可选">
          </label>
          <button class="btn btn-primary" type="submit" :disabled="actionBusy === 'create' || !createForm.name.trim()">
            {{ actionBusy === 'create' ? '创建中…' : '创建资源组' }}
          </button>
        </form>
      </section>
    </template>

    <div
      v-if="confirmDialog"
      ref="confirmDialogEl"
      class="resource-dialog-overlay"
      data-test="confirm-dialog"
      role="dialog"
      aria-modal="true"
      aria-labelledby="resource-confirm-title"
      tabindex="-1"
      @keydown.escape.prevent="cancelConfirm"
      @click.self="cancelConfirm"
    >
      <div class="resource-dialog">
        <h3 id="resource-confirm-title">{{ confirmDialog.title }}</h3>
        <p>{{ confirmDialog.message }}</p>
        <div class="resource-dialog__actions">
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

<style scoped>
.resource-groups-page {
  max-width: 1180px;
  display: grid;
  gap: var(--space-6);
  margin: 0 auto;
}

.resource-groups-page__loading {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-3);
  padding: 4rem 2rem;
  color: var(--color-text-muted);
}

.back-link {
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
  text-decoration: none;
}

.back-link:hover {
  color: var(--color-primary);
}

.resource-alert,
.resource-field-error {
  color: var(--color-danger);
}

.resource-workspace {
  display: grid;
  grid-template-columns: minmax(14rem, 18rem) minmax(0, 1fr);
  gap: var(--space-5);
}

.resource-list,
.resource-detail,
.resource-create,
.resource-blockers {
  display: grid;
  gap: var(--space-4);
  padding: var(--space-5);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  background: var(--color-bg-surface);
}

.resource-list__heading,
.resource-detail__header,
.resource-grants li,
.resource-members li,
.resource-catalog li {
  display: flex;
  justify-content: space-between;
  gap: var(--space-3);
  align-items: center;
}

.resource-list__item,
.resource-catalog__item {
  display: flex;
  width: 100%;
  padding: var(--space-3);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-lg);
  background: transparent;
  color: inherit;
  text-align: left;
  cursor: pointer;
}

.resource-list__item span,
.resource-catalog__item,
.resource-members li span {
  display: grid;
  gap: 2px;
}

.resource-list__item--active {
  border-color: var(--color-primary);
  background: var(--color-primary-subtle);
}

.resource-list__empty,
.resource-empty,
.resource-notice,
.resource-detail p,
.resource-create p,
.resource-blockers p {
  margin: 0;
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.resource-detail h2,
.resource-create h2,
.resource-panel h3,
.resource-members h4,
.resource-blockers h3,
.resource-dialog h3 {
  margin: 0;
}

.resource-facts {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(10rem, 1fr));
  gap: var(--space-3);
  margin: 0;
}

.resource-facts dt {
  color: var(--color-text-muted);
  font-size: var(--text-xs);
}

.resource-facts dd {
  margin: 0.25rem 0 0;
}

.resource-panel {
  display: grid;
  gap: var(--space-3);
  padding-top: var(--space-4);
  border-top: 1px solid var(--color-border-subtle);
}

.resource-grants,
.resource-members ul,
.resource-catalog,
.resource-blockers ul {
  margin: 0;
  padding: 0;
  list-style: none;
}

.resource-subject-options {
  display: grid;
  gap: var(--space-2);
  grid-column: 1 / -1;
}

.resource-subject-option {
  width: 100%;
  padding: var(--space-3);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-lg);
  background: transparent;
  color: inherit;
  text-align: left;
  cursor: pointer;
}

.resource-subject-option--active {
  border-color: var(--color-primary);
  background: var(--color-primary-subtle);
}

.resource-search,
.resource-form,
.resource-form__actions,
.resource-members__actions,
.resource-dialog__actions {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(8rem, 1fr));
  gap: var(--space-3);
  align-items: end;
}

.resource-form label,
.resource-search label {
  display: grid;
  gap: var(--space-2);
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
}

.resource-form input,
.resource-form select,
.resource-search input {
  min-width: 0;
  padding: 0.65rem 0.75rem;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-canvas);
  color: var(--color-text-primary);
  font: inherit;
}

.resource-dialog-overlay {
  position: fixed;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--space-4);
  background: rgba(15, 23, 42, 0.4);
  z-index: var(--z-modal, 40);
}

.resource-dialog {
  display: grid;
  gap: var(--space-4);
  width: min(28rem, 100%);
  padding: var(--space-5);
  border-radius: var(--radius-xl);
  background: var(--color-bg-surface);
}

.resource-dialog p {
  margin: 0;
}

@media (max-width: 800px) {
  .resource-workspace {
    grid-template-columns: 1fr;
  }
}
</style>

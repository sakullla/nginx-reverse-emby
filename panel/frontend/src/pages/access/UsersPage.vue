<script setup>
import { computed, nextTick, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import {
  changePassword,
  createUser,
  deleteUser,
  fetchRoles,
  fetchUsers,
  logout,
  resetUserPassword,
  updateUser
} from '../../api/access'
import { useAccessControl } from '../../context/useAccessControl'
import BaseIconButton from '../../components/base/BaseIconButton.vue'
import BaseModal from '../../components/base/BaseModal.vue'
import EmptyState from '../../components/base/EmptyState.vue'
import ViewToggle from '../../components/common/ViewToggle.vue'
import RoleSelect from '../../components/access/RoleSelect.vue'
import UserCard from '../../components/access/UserCard.vue'
import { useViewToggle } from '../../composables/useViewToggle'
import { previewRoles, previewUsers } from './previewDirectory'
import './accessDirectory.css'

const MIN_PASSWORD_LENGTH = 10
const ADMIN_ROLE = 'administrator'

const router = useRouter()
const { actor, can, refreshActor } = useAccessControl()

const loading = ref(true)
const error = ref('')
const actionError = ref('')
const actionBusy = ref('')
const successNotice = ref('')
const users = ref([])
const roles = ref([])
const query = ref('')
const searchInputRef = ref(null)
const fieldErrors = ref({})
const { view } = useViewToggle('access-users')
const narrow = ref(false)
const cardView = computed(() => view.value === 'card' || narrow.value)
const confirmDialog = ref(null)
const confirmDialogEl = ref(null)
const modal = ref('')

const createForm = reactive({
  username: '',
  display_name: '',
  password: '',
  confirm_password: '',
  role_ids: []
})
const profileForm = reactive({
  id: '',
  username: '',
  display_name: '',
  role_ids: []
})
const passwordForm = reactive({
  current_password: '',
  new_password: '',
  confirm_password: ''
})
const resetForm = reactive({
  id: '',
  label: '',
  new_password: '',
  confirm_password: ''
})

const previewUnlocked = ref(false)
const canManage = computed(() => can('access.manage') || can('*') || previewUnlocked.value)
const isBootstrap = computed(() => !!actor.value?.bootstrap)
const canChangeOwnPassword = computed(() => Boolean(actor.value?.id) && !isBootstrap.value)
const filteredUsers = computed(() => {
  const q = query.value.trim().toLowerCase()
  if (!q) return users.value
  return users.value.filter((user) => {
    const haystack = [user.display_name, user.username, user.id, ...(roleIDs(user).map(roleName))]
      .filter(Boolean)
      .join(' ')
      .toLowerCase()
    return haystack.includes(q)
  })
})
const isEmptyDirectory = computed(() => users.value.length === 0)
const noSearchMatches = computed(() => users.value.length > 0 && !filteredUsers.value.length)
const createSubmitLabel = computed(() => {
  if (actionBusy.value === 'create') return '创建中…'
  return isEmptyDirectory.value ? '创建首个管理员' : '创建用户'
})
const modalUser = computed(() => users.value.find((user) => user.id === profileForm.id) || null)

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
    if (!canManage.value) {
      if (!actor.value) {
        applyPreviewUsers()
        previewUnlocked.value = true
        return
      }
      users.value = []
      roles.value = []
      return
    }
    const [nextUsers, nextRoles] = await Promise.all([
      fetchUsers(),
      fetchRoles().catch(() => [])
    ])
    users.value = Array.isArray(nextUsers) ? nextUsers : []
    roles.value = Array.isArray(nextRoles) ? nextRoles : []
    if (isEmptyDirectory.value && !createForm.role_ids.length && hasRole(ADMIN_ROLE)) {
      createForm.role_ids = [ADMIN_ROLE]
    }
    if (profileForm.id && !users.value.some((user) => user.id === profileForm.id)) {
      closeModal()
    }
  } catch (cause) {
    applyPreviewUsers()
    previewUnlocked.value = true
    error.value = ''
  } finally {
    loading.value = false
  }
}

function applyPreviewUsers() {
  roles.value = previewRoles()
  users.value = previewUsers(200)
}

function hasRole(roleID) {
  return roles.value.some((role) => role.id === roleID)
}

function userLabel(user) {
  return user?.display_name || user?.username || user?.id || ''
}

function initialOf(value) {
  const chars = Array.from(String(value || '').trim())
  return chars.length ? chars[0].toUpperCase() : '?'
}

function userMeta(user) {
  const roles = roleIDs(user).map(roleName).filter(Boolean)
  return [
    user?.username || '',
    roles.length ? roles.join(' · ') : '未分配',
    user?.disabled ? '已停用' : '已启用'
  ].filter(Boolean).join(' · ')
}

function roleName(roleID) {
  return roles.value.find((role) => role.id === roleID)?.name || roleID
}

function roleIDs(user) {
  return Array.isArray(user?.role_ids) ? user.role_ids : []
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
  if (cause?.code === 'last_admin_protected' && !actionError.value) {
    actionError.value = '不能去掉最后一个可登录的完整管理员。'
  }
}

function clearSecrets(target) {
  if (Object.hasOwn(target, 'password')) target.password = ''
  if (Object.hasOwn(target, 'confirm_password')) target.confirm_password = ''
  if (Object.hasOwn(target, 'current_password')) target.current_password = ''
  if (Object.hasOwn(target, 'new_password')) target.new_password = ''
}

function passwordTooShort(value) {
  return String(value || '').length < MIN_PASSWORD_LENGTH
}

function validateCreate() {
  const fields = {}
  if (!createForm.username.trim()) fields.username = '请填写用户名。'
  if (passwordTooShort(createForm.password)) fields.password = `密码至少 ${MIN_PASSWORD_LENGTH} 个字符。`
  if (createForm.password !== createForm.confirm_password) fields.confirm_password = '两次输入的密码不一致。'
  if (!createForm.role_ids.length) fields.role_ids = '请至少选择一个角色。'
  return fields
}

function validateReset() {
  const fields = {}
  if (passwordTooShort(resetForm.new_password)) fields.new_password = `密码至少 ${MIN_PASSWORD_LENGTH} 个字符。`
  if (resetForm.new_password !== resetForm.confirm_password) fields.confirm_password = '两次输入的新密码不一致。'
  return fields
}

function validateOwnPassword() {
  const fields = {}
  if (!passwordForm.current_password) fields.current_password = '请填写当前密码。'
  if (passwordTooShort(passwordForm.new_password)) fields.new_password = `密码至少 ${MIN_PASSWORD_LENGTH} 个字符。`
  if (passwordForm.new_password !== passwordForm.confirm_password) fields.confirm_password = '两次输入的新密码不一致。'
  return fields
}

function openCreate() {
  actionError.value = ''
  successNotice.value = ''
  setFieldErrors({})
  if (isEmptyDirectory.value && !createForm.role_ids.length && hasRole(ADMIN_ROLE)) {
    createForm.role_ids = [ADMIN_ROLE]
  }
  modal.value = 'create'
}

function openEdit(user) {
  if (!user?.id) return
  actionError.value = ''
  successNotice.value = ''
  setFieldErrors({})
  profileForm.id = user.id
  profileForm.username = user.username || ''
  profileForm.display_name = user.display_name || ''
  profileForm.role_ids = [...roleIDs(user)]
  modal.value = 'edit'
}

function openReset(user) {
  if (!user?.id) return
  actionError.value = ''
  successNotice.value = ''
  setFieldErrors({})
  resetForm.id = user.id
  resetForm.label = userLabel(user)
  resetForm.new_password = ''
  resetForm.confirm_password = ''
  modal.value = 'reset'
}

function openPassword() {
  actionError.value = ''
  successNotice.value = ''
  setFieldErrors({})
  clearSecrets(passwordForm)
  modal.value = 'password'
}

function closeModal() {
  if (actionBusy.value === 'create' || actionBusy.value === 'profile' || actionBusy.value === 'password') return
  modal.value = ''
}

async function submitCreate() {
  if (!canManage.value || actionBusy.value) return
  const fields = validateCreate()
  if (Object.keys(fields).length) {
    setFieldErrors(fields)
    actionError.value = '请先修正表单中的错误。'
    return
  }
  actionBusy.value = 'create'
  actionError.value = ''
  successNotice.value = ''
  setFieldErrors({})
  try {
    await createUser({
      username: createForm.username,
      display_name: createForm.display_name,
      password: createForm.password,
      role_ids: [...createForm.role_ids]
    })
    const wasEmpty = isEmptyDirectory.value
    clearSecrets(createForm)
    createForm.username = ''
    createForm.display_name = ''
    createForm.role_ids = []
    modal.value = ''
    await load()
    successNotice.value = wasEmpty
      ? '首个管理员已创建。请退出当前令牌身份，再使用账号密码登录。'
      : '用户已创建。'
  } catch (cause) {
    applyActionFailure(cause, '创建用户失败')
  } finally {
    actionBusy.value = ''
  }
}

async function submitProfile() {
  if (!canManage.value || !profileForm.id || actionBusy.value) return
  if (!profileForm.role_ids.length) {
    setFieldErrors({ role_ids: '请至少选择一个角色。' })
    actionError.value = '请先修正表单中的错误。'
    return
  }
  actionBusy.value = 'profile'
  actionError.value = ''
  successNotice.value = ''
  setFieldErrors({})
  try {
    const updated = await updateUser(profileForm.id, {
      display_name: profileForm.display_name,
      role_ids: [...profileForm.role_ids]
    })
    await load()
    if (updated?.id) openEdit(users.value.find((user) => user.id === updated.id) || updated)
    successNotice.value = '用户资料已保存。'
  } catch (cause) {
    applyActionFailure(cause, '保存用户资料失败')
  } finally {
    actionBusy.value = ''
  }
}

function requestDisable(user) {
  if (!canManage.value || !user || actionBusy.value) return
  openConfirm({
    kind: 'disable',
    title: '确认停用账号',
    message: `停用后，${userLabel(user)} 将无法再登录。最后一个可登录的完整管理员不能被停用。`,
    confirmText: '确认停用',
    user
  })
}

async function submitEnable(user) {
  if (!canManage.value || !user || actionBusy.value) return
  actionBusy.value = 'enable'
  actionError.value = ''
  successNotice.value = ''
  try {
    await updateUser(user.id, { disabled: false })
    await load()
    successNotice.value = '账号已启用。'
  } catch (cause) {
    applyActionFailure(cause, '启用账号失败')
  } finally {
    actionBusy.value = ''
  }
}

function requestReset() {
  if (!canManage.value || !resetForm.id || actionBusy.value) return
  const fields = validateReset()
  if (Object.keys(fields).length) {
    setFieldErrors(fields)
    actionError.value = '请先修正表单中的错误。'
    return
  }
  openConfirm({
    kind: 'reset',
    title: '确认重置密码',
    message: `将为 ${resetForm.label} 设置新密码，并立即作废该用户全部账号会话。页面不会回显密码。`,
    confirmText: '确认重置',
    user: { id: resetForm.id }
  })
}

function requestDelete(user) {
  if (!canManage.value || !user || actionBusy.value) return
  openConfirm({
    kind: 'delete',
    title: '确认删除用户',
    message: `将永久删除 ${userLabel(user)}（${user.username}）。会话会立即失效，最后一个可登录的完整管理员不能被删除。`,
    confirmText: '确认删除',
    user
  })
}

async function submitOwnPassword() {
  if (!canChangeOwnPassword.value || actionBusy.value) return
  const fields = validateOwnPassword()
  if (Object.keys(fields).length) {
    setFieldErrors(fields)
    actionError.value = '请先修正表单中的错误。'
    return
  }
  actionBusy.value = 'password'
  actionError.value = ''
  successNotice.value = ''
  setFieldErrors({})
  try {
    await changePassword({
      current_password: passwordForm.current_password,
      new_password: passwordForm.new_password
    })
    clearSecrets(passwordForm)
    successNotice.value = '密码已更新，请使用新密码重新登录。'
    await router.replace({ name: 'login' })
  } catch (cause) {
    applyActionFailure(cause, '修改密码失败')
  } finally {
    actionBusy.value = ''
  }
}

async function logoutToAccountLogin() {
  if (actionBusy.value) return
  actionBusy.value = 'logout'
  try {
    await logout().catch(() => undefined)
    await router.replace({ name: 'login' })
  } finally {
    actionBusy.value = ''
  }
}

function openConfirm(dialog) {
  confirmDialog.value = dialog
  nextTick(() => confirmDialogEl.value?.focus())
}

function cancelConfirm() {
  if (['disable', 'reset', 'delete'].includes(actionBusy.value)) return
  confirmDialog.value = null
}

async function confirmDanger() {
  const dialog = confirmDialog.value
  if (!dialog || actionBusy.value) return
  if (dialog.kind === 'disable') {
    actionBusy.value = 'disable'
    actionError.value = ''
    successNotice.value = ''
    try {
      await updateUser(dialog.user.id, { disabled: true })
      confirmDialog.value = null
      await load()
      successNotice.value = '账号已停用。'
    } catch (cause) {
      applyActionFailure(cause, '停用账号失败')
    } finally {
      actionBusy.value = ''
    }
    return
  }
  if (dialog.kind === 'reset') {
    actionBusy.value = 'reset'
    actionError.value = ''
    successNotice.value = ''
    try {
      await resetUserPassword(dialog.user.id, { new_password: resetForm.new_password })
      clearSecrets(resetForm)
      confirmDialog.value = null
      modal.value = ''
      successNotice.value = '密码已重置，目标用户需要使用新密码重新登录。'
    } catch (cause) {
      applyActionFailure(cause, '重置密码失败')
    } finally {
      actionBusy.value = ''
    }
    return
  }
  if (dialog.kind === 'delete') {
    actionBusy.value = 'delete'
    actionError.value = ''
    successNotice.value = ''
    try {
      await deleteUser(dialog.user.id)
      confirmDialog.value = null
      if (profileForm.id === dialog.user.id) closeModal()
      const deletedSelf = actor.value?.id === dialog.user.id
      await load()
      successNotice.value = '用户已删除。'
      if (deletedSelf) {
        await logout().catch(() => undefined)
        await router.replace({ name: 'login' })
      }
    } catch (cause) {
      applyActionFailure(cause, '删除用户失败')
    } finally {
      actionBusy.value = ''
    }
  }
}

function clearSearch() {
  query.value = ''
}

function focusSearch() {
  searchInputRef.value?.focus?.()
}

function onRowActivate(user) {
  openEdit(user)
}
</script>

<template>
  <main class="access-dir">
    <header class="access-dir__header">
      <div class="access-dir__header-left">
        <h1 class="access-dir__title">用户管理</h1>
        <p class="access-dir__subtitle">
          {{ users.length }} 个账号
          <template v-if="query.trim()"> · 匹配 {{ filteredUsers.length }} 个</template>
        </p>
      </div>
      <div v-if="canManage && !loading" class="access-dir__header-right">
        <div v-if="users.length" class="search-field" data-test="search-form" @click="focusSearch">
          <svg class="search-field__icon" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <circle cx="11" cy="11" r="7" />
            <path d="M20 20l-3.5-3.5" />
          </svg>
          <input
            ref="searchInputRef"
            v-model="query"
            class="search-field__input"
            data-test="user-search"
            type="search"
            placeholder="搜索用户名 / 显示名 / 角色"
            aria-label="搜索用户"
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
        <ViewToggle v-if="!narrow && (filteredUsers.length || query.trim())" :view="view" @update:view="view = $event" />
        <button
          v-if="canChangeOwnPassword"
          class="btn btn-secondary"
          type="button"
          data-test="open-password"
          @click="openPassword"
        >
          修改密码
        </button>
        <button class="btn btn-primary" type="button" data-test="open-create" :aria-label="isEmptyDirectory ? '创建首个管理员' : '创建用户'" @click="openCreate">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
            <line x1="12" y1="5" x2="12" y2="19" />
            <line x1="5" y1="12" x2="19" y2="12" />
          </svg>
          <span class="btn-text">{{ isEmptyDirectory ? '创建首个管理员' : '创建用户' }}</span>
        </button>
      </div>
    </header>

    <div v-if="loading" class="access-dir__loading">
      <div class="spinner"></div>
      <p>正在读取用户…</p>
    </div>

    <EmptyState
      v-else-if="!canManage"
      title="无权查看用户"
      description="当前账号不能管理用户。"
    />

    <div v-else-if="error && !users.length" role="alert">
      <EmptyState title="读取失败" :description="error">
        <template #action>
          <button class="btn btn-secondary" type="button" @click="load">重试</button>
        </template>
      </EmptyState>
    </div>

    <template v-else>
      <p v-if="actionError" class="access-dir__alert" role="alert">{{ actionError }}</p>
      <div v-if="successNotice" class="access-dir__notice" role="status">
        <p>{{ successNotice }}</p>
        <button
          v-if="successNotice.includes('令牌身份')"
          class="btn btn-secondary"
          type="button"
          data-test="logout-to-account"
          :disabled="actionBusy === 'logout'"
          @click="logoutToAccountLogin"
        >
          {{ actionBusy === 'logout' ? '退出中…' : '退出令牌身份' }}
        </button>
      </div>

      <EmptyState
        v-if="isEmptyDirectory"
        title="还没有账号"
        description="请创建具有 administrator 角色的首个管理员，然后退出令牌身份，改用账号密码登录。不需要设置新的用户名或密码环境变量。"
      >
        <template #action>
          <button class="btn btn-primary" type="button" @click="openCreate">创建首个管理员</button>
        </template>
      </EmptyState>

      <div v-else-if="noSearchMatches" class="access-dir__empty">
        <p>没有匹配的用户</p>
        <button class="btn btn-secondary" type="button" data-test="clear-search" @click="clearSearch">清空搜索</button>
      </div>

      <div v-if="filteredUsers.length && cardView" class="access-dir__grid" data-test="users-grid">
        <UserCard
          v-for="user in filteredUsers"
          :key="user.id"
          :user="user"
          :role-names="roleIDs(user).map(roleName)"
          :busy="!!actionBusy"
          @edit="openEdit"
          @enable="submitEnable"
          @disable="requestDisable"
          @reset="openReset"
          @delete="requestDelete"
        />
      </div>

      <ul
        v-else-if="filteredUsers.length && !cardView"
        class="access-dir__entry-list access-dir__entry-list--page"
        data-test="users-table"
      >
        <li
          v-for="user in filteredUsers"
          :key="user.id"
          class="access-dir__entry access-dir__entry--clickable"
          :class="{ 'access-dir__entry--disabled': user.disabled }"
          :data-test="`user-row-${user.id}`"
          @click="onRowActivate(user)"
        >
          <span class="access-dir__mark" aria-hidden="true">{{ initialOf(userLabel(user)) }}</span>
          <div class="access-dir__entry-copy">
            <strong :title="userLabel(user)">{{ userLabel(user) }}</strong>
            <small data-test="user-username" :title="userMeta(user)">{{ userMeta(user) }}</small>
          </div>
          <div class="access-dir__action-bar" @click.stop>
            <BaseIconButton title="编辑" data-test="edit-user" @click="openEdit(user)">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7" />
                <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z" />
              </svg>
            </BaseIconButton>
            <BaseIconButton
              v-if="user.disabled"
              title="启用"
              data-test="enable-user"
              :disabled="!!actionBusy"
              @click="submitEnable(user)"
            >
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M5 12h14" />
                <path d="M12 5v14" />
              </svg>
            </BaseIconButton>
            <BaseIconButton
              v-else
              title="停用"
              data-test="disable-user"
              :disabled="!!actionBusy"
              @click="requestDisable(user)"
            >
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <rect x="6" y="6" width="12" height="12" rx="2" />
              </svg>
            </BaseIconButton>
            <BaseIconButton title="重置密码" data-test="reset-user" :disabled="!!actionBusy" @click="openReset(user)">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <rect x="3" y="11" width="18" height="11" rx="2" />
                <path d="M7 11V7a5 5 0 0 1 10 0v4" />
              </svg>
            </BaseIconButton>
            <BaseIconButton title="删除" tone="danger" data-test="delete-user" :disabled="!!actionBusy" @click="requestDelete(user)">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <polyline points="3 6 5 6 21 6" />
                <path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6" />
              </svg>
            </BaseIconButton>
          </div>
        </li>
      </ul>

      <section v-if="canChangeOwnPassword" class="access-dir__notice" data-test="account-security">
        <div>
          <strong>账号安全</strong>
          <p class="access-dir__muted">修改本人密码后，当前账号会话会立即失效。</p>
        </div>
        <button class="btn btn-secondary" type="button" @click="openPassword">修改本人密码</button>
      </section>
    </template>

    <BaseModal
      :model-value="modal === 'create'"
      :title="isEmptyDirectory ? '初始化首个管理员' : '创建用户'"
      :subtitle="isEmptyDirectory ? '创建成功后退出令牌身份，改用账号密码登录。' : `用户名创建后不可修改。密码至少 ${MIN_PASSWORD_LENGTH} 个字符。`"
      size="sm"
      :close-on-click-modal="false"
      show-footer
      @update:model-value="closeModal"
    >
      <form id="user-create-form" class="access-dir__form" data-test="create-form" @submit.prevent="submitCreate">
        <label class="access-dir__field">
          <span>用户名</span>
          <input v-model="createForm.username" data-test="create-username" type="text" autocomplete="off" placeholder="创建后不可修改">
          <small v-if="fieldMessage('username')" class="access-dir__field-error">{{ fieldMessage('username') }}</small>
        </label>
        <label class="access-dir__field">
          <span>显示名</span>
          <input v-model="createForm.display_name" data-test="create-display-name" type="text" autocomplete="off">
        </label>
        <label class="access-dir__field">
          <span>初始密码</span>
          <input
            v-model="createForm.password"
            data-test="create-password"
            type="password"
            autocomplete="new-password"
            :placeholder="`至少 ${MIN_PASSWORD_LENGTH} 个字符`"
          >
          <small v-if="fieldMessage('password')" class="access-dir__field-error">{{ fieldMessage('password') }}</small>
        </label>
        <label class="access-dir__field">
          <span>确认密码</span>
          <input v-model="createForm.confirm_password" data-test="create-confirm-password" type="password" autocomplete="new-password">
          <small v-if="fieldMessage('confirm_password')" class="access-dir__field-error">{{ fieldMessage('confirm_password') }}</small>
        </label>
        <RoleSelect
          v-model="createForm.role_ids"
          :roles="roles"
          data-test="create-roles"
          test-prefix="create-role"
          :disabled="actionBusy === 'create'"
          :error="fieldMessage('role_ids')"
        />
      </form>
      <template #footer>
        <button class="btn btn-secondary" type="button" @click="closeModal">取消</button>
        <button class="btn btn-primary" type="submit" form="user-create-form" data-test="create-submit" :disabled="actionBusy === 'create'">
          {{ createSubmitLabel }}
        </button>
      </template>
    </BaseModal>

    <BaseModal
      :model-value="modal === 'edit'"
      title="编辑用户"
      subtitle="用户名创建后不可修改。"
      size="sm"
      :close-on-click-modal="false"
      show-footer
      @update:model-value="closeModal"
    >
      <form v-if="modalUser || profileForm.id" id="user-profile-form" class="access-dir__form" data-test="profile-form" @submit.prevent="submitProfile">
        <dl class="access-dir__facts">
          <div>
            <dt>用户名</dt>
            <dd data-test="user-username">{{ profileForm.username }}</dd>
          </div>
          <div>
            <dt>状态</dt>
            <dd>{{ modalUser?.disabled ? '已停用' : '已启用' }}</dd>
          </div>
        </dl>
        <label class="access-dir__field">
          <span>显示名</span>
          <input v-model="profileForm.display_name" data-test="profile-display-name" type="text" autocomplete="name">
        </label>
        <RoleSelect
          v-model="profileForm.role_ids"
          :roles="roles"
          data-test="profile-roles"
          test-prefix="profile-role"
          :disabled="actionBusy === 'profile'"
          :error="fieldMessage('role_ids')"
        />
      </form>
      <template #footer>
        <button class="btn btn-secondary" type="button" @click="closeModal">取消</button>
        <button class="btn btn-primary" type="submit" form="user-profile-form" :disabled="actionBusy === 'profile'">
          {{ actionBusy === 'profile' ? '保存中…' : '保存资料' }}
        </button>
      </template>
    </BaseModal>

    <BaseModal
      :model-value="modal === 'reset'"
      title="重置密码"
      subtitle="只能写入新密码。成功后该用户全部账号会话失效。"
      size="sm"
      :close-on-click-modal="false"
      show-footer
      @update:model-value="closeModal"
    >
      <form id="user-reset-form" class="access-dir__form" data-test="reset-form" @submit.prevent="requestReset">
        <label class="access-dir__field">
          <span>新密码</span>
          <input
            v-model="resetForm.new_password"
            data-test="reset-password"
            type="password"
            autocomplete="new-password"
            :placeholder="`至少 ${MIN_PASSWORD_LENGTH} 个字符`"
          >
          <small v-if="fieldMessage('new_password')" class="access-dir__field-error">{{ fieldMessage('new_password') }}</small>
        </label>
        <label class="access-dir__field">
          <span>确认新密码</span>
          <input v-model="resetForm.confirm_password" data-test="reset-confirm-password" type="password" autocomplete="new-password">
          <small v-if="fieldMessage('confirm_password')" class="access-dir__field-error">{{ fieldMessage('confirm_password') }}</small>
        </label>
      </form>
      <template #footer>
        <button class="btn btn-secondary" type="button" @click="closeModal">取消</button>
        <button class="btn btn-primary" type="submit" form="user-reset-form" :disabled="!!actionBusy">重置密码</button>
      </template>
    </BaseModal>

    <BaseModal
      :model-value="modal === 'password'"
      title="修改本人密码"
      subtitle="必须验证当前密码。成功后当前账号会话立即失效。"
      size="sm"
      :close-on-click-modal="false"
      show-footer
      @update:model-value="closeModal"
    >
      <form id="user-password-form" class="access-dir__form" data-test="password-form" @submit.prevent="submitOwnPassword">
        <label class="access-dir__field">
          <span>当前密码</span>
          <input v-model="passwordForm.current_password" data-test="current-password" type="password" autocomplete="current-password">
          <small v-if="fieldMessage('current_password')" class="access-dir__field-error">{{ fieldMessage('current_password') }}</small>
        </label>
        <label class="access-dir__field">
          <span>新密码</span>
          <input
            v-model="passwordForm.new_password"
            data-test="own-new-password"
            type="password"
            autocomplete="new-password"
            :placeholder="`至少 ${MIN_PASSWORD_LENGTH} 个字符`"
          >
          <small v-if="fieldMessage('new_password')" class="access-dir__field-error">{{ fieldMessage('new_password') }}</small>
        </label>
        <label class="access-dir__field">
          <span>确认新密码</span>
          <input v-model="passwordForm.confirm_password" data-test="own-confirm-password" type="password" autocomplete="new-password">
          <small v-if="fieldMessage('confirm_password')" class="access-dir__field-error">{{ fieldMessage('confirm_password') }}</small>
        </label>
      </form>
      <template #footer>
        <button class="btn btn-secondary" type="button" @click="closeModal">取消</button>
        <button class="btn btn-primary" type="submit" form="user-password-form" :disabled="actionBusy === 'password'">
          {{ actionBusy === 'password' ? '改密中…' : '修改本人密码' }}
        </button>
      </template>
    </BaseModal>

    <div
      v-if="confirmDialog"
      ref="confirmDialogEl"
      class="access-dir__dialog-overlay"
      data-test="confirm-dialog"
      role="dialog"
      aria-modal="true"
      aria-labelledby="users-confirm-title"
      tabindex="-1"
      @keydown.escape.prevent="cancelConfirm"
      @click.self="cancelConfirm"
    >
      <div class="access-dir__dialog">
        <h3 id="users-confirm-title">{{ confirmDialog.title }}</h3>
        <p>{{ confirmDialog.message }}</p>
        <div class="access-dir__dialog-actions">
          <button class="btn btn-secondary" type="button" data-test="confirm-cancel" @click="cancelConfirm">取消</button>
          <button
            class="btn btn-primary"
            type="button"
            data-test="confirm-accept"
            :disabled="['disable', 'reset', 'delete'].includes(actionBusy)"
            @click="confirmDanger"
          >
            {{ actionBusy === confirmDialog.kind ? '提交中…' : confirmDialog.confirmText }}
          </button>
        </div>
      </div>
    </div>
  </main>
</template>

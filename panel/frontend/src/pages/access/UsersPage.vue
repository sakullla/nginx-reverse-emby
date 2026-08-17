<script setup>
import { computed, nextTick, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import {
  changePassword,
  createUser,
  fetchRoles,
  fetchUsers,
  logout,
  resetUserPassword,
  updateUser
} from '../../api/access'
import { useAccessControl } from '../../context/useAccessControl'
import EmptyState from '../../components/base/EmptyState.vue'

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
const selectedID = ref('')
const query = ref('')
const fieldErrors = ref({})
const confirmDialog = ref(null)
const confirmDialogEl = ref(null)

const createForm = reactive({
  username: '',
  display_name: '',
  password: '',
  confirm_password: '',
  role_ids: []
})
const profileForm = reactive({
  display_name: '',
  role_ids: []
})
const passwordForm = reactive({
  current_password: '',
  new_password: '',
  confirm_password: ''
})
const resetForm = reactive({
  new_password: '',
  confirm_password: ''
})

const canManage = computed(() => can('access.manage') || can('*'))
const isBootstrap = computed(() => !!actor.value?.bootstrap)
const canChangeOwnPassword = computed(() => Boolean(actor.value?.id) && !isBootstrap.value)
const selectedUser = computed(() => users.value.find((user) => user.id === selectedID.value) || null)
const isEmptyDirectory = computed(() => !query.value.trim() && users.value.length === 0)
const createSubmitLabel = computed(() => {
  if (actionBusy.value === 'create') return '创建中…'
  return isEmptyDirectory.value ? '创建首个管理员' : '创建用户'
})

onMounted(load)

async function load() {
  loading.value = true
  error.value = ''
  try {
    if (!actor.value) await refreshActor()
    if (!canManage.value) {
      users.value = []
      roles.value = []
      selectedID.value = ''
      return
    }
    const q = query.value.trim()
    const [nextUsers, nextRoles] = await Promise.all([
      fetchUsers(q ? { q } : undefined),
      fetchRoles().catch(() => [])
    ])
    users.value = Array.isArray(nextUsers) ? nextUsers : []
    roles.value = Array.isArray(nextRoles) ? nextRoles : []
    if (!users.value.some((user) => user.id === selectedID.value)) {
      selectedID.value = users.value[0]?.id || ''
    }
    syncProfileForm(selectedUser.value)
    if (isEmptyDirectory.value && !createForm.role_ids.length && hasRole(ADMIN_ROLE)) {
      createForm.role_ids = [ADMIN_ROLE]
    }
  } catch (cause) {
    error.value = cause?.message || '读取用户失败'
  } finally {
    loading.value = false
  }
}

function hasRole(roleID) {
  return roles.value.some((role) => role.id === roleID)
}

function userLabel(user) {
  return user?.display_name || user?.username || user?.id || ''
}

function roleName(roleID) {
  return roles.value.find((role) => role.id === roleID)?.name || roleID
}

function roleSummary(user) {
  const ids = Array.isArray(user?.role_ids) ? user.role_ids : []
  return ids.length ? ids.map(roleName).join('、') : '未分配角色'
}

function syncProfileForm(user) {
  profileForm.display_name = user?.display_name || ''
  profileForm.role_ids = [...(user?.role_ids || [])]
}

function selectUser(user) {
  selectedID.value = user.id
  fieldErrors.value = {}
  actionError.value = ''
  resetForm.new_password = ''
  resetForm.confirm_password = ''
  syncProfileForm(user)
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

function setRole(target, roleID, enabled) {
  const list = target.role_ids
  const index = list.indexOf(roleID)
  if (enabled && index < 0) list.push(roleID)
  if (!enabled && index >= 0) list.splice(index, 1)
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
    const created = await createUser({
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
    await load()
    if (created?.id) {
      const next = users.value.find((user) => user.id === created.id) || created
      selectUser(next)
    }
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
  if (!canManage.value || !selectedUser.value || actionBusy.value) return
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
    const updated = await updateUser(selectedUser.value.id, {
      display_name: profileForm.display_name,
      role_ids: [...profileForm.role_ids]
    })
    await load()
    if (updated?.id) selectUser(users.value.find((user) => user.id === updated.id) || updated)
    successNotice.value = '用户资料已保存。'
  } catch (cause) {
    applyActionFailure(cause, '保存用户资料失败')
  } finally {
    actionBusy.value = ''
  }
}

function requestDisable() {
  if (!canManage.value || !selectedUser.value || actionBusy.value) return
  openConfirm({
    kind: 'disable',
    title: '确认停用账号',
    message: `停用后，${userLabel(selectedUser.value)} 将无法再登录。最后一个可登录的完整管理员不能被停用。`,
    confirmText: '确认停用',
    user: selectedUser.value
  })
}

async function submitEnable() {
  if (!canManage.value || !selectedUser.value || actionBusy.value) return
  actionBusy.value = 'enable'
  actionError.value = ''
  successNotice.value = ''
  try {
    const updated = await updateUser(selectedUser.value.id, { disabled: false })
    await load()
    if (updated?.id) selectUser(users.value.find((user) => user.id === updated.id) || updated)
    successNotice.value = '账号已启用。'
  } catch (cause) {
    applyActionFailure(cause, '启用账号失败')
  } finally {
    actionBusy.value = ''
  }
}

function requestReset() {
  if (!canManage.value || !selectedUser.value || actionBusy.value) return
  const fields = validateReset()
  if (Object.keys(fields).length) {
    setFieldErrors(fields)
    actionError.value = '请先修正表单中的错误。'
    return
  }
  openConfirm({
    kind: 'reset',
    title: '确认重置密码',
    message: `将为 ${userLabel(selectedUser.value)} 设置新密码，并立即作废该用户全部账号会话。页面不会回显密码。`,
    confirmText: '确认重置',
    user: selectedUser.value
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
  if (actionBusy.value === 'disable' || actionBusy.value === 'reset') return
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
      const updated = await updateUser(dialog.user.id, { disabled: true })
      confirmDialog.value = null
      await load()
      if (updated?.id) selectUser(users.value.find((user) => user.id === updated.id) || updated)
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
      successNotice.value = '密码已重置，目标用户需要使用新密码重新登录。'
    } catch (cause) {
      applyActionFailure(cause, '重置密码失败')
    } finally {
      actionBusy.value = ''
    }
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
</script>

<template>
  <main class="users-page">
    <header class="page-header">
      <div class="page-header__left">
        <RouterLink to="/access" class="back-link">← 访问与安全</RouterLink>
        <h1 class="page-title">用户管理</h1>
        <p class="page-subtitle">创建账号、维护显示名与角色、启停账号。已登录账号可在本页完成本人改密。</p>
      </div>
    </header>

    <div v-if="loading" class="users-page__loading">
      <div class="spinner"></div>
      <p>正在读取用户…</p>
    </div>

    <EmptyState
      v-else-if="!canManage"
      title="无权查看用户"
      description="当前身份没有 access.manage 权限。"
    />

    <div v-else-if="error" role="alert">
      <EmptyState title="读取失败" :description="error">
        <template #action>
          <button class="btn btn-secondary" type="button" @click="load">重试</button>
        </template>
      </EmptyState>
    </div>

    <template v-else>
      <p v-if="actionError" class="users-alert" role="alert">{{ actionError }}</p>
      <div v-if="successNotice" class="users-notice" role="status">
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

      <section class="users-workspace" aria-label="用户管理">
        <aside class="users-list">
          <div class="users-list__heading">
            <strong>用户</strong>
            <span>{{ users.length }}</span>
          </div>
          <form class="users-search" data-test="search-form" @submit.prevent="submitSearch">
            <label class="users-field">
              <span>搜索用户</span>
              <input
                v-model="query"
                data-test="user-search"
                type="search"
                placeholder="用户名或显示名"
                @keydown.esc.prevent="clearSearch"
              >
            </label>
            <button class="btn btn-secondary" type="submit">搜索</button>
          </form>

          <EmptyState
            v-if="isEmptyDirectory"
            title="还没有账号"
            description="请创建具有 administrator 角色的首个管理员，然后退出令牌身份，改用账号密码登录。不需要设置新的用户名或密码环境变量。"
          />
          <p v-else-if="!users.length" class="users-list__empty">没有匹配的用户</p>
          <button
            v-for="user in users"
            :key="user.id"
            type="button"
            :class="['users-list__item', { 'users-list__item--active': selectedID === user.id }]"
            @click="selectUser(user)"
          >
            <span>
              <strong>{{ userLabel(user) }}</strong>
              <small>{{ user.username }} · {{ roleSummary(user) }} · {{ user.disabled ? '已停用' : '已启用' }}</small>
            </span>
          </button>
        </aside>

        <div v-if="selectedUser" class="users-detail">
          <div class="users-detail__header">
            <div>
              <h2>{{ userLabel(selectedUser) }}</h2>
              <p>用户名创建后不可修改，账号不能硬删除。</p>
            </div>
          </div>

          <dl class="users-facts">
            <div>
              <dt>用户名</dt>
              <dd data-test="user-username">{{ selectedUser.username }}</dd>
            </div>
            <div>
              <dt>状态</dt>
              <dd>{{ selectedUser.disabled ? '已停用' : '已启用' }}</dd>
            </div>
            <div>
              <dt>角色</dt>
              <dd>{{ roleSummary(selectedUser) }}</dd>
            </div>
          </dl>

          <form class="users-form" data-test="profile-form" @submit.prevent="submitProfile">
            <label class="users-field">
              <span>显示名</span>
              <input
                v-model="profileForm.display_name"
                data-test="profile-display-name"
                type="text"
                autocomplete="name"
              >
            </label>
            <fieldset class="users-roles" data-test="profile-roles">
              <legend>角色</legend>
              <label v-for="role in roles" :key="role.id" class="users-check">
                <input
                  type="checkbox"
                  :data-test="`profile-role-${role.id}`"
                  :checked="profileForm.role_ids.includes(role.id)"
                  @change="setRole(profileForm, role.id, $event.target.checked)"
                >
                <span>{{ role.name }}</span>
              </label>
              <p v-if="fieldMessage('role_ids')" class="users-field-error">{{ fieldMessage('role_ids') }}</p>
            </fieldset>
            <button class="btn btn-primary" type="submit" :disabled="actionBusy === 'profile'">
              {{ actionBusy === 'profile' ? '保存中…' : '保存资料' }}
            </button>
          </form>

          <div class="users-actions">
            <button
              v-if="selectedUser.disabled"
              class="btn btn-secondary"
              type="button"
              data-test="enable-user"
              :disabled="!!actionBusy"
              @click="submitEnable"
            >
              {{ actionBusy === 'enable' ? '启用中…' : '启用账号' }}
            </button>
            <button
              v-else
              class="btn btn-secondary"
              type="button"
              data-test="disable-user"
              :disabled="!!actionBusy"
              @click="requestDisable"
            >
              停用账号
            </button>
          </div>

          <section class="users-panel" aria-label="重置密码">
            <h3>管理员重置密码</h3>
            <p>只能写入新密码，不能读取或找回现有密码。成功后该用户全部账号会话失效。</p>
            <form class="users-form" data-test="reset-form" @submit.prevent="requestReset">
              <label class="users-field">
                <span>新密码</span>
                <input
                  v-model="resetForm.new_password"
                  data-test="reset-password"
                  type="password"
                  autocomplete="new-password"
                  :placeholder="`至少 ${MIN_PASSWORD_LENGTH} 个字符`"
                >
                <small v-if="fieldMessage('new_password')" class="users-field-error">{{ fieldMessage('new_password') }}</small>
              </label>
              <label class="users-field">
                <span>确认新密码</span>
                <input
                  v-model="resetForm.confirm_password"
                  data-test="reset-confirm-password"
                  type="password"
                  autocomplete="new-password"
                >
                <small v-if="fieldMessage('confirm_password')" class="users-field-error">{{ fieldMessage('confirm_password') }}</small>
              </label>
              <button class="btn btn-secondary" type="submit" :disabled="!!actionBusy">重置密码</button>
            </form>
          </section>
        </div>

        <div v-else class="users-detail users-detail--empty">
          <strong>{{ isEmptyDirectory ? '创建首个管理员' : '选择一个用户' }}</strong>
          <p v-if="isEmptyDirectory">空账号列表时，使用右侧创建表单初始化第一个 administrator。</p>
          <p v-else-if="query.trim()">没有匹配的用户，可清空搜索后再试。</p>
          <p v-else>从左侧选择用户后可改资料、角色和启停状态。</p>
        </div>
      </section>

      <section class="users-create" aria-label="创建用户">
        <h2>{{ isEmptyDirectory ? '初始化首个管理员' : '创建用户' }}</h2>
        <p v-if="isEmptyDirectory">
          用现有 bootstrap 令牌创建具有 administrator 角色的账号。创建成功后退出令牌身份，改用账号密码登录。令牌登录仍可用于应急访问。
        </p>
        <p v-else>用户名会去掉首尾空白并规范为小写，创建后不可修改。密码至少 {{ MIN_PASSWORD_LENGTH }} 个字符，且必须选择至少一个可委派角色。</p>
        <form class="users-form" data-test="create-form" @submit.prevent="submitCreate">
          <label class="users-field">
            <span>用户名</span>
            <input
              v-model="createForm.username"
              data-test="create-username"
              type="text"
              autocomplete="off"
              placeholder="创建后不可修改"
            >
            <small v-if="fieldMessage('username')" class="users-field-error">{{ fieldMessage('username') }}</small>
          </label>
          <label class="users-field">
            <span>显示名</span>
            <input
              v-model="createForm.display_name"
              data-test="create-display-name"
              type="text"
              autocomplete="off"
            >
          </label>
          <label class="users-field">
            <span>初始密码</span>
            <input
              v-model="createForm.password"
              data-test="create-password"
              type="password"
              autocomplete="new-password"
              :placeholder="`至少 ${MIN_PASSWORD_LENGTH} 个字符`"
            >
            <small v-if="fieldMessage('password')" class="users-field-error">{{ fieldMessage('password') }}</small>
          </label>
          <label class="users-field">
            <span>确认密码</span>
            <input
              v-model="createForm.confirm_password"
              data-test="create-confirm-password"
              type="password"
              autocomplete="new-password"
            >
            <small v-if="fieldMessage('confirm_password')" class="users-field-error">{{ fieldMessage('confirm_password') }}</small>
          </label>
          <fieldset class="users-roles" data-test="create-roles">
            <legend>角色</legend>
            <label v-for="role in roles" :key="role.id" class="users-check">
              <input
                type="checkbox"
                :data-test="`create-role-${role.id}`"
                :checked="createForm.role_ids.includes(role.id)"
                @change="setRole(createForm, role.id, $event.target.checked)"
              >
              <span>{{ role.name }}</span>
            </label>
            <p v-if="fieldMessage('role_ids')" class="users-field-error">{{ fieldMessage('role_ids') }}</p>
          </fieldset>
          <button class="btn btn-primary" type="submit" :disabled="actionBusy === 'create'">
            {{ createSubmitLabel }}
          </button>
        </form>
      </section>

      <section v-if="canChangeOwnPassword" class="users-security" aria-label="账号安全" data-test="account-security">
        <h2>账号安全</h2>
        <p>修改本人密码时必须验证当前密码，并确认两次新密码一致。成功后当前账号会话立即失效，bootstrap 令牌不受影响。</p>
        <form class="users-form" data-test="password-form" @submit.prevent="submitOwnPassword">
          <label class="users-field">
            <span>当前密码</span>
            <input
              v-model="passwordForm.current_password"
              data-test="current-password"
              type="password"
              autocomplete="current-password"
            >
            <small v-if="fieldMessage('current_password')" class="users-field-error">{{ fieldMessage('current_password') }}</small>
          </label>
          <label class="users-field">
            <span>新密码</span>
            <input
              v-model="passwordForm.new_password"
              data-test="own-new-password"
              type="password"
              autocomplete="new-password"
              :placeholder="`至少 ${MIN_PASSWORD_LENGTH} 个字符`"
            >
            <small v-if="fieldMessage('new_password')" class="users-field-error">{{ fieldMessage('new_password') }}</small>
          </label>
          <label class="users-field">
            <span>确认新密码</span>
            <input
              v-model="passwordForm.confirm_password"
              data-test="own-confirm-password"
              type="password"
              autocomplete="new-password"
            >
            <small v-if="fieldMessage('confirm_password')" class="users-field-error">{{ fieldMessage('confirm_password') }}</small>
          </label>
          <button class="btn btn-primary" type="submit" :disabled="actionBusy === 'password'">
            {{ actionBusy === 'password' ? '改密中…' : '修改本人密码' }}
          </button>
        </form>
      </section>
    </template>

    <div
      v-if="confirmDialog"
      ref="confirmDialogEl"
      class="users-dialog-overlay"
      data-test="confirm-dialog"
      role="dialog"
      aria-modal="true"
      aria-labelledby="users-confirm-title"
      tabindex="-1"
      @keydown.escape.prevent="cancelConfirm"
      @click.self="cancelConfirm"
    >
      <div class="users-dialog">
        <h3 id="users-confirm-title">{{ confirmDialog.title }}</h3>
        <p>{{ confirmDialog.message }}</p>
        <div class="users-dialog__actions">
          <button class="btn btn-secondary" type="button" data-test="confirm-cancel" @click="cancelConfirm">取消</button>
          <button
            class="btn btn-primary"
            type="button"
            data-test="confirm-accept"
            :disabled="actionBusy === 'disable' || actionBusy === 'reset'"
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
.users-page {
  max-width: 1180px;
  display: grid;
  gap: var(--space-6);
  margin: 0 auto;
}

.users-page__loading {
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

.users-alert {
  color: var(--color-danger);
}

.users-notice {
  display: grid;
  gap: var(--space-3);
  padding: var(--space-4);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  background: var(--color-bg-surface);
}

.users-notice p,
.users-detail p,
.users-create p,
.users-security p,
.users-list__empty {
  margin: 0;
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.users-workspace {
  display: grid;
  grid-template-columns: minmax(14rem, 18rem) minmax(0, 1fr);
  gap: var(--space-5);
}

.users-list,
.users-detail,
.users-create,
.users-security {
  display: grid;
  gap: var(--space-4);
  padding: var(--space-5);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  background: var(--color-bg-surface);
}

.users-list__heading,
.users-detail__header {
  display: flex;
  justify-content: space-between;
  gap: var(--space-3);
}

.users-list__item {
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

.users-list__item span {
  display: grid;
  gap: 2px;
}

.users-list__item--active {
  border-color: var(--color-primary);
  background: var(--color-primary-subtle);
}

.users-search,
.users-form,
.users-actions,
.users-dialog__actions {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(12rem, 1fr));
  gap: var(--space-3);
  align-items: end;
}

.users-field,
.users-roles {
  display: grid;
  gap: var(--space-2);
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
}

.users-field input {
  min-width: 0;
  padding: 0.65rem 0.75rem;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-canvas);
  color: var(--color-text-primary);
  font: inherit;
}

.users-roles {
  grid-column: 1 / -1;
  margin: 0;
  padding: 0;
  border: 0;
}

.users-check {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.users-field-error {
  color: var(--color-danger);
}

.users-facts {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(10rem, 1fr));
  gap: var(--space-3);
  margin: 0;
}

.users-facts dt {
  color: var(--color-text-muted);
  font-size: var(--text-xs);
}

.users-facts dd {
  margin: 0.25rem 0 0;
}

.users-panel {
  display: grid;
  gap: var(--space-3);
  padding-top: var(--space-4);
  border-top: 1px solid var(--color-border-subtle);
}

.users-detail h2,
.users-create h2,
.users-security h2,
.users-panel h3 {
  margin: 0;
}

.users-dialog-overlay {
  position: fixed;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--space-4);
  background: rgba(15, 23, 42, 0.4);
  z-index: var(--z-modal, 40);
}

.users-dialog {
  display: grid;
  gap: var(--space-4);
  width: min(28rem, 100%);
  padding: var(--space-5);
  border-radius: var(--radius-xl);
  background: var(--color-bg-surface);
}

.users-dialog h3,
.users-dialog p {
  margin: 0;
}

@media (max-width: 800px) {
  .users-workspace {
    grid-template-columns: 1fr;
  }
}
</style>

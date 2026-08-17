<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import {
  changePassword,
  fetchAuditEvents,
  fetchQuotaOverview,
  fetchResourceGroups,
  fetchRoles,
  fetchSecrets,
  fetchUsers
} from '../../api/access'
import QuotaUsage from '../../components/access/QuotaUsage.vue'
import { useAccessControl } from '../../context/useAccessControl'

const MIN_PASSWORD_LENGTH = 10
const managedPagePaths = {
  users: '/access/users',
  'resource-groups': '/access/resource-groups'
}

const { actor, can, refreshActor, visibleNavigation } = useAccessControl()
const loading = ref(true)
const error = ref(null)
const counts = ref({})
const quotaUsage = ref([])
const passwordBusy = ref(false)
const passwordError = ref('')
const passwordNotice = ref('')
const passwordFields = ref({})
const passwordForm = reactive({
  current_password: '',
  new_password: '',
  confirm_password: ''
})

const currentActor = computed(() => actor?.value ?? null)
const cards = computed(() => (visibleNavigation?.value || []).map((item) => ({
  ...item,
  path: item.path || managedPagePaths[item.id] || '',
  count: counts.value[item.id]
})))
const showUserBootstrap = computed(() => (
  (can('access.manage') || can('*')) && counts.value.users === 0
))
const canChangeOwnPassword = computed(() => {
  const record = currentActor.value
  return !!(record?.id && !record.bootstrap)
})

onMounted(async () => {
  try {
    await refreshActor()
    const requests = []
    const assign = (id, request) => requests.push(request().then((items) => { counts.value[id] = items.length }))
    if (can('access.manage')) {
      assign('users', fetchUsers)
      assign('roles', fetchRoles)
    }
    if (can('resource.read')) assign('resource-groups', fetchResourceGroups)
    if (can('resource.read')) {
      requests.push(fetchQuotaOverview().then((payload) => {
        quotaUsage.value = payload.quota_usage || []
        counts.value.quotas = quotaUsage.value.length
      }))
    }
    if (can('secret.metadata.read')) assign('secrets', fetchSecrets)
    if (can('audit.read')) assign('audit', () => fetchAuditEvents(20))
    await Promise.all(requests)
  } catch (cause) {
    error.value = cause
  } finally {
    loading.value = false
  }
})

function fieldMessage(name) {
  return passwordFields.value?.[name] || ''
}

async function submitOwnPassword() {
  if (passwordBusy.value || !canChangeOwnPassword.value || typeof changePassword !== 'function') return
  passwordError.value = ''
  passwordNotice.value = ''
  const fields = {}
  if (!passwordForm.current_password) fields.current_password = '请输入当前密码'
  if (!passwordForm.new_password || passwordForm.new_password.length < MIN_PASSWORD_LENGTH) {
    fields.new_password = `新密码至少 ${MIN_PASSWORD_LENGTH} 个字符`
  }
  if (passwordForm.new_password !== passwordForm.confirm_password) {
    fields.confirm_password = '两次输入的新密码不一致'
  }
  passwordFields.value = fields
  if (Object.keys(fields).length) return

  passwordBusy.value = true
  try {
    await changePassword({
      current_password: passwordForm.current_password,
      new_password: passwordForm.new_password
    })
    passwordForm.current_password = ''
    passwordForm.new_password = ''
    passwordForm.confirm_password = ''
    passwordNotice.value = '密码已更新，请使用新密码重新登录。'
  } catch (cause) {
    passwordError.value = cause?.message || '修改密码失败'
    passwordFields.value = cause?.fields && typeof cause.fields === 'object' ? { ...cause.fields } : {}
  } finally {
    passwordBusy.value = false
  }
}
</script>

<template>
  <main class="access-overview">
    <header>
      <h1>访问与安全</h1>
      <p>用户管理与资源组管理可从这里进入。角色、配额、凭据和审计仍是概览信息，没有独立管理页。</p>
    </header>
    <p v-if="loading">正在加载…</p>
    <p v-else-if="error" role="alert">{{ error.message }}</p>
    <template v-else>
      <aside v-if="showUserBootstrap" class="access-bootstrap" role="status">
        <p>当前还没有面板账号。请进入用户管理创建首个管理员，然后退出令牌身份，改用账号密码登录。</p>
        <RouterLink class="access-bootstrap-link" to="/access/users">前往用户管理</RouterLink>
      </aside>
      <p v-if="!cards.length" class="access-empty">当前身份无权查看用户与资源管理。</p>
      <section v-else class="access-cards" aria-label="访问与安全概览">
        <article v-for="card in cards" :key="card.id" class="access-card">
          <div class="access-card-heading">
            <RouterLink v-if="card.path" :to="card.path" class="access-card-link">{{ card.label }}</RouterLink>
            <strong v-else>{{ card.label }}</strong>
            <span v-if="card.count !== undefined">{{ card.count }}</span>
          </div>
          <QuotaUsage
            v-for="usage in card.id === 'quotas' ? quotaUsage : []"
            :key="usage.policy_id"
            :current="usage.current"
            :limit="usage.limit"
            :recovery-condition="usage.recovery_condition"
          />
        </article>
      </section>
      <section v-if="canChangeOwnPassword" class="access-security" aria-label="账号安全">
        <h2>账号安全</h2>
        <p>修改密码后，当前账号的全部会话会立即失效，需要使用新密码重新登录。</p>
        <form class="access-password-form" aria-label="修改密码" @submit.prevent="submitOwnPassword">
          <label for="access-current-password">
            当前密码
            <input
              id="access-current-password"
              v-model="passwordForm.current_password"
              type="password"
              name="current_password"
              autocomplete="current-password"
              :disabled="passwordBusy"
              :aria-invalid="fieldMessage('current_password') ? 'true' : 'false'"
              aria-describedby="access-current-password-error"
            >
          </label>
          <p v-if="fieldMessage('current_password')" id="access-current-password-error" role="alert">{{ fieldMessage('current_password') }}</p>
          <label for="access-new-password">
            新密码
            <input
              id="access-new-password"
              v-model="passwordForm.new_password"
              type="password"
              name="new_password"
              autocomplete="new-password"
              :disabled="passwordBusy"
              :aria-invalid="fieldMessage('new_password') ? 'true' : 'false'"
              aria-describedby="access-new-password-error"
            >
          </label>
          <p v-if="fieldMessage('new_password')" id="access-new-password-error" role="alert">{{ fieldMessage('new_password') }}</p>
          <label for="access-confirm-password">
            确认新密码
            <input
              id="access-confirm-password"
              v-model="passwordForm.confirm_password"
              type="password"
              name="confirm_password"
              autocomplete="new-password"
              :disabled="passwordBusy"
              :aria-invalid="fieldMessage('confirm_password') ? 'true' : 'false'"
              aria-describedby="access-confirm-password-error"
            >
          </label>
          <p v-if="fieldMessage('confirm_password')" id="access-confirm-password-error" role="alert">{{ fieldMessage('confirm_password') }}</p>
          <p v-if="passwordError" role="alert">{{ passwordError }}</p>
          <p v-if="passwordNotice" role="status">{{ passwordNotice }} <RouterLink to="/login">前往登录</RouterLink></p>
          <button type="submit" :disabled="passwordBusy">{{ passwordBusy ? '提交中…' : '更新密码' }}</button>
        </form>
      </section>
    </template>
  </main>
</template>

<style scoped>
.access-overview {
  display: grid;
  gap: 1.25rem;
}

.access-cards {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(12rem, 1fr));
  gap: 0.75rem;
}

.access-card {
  display: grid;
  gap: 0.75rem;
  border: 1px solid var(--border-color, #e5e7eb);
  border-radius: 0.75rem;
  padding: 1rem;
}

.access-card-heading {
  display: flex;
  justify-content: space-between;
}

.access-card-link {
  color: inherit;
  font-weight: 600;
  text-decoration: none;
}

.access-card-link:hover {
  color: var(--color-primary, #2563eb);
}

.access-bootstrap,
.access-security {
  display: grid;
  gap: 0.75rem;
  border: 1px solid var(--border-color, #e5e7eb);
  border-radius: 0.75rem;
  padding: 1rem;
}

.access-bootstrap-link,
.access-empty {
  color: var(--color-text-secondary, #4b5563);
}

.access-password-form {
  display: grid;
  gap: 0.65rem;
  max-width: 24rem;
}

.access-password-form label {
  display: grid;
  gap: 0.35rem;
}

.access-password-form input {
  border: 1px solid var(--border-color, #d1d5db);
  border-radius: 0.5rem;
  padding: 0.65rem 0.75rem;
}

.access-password-form button {
  justify-self: start;
}
</style>

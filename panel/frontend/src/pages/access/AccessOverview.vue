<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
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

const router = useRouter()
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
const fallbackCards = [
  { id: 'users', label: '用户', path: '/access/users' },
  { id: 'resource-groups', label: '资源组', path: '/access/resource-groups' }
]

const cards = computed(() => {
  const mapped = (visibleNavigation?.value || []).map((item) => ({
    ...item,
    path: item.path || managedPagePaths[item.id] || '',
    count: counts.value[item.id]
  })).filter((card) => card.path || (card.id === 'quotas' && quotaUsage.value.length))
  if (mapped.length) return mapped
  if (error.value) {
    return fallbackCards.map((item) => ({ ...item, count: counts.value[item.id] }))
  }
  return []
})
const showUserBootstrap = computed(() => (
  (can('access.manage') || can('*')) && counts.value.users === 0
))
const canChangeOwnPassword = computed(() => {
  const record = currentActor.value
  return !!(record?.id && !record.bootstrap)
})

onMounted(async () => {
  try {
    await refreshActor().catch((cause) => {
      error.value = { message: humanLoadError(cause) }
    })
    const requests = []
    const assign = (id, request) => requests.push(request().then((items) => { counts.value[id] = items.length }).catch(() => undefined))
    if (can('access.manage') || can('*')) {
      assign('users', fetchUsers)
      assign('roles', fetchRoles)
    }
    if (can('resource.read') || can('*')) assign('resource-groups', fetchResourceGroups)
    if (can('resource.read') || can('*')) {
      requests.push(fetchQuotaOverview().then((payload) => {
        quotaUsage.value = payload.quota_usage || []
        counts.value.quotas = quotaUsage.value.length
      }).catch(() => undefined))
    }
    if (can('secret.metadata.read') || can('*')) assign('secrets', fetchSecrets)
    if (can('audit.read') || can('*')) assign('audit', () => fetchAuditEvents(20))
    await Promise.all(requests)
  } catch (cause) {
    error.value = { message: humanLoadError(cause) }
  } finally {
    loading.value = false
  }
})

function humanLoadError(cause) {
  const raw = String(cause?.message || '').trim()
  if (/status code 5\d\d|network error|failed to fetch|502/i.test(raw)) {
    return '暂时连不上服务，页面入口仍可打开。'
  }
  return raw || '读取失败'
}

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
    await router.replace({ name: 'login' })
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
    <header class="page-header">
      <div class="page-header__left">
        <h1 class="page-title">访问与安全</h1>
        <p class="page-subtitle">管理谁能登录，以及谁能看到哪些资源组。账号密码也可以在右上角账号菜单里修改。</p>
      </div>
    </header>
    <p v-if="loading" class="access-empty">正在加载…</p>
    <p v-if="!loading && error" class="access-notice" role="status">{{ error.message }}</p>
    <template v-if="!loading">
      <aside v-if="showUserBootstrap" class="access-bootstrap" role="status">
        <p>当前还没有面板账号。请进入用户管理创建首个管理员，然后退出令牌身份，改用账号密码登录。</p>
        <RouterLink class="access-bootstrap-link" to="/access/users">前往用户管理</RouterLink>
      </aside>
      <p v-if="!cards.length" class="access-empty">当前身份无权查看用户与资源管理。</p>
      <section v-else class="access-cards" aria-label="访问与安全概览">
        <component
          :is="card.path ? 'RouterLink' : 'article'"
          v-for="card in cards"
          :key="card.id"
          class="access-card"
          :class="{ 'access-card-link': !!card.path }"
          :to="card.path || undefined"
        >
          <div class="access-card-heading">
            <strong>{{ card.label }}</strong>
            <span v-if="card.count !== undefined">{{ card.count }}</span>
          </div>
          <p v-if="card.path" class="access-card-hint">{{ card.id === 'users' ? '创建、停用和重置账号' : '授权用户并绑定资源' }}</p>
          <QuotaUsage
            v-for="usage in card.id === 'quotas' ? quotaUsage : []"
            :key="usage.policy_id"
            :current="usage.current"
            :limit="usage.limit"
            :recovery-condition="usage.recovery_condition"
          />
        </component>
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
          <button class="btn btn-primary" type="submit" :disabled="passwordBusy">{{ passwordBusy ? '提交中…' : '更新密码' }}</button>
        </form>
      </section>
    </template>
  </main>
</template>

<style scoped>
.access-overview {
  max-width: 1180px;
  margin: 0 auto;
  display: grid;
  gap: 1.1rem;
}

.access-cards {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(16rem, 1fr));
  gap: 0.75rem;
}

.access-card {
  display: grid;
  gap: 0.45rem;
  min-width: 0;
  padding: 1.05rem 1.15rem;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-2xl);
  background: var(--color-bg-surface);
  box-shadow: var(--shadow-xs);
  color: inherit;
  text-decoration: none;
  transition: border-color 150ms ease, transform 150ms ease, box-shadow 200ms ease;
}

.access-card-heading {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 0.75rem;
}

.access-card-heading span {
  color: var(--color-text-muted);
  font-variant-numeric: tabular-nums;
}

.access-card-heading strong {
  color: var(--color-text-primary);
  font-weight: 700;
}

a.access-card-link:hover {
  border-color: var(--color-primary-300, var(--color-primary));
  transform: translateY(-2px);
  box-shadow: var(--shadow-lg);
}

.access-card-hint {
  margin: 0;
  color: var(--color-text-muted);
  font-size: 0.8125rem;
}

.access-bootstrap,
.access-security {
  display: grid;
  gap: 0.75rem;
  padding: 1rem 1.1rem;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-2xl);
  background: var(--color-bg-surface);
}

.access-bootstrap-link {
  color: var(--color-primary);
  font-weight: 600;
  text-decoration: none;
}

.access-empty,
.access-notice {
  margin: 0;
  color: var(--color-text-secondary);
}

.access-notice {
  padding: 0.75rem 1rem;
  border-radius: var(--radius-lg);
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
}

.access-password-form {
  display: grid;
  gap: 0.65rem;
  max-width: 24rem;
}

.access-password-form label {
  display: grid;
  gap: 0.35rem;
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
}

.access-password-form input {
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-canvas);
  color: var(--color-text-primary);
  padding: 0.65rem 0.75rem;
  font: inherit;
}

.access-password-form .btn {
  justify-self: start;
}
</style>

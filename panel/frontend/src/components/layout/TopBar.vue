<template>
  <header class="topbar">
    <div class="topbar__left">
      <div class="topbar__brand">
        <div class="topbar__logo">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
            <polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"/>
          </svg>
        </div>
        <div class="topbar__title">
          <span class="topbar__name">Nginx Proxy</span>
          <span class="topbar__badge">管理端</span>
        </div>
      </div>
    </div>

    <div class="topbar__actions">
      <button class="topbar__action topbar__action--search" @click="$emit('open-search')" title="全局搜索 (Ctrl+K)">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <circle cx="11" cy="11" r="8"/>
          <line x1="21" y1="21" x2="16.65" y2="16.65"/>
        </svg>
      </button>

      <ThemeSelector />

      <button
        v-if="canChangeOwnPassword"
        type="button"
        class="topbar__account"
        title="账号安全"
        aria-haspopup="dialog"
        :aria-expanded="securityOpen ? 'true' : 'false'"
        @click="openSecurity"
      >
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <rect x="3" y="11" width="18" height="11" rx="2" ry="2"/>
          <path d="M7 11V7a5 5 0 0 1 10 0v4"/>
        </svg>
        <span>账号安全</span>
      </button>

      <button class="topbar__action topbar__action--logout" @click="handleLogout" title="退出登录">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/>
          <polyline points="16 17 21 12 16 7"/>
          <line x1="21" y1="12" x2="9" y2="12"/>
        </svg>
      </button>
    </div>
  </header>

  <BaseModal
    v-model="securityOpen"
    title="账号安全"
    :subtitle="securitySubtitle"
    :close-on-click-modal="!submitting"
    @update:model-value="onSecurityOpenChange"
  >
    <form class="account-security" @submit.prevent="submitPasswordChange">
      <p class="account-security__hint">修改当前账号密码。成功后全部账号会话失效，需要用新密码重新登录。</p>
      <div class="account-security__field">
        <label for="account-current-password">当前密码</label>
        <input
          id="account-current-password"
          v-model="passwordForm.current_password"
          type="password"
          autocomplete="current-password"
          :disabled="submitting"
          :aria-invalid="fieldErrors.current_password ? 'true' : 'false'"
          :aria-describedby="fieldErrors.current_password ? 'account-current-password-error' : undefined"
        >
        <p v-if="fieldErrors.current_password" id="account-current-password-error" class="account-security__field-error">
          {{ fieldErrors.current_password }}
        </p>
      </div>
      <div class="account-security__field">
        <label for="account-new-password">新密码</label>
        <input
          id="account-new-password"
          v-model="passwordForm.new_password"
          type="password"
          autocomplete="new-password"
          :disabled="submitting"
          :aria-invalid="fieldErrors.new_password ? 'true' : 'false'"
          :aria-describedby="fieldErrors.new_password ? 'account-new-password-error' : undefined"
        >
        <p v-if="fieldErrors.new_password" id="account-new-password-error" class="account-security__field-error">
          {{ fieldErrors.new_password }}
        </p>
      </div>
      <div class="account-security__field">
        <label for="account-confirm-password">确认新密码</label>
        <input
          id="account-confirm-password"
          v-model="passwordForm.confirm_password"
          type="password"
          autocomplete="new-password"
          :disabled="submitting"
          :aria-invalid="fieldErrors.confirm_password ? 'true' : 'false'"
          :aria-describedby="fieldErrors.confirm_password ? 'account-confirm-password-error' : undefined"
        >
        <p v-if="fieldErrors.confirm_password" id="account-confirm-password-error" class="account-security__field-error">
          {{ fieldErrors.confirm_password }}
        </p>
      </div>
      <p v-if="formError" class="account-security__error" role="alert">{{ formError }}</p>
      <div class="account-security__actions">
        <button type="button" class="btn btn--secondary" :disabled="submitting" @click="closeSecurity">取消</button>
        <button type="submit" class="btn btn--primary" :disabled="submitting">
          {{ submitting ? '提交中…' : '修改密码' }}
        </button>
      </div>
    </form>
  </BaseModal>
</template>

<script setup>
import { computed, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { changePassword, logout } from '../../api/access'
import { useAccessControl } from '../../context/useAccessControl'
import { useAgent } from '../../context/AgentContext'
import { useAuthState } from '../../context/useAuthState'
import BaseModal from '../base/BaseModal.vue'
import ThemeSelector from '../base/ThemeSelector.vue'

const MIN_PASSWORD_LENGTH = 10

const route = useRoute()
const router = useRouter()
const { selectedAgentId } = useAgent()
const { sessionToken } = useAuthState()
const { actor } = useAccessControl()

const securityOpen = ref(false)
const submitting = ref(false)
const formError = ref('')
const fieldErrors = ref({})
const passwordForm = reactive({
  current_password: '',
  new_password: '',
  confirm_password: ''
})

// Effective agent mirrors what the page uses: route.params.id (agent-detail) wins, then
// route.query.agentId (list pages), then context selection
const effectiveAgentId = computed(() =>
  route.params.id || route.query.agentId || selectedAgentId.value
)

const canChangeOwnPassword = computed(() => !!sessionToken.value && actor.value?.bootstrap !== true)

const securitySubtitle = computed(() => {
  const username = actor.value?.username
  return username ? `当前账号 ${username}` : '已登录账号可修改自己的密码'
})

function resetPasswordForm() {
  passwordForm.current_password = ''
  passwordForm.new_password = ''
  passwordForm.confirm_password = ''
  fieldErrors.value = {}
  formError.value = ''
}

function openSecurity() {
  resetPasswordForm()
  securityOpen.value = true
}

function closeSecurity() {
  if (submitting.value) return
  securityOpen.value = false
  resetPasswordForm()
}

function onSecurityOpenChange(open) {
  if (!open) resetPasswordForm()
}

function validatePasswordForm() {
  const next = {}
  if (!passwordForm.current_password) next.current_password = '请输入当前密码'
  if (!passwordForm.new_password) {
    next.new_password = '请输入新密码'
  } else if (passwordForm.new_password.length < MIN_PASSWORD_LENGTH) {
    next.new_password = `新密码至少 ${MIN_PASSWORD_LENGTH} 位`
  }
  if (!passwordForm.confirm_password) {
    next.confirm_password = '请再次输入新密码'
  } else if (passwordForm.confirm_password !== passwordForm.new_password) {
    next.confirm_password = '两次输入的新密码不一致'
  }
  return next
}

async function submitPasswordChange() {
  if (submitting.value) return
  const next = validatePasswordForm()
  fieldErrors.value = next
  formError.value = ''
  if (Object.keys(next).length) return

  submitting.value = true
  try {
    await changePassword({
      current_password: passwordForm.current_password,
      new_password: passwordForm.new_password
    })
    resetPasswordForm()
    securityOpen.value = false
    await router.replace({ name: 'login' })
  } catch (error) {
    fieldErrors.value = error?.fields && typeof error.fields === 'object' ? { ...error.fields } : {}
    formError.value = error?.response?.data?.message || error?.message || '修改密码失败'
  } finally {
    submitting.value = false
  }
}

async function handleLogout() {
  await logout().catch(() => undefined)
  localStorage.removeItem('selected_agent_id')
  await router.replace({ name: 'login' })
}
</script>

<style scoped>
.topbar {
  height: var(--header-height);
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 var(--space-5);
  background: var(--color-bg-surface);
  border-bottom: 1px solid var(--color-border-subtle);
  position: sticky;
  top: 0;
  z-index: var(--z-sticky);
  flex-shrink: 0;
  box-shadow: var(--shadow-xs);
}

.topbar__left { display: flex; align-items: center; gap: 1rem; }

.topbar__brand { display: flex; align-items: center; gap: 0.75rem; }

.topbar__logo {
  width: 36px;
  height: 36px;
  background: var(--color-primary);
  border-radius: var(--radius-lg);
  display: flex;
  align-items: center;
  justify-content: center;
  color: white;
  box-shadow: var(--shadow-md);
  flex-shrink: 0;
  transition: transform var(--duration-fast) var(--ease-bounce);
}

.topbar__logo:hover {
  transform: scale(1.05);
}

.topbar__title { display: flex; align-items: center; gap: 0.5rem; }

.topbar__name {
  font-size: 1rem;
  font-weight: 700;
  color: var(--color-text-primary);
  letter-spacing: -0.01em;
}

.topbar__badge {
  font-size: 0.6875rem;
  font-weight: 600;
  padding: 2px 8px;
  background: var(--color-primary);
  color: white;
  border-radius: var(--radius-full);
}

.topbar__actions { display: flex; align-items: center; gap: 0.5rem; }

.topbar__account {
  display: flex;
  align-items: center;
  gap: 0.375rem;
  height: 36px;
  padding: 0 0.75rem;
  border: none;
  border-radius: var(--radius-lg);
  background: transparent;
  color: var(--color-text-secondary);
  font: inherit;
  font-size: 0.8125rem;
  font-weight: 600;
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-default);
}

.topbar__account:hover,
.topbar__account[aria-expanded='true'] {
  color: var(--color-text-primary);
  background: var(--color-bg-hover);
}

.account-security {
  display: grid;
  gap: 0.875rem;
}

.account-security__hint {
  margin: 0;
  font-size: 0.8125rem;
  color: var(--color-text-secondary);
  line-height: 1.5;
}

.account-security__field {
  display: grid;
  gap: 0.375rem;
}

.account-security__field label {
  font-size: 0.8125rem;
  font-weight: 600;
  color: var(--color-text-primary);
}

.account-security__field input {
  width: 100%;
  box-sizing: border-box;
  padding: 10px 16px;
  border-radius: 10px;
  border: 1.5px solid var(--color-border-default);
  background: var(--color-bg-surface);
  color: var(--color-text-primary);
  font: inherit;
  font-size: 0.875rem;
}

.account-security__field input:focus {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.account-security__field input:disabled {
  opacity: 0.6;
  cursor: not-allowed;
  background: var(--color-bg-subtle);
}

.account-security__field input[aria-invalid='true'] {
  border-color: var(--color-danger);
}

.account-security__field-error,
.account-security__error {
  margin: 0;
  color: var(--color-danger);
  font-size: 0.75rem;
}

.account-security__actions {
  display: flex;
  justify-content: flex-end;
  gap: 0.5rem;
  padding-top: 0.25rem;
}

.topbar__action {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  border-radius: var(--radius-lg);
  color: var(--color-text-secondary);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-default);
  border: none;
  background: transparent;
}

.topbar__action:hover {
  color: var(--color-text-primary);
  background: var(--color-bg-hover);
}

.topbar__action--logout:hover {
  color: var(--color-danger);
  background: var(--color-danger-50);
}

@media (max-width: 640px) {
  .topbar__title { display: none; }
}
</style>

<template>
  <div class="login-page">
    <div class="login-card">
      <div class="login-card__header">
        <svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
          <rect x="3" y="11" width="18" height="11" rx="2" ry="2"/>
          <path d="M7 11V7a5 5 0 0 1 10 0v4"/>
        </svg>
        <h1 class="login-card__title">nginx-reverse-emby</h1>
        <div class="login-mode" role="group" aria-label="登录方式">
          <button type="button" :class="['login-mode__option', { 'is-active': mode === 'account' }]" @click="mode = 'account'">账号密码</button>
          <button type="button" :class="['login-mode__option', { 'is-active': mode === 'token' }]" @click="mode = 'token'">访问令牌</button>
        </div>
      </div>
      <form class="login-form" @submit.prevent="handleLogin">
        <div v-if="mode === 'account'" class="form-group">
          <label for="username-input" class="sr-only">用户名</label>
          <input
            id="username-input"
            v-model="username"
            type="text"
            class="input"
            placeholder="用户名"
            :disabled="loading"
            autocomplete="username"
          >
        </div>
        <div v-if="mode === 'account'" class="form-group">
          <label for="password-input" class="sr-only">密码</label>
          <input
            id="password-input"
            v-model="password"
            type="password"
            class="input"
            placeholder="密码"
            :disabled="loading"
            autocomplete="current-password"
          >
        </div>
        <div v-else class="form-group">
          <label for="token-input" class="sr-only">访问令牌</label>
          <input
            id="token-input"
            v-model="tokenInput"
            type="password"
            class="input"
            placeholder="输入访问令牌"
            :disabled="loading"
            autocomplete="current-password"
          >
        </div>
        <p v-if="error" class="login-error">{{ error }}</p>
        <button type="submit" class="btn btn--primary btn--full" :disabled="submitDisabled">
          <span v-if="loading" class="spinner spinner--sm"></span>
          <span v-else>连接</span>
        </button>
      </form>
    </div>
  </div>
</template>

<script setup>
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { verifyToken } from '../api'
import { login } from '../api/access'
import { useAuthState } from '../context/useAuthState'

const router = useRouter()
const { clearCredentials, setToken } = useAuthState()
const mode = ref('account')
const username = ref('')
const password = ref('')
const tokenInput = ref('')
const loading = ref(false)
const error = ref('')
const submitDisabled = computed(() => loading.value || (
  mode.value === 'account'
    ? !username.value.trim() || !password.value
    : !tokenInput.value.trim()
))

async function handleLogin() {
  if (submitDisabled.value) return

  loading.value = true
  error.value = ''

  try {
    if (mode.value === 'account') {
      await login(username.value.trim(), password.value)
    } else {
      const token = tokenInput.value.trim()
      clearCredentials()
      const valid = await verifyToken(token)
      if (!valid) {
        error.value = '令牌无效'
        return
      }
      setToken(token)
    }
    await router.push({ name: 'dashboard' })
  } catch (e) {
    error.value = e?.response?.data?.message || e.message || '登录失败'
  } finally {
    loading.value = false
  }
}
</script>

<style scoped>
.login-page {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--color-bg-canvas);
  padding: var(--space-4);
}

.login-card {
  width: 100%;
  max-width: 360px;
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-2xl);
  padding: var(--space-8);
  box-shadow: var(--shadow-xl);
}

.login-card__header {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-3);
  margin-bottom: var(--space-6);
  color: var(--color-primary);
}

.login-card__title {
  font-size: var(--text-xl);
  font-weight: var(--font-bold);
  color: var(--color-text-primary);
  margin: 0;
}

.login-card__subtitle {
  font-size: var(--text-sm);
  color: var(--color-text-muted);
  margin: 0;
}

.login-mode {
  display: grid;
  grid-template-columns: 1fr 1fr;
  width: 100%;
  padding: 3px;
  border-radius: var(--radius-lg);
  background: var(--color-bg-subtle);
  border: 1px solid var(--color-border-default);
}

.login-mode__option {
  min-height: 34px;
  border: 0;
  border-radius: var(--radius-md);
  background: transparent;
  color: var(--color-text-muted);
  cursor: pointer;
  font: inherit;
  font-size: var(--text-sm);
}

.login-mode__option.is-active {
  background: var(--color-bg-surface);
  color: var(--color-text-primary);
  box-shadow: var(--shadow-xs);
}

.login-form {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.form-group {
  display: flex;
  flex-direction: column;
}

.input {
  width: 100%;
  padding: var(--space-3) var(--space-4);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  background: var(--color-bg-subtle);
  font-size: var(--text-sm);
  color: var(--color-text-primary);
  outline: none;
  font-family: inherit;
  box-sizing: border-box;
  transition: border-color var(--duration-fast);
}

.input:focus {
  border-color: var(--color-primary);
}

.input::placeholder {
  color: var(--color-text-muted);
}

.input:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.login-error {
  font-size: var(--text-sm);
  color: var(--color-danger);
  background: var(--color-danger-50);
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-md);
  margin: 0;
}

.btn--full {
  width: 100%;
}

.spinner {
  width: 20px;
  height: 20px;
  border: 2px solid rgba(255, 255, 255, 0.3);
  border-top-color: white;
  border-radius: 50%;
  animation: spin 1s linear infinite;
}

.spinner--sm {
  width: 16px;
  height: 16px;
  border-width: 1.5px;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  margin: -1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
  border: 0;
}
</style>

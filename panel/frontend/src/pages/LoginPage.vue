<template>
  <div class="login-page">
    <SakuraBackdrop />
    <div class="login-card">
      <div class="login-card__header">
        <div class="login-card__mark"><BrandMark fallback="lock" /></div>
        <h1 class="login-card__title">Nginx Proxy</h1>
        <p class="login-card__subtitle">使用访问令牌进入管理端</p>
      </div>
      <form class="login-form" @submit.prevent="handleLogin">
        <div class="form-group">
          <label for="token-input" class="sr-only">访问令牌</label>
          <div class="input-wrap">
            <input
              id="token-input"
              v-model="tokenInput"
              :type="revealToken ? 'text' : 'password'"
              class="input"
              :class="{ 'input--error': error }"
              placeholder="输入访问令牌"
              :disabled="loading"
              autocomplete="current-password"
              autofocus
              :aria-invalid="error ? 'true' : undefined"
              aria-describedby="login-error"
            >
            <button
              type="button"
              class="reveal-btn"
              :aria-pressed="revealToken"
              :aria-label="revealToken ? '隐藏访问令牌' : '显示访问令牌'"
              @click="revealToken = !revealToken"
            >
              {{ revealToken ? '隐藏' : '显示' }}
            </button>
          </div>
        </div>
        <FieldError v-if="error" id="login-error" class="login-error">{{ error }}</FieldError>
        <button type="submit" class="btn btn--primary btn--full" :disabled="loading">
          <span v-if="loading" class="spinner spinner--sm"></span>
          <span v-else>连接</span>
        </button>
      </form>
      <div class="token-help">
        <button
          type="button"
          class="token-help__toggle"
          :aria-expanded="showTokenHelp ? 'true' : 'false'"
          aria-controls="token-help-panel"
          @click="showTokenHelp = !showTokenHelp"
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <circle cx="12" cy="12" r="10"/><path d="M9.09 9a3 3 0 0 1 5.83 1c0 2-3 3-3 3"/><line x1="12" y1="17" x2="12.01" y2="17"/>
          </svg>
          <span>访问令牌从哪里获取？</span>
        </button>
        <div v-if="showTokenHelp" id="token-help-panel" class="token-help__panel">
          <ul>
            <li>一键部署脚本执行完成后，会在终端输出访问令牌。</li>
            <li>也可以在安装目录的 <code>.env</code> 文件中查看 <code>API_TOKEN</code> 的值。</li>
          </ul>
          <a class="token-help__docs" :href="DOC_LINKS.deploy" target="_blank" rel="noopener">查看部署指南</a>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { verifyToken } from '../api'
import { useAuthState } from '../context/useAuthState'
import BrandMark from '../components/base/BrandMark.vue'
import SakuraBackdrop from '../components/base/SakuraBackdrop.vue'
import FieldError from '../components/base/FieldError.vue'
import { DOC_LINKS } from '../constants/docLinks'

const router = useRouter()
const route = useRoute()
const { clearCredentials, setToken } = useAuthState()
const tokenInput = ref('')
const revealToken = ref(false)
const showTokenHelp = ref(false)
const loading = ref(false)
const error = ref('')

function safeReturnPath(value) {
  if (typeof value !== 'string' || !value.startsWith('/') || value.startsWith('//')) {
    return null
  }
  return value
}

async function handleLogin() {
  if (loading.value) return

  const token = tokenInput.value.trim()
  error.value = ''
  if (!token) {
    error.value = '请输入访问令牌'
    return
  }

  loading.value = true

  try {
    clearCredentials()
    const valid = await verifyToken(token)
    if (!valid) {
      error.value = '令牌无效'
      return
    }
    setToken(token)
    const next = safeReturnPath(typeof route.query.return === 'string' ? route.query.return : '')
    if (next && next.startsWith('/panel-api/')) {
      window.location.assign(next)
      return
    }
    await router.push(next || { name: 'dashboard' })
  } catch (e) {
    error.value = e?.response?.data?.message || e.message || '登录失败'
  } finally {
    loading.value = false
  }
}
</script>

<style scoped>
.login-page {
  position: relative;
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--color-bg-atmosphere, var(--color-bg-canvas));
  padding: var(--space-4);
}

.login-card {
  position: relative;
  width: 100%;
  max-width: 360px;
  background: var(--color-bg-panel, var(--color-bg-surface));
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

.login-card__mark {
  width: 56px;
  height: 56px;
  padding: 8px;
  box-sizing: border-box;
  border-radius: var(--radius-xl);
  background: var(--color-brand-bg, transparent);
  color: var(--color-brand-ink, var(--color-primary));
  box-shadow: var(--shadow-sm);
}

.login-card__title {
  font-size: var(--text-xl);
  font-weight: var(--font-bold);
  color: var(--color-text-primary);
  margin: 0;
}

.login-card__subtitle {
  font-size: var(--text-sm);
  color: var(--color-text-secondary);
  margin: 0;
  text-align: center;
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

.input-wrap {
  position: relative;
}

.input {
  width: 100%;
  padding: var(--space-3) 4.5rem var(--space-3) var(--space-4);
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
  color: var(--color-text-secondary);
  opacity: 1;
}

.reveal-btn {
  position: absolute;
  top: 50%;
  right: var(--space-2);
  transform: translateY(-50%);
  border: 0;
  background: transparent;
  color: var(--color-text-secondary);
  font: inherit;
  font-size: var(--text-xs);
  font-weight: var(--font-semibold);
  padding: var(--space-1) var(--space-2);
  border-radius: var(--radius-md);
  cursor: pointer;
}

.reveal-btn:hover {
  color: var(--color-text-primary);
  background: var(--color-bg-hover);
}

.input:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.input--error {
  border-color: var(--color-danger);
}

.input--error:focus {
  border-color: var(--color-danger);
  box-shadow: 0 0 0 3px var(--color-danger-50);
}

.login-error {
  font-size: var(--text-sm);
  font-weight: var(--font-semibold);
  color: var(--color-danger);
  background: var(--color-danger-50);
  border: 1px solid color-mix(in srgb, var(--color-danger) 35%, transparent);
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-md);
  margin: 0;
  animation: errorShake var(--duration-slow) var(--ease-default);
}

.btn--full {
  width: 100%;
}

.token-help {
  margin-top: var(--space-5);
  padding-top: var(--space-4);
  border-top: 1px solid var(--color-border-default);
}

.token-help__toggle {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--color-text-secondary);
  font: inherit;
  font-size: var(--text-xs);
  font-weight: var(--font-semibold);
  cursor: pointer;
}

.token-help__toggle:hover {
  color: var(--color-text-primary);
}

.token-help__toggle svg {
  flex-shrink: 0;
}

.token-help__panel {
  margin-top: var(--space-3);
  padding: var(--space-3);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  background: var(--color-bg-subtle);
  color: var(--color-text-secondary);
  font-size: var(--text-xs);
  line-height: 1.6;
}

.token-help__panel ul {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  margin: 0;
  padding-left: 1.1rem;
}

.token-help__panel code {
  font-family: var(--font-mono, ui-monospace, SFMono-Regular, Menlo, monospace);
  font-size: 0.95em;
  color: var(--color-text-primary);
}

.token-help__docs {
  display: inline-block;
  margin-top: var(--space-2);
  color: var(--color-primary);
  font-weight: var(--font-semibold);
  text-decoration: none;
}

.token-help__docs:hover {
  text-decoration: underline;
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

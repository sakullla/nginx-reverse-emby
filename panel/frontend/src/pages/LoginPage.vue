<template>
  <div class="login-page">
    <aside class="login-hero" aria-hidden="true">
      <div class="login-hero__grid"></div>
      <div class="login-hero__glow"></div>
      <div class="login-hero__inner">
        <div class="login-hero__brand">
          <span class="login-hero__logo"><BrandMark /></span>
          <span class="login-hero__name">Nginx Proxy</span>
        </div>
        <div class="login-hero__copy">
          <p class="login-hero__headline">一个面板，<br>管理全部反向代理。</p>
          <p class="login-hero__lede">HTTP 与 L4 转发、自动证书、多节点下发与流量统计，集中在一处完成。</p>
        </div>
        <ul class="login-hero__features">
          <li>
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3a14 14 0 0 1 0 18M12 3a14 14 0 0 0 0 18"/></svg>
            HTTP / L4 规则即时生效
          </li>
          <li>
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3 4.5 6v5.5c0 4.6 3.2 8.4 7.5 9.5 4.3-1.1 7.5-4.9 7.5-9.5V6L12 3z"/><path d="m9 12 2 2 4-4"/></svg>
            证书自动签发与续期
          </li>
          <li>
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="3" width="20" height="14" rx="2"/><path d="M8 21h8M12 17v4"/></svg>
            多节点统一下发与监控
          </li>
        </ul>
      </div>
    </aside>

    <main class="login-main">
      <div class="login-card">
        <div class="login-card__header">
          <div class="login-card__mark"><BrandMark /></div>
          <h1 class="login-card__title">登录 Nginx Proxy</h1>
          <p class="login-card__subtitle">输入访问令牌以进入控制台</p>
        </div>
        <form class="login-form" @submit.prevent="handleLogin">
          <div class="form-group">
            <label for="token-input" class="login-label">访问令牌</label>
            <div class="input-wrap">
              <svg class="input-wrap__icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <circle cx="7.5" cy="15.5" r="4.5"/><path d="m10.7 12.3 9.3-9.3M16 7l3 3M14 9l2 2"/>
              </svg>
              <input
                id="token-input"
                v-model="tokenInput"
                :type="revealToken ? 'text' : 'password'"
                class="input"
                :class="{ 'input--error': error }"
                placeholder="粘贴 API_TOKEN"
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
          <button type="submit" class="btn btn--primary btn--lg btn--full" :disabled="loading">
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
            <a class="token-help__docs" :href="DOC_LINKS.deploy" target="_blank" rel="noopener">查看部署指南 →</a>
          </div>
        </div>
      </div>
    </main>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { verifyToken } from '../api'
import { useAuthState } from '../context/useAuthState'
import BrandMark from '../components/base/BrandMark.vue'
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
  min-height: 100dvh;
  display: grid;
  grid-template-columns: minmax(0, 1.05fr) minmax(0, 1fr);
  background: var(--color-bg-canvas);
}

/* ---------- Hero ---------- */
.login-hero {
  position: relative;
  overflow: hidden;
  margin: var(--space-3);
  border-radius: var(--radius-3xl);
  background: #0b0b14;
  color: #f4f4f8;
  isolation: isolate;
}

.login-hero__grid {
  position: absolute;
  inset: 0;
  background-image:
    linear-gradient(rgb(255 255 255 / 0.05) 1px, transparent 1px),
    linear-gradient(90deg, rgb(255 255 255 / 0.05) 1px, transparent 1px);
  background-size: 44px 44px;
  -webkit-mask-image: radial-gradient(ellipse 80% 70% at 30% 40%, #000 20%, transparent 75%);
  mask-image: radial-gradient(ellipse 80% 70% at 30% 40%, #000 20%, transparent 75%);
  z-index: -1;
}

.login-hero__glow {
  position: absolute;
  inset: -20% -10% auto auto;
  width: 80%;
  height: 90%;
  background:
    radial-gradient(closest-side, rgb(109 106 245 / 0.55), transparent),
    radial-gradient(closest-side at 70% 70%, rgb(34 184 207 / 0.35), transparent);
  filter: blur(40px);
  z-index: -1;
}

.login-hero__inner {
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  gap: var(--space-10);
  height: 100%;
  padding: clamp(2rem, 4vw, 3.5rem);
  box-sizing: border-box;
}

.login-hero__brand {
  display: flex;
  align-items: center;
  gap: var(--space-3);
}

.login-hero__logo {
  display: flex;
  width: 36px;
  height: 36px;
  border-radius: var(--radius-lg);
  background: var(--color-brand-bg);
  color: #fff;
  box-shadow: inset 0 1px 0 rgb(255 255 255 / 0.3), 0 8px 24px -6px rgb(109 106 245 / 0.6);
}

.login-hero__name {
  font-size: var(--text-lg);
  font-weight: var(--font-semibold);
  letter-spacing: -0.02em;
}

.login-hero__headline {
  margin: 0;
  font-size: clamp(1.9rem, 3.2vw, 2.75rem);
  font-weight: var(--font-bold);
  line-height: 1.15;
  letter-spacing: -0.035em;
  background: linear-gradient(180deg, #ffffff 30%, rgb(255 255 255 / 0.6));
  -webkit-background-clip: text;
  background-clip: text;
  color: transparent;
}

.login-hero__lede {
  max-width: 28rem;
  margin: var(--space-4) 0 0;
  color: rgb(244 244 248 / 0.66);
  font-size: var(--text-base);
  line-height: 1.65;
}

.login-hero__features {
  display: grid;
  gap: var(--space-3);
  margin: 0;
  padding: 0;
  list-style: none;
  color: rgb(244 244 248 / 0.82);
  font-size: var(--text-sm);
}

.login-hero__features li {
  display: flex;
  align-items: center;
  gap: var(--space-3);
}

.login-hero__features svg {
  padding: 7px;
  box-sizing: content-box;
  border-radius: var(--radius-md);
  background: rgb(255 255 255 / 0.06);
  border: 1px solid rgb(255 255 255 / 0.08);
  color: #b9b7ff;
}

/* ---------- Form side ---------- */
.login-main {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--space-8) var(--space-4);
}

.login-card {
  width: 100%;
  max-width: 360px;
}

.login-card__header {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--space-2);
  margin-bottom: var(--space-8);
}

.login-card__mark {
  display: none;
  width: 44px;
  height: 44px;
  margin-bottom: var(--space-3);
  border-radius: var(--radius-lg);
  background: var(--color-brand-bg);
  color: var(--color-brand-ink);
  box-shadow: inset 0 1px 0 rgb(255 255 255 / 0.25), var(--shadow-md);
}

.login-card__title {
  margin: 0;
  font-size: var(--text-2xl);
  font-weight: var(--font-bold);
  letter-spacing: -0.03em;
  color: var(--color-text-primary);
}

.login-card__subtitle {
  margin: 0;
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
}

.login-form {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.form-group {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.login-label {
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  color: var(--color-text-secondary);
}

.input-wrap {
  position: relative;
}

.input-wrap__icon {
  position: absolute;
  top: 50%;
  left: var(--space-3);
  transform: translateY(-50%);
  color: var(--color-text-muted);
  pointer-events: none;
}

.input {
  width: 100%;
  height: var(--control-height-lg);
  padding: 0 4rem 0 2.5rem;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-surface);
  box-shadow: var(--shadow-xs);
  font-size: var(--text-sm);
  color: var(--color-text-primary);
  outline: none;
  font-family: inherit;
  box-sizing: border-box;
  transition: border-color var(--duration-fast), box-shadow var(--duration-fast);
}

.input:focus {
  border-color: var(--color-border-focus);
  box-shadow: var(--shadow-focus);
}

.input::placeholder {
  color: var(--color-text-muted);
  opacity: 1;
}

.reveal-btn {
  position: absolute;
  top: 50%;
  right: var(--space-1-5);
  transform: translateY(-50%);
  border: 0;
  background: transparent;
  color: var(--color-text-tertiary);
  font: inherit;
  font-size: var(--text-xs);
  font-weight: var(--font-medium);
  padding: var(--space-1) var(--space-2);
  border-radius: var(--radius-sm);
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

.input--error,
.input--error:focus {
  border-color: var(--color-danger);
}

.input--error:focus {
  box-shadow: var(--shadow-focus-danger);
}

.login-error {
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  color: var(--color-danger);
  background: var(--color-danger-50);
  border: 1px solid color-mix(in srgb, var(--color-danger) 25%, transparent);
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-md);
  margin: 0;
  animation: errorShake var(--duration-slow) var(--ease-default);
}

.btn--full {
  width: 100%;
}

.token-help {
  margin-top: var(--space-6);
}

.token-help__toggle {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--color-text-tertiary);
  font: inherit;
  font-size: var(--text-xs);
  font-weight: var(--font-medium);
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
  padding: var(--space-3) var(--space-4);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  background: var(--color-bg-surface);
  color: var(--color-text-secondary);
  font-size: var(--text-xs);
  line-height: 1.7;
}

.token-help__panel ul {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  margin: 0;
  padding-left: 1.1rem;
}

.token-help__panel code {
  font-family: var(--font-mono);
  font-size: 0.95em;
  padding: 0 0.3em;
  border-radius: var(--radius-xs);
  background: var(--color-bg-subtle);
  color: var(--color-text-primary);
}

.token-help__docs {
  display: inline-block;
  margin-top: var(--space-2);
  color: var(--color-primary);
  font-weight: var(--font-medium);
  text-decoration: none;
}

.token-help__docs:hover {
  text-decoration: underline;
}

.spinner {
  width: var(--space-5);
  height: var(--space-5);
  border: 2px solid color-mix(in srgb, var(--color-text-inverse) 30%, transparent);
  border-top-color: var(--color-text-inverse);
  border-radius: 50%;
  animation: spin 1s linear infinite;
}

.spinner--sm {
  width: var(--space-4);
  height: var(--space-4);
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

@media (max-width: 900px) {
  .login-page {
    grid-template-columns: 1fr;
    background: var(--color-bg-atmosphere);
  }

  .login-hero {
    display: none;
  }

  .login-card__header {
    align-items: center;
    text-align: center;
  }

  .login-card__mark {
    display: flex;
  }
}
</style>

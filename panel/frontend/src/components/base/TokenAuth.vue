<template>
  <div class="auth-page">
    <div class="auth-card">
      <div class="auth-card__header">
        <div class="auth-logo">
          <div class="auth-logo__icon">
            <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"/>
            </svg>
          </div>
          <h1 class="auth-logo__title">Nginx Proxy</h1>
          <p class="auth-logo__subtitle">集群控制台</p>
        </div>
      </div>

      <form class="auth-form" @submit.prevent="handleLogin">
        <div class="form-group">
          <label class="form-label">访问令牌</label>
          <div class="input-wrapper">
            <span class="input-wrapper__icon">
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <rect x="3" y="11" width="18" height="11" rx="2" ry="2"/>
                <path d="M7 11V7a5 5 0 0 1 10 0v4"/>
              </svg>
            </span>
            <input
              v-model="token"
              type="password"
              class="input"
              :class="{ 'input--error': error }"
              placeholder="请输入访问令牌"
              autofocus
              @input="error = ''"
            >
          </div>
          <p v-if="error" class="form-error">{{ error }}</p>
        </div>

        <button 
          type="submit" 
          class="btn btn--primary btn--full btn--lg"
          :disabled="loading"
        >
          <span v-if="loading" class="spinner spinner--sm"></span>
          <span v-else>登录</span>
        </button>
      </form>

      <div class="auth-card__footer">
        <p class="auth-hint">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <circle cx="12" cy="12" r="10"/>
            <line x1="12" y1="16" x2="12" y2="12"/>
            <line x1="12" y1="8" x2="12.01" y2="8"/>
          </svg>
          令牌由管理员配置，请联系管理员获取
        </p>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import { useRuleStore } from '../../stores/rules'

const ruleStore = useRuleStore()

const token = ref('')
const error = ref('')
const loading = ref(false)

const handleLogin = async () => {
  if (!token.value.trim()) {
    error.value = '请输入访问令牌'
    return
  }

  loading.value = true
  error.value = ''

  try {
    const success = await ruleStore.login(token.value.trim())
    if (!success) {
      error.value = '令牌无效，请检查后重试'
    }
  } catch (err) {
    error.value = '登录失败，请稍后重试'
  } finally {
    loading.value = false
  }
}
</script>

<style scoped>
.auth-page {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--space-6);
  background: var(--theme-bg);
  background-attachment: fixed;
  position: relative;
}

.auth-page::before {
  content: '';
  position: fixed;
  inset: 0;
  background: radial-gradient(ellipse at 25% 25%, rgba(192, 132, 252, 0.08) 0%, transparent 50%),
              radial-gradient(ellipse at 75% 75%, rgba(244, 114, 182, 0.06) 0%, transparent 50%);
  opacity: var(--theme-decorator-opacity, 0.5);
  animation: sparkle 6s ease-in-out infinite alternate;
  pointer-events: none;
}

@keyframes sparkle {
  0% { opacity: 0.3; }
  50% { opacity: 0.6; }
  100% { opacity: 0.3; }
}

.auth-card {
  width: 100%;
  max-width: 420px;
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-3xl);
  box-shadow: var(--shadow-2xl);
  overflow: hidden;
  backdrop-filter: blur(20px);
  animation: scaleIn 0.5s var(--ease-bounce);
  position: relative;
  z-index: 1;
}

.auth-card__header {
  padding: var(--space-10) var(--space-6) var(--space-5);
  text-align: center;
}

.auth-logo {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-4);
}

.auth-logo__icon {
  width: 72px;
  height: 72px;
  background: var(--gradient-primary);
  border-radius: var(--radius-2xl);
  display: flex;
  align-items: center;
  justify-content: center;
  color: white;
  box-shadow: var(--shadow-glow);
  animation: float 4s ease-in-out infinite;
}

.auth-logo__title {
  font-size: var(--text-2xl);
  font-weight: var(--font-bold);
  color: var(--color-text-primary);
  margin: 0;
  background: var(--gradient-primary);
  -webkit-background-clip: text;
  -webkit-text-fill-color: transparent;
  background-clip: text;
}

.auth-logo__subtitle {
  font-size: var(--text-sm);
  color: var(--color-text-secondary);
  margin: 0;
}

.auth-form {
  padding: var(--space-4) var(--space-6) var(--space-6);
  display: flex;
  flex-direction: column;
  gap: var(--space-5);
}

.auth-card__footer {
  padding: var(--space-4) var(--space-6);
  background: var(--gradient-soft);
  border-top: 1px solid var(--color-border-subtle);
}

.auth-hint {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  font-size: var(--text-xs);
  color: var(--color-text-tertiary);
  margin: 0;
}
</style>

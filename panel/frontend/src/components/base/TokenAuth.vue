<template>
  <div class="auth-overlay">
    <div class="auth-card">
      <div class="auth-header">
        <div class="auth-icon-bg">
          <svg viewBox="0 0 24 24"><rect x="3" y="11" width="18" height="11" rx="2" ry="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg>
        </div>
        <h1 class="auth-title">面板鉴权</h1>
        <p class="auth-subtitle">请输入 API 令牌以解锁管理权限</p>
      </div>

      <form @submit.prevent="handleLogin" class="auth-form" novalidate>
        <div class="form-group">
          <label class="form-label">API 令牌</label>
          <div class="input-wrapper" :class="{ 'has-error': showError }">
            <span class="input-icon">
              <svg viewBox="0 0 24 24"><path d="M21 2l-2 2m-7.61 7.61a5.5 5.5 0 1 1-7.778 7.778 5.5 5.5 0 0 1 7.778-7.778zm0 0L15.5 7.5m0 0l3 3L22 7l-3-3L15.5 7.5z"/></svg>
            </span>
            <input
              v-model="inputToken"
              type="password"
              placeholder="输入您的访问令牌..."
              autocomplete="current-password"
              @input="showError = false"
              :disabled="loading"
            />
            <transition name="fade">
              <div v-if="showError" class="error-tip">令牌不能为空</div>
            </transition>
          </div>
        </div>

        <button type="submit" :disabled="loading" class="auth-submit-btn primary">
          <span v-if="!loading" class="btn-content">
            验证并进入
            <svg class="btn-arrow" viewBox="0 0 24 24"><line x1="5" y1="12" x2="19" y2="12"/><polyline points="12 5 19 12 12 19"/></svg>
          </span>
          <span v-else class="loading-spinner"></span>
        </button>
      </form>

      <div class="auth-footer">
        <div class="footer-divider"></div>
        <p class="footer-note">
          <svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="10"/><line x1="12" y1="16" x2="12" y2="12"/><line x1="12" y1="8" x2="12.01" y2="8"/></svg>
          令牌由系统环境变量 <code>API_TOKEN</code> 配置
        </p>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import { useRuleStore } from '../../stores/rules'

const ruleStore = useRuleStore()
const inputToken = ref('')
const loading = ref(false)
const showError = ref(false)

const handleLogin = async () => {
  if (!inputToken.value.trim()) {
    showError.value = true
    return
  }

  loading.value = true
  try {
    await ruleStore.login(inputToken.value)
  } catch (err) {
    // 错误已由 store 处理
  } finally {
    loading.value = false
  }
}
</script>

<style scoped>
.auth-overlay {
  position: fixed;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: var(--z-modal-backdrop, 1000);
  background: var(--gradient-bg);
  padding: var(--spacing-md);
  backdrop-filter: blur(10px);
}

.auth-card {
  width: 100%;
  max-width: 420px;
  background: var(--color-bg-card);
  padding: var(--spacing-3xl) var(--spacing-2xl);
  border-radius: var(--radius-2xl);
  border: 1px solid var(--glass-border);
  box-shadow: 0 20px 50px rgba(0, 0, 0, 0.1);
  animation: auth-appear 0.6s cubic-bezier(0.16, 1, 0.3, 1);
}

@keyframes auth-appear {
  from { opacity: 0; transform: translateY(20px); }
  to { opacity: 1; transform: translateY(0); }
}

.auth-header {
  text-align: center;
  margin-bottom: var(--spacing-2xl);
}

.auth-icon-bg {
  width: 64px;
  height: 64px;
  background: var(--color-primary-bg);
  color: var(--color-primary);
  border-radius: 20px;
  display: flex;
  align-items: center;
  justify-content: center;
  margin: 0 auto var(--spacing-lg);
  box-shadow: 0 8px 16px rgba(37, 99, 235, 0.1);
}

.auth-icon-bg svg {
  width: 32px;
  height: 32px;
  stroke: currentColor;
  stroke-width: 2;
  fill: none;
}

.auth-title {
  font-size: 1.75rem;
  font-weight: 800;
  margin: 0;
  letter-spacing: -0.02em;
  background: var(--gradient-header);
  -webkit-background-clip: text;
  -webkit-text-fill-color: transparent;
}

.auth-subtitle {
  color: var(--color-text-secondary);
  font-size: 0.95rem;
  margin-top: 8px;
  font-weight: 500;
}

.auth-form {
  display: flex;
  flex-direction: column;
  gap: var(--spacing-xl);
}

.form-group {
  display: flex;
  flex-direction: column;
  gap: var(--spacing-xs);
}

.form-label {
  font-size: 0.8rem;
  font-weight: 700;
  color: var(--color-text-tertiary);
  text-transform: uppercase;
  letter-spacing: 0.05em;
  margin-left: 4px;
}

.input-wrapper {
  position: relative;
}

.input-icon {
  position: absolute;
  left: var(--spacing-md);
  top: 50%;
  transform: translateY(-50%);
  width: 18px;
  height: 18px;
  color: var(--color-text-muted);
  pointer-events: none;
  z-index: 1;
}

.input-icon svg {
  width: 100%;
  height: 100%;
  stroke: currentColor;
  stroke-width: 2.2;
  fill: none;
}

.auth-form input {
  width: 100%;
  height: 52px;
  padding-left: calc(var(--spacing-md) * 3) !important;
  background: var(--color-bg-secondary) !important;
  border: 1px solid var(--color-border) !important;
  border-radius: var(--radius-lg) !important;
  font-size: var(--font-size-base) !important;
  transition: all var(--transition-base) !important;
}

.auth-form input:focus {
  background: var(--color-bg-primary) !important;
  border-color: var(--color-primary) !important;
  box-shadow: 0 0 0 4px var(--color-primary-lighter) !important;
}

.input-wrapper.has-error input {
  border-color: var(--color-danger) !important;
  background: var(--color-danger-bg) !important;
  animation: shake 0.4s cubic-bezier(.36,.07,.19,.97) both;
}

.error-tip {
  position: absolute;
  top: calc(100% + 4px);
  left: 4px;
  color: var(--color-danger);
  font-size: 0.75rem;
  font-weight: 600;
}

.auth-submit-btn {
  height: 52px;
  width: 100%;
  border-radius: var(--radius-lg);
  font-size: 1rem;
  font-weight: 700;
  letter-spacing: 0.02em;
  box-shadow: 0 4px 12px rgba(37, 99, 235, 0.2);
}

.btn-content {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
}

.btn-arrow {
  width: 18px;
  height: 18px;
  stroke: currentColor;
  stroke-width: 2.5;
  fill: none;
  transition: transform var(--transition-base);
}

.auth-submit-btn:hover .btn-arrow {
  transform: translateX(4px);
}

.auth-footer {
  margin-top: var(--spacing-2xl);
}

.footer-divider {
  height: 1px;
  background: linear-gradient(to right, transparent, var(--color-border), transparent);
  margin-bottom: var(--spacing-lg);
  opacity: 0.5;
}

.footer-note {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  font-size: 0.75rem;
  color: var(--color-text-muted);
  font-weight: 500;
}

.footer-note svg {
  width: 14px;
  height: 14px;
  stroke: currentColor;
  stroke-width: 2.2;
  fill: none;
}

code {
  background: var(--color-bg-secondary);
  padding: 2px 6px;
  border-radius: 4px;
  font-family: var(--font-family-mono);
  color: var(--color-primary);
  font-weight: 600;
}

@keyframes shake {
  10%, 90% { transform: translate3d(-1px, 0, 0); }
  20%, 80% { transform: translate3d(2px, 0, 0); }
  30%, 50%, 70% { transform: translate3d(-4px, 0, 0); }
  40%, 60% { transform: translate3d(4px, 0, 0); }
}

.loading-spinner {
  width: 24px;
  height: 24px;
  border: 3px solid rgba(255, 255, 255, 0.2);
  border-top-color: white;
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

.fade-enter-active, .fade-leave-active { transition: opacity 0.2s; }
.fade-enter-from, .fade-leave-to { opacity: 0; }

@media (max-width: 480px) {
  .auth-card {
    padding: var(--spacing-xl) var(--spacing-lg);
  }
}
</style>

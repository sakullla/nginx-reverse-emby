<template>
  <div class="auth-container">
    <div class="auth-card glass">
      <div class="auth-header">
        <div class="auth-icon" v-html="icons.lock"></div>
        <h1>面板鉴权</h1>
        <p>请输入访问令牌以继续</p>
      </div>

      <form @submit.prevent="handleLogin" class="auth-form" novalidate>
        <div class="input-wrapper" :class="{ 'has-error': showError }">
          <input
            v-model="inputToken"
            type="password"
            placeholder="输入 API_TOKEN"
            autoautocomplete="current-password"
            @input="showError = false"
          />
          <!-- 自定义主题化错误提示 -->
          <transition name="fade">
            <div v-if="showError" class="error-tip">
              <span class="icon-error">!</span>
              请填写此字段
            </div>
          </transition>
        </div>

        <button type="submit" :disabled="loading" class="login-button primary">
          <span v-if="!loading">验证令牌</span>
          <span v-else class="loading-mini"></span>
        </button>
      </form>

      <div class="auth-footer">
        <p>令牌由环境变量 <code>API_TOKEN</code> 定义</p>
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

const icons = {
  lock: '<svg viewBox="0 0 24 24"><rect x="3" y="11" width="18" height="11" rx="2" ry="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg>'
}

const handleLogin = async () => {
  if (!inputToken.value.trim()) {
    showError.value = true
    return
  }

  loading.value = true
  try {
    await ruleStore.login(inputToken.value)
  } catch (err) {
    // 登录失败的逻辑已经在 store 中通过全局消息处理
  } finally {
    loading.value = false
  }
}
</script>

<style scoped>
.auth-container {
  position: fixed;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: var(--z-modal);
  background: var(--gradient-bg);
  padding: var(--spacing-md);
}

.auth-card {
  width: 100%;
  max-width: 400px;
  padding: var(--spacing-2xl);
  border-radius: var(--radius-xl);
  text-align: center;
  border: 1px solid var(--glass-border);
  box-shadow: var(--glass-shadow);
}

.auth-header {
  margin-bottom: var(--spacing-xl);
}

.auth-icon {
  width: 64px;
  height: 64px;
  margin: 0 auto var(--spacing-md);
  color: var(--color-primary);
}

.auth-icon :deep(svg) {
  width: 100%;
  height: 100%;
  stroke: currentColor;
  stroke-width: 1.5;
  fill: none;
}

.auth-header h1 {
  font-size: var(--font-size-2xl);
  margin-bottom: var(--spacing-xs);
  background: var(--gradient-header);
  -webkit-background-clip: text;
  -webkit-text-fill-color: transparent;
}

.auth-header p {
  color: var(--color-text-secondary);
  font-size: var(--font-size-sm);
}

.auth-form {
  display: flex;
  flex-direction: column;
  gap: var(--spacing-lg);
}

.input-wrapper {
  position: relative;
  text-align: left;
}

.input-wrapper.has-error input {
  border-color: var(--color-danger);
  background: var(--color-danger-bg);
  animation: shake 0.4s cubic-bezier(.36,.07,.19,.97) both;
}

.error-tip {
  position: absolute;
  top: calc(100% + 4px);
  left: 4px;
  color: var(--color-danger);
  font-size: 0.75rem;
  font-weight: var(--font-weight-medium);
  display: flex;
  align-items: center;
  gap: 4px;
}

.icon-error {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 14px;
  height: 14px;
  background: var(--color-danger);
  color: white;
  border-radius: 50%;
  font-size: 10px;
  font-weight: bold;
}

.login-button {
  height: 46px;
  width: 100%;
  margin-top: var(--spacing-xs);
}

.auth-footer {
  margin-top: var(--spacing-2xl);
  font-size: var(--font-size-xs);
  color: var(--color-text-muted);
}

code {
  background: var(--color-bg-secondary);
  padding: 2px 4px;
  border-radius: var(--radius-xs);
  font-family: var(--font-family-mono);
}

@keyframes shake {
  10%, 90% { transform: translate3d(-1px, 0, 0); }
  20%, 80% { transform: translate3d(2px, 0, 0); }
  30%, 50%, 70% { transform: translate3d(-4px, 0, 0); }
  40%, 60% { transform: translate3d(4px, 0, 0); }
}

.fade-enter-active, .fade-leave-active { transition: opacity 0.2s; }
.fade-enter-from, .fade-leave-to { opacity: 0; }

.loading-mini {
  width: 20px;
  height: 20px;
  border: 2px solid rgba(255,255,255,0.3);
  border-top-color: #fff;
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}
</style>

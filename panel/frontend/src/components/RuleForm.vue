<template>
  <form @submit.prevent="handleSubmit" class="rule-form-inline" novalidate>
    <div class="input-wrapper" :class="{ 'has-error': errors.frontend }">
      <span class="input-icon" v-html="icons.globe"></span>
      <input
        v-model="frontend_url"
        type="text"
        placeholder="前端 URL (如: https://example.com)"
        @input="errors.frontend = false"
        :disabled="ruleStore.loading"
      />
      <transition name="fade">
        <div v-if="errors.frontend" class="error-tip">请填写此字段</div>
      </transition>
    </div>

    <div class="separator">
      <span class="arrow-svg" v-html="icons.arrowRight"></span>
    </div>

    <div class="input-wrapper" :class="{ 'has-error': errors.backend }">
      <span class="input-icon" v-html="icons.server"></span>
      <input
        v-model="backend_url"
        type="text"
        placeholder="后端 URL (如: http://backend:8080)"
        @input="errors.backend = false"
        :disabled="ruleStore.loading"
      />
      <transition name="fade">
        <div v-if="errors.backend" class="error-tip">请填写此字段</div>
      </transition>
    </div>

    <button type="submit" :disabled="ruleStore.loading" class="add-button" :class="{ 'is-loading': ruleStore.loading }">
      <span v-if="!ruleStore.loading" class="btn-content">
        <span class="icon-btn" v-html="icons.plus"></span>
        添加规则
      </span>
      <span v-else class="btn-content">
        <span class="loading-mini"></span>
        正在应用配置...
      </span>
    </button>
  </form>
</template>

<script setup>
import { ref, reactive } from 'vue'
import { useRuleStore } from '../stores/rules'

const ruleStore = useRuleStore()
const frontend_url = ref('')
const backend_url = ref('')

const errors = reactive({
  frontend: false,
  backend: false
})

const icons = {
  globe: '<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="10"/><line x1="2" y1="12" x2="22" y2="12"/><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/></svg>',
  server: '<svg viewBox="0 0 24 24"><rect x="2" y="2" width="20" height="8" rx="2" ry="2"/><rect x="2" y="14" width="20" height="8" rx="2" ry="2"/><line x1="6" y1="6" x2="6.01" y2="6"/><line x1="6" y1="18" x2="6.01" y2="18"/></svg>',
  arrowRight: '<svg viewBox="0 0 24 24"><line x1="5" y1="12" x2="19" y2="12"/><polyline points="12 5 19 12 12 19"/></svg>',
  plus: '<svg viewBox="0 0 24 24"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>'
}

const handleSubmit = async () => {
  // 手动校验
  errors.frontend = !frontend_url.value.trim()
  errors.backend = !backend_url.value.trim()

  if (errors.frontend || errors.backend) return

  try {
    await ruleStore.addRule(frontend_url.value, backend_url.value)
    frontend_url.value = ''
    backend_url.value = ''
  } catch (err) {
    // 错误已由 store 处理
  }
}
</script>

<style scoped>
.rule-form-inline {
  display: flex;
  align-items: center;
  gap: var(--spacing-md);
  width: 100%;
}

.input-wrapper {
  position: relative;
  flex: 1;
}

.input-icon {
  position: absolute;
  left: var(--spacing-md);
  top: 50%;
  transform: translateY(-50%);
  width: 16px;
  height: 16px;
  pointer-events: none;
  opacity: 0.5;
  color: var(--color-text-primary);
  z-index: 1;
}

.input-icon :deep(svg) {
  width: 100%;
  height: 100%;
  stroke: currentColor;
  stroke-width: 2;
  fill: none;
}

input {
  padding-left: calc(var(--spacing-md) * 2.8) !important;
  height: 46px;
  transition: all var(--transition-base);
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
  font-size: 0.7rem;
  font-weight: var(--font-weight-medium);
}

.separator {
  color: var(--color-text-muted);
  opacity: 0.3;
  width: 24px;
  height: 24px;
}

.separator :deep(svg) {
  width: 100%;
  height: 100%;
  stroke: currentColor;
  stroke-width: 3;
  fill: none;
}

.add-button {
  height: 46px;
  padding: 0 var(--spacing-lg);
  white-space: nowrap;
  flex-shrink: 0;
  transition: all var(--transition-fast);
}

.add-button:disabled {
  opacity: 0.8;
  cursor: not-allowed;
}

.btn-content {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
}

.icon-btn :deep(svg) {
  width: 16px;
  height: 16px;
  stroke: currentColor;
  stroke-width: 3;
  fill: none;
}

.loading-mini {
  width: 18px;
  height: 18px;
  border: 2px solid rgba(255,255,255,0.3);
  border-top-color: #fff;
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

@keyframes shake {
  10%, 90% { transform: translate3d(-1px, 0, 0); }
  20%, 80% { transform: translate3d(2px, 0, 0); }
  30%, 50%, 70% { transform: translate3d(-4px, 0, 0); }
  40%, 60% { transform: translate3d(4px, 0, 0); }
}

.fade-enter-active, .fade-leave-active { transition: opacity 0.2s; }
.fade-enter-from, .fade-leave-to { opacity: 0; }

@media (max-width: 1024px) {
  .rule-form-inline {
    flex-direction: column;
    align-items: stretch;
    gap: var(--spacing-md);
  }
  .separator {
    display: none;
  }
  .add-button {
    margin-top: var(--spacing-xs);
    width: 100%;
  }
  .error-tip {
    top: auto;
    bottom: -18px;
  }
}

@media (max-width: 480px) {
  input {
    height: 40px;
    font-size: 0.9rem;
  }
  .add-button {
    height: 42px;
  }
  .input-icon {
    width: 14px;
    height: 14px;
  }
}
</style>

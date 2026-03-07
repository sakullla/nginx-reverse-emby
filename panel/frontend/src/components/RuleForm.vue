<template>
  <form @submit.prevent="handleSubmit" class="rule-form-vertical" novalidate>
    <div class="form-group">
      <label class="form-label">前端 URL</label>
      <div class="input-wrapper" :class="{ 'has-error': errors.frontend }">
        <span class="input-icon" v-html="icons.globe"></span>
        <input
          v-model="frontend_url"
          type="text"
          placeholder="如: https://example.com"
          @input="errors.frontend = false"
          :disabled="ruleStore.loading"
        />
        <transition name="fade">
          <div v-if="errors.frontend" class="error-tip">请填写此字段</div>
        </transition>
      </div>
    </div>

    <div class="form-group">
      <label class="form-label">后端 URL</label>
      <div class="input-wrapper" :class="{ 'has-error': errors.backend }">
        <span class="input-icon" v-html="icons.server"></span>
        <input
          v-model="backend_url"
          type="text"
          placeholder="如: http://backend:8080"
          @input="errors.backend = false"
          :disabled="ruleStore.loading"
        />
        <transition name="fade">
          <div v-if="errors.backend" class="error-tip">请填写此字段</div>
        </transition>
      </div>
    </div>

    <div class="form-actions">
      <button type="submit" :disabled="ruleStore.loading" class="add-button primary" :class="{ 'is-loading': ruleStore.loading }">
        <span v-if="!ruleStore.loading" class="btn-content">
          <span class="icon-btn" v-html="icons.plus"></span>
          提交并保存
        </span>
        <span v-else class="btn-content">
          <span class="loading-mini"></span>
          正在处理...
        </span>
      </button>
    </div>
  </form>
</template>

<script setup>
import { ref, reactive } from 'vue'
import { useRuleStore } from '../stores/rules'

const emit = defineEmits(['success'])
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
  plus: '<svg viewBox="0 0 24 24"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>'
}

const handleSubmit = async () => {
  errors.frontend = !frontend_url.value.trim()
  errors.backend = !backend_url.value.trim()

  if (errors.frontend || errors.backend) return

  try {
    await ruleStore.addRule(frontend_url.value.trim(), backend_url.value.trim())
    frontend_url.value = ''
    backend_url.value = ''
    emit('success')
  } catch (err) {
    // 错误已由 store 处理
  }
}
</script>

<style scoped>
.rule-form-vertical {
  display: flex;
  flex-direction: column;
  gap: var(--spacing-lg);
}

.form-group {
  display: flex;
  flex-direction: column;
  gap: var(--spacing-xs);
}

.form-label {
  font-size: var(--font-size-sm);
  font-weight: var(--font-weight-semibold);
  color: var(--color-text-secondary);
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
  width: 100%;
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

.form-actions {
  margin-top: var(--spacing-md);
}

.add-button {
  width: 100%;
  height: 48px;
  display: flex;
  align-items: center;
  justify-content: center;
}

.btn-content {
  display: flex;
  align-items: center;
  gap: 8px;
}

.icon-btn :deep(svg) {
  width: 18px;
  height: 18px;
  stroke: currentColor;
  stroke-width: 2.5;
  fill: none;
}

.loading-mini {
  width: 20px;
  height: 20px;
  border: 2.5px solid rgba(255,255,255,0.3);
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
</style>

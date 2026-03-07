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
      <button type="submit" :disabled="ruleStore.loading" class="submit-button primary" :class="{ 'is-loading': ruleStore.loading }">
        <span v-if="!ruleStore.loading" class="btn-content">
          <span class="icon-btn" v-html="isEdit ? icons.save : icons.plus"></span>
          {{ isEdit ? '保存修改' : '立即创建' }}
        </span>
        <span v-else class="btn-content">
          <span class="loading-mini"></span>
          正在{{ isEdit ? '保存' : '创建' }}...
        </span>
      </button>
    </div>
  </form>
</template>

<script setup>
import { ref, reactive, onMounted, computed } from 'vue'
import { useRuleStore } from '../stores/rules'

const props = defineProps({
  initialData: {
    type: Object,
    default: null
  }
})

const emit = defineEmits(['success'])
const ruleStore = useRuleStore()

const isEdit = computed(() => !!props.initialData)

const frontend_url = ref('')
const backend_url = ref('')

const errors = reactive({
  frontend: false,
  backend: false
})

const icons = {
  globe: '<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="10"/><line x1="2" y1="12" x2="22" y2="12"/><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/></svg>',
  server: '<svg viewBox="0 0 24 24"><rect x="2" y="2" width="20" height="8" rx="2" ry="2"/><rect x="2" y="14" width="20" height="8" rx="2" ry="2"/><line x1="6" y1="6" x2="6.01" y2="6"/><line x1="6" y1="18" x2="6.01" y2="18"/></svg>',
  plus: '<svg viewBox="0 0 24 24"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>',
  save: '<svg viewBox="0 0 24 24"><path d="M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11l5 5v11a2 2 0 0 1-2 2z"/><polyline points="17 21 17 13 7 13 7 21"/><polyline points="7 3 7 8 15 8"/></svg>'
}

onMounted(() => {
  if (props.initialData) {
    frontend_url.value = props.initialData.frontend_url || ''
    backend_url.value = props.initialData.backend_url || ''
  }
})

const handleSubmit = async () => {
  errors.frontend = !frontend_url.value.trim()
  errors.backend = !backend_url.value.trim()

  if (errors.frontend || errors.backend) return

  try {
    if (isEdit.value) {
      await ruleStore.modifyRule(props.initialData.id, frontend_url.value.trim(), backend_url.value.trim())
    } else {
      await ruleStore.addRule(frontend_url.value.trim(), backend_url.value.trim())
    }

    if (!isEdit.value) {
      frontend_url.value = ''
      backend_url.value = ''
    }
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
  gap: var(--spacing-xl);
  padding: var(--spacing-sm) 0;
}

.form-group {
  display: flex;
  flex-direction: column;
  gap: var(--spacing-xs);
}

.form-label {
  font-size: var(--font-size-sm);
  font-weight: var(--font-weight-bold);
  color: var(--color-heading);
  margin-left: 2px;
  display: flex;
  align-items: center;
  gap: 8px;
}

.input-wrapper {
  position: relative;
  transition: transform var(--transition-fast);
}

.input-wrapper:focus-within {
  transform: translateY(-1px);
}

.input-icon {
  position: absolute;
  left: var(--spacing-md);
  top: 50%;
  transform: translateY(-50%);
  width: 18px;
  height: 18px;
  pointer-events: none;
  color: var(--color-text-muted);
  z-index: 1;
  transition: color var(--transition-base);
}

.input-wrapper:focus-within .input-icon {
  color: var(--color-primary);
}

.input-icon :deep(svg) {
  width: 100%;
  height: 100%;
  stroke: currentColor;
  stroke-width: 2.2;
  fill: none;
}

input {
  width: 100%;
  padding-left: calc(var(--spacing-md) * 3) !important;
  height: 52px;
  background: var(--color-bg-secondary) !important;
  border: 1px solid var(--color-border) !important;
  border-radius: var(--radius-lg) !important;
  font-size: var(--font-size-sm) !important;
  font-family: var(--font-family-mono);
  transition: all var(--transition-base) !important;
}

input:focus {
  background: var(--color-bg-primary) !important;
  border-color: var(--color-primary) !important;
  box-shadow: 0 0 0 4px var(--color-primary-lighter) !important;
}

.input-wrapper.has-error input {
  border-color: var(--color-danger) !important;
  background: var(--color-danger-bg) !important;
  box-shadow: 0 0 0 4px rgba(244, 63, 94, 0.1) !important;
}

.error-tip {
  position: absolute;
  top: calc(100% + 4px);
  left: 4px;
  color: var(--color-danger);
  font-size: 0.75rem;
  font-weight: var(--font-weight-medium);
}

.form-actions {
  margin-top: var(--spacing-md);
}

.submit-button {
  width: 100%;
  height: 52px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-lg);
  font-size: var(--font-size-base);
  font-weight: var(--font-weight-bold);
  letter-spacing: 0.025em;
  box-shadow: var(--shadow-md);
}

.submit-button:hover:not(:disabled) {
  transform: translateY(-2px);
  box-shadow: var(--shadow-lg);
}

.btn-content {
  display: flex;
  align-items: center;
  gap: 10px;
}

.icon-btn :deep(svg) {
  width: 20px;
  height: 20px;
  stroke: currentColor;
  stroke-width: 2.5;
  fill: none;
}

.loading-mini {
  width: 20px;
  height: 20px;
  border: 3px solid rgba(255,255,255,0.2);
  border-top-color: #fff;
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

@keyframes spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}
</style>

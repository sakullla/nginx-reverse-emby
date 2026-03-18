<template>
  <form @submit.prevent="handleSubmit" class="rule-form">
    <!-- Frontend URL -->
    <div class="form-group">
      <label class="form-label form-label--required">前端访问地址</label>
      <div class="input-wrapper">
        <span class="input-wrapper__icon">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <circle cx="12" cy="12" r="10"/>
            <line x1="2" y1="12" x2="22" y2="12"/>
            <path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/>
          </svg>
        </span>
        <input
          v-model="form.frontend_url"
          type="text"
          class="input"
          :class="{ 'input--error': errors.frontend_url }"
          placeholder="https://emby.example.com"
          @input="errors.frontend_url = ''"
        >
      </div>
      <p v-if="errors.frontend_url" class="form-error">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <circle cx="12" cy="12" r="10"/>
          <line x1="12" y1="8" x2="12" y2="12"/>
          <line x1="12" y1="16" x2="12.01" y2="16"/>
        </svg>
        {{ errors.frontend_url }}
      </p>
    </div>

    <!-- Backend URL -->
    <div class="form-group">
      <label class="form-label form-label--required">后端目标地址</label>
      <div class="input-wrapper">
        <span class="input-wrapper__icon">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <rect x="2" y="2" width="20" height="8" rx="2" ry="2"/>
            <rect x="2" y="14" width="20" height="8" rx="2" ry="2"/>
            <line x1="6" y1="6" x2="6.01" y2="6"/>
            <line x1="6" y1="18" x2="6.01" y2="18"/>
          </svg>
        </span>
        <input
          v-model="form.backend_url"
          type="text"
          class="input"
          :class="{ 'input--error': errors.backend_url }"
          placeholder="http://192.168.1.10:8096"
          @input="errors.backend_url = ''"
        >
      </div>
      <p v-if="errors.backend_url" class="form-error">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <circle cx="12" cy="12" r="10"/>
          <line x1="12" y1="8" x2="12" y2="12"/>
          <line x1="12" y1="16" x2="12.01" y2="16"/>
        </svg>
        {{ errors.backend_url }}
      </p>
    </div>

    <!-- Tags -->
    <div class="form-group">
      <label class="form-label">分类标签</label>
      <div class="tag-input">
        <div class="tag-input__container">
          <span 
            v-for="(tag, index) in form.tags" 
            :key="tag" 
            class="tag"
          >
            {{ tag }}
            <button 
              type="button"
              class="tag__remove"
              @click="removeTag(index)"
            >
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <line x1="18" y1="6" x2="6" y2="18"/>
                <line x1="6" y1="6" x2="18" y2="18"/>
              </svg>
            </button>
          </span>
          <input
            v-model="tagInput"
            type="text"
            class="tag-input__field"
            placeholder="输入标签按回车..."
            @keydown.enter.prevent="addTag"
          >
        </div>
      </div>
    </div>

    <!-- Toggles -->
    <div class="form-group">
      <div class="toggle-row">
        <label class="toggle">
          <input 
            v-model="form.enabled" 
            type="checkbox" 
            class="toggle__input"
          >
          <span class="toggle__slider"></span>
          <span class="toggle__label">启用此规则</span>
        </label>
      </div>

      <div class="toggle-row">
        <label class="toggle">
          <input 
            v-model="form.proxy_redirect" 
            type="checkbox" 
            class="toggle__input"
          >
          <span class="toggle__slider"></span>
          <span class="toggle__label">
            代理 302/307 重定向
            <span class="form-hint">关闭时，后端返回的重定向将直接传递给客户端</span>
          </span>
        </label>
      </div>
    </div>

    <!-- Submit -->
    <button 
      type="submit" 
      class="btn btn--primary btn--full btn--lg"
      :disabled="ruleStore.loading"
    >
      <span v-if="ruleStore.loading" class="spinner spinner--sm"></span>
      <span v-else>{{ isEdit ? '保存修改' : '创建规则' }}</span>
    </button>
  </form>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { useRuleStore } from '../stores/rules'

const props = defineProps({
  initialData: { type: Object, default: null }
})

const emit = defineEmits(['success'])

const ruleStore = useRuleStore()
const isEdit = computed(() => !!props.initialData)

const form = ref({
  frontend_url: '',
  backend_url: '',
  tags: [],
  enabled: true,
  proxy_redirect: true
})

const tagInput = ref('')
const errors = ref({
  frontend_url: '',
  backend_url: ''
})

onMounted(() => {
  if (props.initialData) {
    form.value = {
      frontend_url: props.initialData.frontend_url || '',
      backend_url: props.initialData.backend_url || '',
      tags: Array.isArray(props.initialData.tags) ? [...props.initialData.tags] : [],
      enabled: props.initialData.enabled !== false,
      proxy_redirect: props.initialData.proxy_redirect !== false
    }
  }
})

const addTag = () => {
  const tag = tagInput.value.trim()
  if (tag && !form.value.tags.includes(tag)) {
    form.value.tags.push(tag)
  }
  tagInput.value = ''
}

const removeTag = (index) => {
  form.value.tags.splice(index, 1)
}

const validate = () => {
  errors.value.frontend_url = ''
  errors.value.backend_url = ''

  if (!form.value.frontend_url.trim()) {
    errors.value.frontend_url = '请输入前端访问地址'
  }

  if (!form.value.backend_url.trim()) {
    errors.value.backend_url = '请输入后端目标地址'
  }

  return !errors.value.frontend_url && !errors.value.backend_url
}

const handleSubmit = async () => {
  if (!validate()) return

  try {
    const params = [
      props.initialData?.id,
      form.value.frontend_url.trim(),
      form.value.backend_url.trim(),
      [...form.value.tags],
      form.value.enabled,
      form.value.proxy_redirect
    ]

    if (isEdit.value) {
      await ruleStore.modifyRule(...params)
    } else {
      await ruleStore.addRule(params[1], params[2], params[3], params[4], params[5])
    }

    emit('success')
  } catch (err) {
    // Error handled by store
  }
}
</script>

<style scoped>
.rule-form {
  display: flex;
  flex-direction: column;
  gap: var(--space-5);
}

.tag-input {
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  transition: all var(--duration-fast) var(--ease-default);
}

.tag-input:focus-within {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.tag-input__container {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  padding: var(--space-2);
  align-items: center;
  min-height: 44px;
}

.tag-input__field {
  flex: 1;
  min-width: 120px;
  border: none;
  background: transparent;
  padding: var(--space-1);
  font-size: var(--text-sm);
  color: var(--color-text-primary);
  outline: none;
}

.tag-input__field::placeholder {
  color: var(--color-text-muted);
}

.toggle-row {
  padding: var(--space-2) 0;
  border-bottom: 1px solid var(--color-border-subtle);
}

.toggle-row:last-child {
  border-bottom: none;
}

.toggle {
  display: flex;
  align-items: flex-start;
  gap: var(--space-3);
  cursor: pointer;
}

.toggle__input {
  position: absolute;
  opacity: 0;
}

.toggle__slider {
  position: relative;
  width: 44px;
  height: 24px;
  background: var(--color-slate-300);
  border-radius: var(--radius-full);
  transition: background var(--duration-fast) var(--ease-default);
  flex-shrink: 0;
  margin-top: 2px;
}

.toggle__slider::after {
  content: '';
  position: absolute;
  top: 2px;
  left: 2px;
  width: 20px;
  height: 20px;
  background: white;
  border-radius: var(--radius-full);
  transition: transform var(--duration-fast) var(--ease-bounce);
  box-shadow: var(--shadow-sm);
}

.toggle__input:checked + .toggle__slider {
  background: var(--color-success);
}

.toggle__input:checked + .toggle__slider::after {
  transform: translateX(20px);
}

.toggle__input:focus-visible + .toggle__slider {
  box-shadow: var(--shadow-focus);
}

.toggle__label {
  font-size: var(--text-sm);
  color: var(--color-text-primary);
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.toggle__label .form-hint {
  margin-top: 0;
}
</style>

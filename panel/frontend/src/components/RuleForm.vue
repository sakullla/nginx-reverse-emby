<template>
  <form @submit.prevent="handleSubmit" class="rule-form-clean" novalidate data-testid="rule-form">
    <!-- 地址配置区 -->
    <div class="form-group">
      <label class="field-label">前端访问地址</label>
      <div class="input-container" :class="{ 'has-error': errors.frontend }">
        <div class="input-icon">
          <svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="10"/><line x1="2" y1="12" x2="22" y2="12"/><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/></svg>
        </div>
        <input v-model="frontend_url" data-testid="frontend-url-input" type="text" placeholder="https://emby.example.com" @input="errors.frontend = false" />
      </div>
    </div>

    <div class="form-group">
      <label class="field-label">后端目标地址</label>
      <div class="input-container" :class="{ 'has-error': errors.backend }">
        <div class="input-icon">
          <svg viewBox="0 0 24 24"><rect x="2" y="2" width="20" height="8" rx="2" ry="2"/><rect x="2" y="14" width="20" height="8" rx="2" ry="2"/><line x1="6" y1="6" x2="6.01" y2="6"/><line x1="6" y1="18" x2="6.01" y2="18"/></svg>
        </div>
        <input v-model="backend_url" data-testid="backend-url-input" type="text" placeholder="http://192.168.1.10:8096" @input="errors.backend = false" />
      </div>
    </div>

    <!-- 标签管理区 -->
    <div class="form-group">
      <label class="field-label">分类标签</label>
      <div class="tag-input-area" :class="{ 'is-focused': isTagsFocused }">
        <div class="tag-chips-wrapper">
          <span v-for="(tag, index) in tags" :key="tag" :class="['tag-pill', getTagColorClass(tag)]">
            {{ tag }}
            <button type="button" @click="removeTag(index)" class="tag-del-badge">&times;</button>
          </span>
          <input
            v-model="tagInput"
            data-testid="tag-input"
            type="text"
            placeholder="输入并回车..."
            @keydown.enter.prevent="addTag"
            @focus="isTagsFocused = true"
            @blur="isTagsFocused = false"
            class="tag-raw-input"
          />
        </div>
      </div>
    </div>

    <!-- 开关区 -->
    <div class="switch-area">
      <label class="field-label">启用此规则</label>
      <label class="pro-toggle">
        <input type="checkbox" data-testid="enabled-checkbox" v-model="enabled">
        <span class="pro-toggle-slider"></span>
      </label>
    </div>

    <div class="switch-area">
      <label class="field-label">
        代理 302/307 重定向
        <span class="field-hint">关闭时，后端返回的重定向将直接传递给客户端</span>
      </label>
      <label class="pro-toggle">
        <input type="checkbox" data-testid="proxy-redirect-checkbox" v-model="proxy_redirect">
        <span class="pro-toggle-slider"></span>
      </label>
    </div>

    <!-- 操作提交 -->
    <div class="form-footer-action">
      <button type="submit" data-testid="rule-submit" :disabled="ruleStore.loading" class="btn-full-primary">
        <div v-if="ruleStore.loading" class="loading-spin"></div>
        <span v-else>{{ isEdit ? '保存修改' : '立即创建' }}</span>
      </button>
    </div>
  </form>
</template>

<script setup>
import { ref, reactive, onMounted, computed } from 'vue'
import { useRuleStore } from '../stores/rules'

const props = defineProps({ initialData: { type: Object, default: null } })
const emit = defineEmits(['success'])
const ruleStore = useRuleStore()

const isEdit = computed(() => !!props.initialData)
const frontend_url = ref('')
const backend_url = ref('')
const enabled = ref(true)
const proxy_redirect = ref(true)
const tags = ref([])
const tagInput = ref('')
const isTagsFocused = ref(false)

const errors = reactive({ frontend: false, backend: false })

onMounted(() => {
  if (props.initialData) {
    frontend_url.value = props.initialData.frontend_url || ''
    backend_url.value = props.initialData.backend_url || ''
    enabled.value = props.initialData.enabled !== false
    proxy_redirect.value = props.initialData.proxy_redirect !== false
    tags.value = Array.isArray(props.initialData.tags) ? [...props.initialData.tags] : []
  }
})

const getTagColorClass = (tag) => {
  let hash = 0
  for (let i = 0; i < tag.length; i++) hash = tag.charCodeAt(i) + ((hash << 5) - hash)
  return `color-set-${Math.abs(hash % 6) + 1}`
}

const addTag = () => {
  const val = tagInput.value.trim().replace(/,$/, '')
  if (val && !tags.value.includes(val)) {
    tags.value.push(val)
  }
  tagInput.value = ''
}

const removeTag = (index) => { tags.value.splice(index, 1) }

const handleSubmit = async () => {
  errors.frontend = !frontend_url.value.trim()
  errors.backend = !backend_url.value.trim()
  if (errors.frontend || errors.backend) return
  try {
    const params = [props.initialData?.id, frontend_url.value.trim(), backend_url.value.trim(), [...tags.value], enabled.value, proxy_redirect.value]
    if (isEdit.value) await ruleStore.modifyRule(...params)
    else await ruleStore.addRule(params[1], params[2], params[3], params[4], params[5])
    emit('success')
  } catch (err) {}
}
</script>

<style scoped>
/* 全局字体统一 */
.rule-form-clean {
  display: flex;
  flex-direction: column;
  gap: 20px;
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
  color: var(--color-text-primary);
}

.form-group {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.field-label {
  font-size: 0.85rem;
  font-weight: 600;
  color: var(--color-text-secondary);
  margin-left: 2px;
}

.field-hint {
  display: block;
  font-size: 0.75rem;
  font-weight: 400;
  color: var(--color-text-muted);
  margin-top: 2px;
}

/* 输入框重设：消除截图中的重影效果 */
.input-container {
  position: relative;
  display: flex;
  align-items: center;
  background: var(--color-bg-secondary);
  border: 2px solid var(--color-border);
  border-radius: 10px;
  transition: all 0.25s cubic-bezier(0.4, 0, 0.2, 1);
  overflow: hidden;
}

.input-container:focus-within {
  background: var(--color-bg-card);
  border-color: var(--color-primary);
}

.input-container.has-error {
  border-color: var(--color-danger);
  background: var(--color-danger-bg);
  animation: shake 0.4s ease;
}

@keyframes shake {
  0%, 100% { transform: translateX(0); }
  25% { transform: translateX(-4px); }
  75% { transform: translateX(4px); }
}

.input-icon {
  position: absolute;
  left: 14px;
  color: var(--color-text-muted);
  display: flex;
  align-items: center;
  pointer-events: none;
  transition: all 0.25s ease;
}

.input-container:focus-within .input-icon {
  color: var(--color-primary);
  transform: scale(1.1);
}

.input-icon svg {
  width: 20px;
  height: 20px;
  stroke: currentColor;
  stroke-width: 2.2;
  fill: none;
}

.input-container input {
  width: 100%;
  height: 48px;
  padding: 0 14px 0 46px;
  border: none;
  outline: none;
  background: transparent;
  font-size: 0.95rem;
  font-family: var(--font-family-mono); /* 仅URL保持等宽 */
  color: var(--color-text-primary);
  transition: all 0.2s ease;
}

.input-container input::placeholder {
  color: var(--color-text-disabled);
  transition: color 0.2s ease;
}

.input-container:focus-within input::placeholder {
  color: var(--color-text-muted);
}

/* 标签录入区优化 */
.tag-input-area {
  background: var(--color-bg-secondary);
  border: 2px solid var(--color-border);
  border-radius: 10px;
  padding: 8px 12px;
  min-height: 48px;
  transition: all 0.25s cubic-bezier(0.4, 0, 0.2, 1);
}

.tag-input-area.is-focused {
  background: var(--color-bg-card);
  border-color: var(--color-primary);
}

.tag-chips-wrapper {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  align-items: center;
}

.tag-pill {
  position: relative;
  display: inline-flex;
  align-items: center;
  padding: 5px 12px;
  border-radius: 6px;
  font-size: 0.75rem;
  font-weight: 700;
  line-height: 1.2;
  transition: all 0.2s ease;
  cursor: default;
}

.tag-pill:hover {
  transform: translateY(-1px);
}

.tag-del-badge {
  position: absolute;
  top: -8px;
  right: -8px;
  width: 18px;
  height: 18px;
  border-radius: 50%;
  background: linear-gradient(135deg, var(--color-danger) 0%, var(--color-danger-dark) 100%);
  color: white;
  border: 2px solid var(--color-bg-card);
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 11px;
  cursor: pointer;
  opacity: 0;
  transition: all 0.2s cubic-bezier(0.4, 0, 0.2, 1);
  padding: 0;
}

.tag-pill:hover .tag-del-badge {
  opacity: 1;
  transform: scale(1.1);
}

.tag-del-badge:hover {
  transform: scale(1.2) rotate(90deg);
}

.tag-del-badge:active {
  transform: scale(0.95);
}

.tag-raw-input {
  border: none !important;
  outline: none !important;
  background: transparent !important;
  font-size: 0.85rem;
  flex: 1;
  min-width: 100px;
  padding: 4px 0;
  color: var(--color-text-secondary);
}

/* 开关行简洁化 */
.switch-area {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 4px 2px;
}

.pro-toggle {
  position: relative;
  width: 40px;
  height: 22px;
}

.pro-toggle input { opacity: 0; width: 0; height: 0; }

.pro-toggle-slider {
  position: absolute;
  cursor: pointer;
  top: 0; left: 0; right: 0; bottom: 0;
  background: var(--color-border-dark);
  transition: .3s;
  border-radius: 22px;
}

.pro-toggle-slider:before {
  position: absolute;
  content: "";
  height: 16px; width: 16px;
  left: 3px; bottom: 3px;
  background: white;
  transition: .3s;
  border-radius: 50%;
}

input:checked + .pro-toggle-slider { background: var(--color-success); }
input:checked + .pro-toggle-slider:before { transform: translateX(18px); }

/* 提交按钮 */
.form-footer-action {
  margin-top: 16px;
}

.btn-full-primary {
  width: 100%;
  height: 50px;
  background: linear-gradient(135deg, var(--color-primary) 0%, var(--color-primary-dark) 100%);
  color: white;
  border: none;
  border-radius: 10px;
  font-weight: 700;
  font-size: 1rem;
  cursor: pointer;
  transition: all 0.25s cubic-bezier(0.4, 0, 0.2, 1);
  position: relative;
  overflow: hidden;
}

.btn-full-primary::before {
  content: '';
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: linear-gradient(135deg, rgba(255, 255, 255, 0.2) 0%, rgba(255, 255, 255, 0) 100%);
  opacity: 0;
  transition: opacity 0.25s ease;
}

.btn-full-primary:hover {
  transform: translateY(-2px);
}

.btn-full-primary:hover::before {
  opacity: 1;
}

.btn-full-primary:active {
  transform: translateY(0);
}

.btn-full-primary:disabled {
  opacity: 0.6;
  cursor: not-allowed;
  transform: none;
}

.loading-spin {
  width: 20px;
  height: 20px;
  border: 2px solid rgba(255,255,255,0.3);
  border-top-color: #fff;
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
  margin: 0 auto;
}

@keyframes spin { to { transform: rotate(360deg); } }

/* 颜色集 */
.color-set-1 { background: var(--color-primary-lighter); color: var(--color-primary); }
.color-set-2 { background: var(--color-success-bg); color: var(--color-success); }
.color-set-3 { background: var(--color-danger-bg); color: var(--color-danger); }
.color-set-4 { background: var(--color-warning-bg); color: var(--color-warning); }
.color-set-5 { background: #faf5ff; color: #9333ea; }
.color-set-6 { background: #f0fdfa; color: #0d9488; }
</style>

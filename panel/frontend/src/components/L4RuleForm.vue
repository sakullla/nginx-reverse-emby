<template>
  <form class="rule-form" @submit.prevent="handleSubmit">
    <div class="form-row">
      <div class="form-group">
        <label class="form-label form-label--required">协议</label>
        <select v-model="form.protocol" class="input" @change="handleProtocolChange">
          <option value="tcp">TCP</option>
          <option value="udp">UDP</option>
        </select>
      </div>
    </div>

    <div class="form-row">
      <div class="form-group">
        <label class="form-label form-label--required">监听地址</label>
        <input v-model="form.listen_host" class="input" placeholder="0.0.0.0" @input="handleListenInput">
      </div>
      <div class="form-group">
        <label class="form-label form-label--required">监听端口</label>
        <input v-model.number="form.listen_port" class="input" type="number" min="1" max="65535" placeholder="25565" @input="updateAutoTags">
      </div>
    </div>

    <div class="form-row">
      <div class="form-group">
        <label class="form-label form-label--required">上游地址</label>
        <input v-model="form.upstream_host" class="input" placeholder="192.168.1.10" @input="handleUpstreamHostInput">
      </div>
      <div class="form-group">
        <label class="form-label form-label--required">上游端口</label>
        <input v-model.number="form.upstream_port" class="input" type="number" min="1" max="65535" placeholder="25565" @input="updateAutoTags">
      </div>
    </div>

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

    <label class="toggle-row">
      <input v-model="form.enabled" type="checkbox" class="toggle__input">
      <span class="toggle__slider"></span>
      <span class="toggle__label">启用规则</span>
    </label>

    <button type="submit" class="btn btn--primary btn--full" :disabled="ruleStore.loading">
      {{ isEdit ? '保存修改' : '创建规则' }}
    </button>
  </form>
</template>

<script setup>
import { computed, ref, watch } from 'vue'
import { useRuleStore } from '../stores/rules'

const props = defineProps({ initialData: { type: Object, default: null } })
const emit = defineEmits(['success'])
const ruleStore = useRuleStore()
const isEdit = computed(() => !!props.initialData)
const form = ref({
  protocol: props.initialData?.protocol || 'tcp',
  listen_host: props.initialData?.listen_host || '0.0.0.0',
  listen_port: props.initialData?.listen_port || 0,
  upstream_host: props.initialData?.upstream_host || '',
  upstream_port: props.initialData?.upstream_port || 0,
  enabled: props.initialData?.enabled !== false,
  tags: Array.isArray(props.initialData?.tags) ? [...props.initialData.tags] : []
})
const tagInput = ref('')

function isL4AutoTag(t) {
  return t === 'TCP' || t === 'UDP' || /^:\d+$/.test(t) ||
    /^(TCP|UDP) 监听端口 \d+/.test(t) ||
    t.startsWith('监听端口') || t.startsWith('上游端口')
}

// Auto-generate tags for new rules (real-time preview while filling form)
function updateAutoTags() {
  if (isEdit.value) return

  const protocol = form.value.protocol.toUpperCase()
  const listenPort = form.value.listen_port

  form.value.tags = form.value.tags.filter(t => !isL4AutoTag(t))

  const sysTags = [protocol, ...(listenPort ? [`:${listenPort}`] : [])]
  form.value.tags = [...sysTags, ...form.value.tags]
}

function handleProtocolChange() {
  if (!isEdit.value) {
    updateAutoTags()
  }
}

function handleListenInput(e) {
  const value = e.target.value.trim()
  // Match ip:port pattern for listen address too
  const match = value.match(/^(.+):(\d+)$/)
  if (match) {
    form.value.listen_host = match[1]
    form.value.listen_port = parseInt(match[2], 10)
    if (!isEdit.value) {
      updateAutoTags()
    }
  }
}

function handleUpstreamHostInput(e) {
  const value = e.target.value.trim()
  // Match ip:port pattern like 192.168.1.10:25565 or [::1]:8080
  const match = value.match(/^(.+):(\d+)$/)
  if (match) {
    form.value.upstream_host = match[1]
    form.value.upstream_port = parseInt(match[2], 10)
    if (!isEdit.value) {
      updateAutoTags()
    }
  }
}

function addTag() {
  const tag = tagInput.value.trim()
  if (tag && !form.value.tags.includes(tag)) {
    form.value.tags.push(tag)
  }
  tagInput.value = ''
}

function removeTag(index) {
  form.value.tags.splice(index, 1)
}

function buildPayload() {
  const protocol = form.value.protocol.toUpperCase()
  const listenPort = form.value.listen_port
  const userTags = form.value.tags.filter(t => !isL4AutoTag(t))
  const sysTags = [protocol, ...(listenPort ? [`:${listenPort}`] : [])]
  return {
    ...form.value,
    listen_host: form.value.listen_host.trim(),
    upstream_host: form.value.upstream_host.trim(),
    tags: [...sysTags, ...userTags]
  }
}

async function handleSubmit() {
  const payload = buildPayload()
  if (isEdit.value) {
    await ruleStore.modifyL4Rule(props.initialData.id, payload)
  } else {
    await ruleStore.addL4Rule(payload)
  }
  emit('success')
}
</script>

<style scoped>
.rule-form {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.form-row {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
  gap: var(--space-3);
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

.tag {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  padding: 2px 8px;
  background: var(--color-bg-subtle);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-full);
  font-size: var(--text-xs);
  color: var(--color-text-primary);
}

.tag__remove {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 14px;
  height: 14px;
  border: none;
  background: transparent;
  color: var(--color-text-muted);
  cursor: pointer;
  padding: 0;
  border-radius: 50%;
  transition: all var(--duration-fast);
}

.tag__remove:hover {
  background: var(--color-danger-50);
  color: var(--color-danger);
}

.toggle-row {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  cursor: pointer;
}

.toggle__input {
  position: absolute;
  opacity: 0;
  width: 0;
  height: 0;
}

.toggle__slider {
  position: relative;
  width: 44px;
  height: 24px;
  background: var(--color-border-strong);
  border-radius: var(--radius-full);
  transition: all var(--duration-normal) var(--ease-bounce);
  flex-shrink: 0;
}

.toggle__slider::after {
  content: '';
  position: absolute;
  top: 3px;
  left: 3px;
  width: 18px;
  height: 18px;
  background: white;
  border-radius: var(--radius-full);
  transition: all var(--duration-normal) var(--ease-bounce);
  box-shadow: var(--shadow-sm);
}

.toggle__input:checked + .toggle__slider {
  background: var(--gradient-primary);
}

.toggle__input:checked + .toggle__slider::after {
  transform: translateX(20px);
}

.toggle__label {
  font-size: var(--text-sm);
  color: var(--color-text-secondary);
}
</style>

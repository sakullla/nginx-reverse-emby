<template>
  <form class="cert-form" @submit.prevent="handleSubmit">
    <!-- Domain -->
    <div class="form-group">
      <label class="form-label form-label--required">域名 / IP</label>
      <input
        v-model="form.domain"
        class="input"
        :class="{ 'input--error': errors.domain }"
        placeholder="media.example.com 或 1.2.3.4"
        @input="errors.domain = ''"
      >
      <p v-if="errors.domain" class="form-error">{{ errors.domain }}</p>
    </div>

    <!-- Scope + Issuer Mode -->
    <div class="form-row">
      <div class="form-group">
        <label class="form-label">证书类型</label>
        <select v-model="form.scope" class="input" @change="handleScopeChange">
          <option value="domain">域名证书</option>
          <option value="ip">IP 证书</option>
        </select>
      </div>
      <div class="form-group">
        <label class="form-label">签发模式</label>
        <select v-model="form.issuer_mode" class="input" :disabled="form.scope === 'ip'">
          <option value="master_cf_dns">Master 统一签发 (DNS)</option>
          <option value="local_http01">节点本地签发</option>
        </select>
      </div>
    </div>

    <!-- Info banners -->
    <div v-if="form.scope === 'ip'" class="cert-banner cert-banner--warn">
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
        <circle cx="12" cy="12" r="10"/>
        <line x1="12" y1="8" x2="12" y2="12"/>
        <line x1="12" y1="16" x2="12.01" y2="16"/>
      </svg>
      IP 证书仅支持节点本地申请签发
    </div>
    <div v-else-if="form.issuer_mode === 'master_cf_dns'" class="cert-banner cert-banner--info">
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
        <circle cx="12" cy="12" r="10"/>
        <line x1="12" y1="8" x2="12" y2="12"/>
        <line x1="12" y1="16" x2="12.01" y2="16"/>
      </svg>
      需要在 Master 节点配置 Cloudflare DNS Token，由 Master 节点统一执行申请；相同域名多节点将合并申请
    </div>

    <!-- Tags -->
    <div class="form-group">
      <label class="form-label">分类标签</label>
      <div class="tag-input">
        <div class="tag-input__container">
          <span v-for="(tag, index) in form.tags" :key="tag" class="tag">
            {{ tag }}
            <button type="button" class="tag__remove" @click="removeTag(index)">
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

    <!-- Enable toggle -->
    <label class="toggle-row">
      <input v-model="form.enabled" type="checkbox" class="toggle__input">
      <span class="toggle__slider"></span>
      <span class="toggle__label">启用并参与分发</span>
    </label>

    <button type="submit" class="btn btn--primary btn--full" :disabled="isLoading">
      {{ isEdit ? '保存修改' : '创建证书' }}
    </button>
  </form>
</template>

<script setup>
import { computed, ref } from 'vue'
import { useCreateCertificate, useUpdateCertificate } from '../hooks/useCertificates'

const props = defineProps({
  initialData: { type: Object, default: null },
  agentId: { type: [String, Object], required: true }
})
const emit = defineEmits(['success'])
const createCertificate = useCreateCertificate(props.agentId)
const updateCertificate = useUpdateCertificate(props.agentId)
const isEdit = computed(() => !!props.initialData?.id)
const isLoading = computed(() => createCertificate.isPending.value || updateCertificate.isPending.value)

const form = ref({
  domain: props.initialData?.domain || '',
  scope: props.initialData?.scope || 'domain',
  issuer_mode: props.initialData?.issuer_mode || 'master_cf_dns',
  enabled: props.initialData?.enabled !== false,
  tags: Array.isArray(props.initialData?.tags) ? [...props.initialData.tags] : []
})

const tagInput = ref('')
const errors = ref({ domain: '' })

function handleScopeChange() {
  if (form.value.scope === 'ip') {
    form.value.issuer_mode = 'local_http01'
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

async function handleSubmit() {
  errors.value.domain = ''
  if (!form.value.domain.trim()) {
    errors.value.domain = '请输入域名或 IP'
    return
  }
  const payload = { ...form.value, domain: form.value.domain.trim() }
  try {
    if (isEdit.value) {
      await updateCertificate.mutateAsync({ id: props.initialData.id, ...payload })
    } else {
      await createCertificate.mutateAsync(payload)
    }
    emit('success')
  } catch (err) {
    errors.value.domain = err?.message || '操作失败'
  }
}
</script>

<style scoped>
.cert-form {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.form-group {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.form-label {
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  color: var(--color-text-secondary);
}

.form-label--required::after {
  content: ' *';
  color: var(--color-danger);
}

.form-error {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  background: var(--color-danger-50);
  color: var(--color-danger);
  border-radius: var(--radius-md);
  font-size: var(--text-sm);
}

.form-row {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
  gap: var(--space-3);
}

.input {
  width: 100%;
  padding: var(--space-2) var(--space-3);
  font-size: var(--text-sm);
  color: var(--color-text-primary);
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  transition: all var(--duration-fast) var(--ease-default);
  font-family: inherit;
}

.input:focus {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.input::placeholder { color: var(--color-text-muted); }

.cert-banner {
  display: flex;
  align-items: flex-start;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-md);
  font-size: var(--text-xs);
  line-height: 1.6;
}
.cert-banner--warn { background: var(--color-warning-50); color: var(--color-warning); border: 1px solid var(--color-warning); }
.cert-banner--info { background: var(--color-primary-subtle); color: var(--color-primary); border: 1px solid var(--color-primary); }
.cert-banner svg { flex-shrink: 0; margin-top: 1px; }

/* Tag input */
.tag-input {
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  transition: all var(--duration-fast) var(--ease-default);
}
.tag-input:focus-within { border-color: var(--color-primary); box-shadow: var(--shadow-focus); }
.tag-input__container { display: flex; flex-wrap: wrap; gap: var(--space-2); padding: var(--space-1) var(--space-2); align-items: center; min-height: 36px; }
.tag-input__field { flex: 1; min-width: 80px; border: none; background: transparent; padding: var(--space-1); font-size: var(--text-sm); color: var(--color-text-primary); outline: none; }
.tag-input__field::placeholder { color: var(--color-text-muted); }
.tag { display: inline-flex; align-items: center; gap: var(--space-1); padding: 2px 8px; background: var(--color-bg-subtle); border: 1px solid var(--color-border-default); border-radius: var(--radius-full); font-size: var(--text-xs); color: var(--color-text-primary); }
.tag__remove { display: flex; align-items: center; justify-content: center; width: 14px; height: 14px; border: none; background: transparent; color: var(--color-text-muted); cursor: pointer; padding: 0; border-radius: 50%; transition: all var(--duration-fast); }
.tag__remove:hover { background: var(--color-danger-50); color: var(--color-danger); }

/* Toggle */
.toggle-row { display: flex; align-items: center; gap: var(--space-3); cursor: pointer; padding: var(--space-2) 0; }
.toggle__input { position: absolute; opacity: 0; width: 0; height: 0; }
.toggle__slider { position: relative; width: 44px; height: 24px; background: var(--color-border-strong); border-radius: var(--radius-full); transition: all var(--duration-normal) var(--ease-bounce); flex-shrink: 0; }
.toggle__slider::after { content: ''; position: absolute; top: 3px; left: 3px; width: 18px; height: 18px; background: white; border-radius: var(--radius-full); transition: all var(--duration-normal) var(--ease-bounce); box-shadow: var(--shadow-sm); }
.toggle__input:checked + .toggle__slider { background: var(--gradient-primary); }
.toggle__input:checked + .toggle__slider::after { transform: translateX(20px); }
.toggle__label { font-size: var(--text-sm); color: var(--color-text-secondary); }

/* Button — standard, NOT --lg */
.btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-4);
  border: none;
  border-radius: var(--radius-md);
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-default);
  font-family: inherit;
}
.btn--primary { background: var(--gradient-primary); color: white; }
.btn--primary:hover:not(:disabled) { opacity: 0.9; transform: translateY(-1px); }
.btn--full { width: 100%; }
.btn:disabled { opacity: 0.6; cursor: not-allowed; }
</style>

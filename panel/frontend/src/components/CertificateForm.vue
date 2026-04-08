<template>
  <form class='cert-form' @submit.prevent='handleSubmit'>
    <section class='form-section'>
      <div class='section-heading'>
        <h3>用途模板</h3>
        <p v-if='isProtectedSystemRelayCA'>系统 Relay CA 由控制面维护，创建入口与用途切换已锁定。</p>
        <p v-else>先选最接近的场景，再补充域名与证书材料。</p>
      </div>
      <div v-if='!isProtectedSystemRelayCA' class='template-grid'>
        <button
          v-for='template in CERTIFICATE_TEMPLATES'
          :key='template.id'
          type='button'
          class='template-card'
          :class="{ 'template-card--active': selectedTemplate === template.id }"
          @click='selectTemplate(template.id)'
        >
          <strong>{{ template.label }}</strong>
          <span>{{ template.description }}</span>
        </button>
      </div>
      <div v-else class='cert-banner cert-banner--info'>
        系统 Relay CA 始终保持固定身份与用途，当前窗口仅用于查看状态。
      </div>
    </section>

    <section class='form-section'>
      <div class='form-group'>
        <label class='form-label form-label--required'>域名 / IP</label>
        <input
          v-model='form.domain'
          class='input'
          :class="{ 'input--error': errors.domain }"
          placeholder='media.example.com 或 1.2.3.4'
          :disabled='isProtectedSystemRelayCA'
          @input='errors.domain = ""'
        >
        <p v-if='errors.domain' class='form-error'>{{ errors.domain }}</p>
      </div>

      <div v-if='form.scope === "ip"' class='cert-banner cert-banner--warn'>
        IP 证书仅支持节点本地签发。
      </div>
      <div v-else-if='isProtectedSystemRelayCA' class='cert-banner cert-banner--info'>
        当前证书是系统 Relay CA，由控制面统一维护并用于 Relay 自动信任链。
      </div>
      <div v-else-if='isSystemManagedRelayListener' class='cert-banner cert-banner--info'>
        Relay 监听证书默认由控制面使用全局 Relay CA 自动签发并分发。
      </div>
      <div v-else-if='form.issuer_mode === "master_cf_dns"' class='cert-banner cert-banner--info'>
        Master 统一签发需要已配置 Cloudflare DNS Token。
      </div>
      <div v-else-if='form.certificate_type === "uploaded"' class='cert-banner cert-banner--info'>
        手动上传会直接写入 PEM 材料，并立即同步到目标节点。
      </div>
    </section>

    <section v-if='form.certificate_type === "uploaded"' class='form-section'>
      <div class='section-heading'>
        <h3>手动上传入口</h3>
        <p>{{ isEdit ? '留空表示保留当前已保存的证书材料。' : '创建时必须提供证书 PEM 和私钥 PEM。' }}</p>
      </div>

      <div class='form-group'>
        <label class='form-label'>证书 PEM</label>
        <textarea
          v-model='uploadedMaterial.certificate_pem'
          class='input textarea'
          :class="{ 'input--error': errors.certificate_pem }"
          placeholder='-----BEGIN CERTIFICATE-----'
        ></textarea>
        <p v-if='errors.certificate_pem' class='form-error'>{{ errors.certificate_pem }}</p>
      </div>

      <div class='form-group'>
        <label class='form-label'>私钥 PEM</label>
        <textarea
          v-model='uploadedMaterial.private_key_pem'
          class='input textarea'
          :class="{ 'input--error': errors.private_key_pem }"
          placeholder='-----BEGIN PRIVATE KEY-----'
        ></textarea>
        <p v-if='errors.private_key_pem' class='form-error'>{{ errors.private_key_pem }}</p>
      </div>

      <div class='form-group'>
        <label class='form-label'>CA 链 PEM（可选）</label>
        <textarea
          v-model='uploadedMaterial.ca_pem'
          class='input textarea textarea--compact'
          placeholder='-----BEGIN CERTIFICATE-----'
        ></textarea>
      </div>
    </section>

    <section class='form-section'>
      <div class='form-group'>
        <label class='form-label'>分类标签</label>
        <div class='tag-input'>
          <div class='tag-input__container'>
            <span v-for='(tag, index) in form.tags' :key='tag' class='tag'>
              {{ tag }}
              <button v-if='!isProtectedSystemRelayCA' type='button' class='tag__remove' @click='removeTag(index)'>×</button>
            </span>
            <input
              v-if='!isProtectedSystemRelayCA'
              v-model='tagInput'
              type='text'
              class='tag-input__field'
              placeholder='输入标签后回车'
              @keydown.enter.prevent='addTag'
            >
          </div>
        </div>
      </div>

      <label class='toggle-row'>
        <input v-model='form.enabled' type='checkbox' class='toggle__input' :disabled='isProtectedSystemRelayCA'>
        <span class='toggle__slider'></span>
        <span class='toggle__label'>{{ isProtectedSystemRelayCA ? '系统证书始终由控制面管理' : '启用并参与分发' }}</span>
      </label>
    </section>

    <section class='form-section form-section--compact'>
      <button
        v-if='!isProtectedSystemRelayCA'
        type='button'
        class='advanced-toggle'
        @click='showAdvanced = !showAdvanced'
      >
        {{ showAdvanced ? '收起高级证书设置' : '高级证书设置' }}
      </button>

      <div v-if='showAdvanced || isProtectedSystemRelayCA' class='advanced-panel'>
        <div class='form-row'>
          <div class='form-group'>
            <label class='form-label'>证书类型</label>
            <select v-model='form.scope' class='input' :disabled='isProtectedSystemRelayCA' @change='handleScopeChange'>
              <option value='domain'>域名证书</option>
              <option value='ip'>IP 证书</option>
            </select>
          </div>
          <div class='form-group'>
            <label class='form-label'>签发模式</label>
            <select v-model='form.issuer_mode' class='input' :disabled='form.scope === "ip" || isProtectedSystemRelayCA'>
              <option value='master_cf_dns'>Master 统一签发 (DNS)</option>
              <option value='local_http01'>节点本地签发</option>
            </select>
          </div>
        </div>

        <div class='form-row'>
          <div class='form-group'>
            <label class='form-label'>用途</label>
            <select v-model='form.usage' class='input' :disabled='isProtectedSystemRelayCA'>
              <option value='https'>HTTPS</option>
              <option value='relay_tunnel'>Relay 隧道</option>
              <option v-if='isProtectedSystemRelayCA' value='relay_ca'>系统 Relay CA</option>
              <option value='mixed'>混合用途</option>
            </select>
          </div>
          <div class='form-group'>
            <label class='form-label'>证书来源</label>
            <select v-model='form.certificate_type' class='input' :disabled='isProtectedSystemRelayCA'>
              <option value='acme'>自动签发</option>
              <option value='uploaded'>手动上传</option>
              <option value='internal_ca'>内部自签</option>
            </select>
          </div>
        </div>

        <label class='toggle-row'>
          <input v-model='form.self_signed' type='checkbox' class='toggle__input' :disabled='isProtectedSystemRelayCA'>
          <span class='toggle__slider'></span>
          <span class='toggle__label'>标记为自签名证书</span>
        </label>
      </div>
    </section>

    <p v-if='errors.submit' class='form-error form-error--block'>{{ errors.submit }}</p>

    <div v-if='isProtectedSystemRelayCA' class='cert-banner cert-banner--info'>
      系统 Relay CA 不提供前端保存或删除操作。
    </div>

    <button v-else type='submit' class='btn btn--primary btn--full' :disabled='isLoading'>
      {{ isEdit ? '保存修改' : '创建证书' }}
    </button>
  </form>
</template>

<script setup>
import { computed, reactive, ref } from 'vue'
import { useCreateCertificate, useUpdateCertificate } from '../hooks/useCertificates'
import {
  CERTIFICATE_TEMPLATES,
  applyCertificateTemplate,
  inferCertificateTemplate,
  isSystemManagedRelayListenerCertificate,
  isSystemRelayCA
} from '../utils/certificateTemplates'

const props = defineProps({
  initialData: { type: Object, default: null },
  agentId: { type: [String, Object], required: true }
})

const emit = defineEmits(['success'])

const createCertificate = useCreateCertificate(props.agentId)
const updateCertificate = useUpdateCertificate(props.agentId)
const isEdit = computed(() => !!props.initialData?.id)
const isLoading = computed(() => createCertificate.isPending.value || updateCertificate.isPending.value)

const selectedTemplate = ref(inferCertificateTemplate(props.initialData))
const showAdvanced = ref(!!props.initialData)
const form = ref(createInitialForm())
const uploadedMaterial = reactive({
  certificate_pem: props.initialData?.certificate_pem || '',
  private_key_pem: props.initialData?.private_key_pem || '',
  ca_pem: props.initialData?.ca_pem || ''
})
const tagInput = ref('')
const errors = reactive({
  domain: '',
  certificate_pem: '',
  private_key_pem: '',
  submit: ''
})
const isProtectedSystemRelayCA = computed(() => isSystemRelayCA(props.initialData))
const isSystemManagedRelayListener = computed(() => isSystemManagedRelayListenerCertificate(props.initialData))

function createInitialForm() {
  return {
    domain: props.initialData?.domain || '',
    scope: props.initialData?.scope || 'domain',
    issuer_mode: props.initialData?.issuer_mode || 'master_cf_dns',
    usage: props.initialData?.usage || 'https',
    certificate_type: props.initialData?.certificate_type || 'acme',
    self_signed: props.initialData?.self_signed === true,
    enabled: props.initialData?.enabled !== false,
    tags: Array.isArray(props.initialData?.tags) ? [...props.initialData.tags] : []
  }
}

function selectTemplate(templateId) {
  selectedTemplate.value = templateId
  form.value = applyCertificateTemplate(form.value, templateId)
  handleScopeChange()
}

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

function validateUploadedFields() {
  errors.certificate_pem = ''
  errors.private_key_pem = ''
  if (form.value.certificate_type !== 'uploaded') {
    return true
  }

  const certificatePEM = uploadedMaterial.certificate_pem.trim()
  const privateKeyPEM = uploadedMaterial.private_key_pem.trim()
  const caPEM = uploadedMaterial.ca_pem.trim()
  const hasAnyUploadValue = !!certificatePEM || !!privateKeyPEM || !!caPEM

  if (!isEdit.value && !certificatePEM) {
    errors.certificate_pem = '创建上传证书时必须提供证书 PEM'
  }
  if (!isEdit.value && !privateKeyPEM) {
    errors.private_key_pem = '创建上传证书时必须提供私钥 PEM'
  }
  if (hasAnyUploadValue && !certificatePEM) {
    errors.certificate_pem = '填写上传材料时必须同时提供证书 PEM'
  }
  if (hasAnyUploadValue && !privateKeyPEM) {
    errors.private_key_pem = '填写上传材料时必须同时提供私钥 PEM'
  }

  return !errors.certificate_pem && !errors.private_key_pem
}

async function handleSubmit() {
  if (isProtectedSystemRelayCA.value) {
    return
  }
  errors.domain = ''
  errors.submit = ''

  if (!form.value.domain.trim()) {
    errors.domain = '请输入域名或 IP'
    return
  }
  if (!validateUploadedFields()) {
    return
  }

  const payload = {
    ...form.value,
    domain: form.value.domain.trim(),
    certificate_pem: uploadedMaterial.certificate_pem.trim(),
    private_key_pem: uploadedMaterial.private_key_pem.trim(),
    ca_pem: uploadedMaterial.ca_pem.trim()
  }

  try {
    if (isEdit.value) {
      await updateCertificate.mutateAsync({ id: props.initialData.id, ...payload })
    } else {
      await createCertificate.mutateAsync(payload)
    }
    emit('success')
  } catch (err) {
    errors.submit = err?.message || '操作失败'
  }
}
</script>

<style scoped>
.cert-form {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.form-section {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  padding: var(--space-4);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-xl);
  background: var(--color-bg-subtle);
}

.form-section--compact {
  gap: var(--space-2);
}

.section-heading h3 {
  margin: 0;
  font-size: var(--text-base);
  color: var(--color-text-primary);
}

.section-heading p {
  margin: var(--space-1) 0 0;
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
}

.template-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: var(--space-3);
}

.template-card {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  padding: var(--space-3);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  background: var(--color-bg-surface);
  text-align: left;
  cursor: pointer;
  transition: border-color var(--duration-fast) var(--ease-default), transform var(--duration-fast) var(--ease-default);
}

.template-card strong {
  font-size: var(--text-sm);
  color: var(--color-text-primary);
}

.template-card span {
  font-size: var(--text-xs);
  color: var(--color-text-tertiary);
  line-height: 1.5;
}

.template-card:hover {
  transform: translateY(-1px);
  border-color: var(--color-primary);
}

.template-card--active {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.form-group {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
}

.form-row {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
  gap: var(--space-3);
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
  margin: 0;
  font-size: var(--text-xs);
  color: var(--color-danger);
}

.form-error--block {
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-md);
  background: var(--color-danger-50);
}

.input {
  width: 100%;
  min-width: 0;
  padding: var(--space-2) var(--space-3);
  font-size: var(--text-sm);
  color: var(--color-text-primary);
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  box-sizing: border-box;
  font-family: inherit;
}

.input--error {
  border-color: var(--color-danger);
}

.textarea {
  min-height: 132px;
  resize: vertical;
}

.textarea--compact {
  min-height: 96px;
}

.cert-banner {
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-md);
  font-size: var(--text-xs);
  line-height: 1.6;
}

.cert-banner--warn {
  background: var(--color-warning-50);
  color: var(--color-warning);
}

.cert-banner--info {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
}

.tag-input {
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
}

.tag-input__container {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  padding: var(--space-1) var(--space-2);
  min-height: 40px;
  align-items: center;
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

.tag {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  padding: 2px 8px;
  background: var(--color-bg-subtle);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-full);
  font-size: var(--text-xs);
}

.tag__remove {
  border: none;
  background: transparent;
  color: var(--color-text-muted);
  cursor: pointer;
  padding: 0;
}

.toggle-row {
  display: flex;
  align-items: center;
  gap: var(--space-3);
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
  transition: transform var(--duration-fast) var(--ease-default);
}

.toggle__input:checked + .toggle__slider {
  background: var(--gradient-primary);
}

.toggle__input:checked + .toggle__slider::after {
  transform: translateX(20px);
}

.toggle__label {
  font-size: var(--text-sm);
  color: var(--color-text-primary);
}

.advanced-toggle {
  align-self: flex-start;
  border: 1px solid var(--color-border-default);
  background: var(--color-bg-surface);
  border-radius: var(--radius-md);
  padding: var(--space-2) var(--space-3);
  font-size: var(--text-sm);
  cursor: pointer;
}

.advanced-panel {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  padding-top: var(--space-2);
}

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
  font-family: inherit;
}

.btn--primary {
  background: var(--gradient-primary);
  color: white;
}

.btn--full {
  width: 100%;
}

.btn:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}
</style>

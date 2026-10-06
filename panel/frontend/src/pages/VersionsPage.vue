<template>
  <div class='versions-page'>
    <div class='versions-page__header'>
      <div>
        <h1 class='versions-page__title'>版本策略</h1>
        <p class='versions-page__subtitle'>管理各发布通道的目标版本与下载包</p>
      </div>
      <button class='btn btn-primary' @click='openCreate'>新增策略</button>
    </div>

    <OperationStatusList />

    <QuickAgentSelect
      :agentId="selectedAgentId"
      :agents="allAgents"
      @update:agentId="selectAgent"
    />

    <SkeletonList v-if='isLoading' variant='rows' :count='4' label='版本策略加载中' />

    <div v-else-if='isLoadError' role='alert'>
      <EmptyState title='读取失败' :description='loadErrorText'>
        <template #icon>
          <svg width='40' height='40' viewBox='0 0 24 24' fill='none' stroke='currentColor' stroke-width='1.5'>
            <circle cx='12' cy='12' r='10'/>
            <line x1='12' y1='8' x2='12' y2='12'/>
            <line x1='12' y1='16' x2='12.01' y2='16'/>
          </svg>
        </template>
        <template #action>
          <button class='btn btn-secondary' type='button' @click='refetchPolicies'>重试</button>
        </template>
      </EmptyState>
    </div>

    <template v-else>
      <div v-if='policies.length' class='versions-page__toolbar'>
        <input
          v-model='filterText'
          class='input versions-page__search'
          type='search'
          placeholder='搜索通道 / 目标版本 / 标签'
          aria-label='搜索版本策略'
        >
      </div>

      <EmptyState v-if='!policies.length' compact title='暂无版本策略' description='点击「新增策略」创建第一个发布通道。'>
        <template #icon>
          <svg width='40' height='40' viewBox='0 0 24 24' fill='none' stroke='currentColor' stroke-width='1.5'>
            <path d='M21 8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z'/>
            <path d='m3.3 7 8.7 5 8.7-5'/>
            <path d='M12 22V12'/>
          </svg>
        </template>
      </EmptyState>

      <EmptyState
        v-else-if='!filteredPolicies.length'
        compact
        title='没有匹配的版本策略'
        description='调整搜索词后再试。'
      >
        <template #icon>
          <svg width='40' height='40' viewBox='0 0 24 24' fill='none' stroke='currentColor' stroke-width='1.5'>
            <circle cx='11' cy='11' r='8'/>
            <line x1='21' y1='21' x2='16.65' y2='16.65'/>
          </svg>
        </template>
      </EmptyState>

      <div v-else class='versions-grid'>
        <article v-for='policy in filteredPolicies' :key='policy.id' class='version-card'>
          <div class='version-card__header'>
            <div>
              <h3 class='version-card__channel'>{{ policy.channel }}</h3>
              <p class='version-card__desired'>目标版本: {{ policy.desired_version || '-' }}</p>
            </div>
            <div class='version-card__actions'>
              <button class='icon-btn' @click='openEdit(policy)'>编辑</button>
              <button class='icon-btn icon-btn--danger' @click='startDelete(policy)'>删除</button>
            </div>
          </div>

          <ul class='package-list'>
            <li v-for='(pkg, index) in policy.packages || []' :key='`${policy.id}-${index}`' class='package-item'>
              <span>{{ pkg.platform }}</span>
              <a :href='pkg.url' target='_blank' rel='noreferrer'>包地址</a>
            </li>
          </ul>

          <div class='version-card__tags'>
            <span v-for='tag in policy.tags || []' :key='tag' class='tag'>{{ tag }}</span>
          </div>
        </article>
      </div>
    </template>

    <BaseModal
      :model-value='showForm'
      :title="editingPolicy?.id ? '编辑版本策略' : '新增版本策略'"
      size='xl'
      :close-on-click-modal='false'
      data-test='policy-form-modal'
      @update:model-value='closeForm'
    >
      <form class='policy-form' @submit.prevent='submitPolicy'>
        <div class='form-row'>
          <div class='form-group'>
            <label class='form-label form-label--required' for='policy-channel'>通道</label>
            <input
              id='policy-channel'
              v-model='form.channel'
              class='input'
              :class="{ 'input--error': errors.channel }"
              placeholder='stable'
              :aria-invalid="errors.channel ? 'true' : undefined"
              :aria-describedby="errors.channel ? 'policy-channel-error' : undefined"
              @blur='validateTextField("channel")'
            >
            <FieldError v-if='errors.channel' id='policy-channel-error'>{{ errors.channel }}</FieldError>
          </div>
          <div class='form-group'>
            <label class='form-label form-label--required' for='policy-version'>目标版本</label>
            <input
              id='policy-version'
              v-model='form.desired_version'
              class='input'
              :class="{ 'input--error': errors.desired_version }"
              placeholder='1.2.3'
              :aria-invalid="errors.desired_version ? 'true' : undefined"
              :aria-describedby="errors.desired_version ? 'policy-version-error' : undefined"
              @blur='validateTextField("desired_version")'
            >
            <FieldError v-if='errors.desired_version' id='policy-version-error'>{{ errors.desired_version }}</FieldError>
          </div>
        </div>

        <div class='form-group'>
          <div class='form-group__header'>
            <label class='form-label'>安装包</label>
            <button type='button' class='btn btn-secondary btn-sm' @click='addPackage'>添加包</button>
          </div>
          <div class='package-edit-list'>
            <div v-for='(pkg, index) in form.packages' :key='`edit-${index}`' class='package-edit-item'>
              <div class='package-edit-field'>
                <input
                  v-model='pkg.platform'
                  class='input'
                  :class="{ 'input--error': packageErrors[index]?.platform }"
                  :aria-invalid="packageErrors[index]?.platform ? 'true' : undefined"
                  :aria-describedby="packageErrors[index]?.platform ? `policy-pkg-${index}-platform-error` : undefined"
                  :aria-label="`安装包 ${index + 1} 平台`"
                  placeholder='linux-amd64'
                  @blur='validatePackageField(index, "platform")'
                >
                <FieldError v-if='packageErrors[index]?.platform' :id="`policy-pkg-${index}-platform-error`">{{ packageErrors[index].platform }}</FieldError>
              </div>
              <input v-model='pkg.filename' class='input' :aria-label="`安装包 ${index + 1} 文件名`" placeholder='文件名（可自动推导）'>
              <div class='package-edit-field'>
                <input
                  v-model='pkg.size'
                  class='input'
                  :class="{ 'input--error': packageErrors[index]?.size }"
                  :aria-invalid="packageErrors[index]?.size ? 'true' : undefined"
                  :aria-describedby="packageErrors[index]?.size ? `policy-pkg-${index}-size-error` : undefined"
                  :aria-label="`安装包 ${index + 1} 字节数`"
                  inputmode='numeric'
                  placeholder='字节数（可自动推导）'
                  @blur='validatePackageField(index, "size")'
                >
                <FieldError v-if='packageErrors[index]?.size' :id="`policy-pkg-${index}-size-error`">{{ packageErrors[index].size }}</FieldError>
              </div>
              <button type='button' class='icon-btn icon-btn--danger' @click='removePackage(index)'>删除</button>
              <div class='package-edit-field package-edit-item__url'>
                <input
                  v-model='pkg.url'
                  class='input'
                  :class="{ 'input--error': packageErrors[index]?.url }"
                  :aria-invalid="packageErrors[index]?.url ? 'true' : undefined"
                  :aria-describedby="packageErrors[index]?.url ? `policy-pkg-${index}-url-error` : undefined"
                  :aria-label="`安装包 ${index + 1} 下载地址`"
                  placeholder='https://...'
                  @blur='validatePackageField(index, "url")'
                >
                <FieldError v-if='packageErrors[index]?.url' :id="`policy-pkg-${index}-url-error`">{{ packageErrors[index].url }}</FieldError>
              </div>
              <div class='package-edit-field package-edit-item__sha'>
                <input
                  v-model='pkg.sha256'
                  class='input'
                  :class="{ 'input--error': packageErrors[index]?.sha256 }"
                  :aria-invalid="packageErrors[index]?.sha256 ? 'true' : undefined"
                  :aria-describedby="packageErrors[index]?.sha256 ? `policy-pkg-${index}-sha256-error` : undefined"
                  :aria-label="`安装包 ${index + 1} sha256`"
                  placeholder='sha256'
                  @blur='validatePackageField(index, "sha256")'
                >
                <FieldError v-if='packageErrors[index]?.sha256' :id="`policy-pkg-${index}-sha256-error`">{{ packageErrors[index].sha256 }}</FieldError>
              </div>
            </div>
          </div>
          <FieldError v-if='errors.packages'>{{ errors.packages }}</FieldError>
        </div>

        <div class='form-group'>
          <label class='form-label' for='policy-tags'>标签（逗号分隔）</label>
          <input id='policy-tags' v-model='tagsText' class='input' placeholder='rollout, canary'>
        </div>

        <FieldError v-if='errors.submit' block>{{ errors.submit }}</FieldError>

        <div class='policy-form__footer'>
          <button type='button' class='btn btn-secondary' @click='closeForm'>取消</button>
          <button type='submit' class='btn btn-primary' :disabled='isMutating'>保存</button>
        </div>
      </form>
    </BaseModal>

    <DeleteConfirmDialog
      :show='!!deletingPolicy'
      title='确认删除策略'
      message='删除后该策略将立即失效，相关配置将无法恢复。'
      :name='deletingPolicy?.channel'
      confirm-text='确认删除'
      :loading='deletePolicy.isPending?.value'
      :error='deleteError'
      @confirm='confirmDelete'
      @cancel='deletingPolicy = null'
    />
  </div>
</template>

<script setup>
import { computed, ref } from 'vue'
import {
  useVersionPolicies,
  useCreateVersionPolicy,
  useUpdateVersionPolicy,
  useDeleteVersionPolicy
} from '../hooks/useVersionPolicies'
import { useAgents } from '../hooks/useAgents'
import { useAgent } from '../context/AgentContext'
import DeleteConfirmDialog from '../components/DeleteConfirmDialog.vue'
import QuickAgentSelect from '../components/QuickAgentSelect.vue'
import OperationStatusList from '../components/operations/OperationStatusList.vue'
import SkeletonList from '../components/base/SkeletonList.vue'
import EmptyState from '../components/base/EmptyState.vue'
import BaseModal from '../components/base/BaseModal.vue'
import FieldError from '../components/base/FieldError.vue'

const { data: policiesData, isLoading, isError, error: loadError, refetch: refetchPolicies } = useVersionPolicies()
const createPolicy = useCreateVersionPolicy()
const updatePolicy = useUpdateVersionPolicy()
const deletePolicy = useDeleteVersionPolicy()

const { data: agentsData } = useAgents()
const allAgents = computed(() => agentsData.value ?? [])
const { selectedAgentId, selectAgent } = useAgent()

const policies = computed(() => policiesData.value ?? [])
const isLoadError = computed(() => isError.value && !policies.value.length)
const loadErrorText = computed(() => loadError.value?.message || '版本策略读取失败，请稍后重试')
const isMutating = computed(() => createPolicy.isPending.value || updatePolicy.isPending.value)

const filterText = ref('')
const filteredPolicies = computed(() => {
  const query = filterText.value.trim().toLowerCase()
  if (!query) return policies.value
  return policies.value.filter((policy) => {
    const channel = String(policy.channel || '').toLowerCase()
    const version = String(policy.desired_version || '').toLowerCase()
    const tags = (policy.tags || []).map((tag) => String(tag).toLowerCase())
    return channel.includes(query) || version.includes(query) || tags.some((tag) => tag.includes(query))
  })
})

const showForm = ref(false)
const editingPolicy = ref(null)
const deletingPolicy = ref(null)
const deleteError = ref('')

const form = ref(createDefaultForm())
const tagsText = ref('')
const errors = ref({ channel: '', desired_version: '', packages: '', submit: '' })
const packageErrors = ref([])

function createDefaultForm() {
  return {
    channel: '',
    desired_version: '',
    packages: []
  }
}

function openCreate() {
  editingPolicy.value = null
  form.value = createDefaultForm()
  tagsText.value = ''
  packageErrors.value = []
  errors.value = { channel: '', desired_version: '', packages: '', submit: '' }
  showForm.value = true
}

function openEdit(policy) {
  editingPolicy.value = policy
  form.value = {
    channel: policy.channel || '',
    desired_version: policy.desired_version || '',
    packages: Array.isArray(policy.packages)
      ? policy.packages.map((pkg) => ({
          platform: pkg.platform || '',
          url: pkg.url || '',
          sha256: pkg.sha256 || '',
          filename: pkg.filename || '',
          size: pkg.size > 0 ? String(pkg.size) : ''
        }))
      : []
  }
  tagsText.value = Array.isArray(policy.tags) ? policy.tags.join(', ') : ''
  packageErrors.value = form.value.packages.map(() => ({ platform: '', url: '', sha256: '', size: '' }))
  errors.value = { channel: '', desired_version: '', packages: '', submit: '' }
  showForm.value = true
}

function closeForm() {
  showForm.value = false
  editingPolicy.value = null
}

function addPackage() {
  form.value.packages.push({ platform: '', url: '', sha256: '', filename: '', size: '' })
  packageErrors.value.push({ platform: '', url: '', sha256: '', size: '' })
}

function removePackage(index) {
  form.value.packages.splice(index, 1)
  packageErrors.value.splice(index, 1)
}

function ensurePackageErrorSlot(index) {
  while (packageErrors.value.length <= index) {
    packageErrors.value.push({ platform: '', url: '', sha256: '', size: '' })
  }
}

function validateTextField(field) {
  if (field === 'channel') {
    errors.value.channel = form.value.channel.trim() ? '' : '请输入通道名'
  }
  if (field === 'desired_version') {
    errors.value.desired_version = form.value.desired_version.trim() ? '' : '请输入目标版本'
  }
}

function packageFieldError(pkg, field) {
  const value = String(pkg?.[field] || '').trim()
  if (field === 'size') {
    if (!value) return ''
    return (!/^\d+$/.test(value) || Number(value) <= 0 || !Number.isSafeInteger(Number(value)))
      ? '字节数必须是正整数'
      : ''
  }
  return value ? '' : `缺少 ${field}`
}

function validatePackageField(index, field) {
  ensurePackageErrorSlot(index)
  const pkg = form.value.packages[index]
  if (!pkg) return
  packageErrors.value[index] = {
    ...packageErrors.value[index],
    [field]: packageFieldError(pkg, field)
  }
}

function validateForm() {
  errors.value = { channel: '', desired_version: '', packages: '', submit: '' }
  packageErrors.value = form.value.packages.map(() => ({ platform: '', url: '', sha256: '', size: '' }))

  validateTextField('channel')
  validateTextField('desired_version')

  let hasPackageError = false
  form.value.packages.forEach((pkg, index) => {
    for (const field of ['platform', 'url', 'sha256', 'size']) {
      const fieldError = packageFieldError(pkg, field)
      if (fieldError) {
        hasPackageError = true
        packageErrors.value[index][field] = fieldError
      }
    }
  })
  if (hasPackageError) {
    errors.value.packages = '请补全必填字段并修正安装包信息'
  }

  return !errors.value.channel && !errors.value.desired_version && !errors.value.packages
}

async function submitPolicy() {
  if (!validateForm()) return

  const payload = {
    channel: form.value.channel.trim(),
    desired_version: form.value.desired_version.trim(),
    packages: form.value.packages
      .map((pkg) => ({
        platform: String(pkg.platform || '').trim(),
        url: String(pkg.url || '').trim(),
        sha256: String(pkg.sha256 || '').trim(),
        filename: String(pkg.filename || '').trim(),
        size: String(pkg.size || '').trim() ? Number(pkg.size) : 0
      })),
    tags: tagsText.value
      .split(',')
      .map((tag) => tag.trim())
      .filter(Boolean)
  }

  try {
    if (editingPolicy.value?.id) {
      await updatePolicy.mutateAsync({ id: editingPolicy.value.id, ...payload })
    } else {
      await createPolicy.mutateAsync(payload)
    }
    closeForm()
  } catch {
    // API failures are toasted by useVersionPolicies.
  }
}

function startDelete(policy) {
  deletingPolicy.value = policy
  deleteError.value = ''
}

async function confirmDelete() {
  if (!deletingPolicy.value) return
  try {
    await deletePolicy.mutateAsync(deletingPolicy.value.id)
    deleteError.value = ''
    deletingPolicy.value = null
  } catch (error) {
    // Keep the dialog open with the failure reason; the hook also toasts.
    deleteError.value = error?.message || '删除失败，请稍后重试'
  }
}
</script>

<style scoped>
.versions-page {
  max-width: 1200px;
  margin: 0 auto;
}

.versions-page__header {
  display: flex;
  justify-content: space-between;
  gap: var(--space-3);
  align-items: center;
  margin-bottom: var(--space-6);
}

.versions-page__title {
  margin: 0 0 0.15rem;
  font-size: 1.3125rem;
  font-weight: 700;
  color: var(--color-text-primary);
  letter-spacing: -0.02em;
  line-height: 1.25;
}

.versions-page__subtitle {
  margin: 0;
  color: var(--color-text-tertiary);
  font-size: var(--text-sm);
}

.versions-page__toolbar {
  display: flex;
  justify-content: flex-end;
  margin-bottom: var(--space-3);
}

.versions-page__search {
  max-width: 260px;
}

.versions-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: var(--space-4);
}

.version-card {
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  background: var(--color-bg-surface);
  padding: var(--space-4);
}

.version-card__header {
  display: flex;
  justify-content: space-between;
  gap: var(--space-2);
}

.version-card__channel {
  margin: 0;
  font-size: var(--text-base);
}

.version-card__desired {
  margin: var(--space-1) 0 0;
  font-size: var(--text-xs);
  color: var(--color-text-muted);
}

.version-card__actions {
  display: flex;
  gap: var(--space-1);
}

.package-list {
  margin: var(--space-3) 0 0;
  padding-left: var(--space-4);
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.package-item {
  font-size: var(--text-xs);
  color: var(--color-text-secondary);
}

.version-card__tags {
  margin-top: var(--space-3);
  display: flex;
  gap: var(--space-2);
  flex-wrap: wrap;
}

.tag {
  font-size: var(--text-xs);
  padding: var(--space-0-5) var(--space-2);
  border-radius: var(--radius-full);
  background: var(--color-primary-subtle);
  color: var(--color-primary);
}

.policy-form {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.form-row {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-3);
}

.form-group {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.form-group__header {
  display: flex;
  justify-content: space-between;
  gap: var(--space-2);
  align-items: center;
}

.form-label {
  font-size: var(--text-sm);
  color: var(--color-text-secondary);
  font-weight: var(--font-medium);
}

.form-label--required::after {
  content: ' *';
  color: var(--color-danger);
}

.input {
  width: 100%;
  min-width: 0;
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-surface);
  color: var(--color-text-primary);
  font-size: var(--text-sm);
  box-sizing: border-box;
}

.input--error {
  border-color: var(--color-danger);
}

.package-edit-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.package-edit-item {
  display: grid;
  grid-template-columns: 0.9fr 1.4fr 0.9fr auto;
  gap: var(--space-2);
  align-items: start;
}

.package-edit-field {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  min-width: 0;
}

.package-edit-item__url {
  grid-column: 1 / 3;
}

.package-edit-item__sha {
  grid-column: 3 / 5;
}

.btn-sm {
  padding: var(--space-1) var(--space-3);
  font-size: var(--text-xs);
}

.icon-btn {
  border: 1px solid var(--color-border-default);
  background: var(--color-bg-surface);
  border-radius: var(--radius-sm);
  padding: var(--space-0-5) var(--space-2);
  font-size: var(--text-xs);
  cursor: pointer;
}

.icon-btn--danger {
  color: var(--color-danger);
}

.policy-form__footer {
  padding-top: var(--space-3);
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
}

@media (max-width: 900px) {
  .form-row {
    grid-template-columns: 1fr;
  }

  .package-edit-item {
    grid-template-columns: 1fr;
  }

  .package-edit-item__url,
  .package-edit-item__sha {
    grid-column: auto;
  }
}
/* Wide-screen (2K/4K) width steps */
@media (min-width: 1920px) {
  .versions-page { max-width: 1600px; }
}
@media (min-width: 2560px) {
  .versions-page { max-width: 2000px; }
}
</style>

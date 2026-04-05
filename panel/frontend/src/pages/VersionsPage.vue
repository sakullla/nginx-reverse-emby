<template>
  <div class='versions-page'>
    <div class='versions-page__header'>
      <div>
        <h1 class='versions-page__title'>版本策略</h1>
        <p class='versions-page__subtitle'>管理各发布通道的目标版本与下载包</p>
      </div>
      <button class='btn btn-primary' @click='openCreate'>新增策略</button>
    </div>

    <div v-if='isLoading' class='versions-page__empty'>加载中...</div>
    <div v-else-if='!policies.length' class='versions-page__empty'>暂无版本策略</div>

    <div v-else class='versions-grid'>
      <article v-for='policy in policies' :key='policy.id' class='version-card'>
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

    <Teleport to='body'>
      <div v-if='showForm' class='modal-overlay'>
        <div class='modal modal--large'>
          <div class='modal__header'>
            <span>{{ editingPolicy?.id ? '编辑版本策略' : '新增版本策略' }}</span>
            <button class='modal__close' @click='closeForm'>✕</button>
          </div>
          <div class='modal__body'>
            <form class='policy-form' @submit.prevent='submitPolicy'>
              <div class='form-row'>
                <div class='form-group'>
                  <label class='form-label form-label--required'>通道</label>
                  <input v-model='form.channel' class='input' :class="{ 'input--error': errors.channel }" placeholder='stable'>
                  <p v-if='errors.channel' class='form-error'>{{ errors.channel }}</p>
                </div>
                <div class='form-group'>
                  <label class='form-label'>目标版本</label>
                  <input v-model='form.desired_version' class='input' placeholder='1.2.3'>
                </div>
              </div>

              <div class='form-group'>
                <div class='form-group__header'>
                  <label class='form-label'>安装包</label>
                  <button type='button' class='btn btn-secondary btn-sm' @click='addPackage'>添加包</button>
                </div>
                <div class='package-edit-list'>
                  <div v-for='(pkg, index) in form.packages' :key='`edit-${index}`' class='package-edit-item'>
                    <input v-model='pkg.platform' class='input' placeholder='linux-amd64'>
                    <input v-model='pkg.url' class='input' placeholder='https://...'>
                    <input v-model='pkg.sha256' class='input' placeholder='sha256'>
                    <button type='button' class='icon-btn icon-btn--danger' @click='removePackage(index)'>删除</button>
                  </div>
                </div>
              </div>

              <div class='form-group'>
                <label class='form-label'>标签（逗号分隔）</label>
                <input v-model='tagsText' class='input' placeholder='rollout, canary'>
              </div>

              <div class='modal__footer'>
                <button type='button' class='btn btn-secondary' @click='closeForm'>取消</button>
                <button type='submit' class='btn btn-primary' :disabled='isMutating'>保存</button>
              </div>
            </form>
          </div>
        </div>
      </div>
    </Teleport>

    <Teleport to='body'>
      <div v-if='deletingPolicy' class='modal-overlay' @click.self='deletingPolicy = null'>
        <div class='modal'>
          <div class='modal__header'>确认删除</div>
          <div class='modal__body'>
            <p>确定删除策略 <strong>{{ deletingPolicy.channel }}</strong> 吗？</p>
          </div>
          <div class='modal__footer'>
            <button class='btn btn-secondary' @click='deletingPolicy = null'>取消</button>
            <button class='btn btn-danger' @click='confirmDelete'>删除</button>
          </div>
        </div>
      </div>
    </Teleport>
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

const { data: policiesData, isLoading } = useVersionPolicies()
const createPolicy = useCreateVersionPolicy()
const updatePolicy = useUpdateVersionPolicy()
const deletePolicy = useDeleteVersionPolicy()

const policies = computed(() => policiesData.value ?? [])
const isMutating = computed(() => createPolicy.isPending.value || updatePolicy.isPending.value)

const showForm = ref(false)
const editingPolicy = ref(null)
const deletingPolicy = ref(null)

const form = ref(createDefaultForm())
const tagsText = ref('')
const errors = ref({ channel: '' })

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
  errors.value.channel = ''
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
          sha256: pkg.sha256 || ''
        }))
      : []
  }
  tagsText.value = Array.isArray(policy.tags) ? policy.tags.join(', ') : ''
  errors.value.channel = ''
  showForm.value = true
}

function closeForm() {
  showForm.value = false
  editingPolicy.value = null
}

function addPackage() {
  form.value.packages.push({ platform: '', url: '', sha256: '' })
}

function removePackage(index) {
  form.value.packages.splice(index, 1)
}

async function submitPolicy() {
  errors.value.channel = ''
  if (!form.value.channel.trim()) {
    errors.value.channel = '请输入通道名'
    return
  }

  const payload = {
    channel: form.value.channel.trim(),
    desired_version: form.value.desired_version.trim(),
    packages: form.value.packages
      .map((pkg) => ({
        platform: String(pkg.platform || '').trim(),
        url: String(pkg.url || '').trim(),
        sha256: String(pkg.sha256 || '').trim()
      }))
      .filter((pkg) => pkg.platform || pkg.url || pkg.sha256),
    tags: tagsText.value
      .split(',')
      .map((tag) => tag.trim())
      .filter(Boolean)
  }

  if (editingPolicy.value?.id) {
    await updatePolicy.mutateAsync({ id: editingPolicy.value.id, ...payload })
  } else {
    await createPolicy.mutateAsync(payload)
  }

  closeForm()
}

function startDelete(policy) {
  deletingPolicy.value = policy
}

function confirmDelete() {
  if (!deletingPolicy.value) return
  deletePolicy.mutate(deletingPolicy.value.id)
  deletingPolicy.value = null
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
  margin: 0;
  font-size: 1.5rem;
}

.versions-page__subtitle {
  margin: 0;
  color: var(--color-text-tertiary);
  font-size: var(--text-sm);
}

.versions-page__empty {
  padding: var(--space-8);
  text-align: center;
  color: var(--color-text-muted);
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
  padding: 2px 8px;
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

.form-error {
  margin: 0;
  color: var(--color-danger);
  font-size: var(--text-xs);
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
  grid-template-columns: 1fr 1.5fr 1fr auto;
  gap: var(--space-2);
}

.btn {
  border: none;
  border-radius: var(--radius-md);
  padding: var(--space-2) var(--space-4);
  cursor: pointer;
}

.btn-primary {
  background: var(--gradient-primary);
  color: white;
}

.btn-secondary {
  background: var(--color-bg-subtle);
  border: 1px solid var(--color-border-default);
}

.btn-danger {
  background: var(--color-danger);
  color: white;
}

.btn-sm {
  padding: var(--space-1) var(--space-3);
  font-size: var(--text-xs);
}

.icon-btn {
  border: 1px solid var(--color-border-default);
  background: var(--color-bg-surface);
  border-radius: var(--radius-sm);
  padding: 2px 8px;
  font-size: var(--text-xs);
  cursor: pointer;
}

.icon-btn--danger {
  color: var(--color-danger);
}

.modal-overlay {
  position: fixed;
  inset: 0;
  background: rgba(37, 23, 54, 0.4);
  backdrop-filter: blur(8px);
  z-index: var(--z-modal);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--space-4);
}

.modal {
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-3xl);
  width: min(480px, 90vw);
  max-height: calc(100vh - var(--space-8));
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.modal--large {
  width: min(900px, 95vw);
}

.modal__header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: var(--space-5) var(--space-6);
  border-bottom: 1px solid var(--color-border-subtle);
}

.modal__body {
  padding: var(--space-6);
  overflow: auto;
}

.modal__footer {
  padding-top: var(--space-3);
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
}

.modal__close {
  border: none;
  background: transparent;
  cursor: pointer;
}

@media (max-width: 900px) {
  .form-row {
    grid-template-columns: 1fr;
  }

  .package-edit-item {
    grid-template-columns: 1fr;
  }
}
</style>

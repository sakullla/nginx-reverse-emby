<template>
  <div class="client-packages-page">
    <div class="client-packages-page__header">
      <div>
        <h1 class="client-packages-page__title">客户端发布包</h1>
        <p class="client-packages-page__subtitle">
          {{ packages.length }} 个记录 · GitHub Release / 仓库 URL 分发 · Docker 镜像不内置客户端包
        </p>
      </div>
      <button class="btn btn-primary" @click="openCreate">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
          <line x1="12" y1="5" x2="12" y2="19" />
          <line x1="5" y1="12" x2="19" y2="12" />
        </svg>
        新增发布包
      </button>
    </div>

    <section class="worker-panel">
      <div class="section-heading">
        <div>
          <h2>Cloudflare Worker 部署</h2>
          <p>选择已发布的 Worker 脚本包，生成 Wrangler 命令和环境变量。</p>
        </div>
      </div>

      <div class="worker-panel__grid">
        <div class="form-group">
          <label class="form-label">Worker 脚本包</label>
          <select v-model="workerForm.packageId" class="input">
            <option value="">自动选择最新脚本包</option>
            <option v-for="pkg in workerPackages" :key="pkg.id" :value="pkg.id">
              {{ pkg.version }} · {{ pkg.download_url }}
            </option>
          </select>
          <p v-if="workerErrors.packageRecord" class="form-error">{{ workerErrors.packageRecord }}</p>
        </div>
        <div class="form-group">
          <label class="form-label form-label--required">Worker 名称</label>
          <input
            v-model="workerForm.workerName"
            name="worker-name"
            class="input"
            :class="{ 'input--error': workerErrors.workerName }"
            placeholder="nre-edge"
          >
          <p v-if="workerErrors.workerName" class="form-error">{{ workerErrors.workerName }}</p>
        </div>
        <div class="form-group">
          <label class="form-label form-label--required">Master URL</label>
          <input
            v-model="workerForm.masterUrl"
            name="worker-master-url"
            class="input"
            :class="{ 'input--error': workerErrors.masterUrl }"
            placeholder="https://panel.example.com"
          >
          <p v-if="workerErrors.masterUrl" class="form-error">{{ workerErrors.masterUrl }}</p>
        </div>
        <div class="form-group">
          <label class="form-label form-label--required">Worker 访问令牌</label>
          <input
            v-model="workerForm.token"
            name="worker-token"
            class="input"
            :class="{ 'input--error': workerErrors.token }"
            type="password"
            autocomplete="off"
            placeholder="worker-token"
          >
          <p v-if="workerErrors.token" class="form-error">{{ workerErrors.token }}</p>
        </div>
      </div>

      <div class="worker-panel__actions">
        <button class="btn btn-primary" data-testid="build-worker-command" @click="buildWorkerCommand">
          生成部署命令
        </button>
        <button class="btn btn-secondary" :disabled="!workerDeployModel" @click="copyWorkerCommand">
          {{ workerCopied ? '已复制' : '复制命令' }}
        </button>
      </div>
      <p v-if="workerErrors.submit" class="form-error worker-panel__error">{{ workerErrors.submit }}</p>

      <div v-if="workerDeployModel" class="deploy-output">
        <div>
          <span class="deploy-output__label">环境变量命令</span>
          <pre>{{ workerDeployModel.envCommands.join('\n') }}</pre>
        </div>
        <div>
          <span class="deploy-output__label">Wrangler 命令</span>
          <pre>{{ workerDeployModel.secretCommands.join('\n') }}
{{ workerDeployModel.command }}</pre>
        </div>
        <div>
          <span class="deploy-output__label">下载与校验</span>
          <pre>{{ workerDeployModel.downloadCommand }}
{{ workerDeployModel.checksumCommand }}
sha256: {{ workerDeployModel.sha256 }}</pre>
        </div>
      </div>
    </section>

    <div v-if="isLoading" class="client-packages-page__empty">加载中...</div>
    <div v-else-if="!packages.length" class="client-packages-page__empty">暂无客户端发布包</div>

    <section v-else class="packages-table-wrap">
      <table class="packages-table">
        <thead>
          <tr>
            <th>类型</th>
            <th>平台 / 架构</th>
            <th>版本</th>
            <th>下载地址</th>
            <th>SHA256</th>
            <th>说明</th>
            <th>操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="pkg in packages" :key="pkg.id">
            <td>
              <span class="kind-badge">{{ kindLabel(pkg.kind) }}</span>
            </td>
            <td>{{ pkg.platform }} / {{ pkg.arch }}</td>
            <td>{{ pkg.version }}</td>
            <td>
              <a :href="pkg.download_url" target="_blank" rel="noreferrer">{{ compactUrl(pkg.download_url) }}</a>
            </td>
            <td><code>{{ compactSha(pkg.sha256) }}</code></td>
            <td>{{ pkg.notes || '-' }}</td>
            <td>
              <div class="table-actions">
                <button class="icon-btn" @click="openEdit(pkg)">编辑</button>
                <button class="icon-btn icon-btn--danger" @click="startDelete(pkg)">删除</button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </section>

    <Teleport to="body">
      <div v-if="showForm" class="modal-overlay">
        <div class="modal modal--large">
          <div class="modal__header">
            <span>{{ editingPackage?.id ? '编辑客户端发布包' : '新增客户端发布包' }}</span>
            <button class="modal__close" @click="closeForm">x</button>
          </div>
          <div class="modal__body">
            <form class="package-form" @submit.prevent="submitPackage">
              <div class="form-row form-row--three">
                <div class="form-group">
                  <label class="form-label form-label--required">类型</label>
                  <select v-model="form.kind" class="input" :class="{ 'input--error': errors.kind }">
                    <option value="flutter_gui">Flutter GUI</option>
                    <option value="go_agent">Go Agent</option>
                    <option value="worker_script">Worker Script</option>
                  </select>
                  <p v-if="errors.kind" class="form-error">{{ errors.kind }}</p>
                </div>
                <div class="form-group">
                  <label class="form-label form-label--required">平台</label>
                  <select v-model="form.platform" class="input" :class="{ 'input--error': errors.platform }">
                    <option value="windows">Windows</option>
                    <option value="macos">macOS</option>
                    <option value="android">Android</option>
                    <option value="cloudflare_worker">Cloudflare Worker</option>
                  </select>
                  <p v-if="errors.platform" class="form-error">{{ errors.platform }}</p>
                </div>
                <div class="form-group">
                  <label class="form-label form-label--required">架构</label>
                  <select v-model="form.arch" class="input" :class="{ 'input--error': errors.arch }">
                    <option value="amd64">amd64</option>
                    <option value="arm64">arm64</option>
                    <option value="universal">universal</option>
                    <option value="script">script</option>
                  </select>
                  <p v-if="errors.arch" class="form-error">{{ errors.arch }}</p>
                </div>
              </div>

              <div class="form-row">
                <div class="form-group">
                  <label class="form-label form-label--required">版本</label>
                  <input v-model="form.version" class="input" :class="{ 'input--error': errors.version }" placeholder="1.1.0">
                  <p v-if="errors.version" class="form-error">{{ errors.version }}</p>
                </div>
                <div class="form-group">
                  <label class="form-label">说明</label>
                  <input v-model="form.notes" class="input" placeholder="Windows Flutter GUI">
                </div>
              </div>

              <div class="form-group">
                <label class="form-label form-label--required">下载 URL</label>
                <input
                  v-model="form.download_url"
                  class="input"
                  :class="{ 'input--error': errors.download_url }"
                  placeholder="https://github.com/.../releases/download/..."
                >
                <p v-if="errors.download_url" class="form-error">{{ errors.download_url }}</p>
              </div>

              <div class="form-group">
                <label class="form-label form-label--required">SHA256</label>
                <input v-model="form.sha256" class="input" :class="{ 'input--error': errors.sha256 }" placeholder="64 位十六进制">
                <p v-if="errors.sha256" class="form-error">{{ errors.sha256 }}</p>
              </div>

              <p v-if="errors.submit" class="form-error">{{ errors.submit }}</p>

              <div class="modal__footer">
                <button type="button" class="btn btn-secondary" @click="closeForm">取消</button>
                <button type="submit" class="btn btn-primary" :disabled="isMutating">保存</button>
              </div>
            </form>
          </div>
        </div>
      </div>
    </Teleport>

    <DeleteConfirmDialog
      :show="!!deletingPackage"
      title="确认删除发布包"
      message="删除后该发布记录将不再用于客户端更新和 Worker 部署。"
      :name="deletingPackage?.id"
      confirm-text="确认删除"
      :loading="deletePackage.isPending?.value"
      @confirm="confirmDelete"
      @cancel="deletingPackage = null"
    />
  </div>
</template>

<script setup>
import { computed, reactive, ref } from 'vue'
import {
  useClientPackages,
  useCreateClientPackage,
  useUpdateClientPackage,
  useDeleteClientPackage
} from '../hooks/useClientPackages'
import { compareClientPackageVersions } from '../utils/clientPackageVersions'
import { buildWorkerDeployModel } from '../utils/workerDeploy'
import DeleteConfirmDialog from '../components/DeleteConfirmDialog.vue'
import { messageStore } from '../stores/messages'

const { data: packagesData, isLoading } = useClientPackages()
const createPackage = useCreateClientPackage()
const updatePackage = useUpdateClientPackage()
const deletePackage = useDeleteClientPackage()

const packages = computed(() => packagesData.value ?? [])
const workerPackages = computed(() =>
  packages.value
    .filter((pkg) => pkg.platform === 'cloudflare_worker' && pkg.arch === 'script' && pkg.kind === 'worker_script')
    .slice()
    .sort((left, right) => compareClientPackageVersions(right.version, left.version))
)
const isMutating = computed(() => createPackage.isPending.value || updatePackage.isPending.value)

const showForm = ref(false)
const editingPackage = ref(null)
const deletingPackage = ref(null)
const form = ref(createDefaultForm())
const errors = ref(createErrorState())

const workerForm = reactive({
  packageId: '',
  workerName: '',
  masterUrl: '',
  token: ''
})
const workerErrors = ref({})
const workerDeployModel = ref(null)
const workerCopied = ref(false)

const selectedWorkerPackage = computed(() => {
  if (workerForm.packageId) {
    return workerPackages.value.find((pkg) => pkg.id === workerForm.packageId) || null
  }
  return workerPackages.value[0] || null
})

function createDefaultForm() {
  return {
    version: '',
    platform: 'windows',
    arch: 'amd64',
    kind: 'flutter_gui',
    download_url: '',
    sha256: '',
    notes: ''
  }
}

function createErrorState() {
  return {
    version: '',
    platform: '',
    arch: '',
    kind: '',
    download_url: '',
    sha256: '',
    submit: ''
  }
}

function openCreate() {
  editingPackage.value = null
  form.value = createDefaultForm()
  errors.value = createErrorState()
  showForm.value = true
}

function openEdit(pkg) {
  editingPackage.value = pkg
  form.value = {
    version: pkg.version || '',
    platform: pkg.platform || 'windows',
    arch: pkg.arch || 'amd64',
    kind: pkg.kind || 'flutter_gui',
    download_url: pkg.download_url || '',
    sha256: pkg.sha256 || '',
    notes: pkg.notes || ''
  }
  errors.value = createErrorState()
  showForm.value = true
}

function closeForm() {
  showForm.value = false
  editingPackage.value = null
}

function isHttpsUrl(value) {
  try {
    const parsed = new URL(String(value || '').trim())
    return parsed.protocol === 'https:' && !!parsed.host
  } catch {
    return false
  }
}

function validatePackageForm() {
  errors.value = createErrorState()
  if (!form.value.version.trim()) errors.value.version = '请输入版本'
  if (!form.value.platform) errors.value.platform = '请选择平台'
  if (!form.value.arch) errors.value.arch = '请选择架构'
  if (!form.value.kind) errors.value.kind = '请选择类型'
  if (!isHttpsUrl(form.value.download_url)) errors.value.download_url = '请输入有效的 https 下载 URL'
  if (!/^[a-f0-9]{64}$/i.test(form.value.sha256.trim())) errors.value.sha256 = '请输入 64 位 SHA256'
  if (
    (form.value.platform === 'cloudflare_worker' || form.value.kind === 'worker_script' || form.value.arch === 'script') &&
    !(form.value.platform === 'cloudflare_worker' && form.value.kind === 'worker_script' && form.value.arch === 'script')
  ) {
    errors.value.platform = 'Worker 脚本必须使用 cloudflare_worker / script / worker_script'
    errors.value.arch = 'Worker 脚本必须使用 script 架构'
    errors.value.kind = 'Worker 脚本必须使用 worker_script 类型'
  }
  return Object.values(errors.value).every((value) => !value)
}

async function submitPackage() {
  if (!validatePackageForm()) return
  const payload = {
    version: form.value.version.trim(),
    platform: form.value.platform,
    arch: form.value.arch,
    kind: form.value.kind,
    download_url: form.value.download_url.trim(),
    sha256: form.value.sha256.trim(),
    notes: form.value.notes.trim()
  }
  try {
    if (editingPackage.value?.id) {
      await updatePackage.mutateAsync({ id: editingPackage.value.id, ...payload })
    } else {
      await createPackage.mutateAsync(payload)
    }
    closeForm()
  } catch (err) {
    errors.value.submit = err?.message || '保存客户端发布包失败'
  }
}

function startDelete(pkg) {
  deletingPackage.value = pkg
}

function confirmDelete() {
  if (!deletingPackage.value) return
  deletePackage.mutate(deletingPackage.value.id)
  deletingPackage.value = null
}

function buildWorkerCommand() {
  try {
    workerDeployModel.value = buildWorkerDeployModel({
      workerName: workerForm.workerName,
      masterUrl: workerForm.masterUrl,
      token: workerForm.token,
      packageRecord: selectedWorkerPackage.value
    })
    workerErrors.value = {}
  } catch (err) {
    workerDeployModel.value = null
    workerErrors.value = err?.errors || { submit: '生成 Worker 部署命令失败' }
  }
}

async function copyWorkerCommand() {
  if (!workerDeployModel.value) return
  const text = [
    ...workerDeployModel.value.envCommands,
    workerDeployModel.value.downloadCommand,
    workerDeployModel.value.checksumCommand,
    ...workerDeployModel.value.secretCommands,
    workerDeployModel.value.command
  ].join('\n')
  try {
    await copyText(text)
    workerCopied.value = true
    messageStore.success('已复制到剪贴板')
    setTimeout(() => { workerCopied.value = false }, 1500)
  } catch (err) {
    messageStore.error(err, '复制失败，请手动选择复制')
  }
}

async function copyText(text) {
  if (navigator.clipboard && window.isSecureContext) {
    await navigator.clipboard.writeText(text)
    return
  }
  const textarea = document.createElement('textarea')
  textarea.value = text
  textarea.style.position = 'fixed'
  textarea.style.left = '-999999px'
  document.body.appendChild(textarea)
  textarea.select()
  const success = document.execCommand('copy')
  document.body.removeChild(textarea)
  if (!success) throw new Error('execCommand failed')
}

function kindLabel(kind) {
  return {
    flutter_gui: 'Flutter GUI',
    go_agent: 'Go Agent',
    worker_script: 'Worker Script'
  }[kind] || kind
}

function compactUrl(url) {
  const value = String(url || '')
  if (value.length <= 56) return value
  return `${value.slice(0, 34)}...${value.slice(-18)}`
}

function compactSha(value) {
  const sha = String(value || '')
  if (sha.length <= 16) return sha
  return `${sha.slice(0, 12)}...${sha.slice(-8)}`
}
</script>

<style scoped>
.client-packages-page {
  max-width: 1200px;
  margin: 0 auto;
}

.client-packages-page__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  margin-bottom: var(--space-5);
}

.client-packages-page__title {
  margin: 0;
  font-size: 1.5rem;
  font-weight: 700;
  color: var(--color-text-primary);
}

.client-packages-page__subtitle,
.section-heading p {
  margin: var(--space-1) 0 0;
  color: var(--color-text-tertiary);
  font-size: var(--text-sm);
}

.section-heading {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: var(--space-3);
  margin-bottom: var(--space-4);
}

.section-heading h2 {
  margin: 0;
  color: var(--color-text-primary);
  font-size: var(--text-lg);
}

.worker-panel,
.packages-table-wrap {
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  background: var(--color-bg-surface);
}

.worker-panel {
  padding: var(--space-4);
  margin-bottom: var(--space-5);
}

.worker-panel__grid {
  display: grid;
  grid-template-columns: 1.4fr 0.8fr 1fr 1fr;
  gap: var(--space-3);
}

.worker-panel__actions {
  display: flex;
  gap: var(--space-2);
  margin-top: var(--space-4);
}

.worker-panel__error {
  margin-top: var(--space-3);
}

.deploy-output {
  margin-top: var(--space-4);
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: var(--space-3);
}

.deploy-output__label {
  display: block;
  margin-bottom: var(--space-2);
  color: var(--color-text-secondary);
  font-size: var(--text-xs);
  font-weight: var(--font-medium);
}

.deploy-output pre {
  min-height: 92px;
  margin: 0;
  padding: var(--space-3);
  overflow: auto;
  border-radius: var(--radius-md);
  border: 1px solid var(--color-border-subtle);
  background: var(--color-bg-subtle);
  color: var(--color-text-primary);
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-word;
}

.packages-table-wrap {
  overflow-x: auto;
}

.packages-table {
  width: 100%;
  border-collapse: collapse;
  font-size: var(--text-sm);
}

.packages-table th,
.packages-table td {
  padding: var(--space-3);
  border-bottom: 1px solid var(--color-border-subtle);
  text-align: left;
  vertical-align: top;
}

.packages-table th {
  color: var(--color-text-secondary);
  font-weight: var(--font-medium);
  background: var(--color-bg-subtle);
}

.packages-table tr:last-child td {
  border-bottom: none;
}

.kind-badge {
  display: inline-flex;
  align-items: center;
  padding: 2px 8px;
  border-radius: var(--radius-sm);
  background: var(--color-primary-subtle);
  color: var(--color-primary);
  font-size: var(--text-xs);
  font-weight: var(--font-medium);
  white-space: nowrap;
}

.table-actions {
  display: flex;
  gap: var(--space-1);
  white-space: nowrap;
}

.client-packages-page__empty {
  padding: var(--space-8);
  text-align: center;
  color: var(--color-text-muted);
}

.btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  border: none;
  border-radius: var(--radius-md);
  padding: var(--space-2) var(--space-4);
  font-family: inherit;
  font-size: var(--text-sm);
  cursor: pointer;
}

.btn:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.btn-primary {
  background: var(--gradient-primary);
  color: white;
}

.btn-secondary {
  background: var(--color-bg-subtle);
  color: var(--color-text-secondary);
  border: 1px solid var(--color-border-default);
}

.icon-btn {
  border: 1px solid var(--color-border-default);
  background: var(--color-bg-surface);
  border-radius: var(--radius-sm);
  padding: 2px 8px;
  color: var(--color-text-secondary);
  font-size: var(--text-xs);
  cursor: pointer;
}

.icon-btn--danger {
  color: var(--color-danger);
}

.form-row {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-3);
}

.form-row--three {
  grid-template-columns: repeat(3, minmax(0, 1fr));
}

.form-group,
.package-form {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.package-form {
  gap: var(--space-4);
}

.form-label {
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
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
  box-sizing: border-box;
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-surface);
  color: var(--color-text-primary);
  font-family: inherit;
  font-size: var(--text-sm);
}

.input--error {
  border-color: var(--color-danger);
}

.modal-overlay {
  position: fixed;
  inset: 0;
  z-index: var(--z-modal);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--space-4);
  background: rgba(37, 23, 54, 0.4);
  backdrop-filter: blur(8px);
}

.modal {
  width: min(520px, 90vw);
  max-height: calc(100vh - var(--space-8));
  display: flex;
  flex-direction: column;
  overflow: hidden;
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-2xl);
  background: var(--color-bg-surface);
  box-shadow: var(--shadow-xl);
}

.modal--large {
  width: min(860px, 95vw);
}

.modal__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-5) var(--space-6);
  border-bottom: 1px solid var(--color-border-subtle);
  color: var(--color-text-primary);
  font-weight: var(--font-semibold);
}

.modal__body {
  padding: var(--space-6);
  overflow: auto;
}

.modal__footer {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
  padding-top: var(--space-3);
}

.modal__close {
  border: none;
  background: transparent;
  color: var(--color-text-muted);
  cursor: pointer;
  font-size: var(--text-base);
}

@media (max-width: 980px) {
  .client-packages-page__header,
  .worker-panel__actions {
    align-items: stretch;
    flex-direction: column;
  }

  .worker-panel__grid,
  .deploy-output,
  .form-row,
  .form-row--three {
    grid-template-columns: 1fr;
  }
}
</style>

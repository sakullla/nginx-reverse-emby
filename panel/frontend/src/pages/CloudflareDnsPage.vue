<template>
  <div class="cf-dns-page">
    <div class="cf-dns-page__header">
      <div>
        <h1 class="cf-dns-page__title">Cloudflare 域名 Token 映射</h1>
        <p class="cf-dns-page__subtitle">
          按域名后缀绑定 Cloudflare DNS Token。保存后列表、详情和编辑框都不会回显 Token。
          未命中时由环境变量全局 Token 兜底。本页不配置区域 Token，也不管理 DNS 记录。
        </p>
      </div>
    </div>

    <p v-if="denied" id="mapping-denied" data-testid="mapping-denied" role="alert">
      无权访问 Cloudflare 域名 Token 映射，请求已被明确拒绝。
    </p>
    <p v-else-if="unavailable" data-testid="mapping-unavailable" role="alert">
      cloudflare-dns 插件未安装或不可用。
    </p>
    <p v-else-if="loadError" data-testid="mapping-load-error" role="alert">{{ loadError }}</p>

    <div v-else-if="loading" class="cf-dns-page__loading" data-testid="mapping-loading">
      <div class="spinner"></div>
    </div>

    <template v-else>
      <section class="cf-dns-card" aria-labelledby="mapping-list-title">
        <h2 id="mapping-list-title" class="cf-dns-card__title">已保存映射</h2>
        <div v-if="!mappings.length" id="mapping-empty" data-testid="mapping-empty">还没有映射。</div>
        <table v-else class="cf-dns-table" data-testid="mapping-list">
          <thead>
            <tr>
              <th>域名后缀</th>
              <th>已配置</th>
              <th>最近更新</th>
              <th v-if="canWrite || canRotate">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="item in mappings" :key="item.suffix" :data-suffix="item.suffix">
              <td>
                <button type="button" class="cf-dns-link" data-testid="mapping-suffix" @click="openDetail(item.suffix)">
                  {{ item.suffix }}
                </button>
              </td>
              <td>{{ item.configured ? '是' : '否' }}</td>
              <td>{{ formatUpdatedAt(item.updated_at) }}</td>
              <td v-if="canWrite || canRotate" class="cf-dns-table__actions">
                <form v-if="canWrite" class="cf-dns-inline" data-testid="mapping-rename-form" @submit.prevent="requestRename(item.suffix)">
                  <label>
                    新后缀
                    <input v-model="renameDrafts[item.suffix]" name="suffix" autocomplete="off" required data-testid="mapping-rename-suffix">
                  </label>
                  <button type="submit" class="btn btn-secondary" data-testid="mapping-rename">改后缀</button>
                </form>
                <form v-if="canRotate" class="cf-dns-inline" data-testid="mapping-rotate-form" @submit.prevent="requestRotate(item.suffix)">
                  <label>
                    新 Token
                    <input
                      v-model="rotateDrafts[item.suffix]"
                      name="token"
                      type="password"
                      autocomplete="new-password"
                      required
                      data-testid="mapping-rotate-token"
                    >
                  </label>
                  <button type="submit" class="btn btn-secondary" data-testid="mapping-rotate">轮换 Token</button>
                </form>
                <button
                  v-if="canWrite"
                  type="button"
                  class="btn btn-danger"
                  data-testid="mapping-delete"
                  @click="requestDelete(item.suffix)"
                >
                  删除
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </section>

      <section v-if="detail" class="cf-dns-card" data-testid="mapping-detail" aria-labelledby="mapping-detail-title">
        <h2 id="mapping-detail-title" class="cf-dns-card__title">映射详情</h2>
        <dl class="cf-dns-detail">
          <div>
            <dt>域名后缀</dt>
            <dd>{{ detail.suffix }}</dd>
          </div>
          <div>
            <dt>已配置</dt>
            <dd>{{ detail.configured ? '是' : '否' }}</dd>
          </div>
          <div>
            <dt>最近更新</dt>
            <dd>{{ formatUpdatedAt(detail.updated_at) }}</dd>
          </div>
        </dl>
      </section>

      <section v-if="canWrite" class="cf-dns-card" id="mapping-create" data-testid="mapping-create" aria-labelledby="mapping-create-title">
        <h2 id="mapping-create-title" class="cf-dns-card__title">新增映射</h2>
        <form class="cf-dns-form" data-testid="mapping-create-form" @submit.prevent="createMapping">
          <label>
            域名后缀
            <input v-model="createSuffix" name="suffix" autocomplete="off" required data-testid="mapping-create-suffix">
          </label>
          <label>
            Cloudflare DNS Token
            <input
              v-model="createToken"
              name="token"
              type="password"
              autocomplete="new-password"
              required
              data-testid="mapping-create-token"
            >
          </label>
          <button type="submit" class="btn btn-primary" :disabled="saving" data-testid="mapping-create-submit">保存</button>
        </form>
      </section>

      <p v-if="status" id="mapping-status" data-testid="mapping-status" role="status" :data-error="statusError ? 'true' : 'false'">
        {{ status }}
      </p>
    </template>

    <DeleteConfirmDialog
      :show="!!pending"
      :title="pendingTitle"
      :message="pendingMessage"
      :name="pending?.suffix || ''"
      :confirm-text="pendingConfirmText"
      :loading="saving"
      @confirm="confirmPending"
      @cancel="cancelPending"
    />
  </div>
</template>

<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import DeleteConfirmDialog from '../components/DeleteConfirmDialog.vue'
import {
  createCloudflareDnsMapping,
  deleteCloudflareDnsMapping,
  fetchCloudflareDnsMapping,
  fetchCloudflareDnsMappings,
  renameCloudflareDnsMapping,
  rotateCloudflareDnsMapping
} from '../api'

const mappings = ref([])
const detail = ref(null)
const loading = ref(true)
const saving = ref(false)
const denied = ref(false)
const unavailable = ref(false)
const loadError = ref('')
const status = ref('')
const statusError = ref(false)
const canWrite = ref(false)
const canRotate = ref(false)
const createSuffix = ref('')
const createToken = ref('')
const renameDrafts = reactive({})
const rotateDrafts = reactive({})
const pending = ref(null)

const pendingTitle = computed(() => {
  switch (pending.value?.kind) {
    case 'rename':
      return '确认改后缀'
    case 'rotate':
      return '确认轮换 Token'
    default:
      return '确认删除'
  }
})

const pendingMessage = computed(() => {
  switch (pending.value?.kind) {
    case 'rename':
      return `确认把 ${pending.value.suffix} 改为 ${pending.value.nextSuffix}？取消不会更改映射。`
    case 'rotate':
      return `确认轮换 ${pending.value.suffix} 的 Token？取消不会更改映射。`
    default:
      return '确认删除以下映射？取消不会更改映射。'
  }
})

const pendingConfirmText = computed(() => {
  switch (pending.value?.kind) {
    case 'rename':
      return '确认改后缀'
    case 'rotate':
      return '确认轮换'
    default:
      return '确认删除'
  }
})

function formatUpdatedAt(value) {
  const numeric = Number(value)
  if (!numeric) return '—'
  const millis = numeric < 1e12 ? numeric * 1000 : numeric
  const date = new Date(millis)
  if (Number.isNaN(date.getTime())) return '—'
  return date.toLocaleString()
}

function classifyError(error) {
  const statusCode = error?.status || error?.response?.status
  if (statusCode === 401 || statusCode === 403) return 'denied'
  if (statusCode === 503) return 'unavailable'
  return 'error'
}

function publicError(error) {
  const data = error?.response?.data
  return data?.error || data?.message || error?.message || '请求失败'
}

function resetDrafts(items) {
  Object.keys(renameDrafts).forEach((key) => { delete renameDrafts[key] })
  Object.keys(rotateDrafts).forEach((key) => { delete rotateDrafts[key] })
  for (const item of items) {
    renameDrafts[item.suffix] = ''
    rotateDrafts[item.suffix] = ''
  }
}

async function loadMappings() {
  loading.value = true
  denied.value = false
  unavailable.value = false
  loadError.value = ''
  try {
    const result = await fetchCloudflareDnsMappings()
    mappings.value = result.mappings
    canWrite.value = result.access.can_write
    canRotate.value = result.access.can_rotate
    resetDrafts(result.mappings)
    if (detail.value) {
      const stillThere = result.mappings.find((item) => item.suffix === detail.value.suffix)
      detail.value = stillThere || null
    }
  } catch (error) {
    const kind = classifyError(error)
    if (kind === 'denied') {
      denied.value = true
      canWrite.value = false
      canRotate.value = false
      mappings.value = []
      detail.value = null
      return
    }
    if (kind === 'unavailable') {
      unavailable.value = true
      canWrite.value = false
      canRotate.value = false
      mappings.value = []
      return
    }
    loadError.value = publicError(error)
  } finally {
    loading.value = false
  }
}

async function openDetail(suffix) {
  try {
    detail.value = await fetchCloudflareDnsMapping(suffix)
    status.value = ''
    statusError.value = false
  } catch (error) {
    if (classifyError(error) === 'denied') {
      denied.value = true
      canWrite.value = false
      canRotate.value = false
      return
    }
    status.value = publicError(error)
    statusError.value = true
  }
}

function showStatus(message, isError) {
  status.value = message
  statusError.value = isError
}

async function createMapping() {
  saving.value = true
  try {
    await createCloudflareDnsMapping({ suffix: createSuffix.value, token: createToken.value })
    createSuffix.value = ''
    createToken.value = ''
    showStatus('映射已保存。', false)
    await loadMappings()
  } catch (error) {
    if (classifyError(error) === 'denied') {
      denied.value = true
      return
    }
    showStatus(publicError(error), true)
  } finally {
    saving.value = false
  }
}

function requestRename(suffix) {
  const nextSuffix = String(renameDrafts[suffix] || '').trim()
  if (!nextSuffix) return
  pending.value = { kind: 'rename', suffix, nextSuffix }
}

function requestRotate(suffix) {
  const token = String(rotateDrafts[suffix] || '')
  if (!token) return
  pending.value = { kind: 'rotate', suffix, token }
}

function requestDelete(suffix) {
  pending.value = { kind: 'delete', suffix }
}

function cancelPending() {
  pending.value = null
  showStatus('已取消，映射未更改。', false)
}

async function confirmPending() {
  const action = pending.value
  if (!action) return
  saving.value = true
  try {
    if (action.kind === 'rename') {
      await renameCloudflareDnsMapping(action.suffix, action.nextSuffix)
    } else if (action.kind === 'rotate') {
      await rotateCloudflareDnsMapping(action.suffix, action.token)
      rotateDrafts[action.suffix] = ''
    } else if (action.kind === 'delete') {
      await deleteCloudflareDnsMapping(action.suffix)
      if (detail.value?.suffix === action.suffix) detail.value = null
    }
    pending.value = null
    showStatus('映射已更新。', false)
    await loadMappings()
  } catch (error) {
    if (classifyError(error) === 'denied') {
      pending.value = null
      denied.value = true
      return
    }
    showStatus(publicError(error), true)
  } finally {
    saving.value = false
  }
}

onMounted(loadMappings)
</script>

<style scoped>
.cf-dns-page {
  max-width: 960px;
  margin: 0 auto;
}
.cf-dns-page__header {
  margin-bottom: var(--space-6);
  padding-bottom: var(--space-4);
  border-bottom: 1px solid var(--color-border-subtle);
}
.cf-dns-page__title {
  font-size: var(--text-2xl);
  font-weight: var(--font-bold);
  margin: 0 0 var(--space-1);
  color: var(--color-text-primary);
}
.cf-dns-page__subtitle {
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
  margin: 0;
  max-width: 46rem;
}
.cf-dns-page__loading {
  display: flex;
  justify-content: center;
  padding: var(--space-8);
}
.cf-dns-card {
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-xl);
  padding: var(--space-5);
  margin-bottom: var(--space-4);
}
.cf-dns-card__title {
  margin: 0 0 var(--space-4);
  font-size: var(--text-lg);
  color: var(--color-text-primary);
}
.cf-dns-table {
  width: 100%;
  border-collapse: collapse;
  font-size: var(--text-sm);
}
.cf-dns-table th,
.cf-dns-table td {
  text-align: left;
  padding: 0.75rem 0.5rem;
  border-bottom: 1px solid var(--color-border-subtle);
  vertical-align: top;
}
.cf-dns-table__actions {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}
.cf-dns-inline,
.cf-dns-form {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem 0.75rem;
  align-items: flex-end;
}
.cf-dns-inline label,
.cf-dns-form label {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
  font-size: var(--text-xs);
  color: var(--color-text-secondary);
}
.cf-dns-inline input,
.cf-dns-form input {
  min-width: 12rem;
  padding: 0.4rem 0.6rem;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-canvas);
  color: var(--color-text-primary);
}
.cf-dns-link {
  border: 0;
  background: none;
  color: var(--color-primary);
  cursor: pointer;
  padding: 0;
  font: inherit;
}
.cf-dns-detail {
  display: grid;
  gap: 0.75rem;
  margin: 0;
}
.cf-dns-detail dt {
  font-size: var(--text-xs);
  color: var(--color-text-tertiary);
}
.cf-dns-detail dd {
  margin: 0.15rem 0 0;
  color: var(--color-text-primary);
}
#mapping-denied,
[data-testid='mapping-unavailable'],
[data-testid='mapping-load-error'] {
  color: var(--color-danger, #b42318);
  margin: 0 0 var(--space-4);
}
#mapping-status[data-error='true'] {
  color: var(--color-danger, #b42318);
}
</style>

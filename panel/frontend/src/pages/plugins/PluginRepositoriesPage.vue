<template>
  <div class="plugin-repositories-page">
    <header class="page-header">
      <div class="page-header__left">
        <RouterLink to="/plugins/marketplace" class="back-link">← 插件市场</RouterLink>
        <p class="repository-advanced-label">高级入口 · 来源管理</p>
        <h1 class="page-title">插件仓库</h1>
        <p class="page-subtitle">这里只管理市场和插件包从哪里来，不是安装或发布的起点。下一步：查看最近刷新是否成功，或新增仓库源；安装官方插件请先去市场。</p>
      </div>
      <div class="page-header__right">
        <RouterLink class="btn btn-secondary" to="/plugins/marketplace">去市场安装</RouterLink>
        <button class="btn btn-primary" type="button" @click="openCreate">新增仓库源</button>
      </div>
    </header>

    <div v-if="loading" class="plugin-repositories-page__loading">
      <div class="spinner"></div>
      <p>正在读取仓库源…</p>
    </div>

    <div v-else-if="loadError && !sources.length" role="alert">
      <EmptyState title="读取失败" :description="`${loadError} 下一步：重试读取仓库源，或先去市场安装官方插件。`">
        <template #action>
          <div class="repository-empty-actions">
            <button class="btn btn-secondary" type="button" @click="loadSources">重试</button>
            <RouterLink class="btn btn-secondary" to="/plugins/marketplace">去市场安装</RouterLink>
          </div>
        </template>
      </EmptyState>
    </div>

    <template v-else>
      <p v-if="error" class="repository-alert repository-alert--error" role="alert">{{ error }}</p>

      <EmptyState
        v-if="!sources.length"
        title="还没有仓库源"
        description="下一步：新增一个市场索引或插件包来源。安装官方插件不必从这里开始，请先去市场。"
      >
        <template #action>
          <div class="repository-empty-actions">
            <button class="btn btn-primary" type="button" @click="openCreate">新增仓库源</button>
            <RouterLink class="btn btn-secondary" to="/plugins/marketplace">去市场安装</RouterLink>
          </div>
        </template>
      </EmptyState>

      <section v-else class="repository-workspace" aria-label="插件仓库源">
        <aside class="repository-list">
          <div class="repository-list__heading">
            <strong>仓库源</strong>
            <span>{{ sources.length }}</span>
          </div>
          <button
            v-for="source in sources"
            :key="source.id"
            type="button"
            :class="['repository-list__item', { 'repository-list__item--active': selectedId === source.id }]"
            @click="selectedId = source.id"
          >
            <span class="repository-list__item-main">
              <strong>{{ source.name }}</strong>
              <small>{{ purposeLabel(source.purpose) }} · {{ statusOf(source).label }}</small>
            </span>
            <span :class="['repository-status-dot', `repository-status-dot--${statusOf(source).tone}`]" :title="statusOf(source).label" />
          </button>
        </aside>

        <div v-if="selectedSource" class="repository-detail">
          <div class="repository-detail__header">
            <div>
              <div class="repository-detail__eyebrow">
                <span>{{ purposeLabel(selectedSource.purpose) }}</span>
                <span :class="['repository-risk', { 'repository-risk--official': isOfficial(selectedSource) }]">
                  {{ sourceKindLabel(selectedSource) }} · {{ selectedSource.risk_label || '风险未标注' }}
                </span>
              </div>
              <h2>{{ selectedSource.name }}</h2>
              <p>{{ selectedSource.url }}</p>
            </div>
            <div class="repository-detail__actions">
              <button class="btn btn-secondary" type="button" :disabled="refreshing" @click="refreshSelected">
                {{ refreshing ? '刷新中…' : '立即刷新' }}
              </button>
              <button v-if="!isOfficial(selectedSource)" class="btn btn-secondary" type="button" @click="openEdit">编辑</button>
              <button v-if="!isOfficial(selectedSource)" class="btn repository-delete-button" type="button" @click="confirmingDelete = true">删除源</button>
            </div>
          </div>

          <p v-if="selectedSource.last_error" class="repository-alert repository-alert--error">
            最近刷新失败：{{ selectedSource.last_error }}
          </p>

          <dl class="repository-detail__facts">
            <div>
              <dt>当前状态</dt>
              <dd><span :class="['repository-state', `repository-state--${statusOf(selectedSource).tone}`]">{{ statusOf(selectedSource).label }}</span></dd>
            </div>
            <div>
              <dt>最近完成</dt>
              <dd>{{ formatDate(selectedSource.last_completed_at) }}</dd>
            </div>
            <div>
              <dt>用途</dt>
              <dd>{{ purposeLabel(selectedSource.purpose) }}</dd>
            </div>
            <div>
              <dt>Git 凭据</dt>
              <dd>{{ selectedSource.credential_configured ? '已配置' : '未配置' }}</dd>
            </div>
          </dl>

          <details class="repository-technical">
            <summary>技术详情</summary>
            <dl class="repository-detail__facts">
              <div>
                <dt>配置引用</dt>
                <dd>{{ selectedSource.ref_kind === 'tag' ? '标签' : '分支' }} / {{ selectedSource.ref_name }}</dd>
              </div>
              <div>
                <dt>配置修订</dt>
                <dd>{{ selectedSource.config_revision || 0 }}</dd>
              </div>
              <div class="repository-detail__fact-wide">
                <dt>解析提交 OID</dt>
                <dd><code>{{ selectedSource.current_resolved_oid || '尚未解析' }}</code></dd>
              </div>
              <div>
                <dt>当前快照</dt>
                <dd>{{ selectedSource.current_snapshot || '—' }}</dd>
              </div>
              <div>
                <dt>签名身份</dt>
                <dd>{{ selectedSource.signer_key_id || '未配置' }}</dd>
              </div>
              <div class="repository-detail__fact-wide">
                <dt>签名指纹</dt>
                <dd><code>{{ selectedSource.signer_fingerprint || '—' }}</code></dd>
              </div>
              <div>
                <dt>刷新间隔</dt>
                <dd>{{ formatInterval(selectedSource.refresh_interval_ns) }}</dd>
              </div>
            </dl>
          </details>

          <section class="repository-packages" aria-labelledby="repository-packages-title">
            <div class="repository-packages__heading">
              <h3 id="repository-packages-title">{{ selectedSource.purpose === 'plugin' ? '插件包' : '市场条目' }}</h3>
              <span v-if="selectedSource.purpose === 'market'">{{ repositoryContents.entries.length }}</span>
            </div>
            <p v-if="contentsLoading" class="repository-packages__empty">正在读取包投影…</p>
            <p v-else-if="contentsFailed" class="repository-packages__empty">读取包投影失败</p>
            <div v-else-if="repositoryContents.directPlugin" class="repository-package-row">
              <div>
                <strong>{{ repositoryContents.directPlugin.id }}</strong>
                <small>{{ repositoryContents.directPlugin.version }} · {{ repositoryContents.directPlugin.runtime?.kind || 'runtime 未声明' }}</small>
              </div>
              <code>{{ repositoryContents.directPlugin.sha256 }}</code>
            </div>
            <div v-else-if="repositoryContents.entries.length" class="repository-package-list">
              <div v-for="entry in repositoryContents.entries" :key="`${entry.id}@${entry.version}`" class="repository-package-row">
                <div>
                  <strong>{{ entry.id }}</strong>
                  <small>{{ entry.version }} · {{ entry.runtime?.kind || 'runtime 未声明' }}</small>
                </div>
                <code>{{ entry.sha256 }}</code>
              </div>
            </div>
            <p v-else class="repository-packages__empty">当前快照没有可用包。下一步：立即刷新该来源，成功后再去市场安装。</p>
          </section>

          <p class="repository-detail__notice">
            删除仓库源只会停止从该 Git 来源继续发现或刷新内容，不会卸载已经安装的插件。
            <RouterLink to="/plugins">到已安装列表继续部署或发布</RouterLink>
          </p>
        </div>

        <div v-else class="repository-detail repository-detail--empty">
          <strong>选择一个仓库源查看刷新是否成功</strong>
          <p>下一步：确认来源可用后再去市场安装，或新增一个来源。</p>
        </div>
      </section>

      <RepositorySourceForm
        v-if="showForm"
        :source="editingSource"
        :saving="saving"
        :submit-error="error"
        @save="saveSource"
        @cancel="closeForm"
      />

      <DeleteConfirmDialog
        :show="confirmingDelete"
        title="删除仓库源？"
        message="将停止从该仓库源继续发现和刷新内容。此操作不会卸载已经安装的插件。"
        :name="selectedSource?.name || ''"
        confirm-text="确认删除源"
        :loading="saving"
        @confirm="removeSelected"
        @cancel="confirmingDelete = false"
      />
    </template>
  </div>
</template>

<script setup>
import { computed, onMounted, ref, watch } from 'vue'
import { sanitizePluginText } from '../../api/pluginSecurity'
import {
  createRepositorySource,
  deleteRepositorySource,
  fetchRepositoryContents,
  fetchRepositorySources,
  refreshRepositorySource,
  updateRepositorySource
} from '../../api/pluginRepositories'
import RepositorySourceForm from '../../components/plugins/RepositorySourceForm.vue'
import DeleteConfirmDialog from '../../components/DeleteConfirmDialog.vue'
import EmptyState from '../../components/base/EmptyState.vue'

const sources = ref([])
const selectedId = ref('')
const loading = ref(false)
const saving = ref(false)
const refreshingId = ref('')
const loadError = ref('')
const error = ref('')
const showForm = ref(false)
const editingSource = ref(null)
const confirmingDelete = ref(false)
const contentsLoading = ref(false)
const contentsFailed = ref(false)
const repositoryContents = ref({ entries: [], directPlugin: null })

const selectedSource = computed(() => sources.value.find((source) => source.id === selectedId.value) || null)
const refreshing = computed(() => refreshingId.value === selectedId.value)

onMounted(loadSources)
watch(selectedId, (id) => loadContents(id))

async function loadSources(preferredId = selectedId.value) {
  loading.value = true
  loadError.value = ''
  try {
    sources.value = await fetchRepositorySources()
    selectedId.value = sources.value.some((source) => source.id === preferredId)
      ? preferredId
      : sources.value[0]?.id || ''
  } catch (cause) {
    loadError.value = sanitizePluginText(cause?.message || '读取仓库源失败')
  } finally {
    loading.value = false
  }
}

async function loadContents(id) {
  repositoryContents.value = { entries: [], directPlugin: null }
  contentsFailed.value = false
  if (!id) return
  error.value = ''
  contentsLoading.value = true
  try {
    const contents = await fetchRepositoryContents(id)
    if (selectedId.value === id) repositoryContents.value = contents
  } catch (cause) {
    if (selectedId.value === id) {
      error.value = sanitizePluginText(cause?.message || '读取仓库包投影失败')
      contentsFailed.value = true
    }
  } finally {
    if (selectedId.value === id) contentsLoading.value = false
  }
}

function openCreate() {
  error.value = ''
  editingSource.value = null
  showForm.value = true
}

function openEdit() {
  if (!selectedSource.value || isOfficial(selectedSource.value)) return
  error.value = ''
  editingSource.value = selectedSource.value
  showForm.value = true
}

function closeForm() {
  showForm.value = false
  editingSource.value = null
  error.value = ''
}

async function saveSource(payload) {
  saving.value = true
  error.value = ''
  try {
    const source = editingSource.value
      ? await updateRepositorySource(editingSource.value.id, { ...payload, config_revision: editingSource.value.config_revision })
      : await createRepositorySource(payload)
    closeForm()
    await loadSources(source.id)
  } catch (cause) {
    error.value = sanitizePluginText(cause?.message || '保存仓库源失败')
  } finally {
    saving.value = false
  }
}

async function refreshSelected() {
  if (!selectedSource.value) return
  const id = selectedSource.value.id
  refreshingId.value = id
  error.value = ''
  try {
    await refreshRepositorySource(id)
    await loadSources(id)
    await loadContents(id)
  } catch (cause) {
    error.value = sanitizePluginText(cause?.message || '刷新仓库源失败')
  } finally {
    refreshingId.value = ''
  }
}

async function removeSelected() {
  if (!selectedSource.value || isOfficial(selectedSource.value)) return
  const id = selectedSource.value.id
  saving.value = true
  error.value = ''
  try {
    await deleteRepositorySource(id)
    confirmingDelete.value = false
    selectedId.value = ''
    await loadSources()
  } catch (cause) {
    confirmingDelete.value = false
    error.value = sanitizePluginText(cause?.message || '删除仓库源失败')
  } finally {
    saving.value = false
  }
}

function isOfficial(source) {
  return source?.kind === 'official'
}

function purposeLabel(purpose) {
  return purpose === 'plugin' ? '插件包' : '市场索引'
}

function sourceKindLabel(source) {
  return isOfficial(source) ? '官方来源' : '自定义来源'
}

function statusOf(source) {
  if (refreshingId.value === source?.id) return { label: '刷新中', tone: 'pending' }
  if (source?.last_error) return { label: '刷新失败', tone: 'error' }
  if (source?.current_resolved_oid) return { label: source.last_result && source.last_result !== 'succeeded' ? `当前 · ${source.last_result}` : '当前可用', tone: 'current' }
  return { label: source?.last_result || '等待首次刷新', tone: 'pending' }
}

function formatInterval(value) {
  const nanoseconds = Number(value) || 0
  if (nanoseconds <= 0) return '不自动刷新'
  const minutes = nanoseconds / 60_000_000_000
  if (minutes >= 60 && Number.isInteger(minutes / 60)) return `${minutes / 60} 小时`
  if (Number.isInteger(minutes)) return `${minutes} 分钟`
  return `${nanoseconds} ns`
}

function formatDate(value) {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN')
}
</script>

<style scoped>
.plugin-repositories-page { max-width: 1280px; margin: 0 auto; }

.plugin-repositories-page__loading {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-3);
  padding: 4rem 2rem;
  color: var(--color-text-muted);
}

.back-link {
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
  text-decoration: none;
}
.back-link:hover { color: var(--color-primary); }

.repository-advanced-label {
  margin: var(--space-2) 0 0;
  color: var(--color-text-muted);
  font-size: var(--text-xs);
}

.repository-empty-actions {
  display: flex;
  justify-content: center;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.repository-workspace {
  min-height: 520px;
  display: grid;
  grid-template-columns: minmax(230px, 300px) minmax(0, 1fr);
  overflow: hidden;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  background: var(--color-bg-surface);
}

.repository-list { border-right: 1px solid var(--color-border-subtle); background: var(--color-bg-subtle); }
.repository-list__heading { display: flex; justify-content: space-between; padding: var(--space-4); border-bottom: 1px solid var(--color-border-subtle); font-size: var(--text-sm); }
.repository-list__heading span { color: var(--color-text-muted); }
.repository-list__empty { padding: var(--space-6) var(--space-4); color: var(--color-text-muted); font-size: var(--text-sm); text-align: center; }
.repository-list__item {
  width: 100%;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
  border: 0;
  border-bottom: 1px solid var(--color-border-subtle);
  background: transparent;
  color: var(--color-text-primary);
  cursor: pointer;
  text-align: left;
}
.repository-list__item:hover { background: var(--color-bg-surface); }
.repository-list__item--active { background: var(--color-bg-surface); box-shadow: inset 3px 0 0 var(--color-primary); }
.repository-list__item-main { min-width: 0; display: flex; flex-direction: column; gap: 2px; }
.repository-list__item-main strong,
.repository-list__item-main small { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.repository-list__item-main small { color: var(--color-text-muted); }
.repository-status-dot { flex: 0 0 auto; width: 8px; height: 8px; border-radius: 50%; background: var(--color-text-muted); }
.repository-status-dot--current { background: var(--color-success); }
.repository-status-dot--error { background: var(--color-danger); }
.repository-status-dot--pending { background: var(--color-warning); }

.repository-detail { min-width: 0; padding: var(--space-6); }
.repository-detail--empty { display: grid; place-content: center; color: var(--color-text-muted); text-align: center; }
.repository-detail--empty p { margin: var(--space-1) 0 0; font-size: var(--text-sm); }
.repository-detail__header { display: flex; justify-content: space-between; align-items: flex-start; gap: var(--space-4); padding-bottom: var(--space-5); border-bottom: 1px solid var(--color-border-subtle); }
.repository-detail__header h2 { margin: var(--space-2) 0 0; font-size: var(--text-xl); }
.repository-detail__header p { max-width: 680px; margin: var(--space-1) 0 0; overflow-wrap: anywhere; color: var(--color-text-muted); font-size: var(--text-sm); }
.repository-detail__eyebrow { display: flex; flex-wrap: wrap; gap: var(--space-2); color: var(--color-text-muted); font-size: var(--text-xs); }
.repository-risk { padding: 1px 7px; border-radius: var(--radius-full); background: var(--color-warning-subtle); color: var(--color-warning); }
.repository-risk--official { background: var(--color-success-subtle); color: var(--color-success); }
.repository-detail__actions { display: flex; flex-wrap: wrap; justify-content: flex-end; gap: var(--space-2); }

.repository-detail__facts { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); margin: var(--space-5) 0 0; }
.repository-detail__facts > div { min-width: 0; padding: var(--space-3) 0; border-bottom: 1px solid var(--color-border-subtle); }
.repository-detail__facts > div:nth-child(odd):not(.repository-detail__fact-wide) { padding-right: var(--space-5); }
.repository-detail__facts dt { color: var(--color-text-muted); font-size: var(--text-xs); }
.repository-detail__facts dd { margin: var(--space-1) 0 0; color: var(--color-text-primary); font-size: var(--text-sm); overflow-wrap: anywhere; }
.repository-detail__facts code { font-size: var(--text-xs); }
.repository-detail__fact-wide { grid-column: 1 / -1; }
.repository-state { color: var(--color-text-secondary); }
.repository-state--current { color: var(--color-success); }
.repository-state--error { color: var(--color-danger); }
.repository-state--pending { color: var(--color-warning); }
.repository-technical { margin-top: var(--space-4); }
.repository-technical summary { cursor: pointer; color: var(--color-text-secondary); font-size: var(--text-sm); }
.repository-packages { margin-top: var(--space-5); border-top: 1px solid var(--color-border-subtle); padding-top: var(--space-4); }
.repository-packages__heading { display: flex; align-items: center; justify-content: space-between; gap: var(--space-3); }
.repository-packages__heading h3 { margin: 0; font-size: var(--text-sm); }
.repository-packages__heading span { color: var(--color-text-muted); font-size: var(--text-xs); }
.repository-package-list { display: grid; gap: var(--space-2); margin-top: var(--space-3); }
.repository-package-row { display: grid; grid-template-columns: minmax(0, 1fr) minmax(12rem, 0.8fr); align-items: center; gap: var(--space-4); margin-top: var(--space-3); padding: var(--space-3) 0; border-bottom: 1px solid var(--color-border-subtle); }
.repository-package-row div { display: grid; gap: var(--space-1); min-width: 0; }
.repository-package-row small { color: var(--color-text-muted); }
.repository-package-row code { overflow-wrap: anywhere; color: var(--color-text-secondary); font-size: var(--text-xs); }
.repository-packages__empty { margin: var(--space-3) 0 0; color: var(--color-text-muted); font-size: var(--text-sm); }
.repository-detail__notice { margin: var(--space-5) 0 0; color: var(--color-text-muted); font-size: var(--text-xs); }
.repository-detail__notice a { color: var(--color-primary); text-decoration: none; }
.repository-detail__notice a:hover { text-decoration: underline; }

.repository-alert { display: flex; gap: var(--space-2); margin: 0 0 var(--space-4); padding: var(--space-3); border-radius: var(--radius-md); font-size: var(--text-sm); }
.repository-alert--error { background: var(--color-danger-subtle); color: var(--color-danger); }
.repository-delete-button { border: 1px solid var(--color-danger); background: transparent; color: var(--color-danger); }

@media (max-width: 760px) {
  .repository-workspace { grid-template-columns: 1fr; }
  .repository-list { border-right: 0; border-bottom: 1px solid var(--color-border-subtle); }
  .repository-detail__header { flex-direction: column; }
  .repository-detail__actions { justify-content: flex-start; }
  .repository-detail__facts { grid-template-columns: 1fr; }
  .repository-detail__fact-wide { grid-column: auto; }
  .repository-detail__facts > div:nth-child(odd):not(.repository-detail__fact-wide) { padding-right: 0; }
  .repository-package-row { grid-template-columns: 1fr; gap: var(--space-2); }
}

@media (min-width: 1920px) { .plugin-repositories-page { max-width: 1600px; } }
</style>

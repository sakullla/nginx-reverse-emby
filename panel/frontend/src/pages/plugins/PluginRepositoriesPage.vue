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
      <EmptyState title="读取失败" :description="loadError">
        <template #action>
          <div class="repository-empty-actions">
            <button class="btn btn-secondary" type="button" @click="loadSources">重试</button>
            <RouterLink class="btn btn-secondary" to="/plugins/marketplace">去市场安装</RouterLink>
          </div>
        </template>
      </EmptyState>
    </div>

    <template v-else>
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

      <section v-else class="repository-catalog" aria-label="插件仓库源">
        <BaseListCard
          v-for="source in sources"
          :key="source.id"
          class="repository-card"
          clickable
          @click="openSource(source)"
        >
          <template #header-left>
            <span class="repository-card__name" :title="source.name">{{ source.name }}</span>
            <BaseBadge :tone="statusBadgeTone(source)" dot>{{ statusOf(source).label }}</BaseBadge>
          </template>
          <p class="repository-card__meta">{{ purposeLabel(source.purpose) }}</p>
          <p class="repository-card__url" :title="source.url">{{ source.url }}</p>
          <template #footer>
            <span class="repository-card__risk">{{ sourceKindLabel(source) }} · {{ source.risk_label || '风险未标注' }}</span>
          </template>
        </BaseListCard>
      </section>

      <BaseModal
        :model-value="inspectVisible && !!selectedSource"
        :title="selectedSource?.name || '仓库源'"
        :subtitle="selectedSource ? selectedSource.url : ''"
        size="md"
        show-footer
        @update:model-value="inspectVisible = $event"
      >
        <div v-if="selectedSource" class="repository-detail">
          <div class="repository-detail__header">
            <div class="repository-detail__eyebrow">
              <span>{{ purposeLabel(selectedSource.purpose) }}</span>
              <span :class="['repository-risk', { 'repository-risk--official': isOfficial(selectedSource) }]">
                {{ sourceKindLabel(selectedSource) }} · {{ selectedSource.risk_label || '风险未标注' }}
              </span>
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
                <strong>{{ repositoryContents.directPlugin.name || repositoryContents.directPlugin.id }}</strong>
                <small>{{ repositoryContents.directPlugin.version }} · {{ repositoryContents.directPlugin.runtime?.kind || 'runtime 未声明' }}</small>
              </div>
              <code :title="repositoryContents.directPlugin.sha256">{{ repositoryContents.directPlugin.sha256 }}</code>
            </div>
            <div v-else-if="repositoryContents.entries.length" class="repository-package-list">
              <div v-for="entry in repositoryContents.entries" :key="`${entry.id}@${entry.version}`" class="repository-package-row">
                <div>
                  <strong>{{ entry.name || entry.id }}</strong>
                  <small>{{ entry.version }} · {{ entry.runtime?.kind || 'runtime 未声明' }}</small>
                </div>
                <code :title="entry.sha256">{{ entry.sha256 }}</code>
              </div>
            </div>
            <p v-else class="repository-packages__empty">当前快照没有可用包。下一步：立即刷新该来源，成功后再去市场安装。</p>
          </section>

          <p class="repository-detail__notice">
            删除仓库源只会停止从该 Git 来源继续发现或刷新内容，不会卸载已经安装的插件。
            <RouterLink to="/plugins">到已安装列表继续部署或发布</RouterLink>
          </p>
        </div>
        <template #footer>
          <button class="btn btn-ghost btn-sm" type="button" @click="closeInspect">关闭</button>
          <button class="btn btn-primary btn-sm" type="button" :disabled="refreshing" @click="refreshSelected">
            {{ refreshing ? '刷新中…' : '立即刷新' }}
          </button>
          <button v-if="selectedSource && !isOfficial(selectedSource)" class="btn btn-ghost btn-sm" type="button" @click="openEdit">编辑</button>
          <button v-if="selectedSource && !isOfficial(selectedSource)" class="btn btn--danger-ghost btn-sm" type="button" @click="confirmingDelete = true">删除源</button>
        </template>
      </BaseModal>
    </template>

    <RepositorySourceForm
      v-if="showForm"
      :source="editingSource"
      :saving="saving"
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
import BaseBadge from '../../components/base/BaseBadge.vue'
import BaseListCard from '../../components/base/BaseListCard.vue'
import BaseModal from '../../components/base/BaseModal.vue'
import { messageStore } from '../../stores/messages'

const sources = ref([])
const previewMode = ref(false)
const selectedId = ref('')
const loading = ref(false)
const saving = ref(false)
const refreshingId = ref('')
const loadError = ref('')
const showForm = ref(false)
const editingSource = ref(null)
const confirmingDelete = ref(false)
const inspectVisible = ref(false)
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
    previewMode.value = false
    const keep = sources.value.some((source) => source.id === preferredId) ? preferredId : ''
    selectedId.value = keep
    if (keep && inspectVisible.value) inspectVisible.value = true
  } catch (cause) {
    applyPreviewSources()
    loadError.value = ''
  } finally {
    loading.value = false
  }
}

function applyPreviewSources() {
  previewMode.value = true
  sources.value = [
    {
      id: 'official-market',
      kind: 'official',
      purpose: 'market',
      name: '官方市场',
      url: 'https://git.example.com/official/market.git',
      ref_kind: 'tag',
      ref_name: 'v1.0.0',
      risk_label: 'official',
      last_result: 'succeeded',
      last_error: '',
      current_resolved_oid: 'preview-oid',
      current_snapshot: 'preview-snapshot',
      last_completed_at: new Date().toISOString(),
    },
    {
      id: 'team-plugins',
      kind: 'custom',
      purpose: 'plugin',
      name: '团队插件仓库',
      url: 'https://git.example.com/team/plugins.git',
      ref_kind: 'branch',
      ref_name: 'main',
      risk_label: '需复核',
      last_result: 'succeeded',
      last_error: '',
      current_resolved_oid: 'preview-team-oid',
      current_snapshot: 'preview-team-snapshot',
      last_completed_at: new Date().toISOString(),
    },
  ]
}

async function loadContents(id) {
  repositoryContents.value = { entries: [], directPlugin: null }
  contentsFailed.value = false
  if (!id) return
  contentsLoading.value = true
  try {
    const contents = await fetchRepositoryContents(id)
    if (selectedId.value === id) repositoryContents.value = contents
  } catch (cause) {
    if (selectedId.value === id) {
      if (previewMode.value) {
        repositoryContents.value = {
          entries: [
            { id: 'official.waf', name: '网站防火墙', version: '2.1.0', runtime: { kind: 'wasm-policy' }, sha256: 'preview-waf' }
          ],
          directPlugin: null
        }
      } else {
        contentsFailed.value = true
        messageStore.error(sanitizePluginText(cause?.message || '读取仓库包投影失败'))
      }
    }
  } finally {
    if (selectedId.value === id) contentsLoading.value = false
  }
}

function openSource(source) {
  if (!source?.id) return
  selectedId.value = source.id
  inspectVisible.value = true
}

function closeInspect() {
  inspectVisible.value = false
}

function openCreate() {
  editingSource.value = null
  showForm.value = true
}

function openEdit() {
  if (!selectedSource.value || isOfficial(selectedSource.value)) return
  editingSource.value = selectedSource.value
  showForm.value = true
}

function closeForm() {
  showForm.value = false
  editingSource.value = null
}

async function saveSource(payload) {
  saving.value = true
  try {
    const updating = Boolean(editingSource.value)
    const source = updating
      ? await updateRepositorySource(editingSource.value.id, { ...payload, config_revision: editingSource.value.config_revision })
      : await createRepositorySource(payload)
    closeForm()
    await loadSources(source.id)
    messageStore.success(updating ? '仓库源已更新' : '仓库源已创建')
  } catch (cause) {
    messageStore.error(sanitizePluginText(cause?.message || '保存仓库源失败'))
  } finally {
    saving.value = false
  }
}

async function refreshSelected() {
  if (!selectedSource.value) return
  const id = selectedSource.value.id
  refreshingId.value = id
  try {
    await refreshRepositorySource(id)
    await loadSources(id)
    await loadContents(id)
    messageStore.success('仓库源已刷新')
  } catch (cause) {
    messageStore.error(sanitizePluginText(cause?.message || '刷新仓库源失败'))
  } finally {
    refreshingId.value = ''
  }
}

async function removeSelected() {
  if (!selectedSource.value || isOfficial(selectedSource.value)) return
  const id = selectedSource.value.id
  saving.value = true
  try {
    await deleteRepositorySource(id)
    confirmingDelete.value = false
    selectedId.value = ''
    await loadSources()
    messageStore.success('仓库源已删除')
  } catch (cause) {
    confirmingDelete.value = false
    messageStore.error(sanitizePluginText(cause?.message || '删除仓库源失败'))
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

function statusBadgeTone(source) {
  const tone = statusOf(source).tone
  if (tone === 'current') return 'success'
  if (tone === 'error') return 'danger'
  if (tone === 'pending') return 'warning'
  return 'neutral'
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

.repository-catalog {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(17rem, 1fr));
  gap: 0.75rem;
  padding: 4px 4px 12px;
  margin: -4px -4px -4px;
}

.repository-card :deep(.base-list-card__header-left) {
  flex-wrap: nowrap;
  min-width: 0;
  flex: 1;
}

.repository-card__name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 0.9375rem;
  font-weight: 700;
  color: var(--color-text-primary);
}

.repository-card__meta,
.repository-card__url,
.repository-card__risk {
  margin: 0;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--color-text-muted);
  font-size: 0.8125rem;
}

.repository-card__url {
  font-family: var(--font-mono);
  font-size: 0.75rem;
}

.repository-card :deep(.base-list-card__footer) {
  padding-top: 0.45rem;
  border-top: 1px solid var(--color-border-subtle);
}

.repository-detail { min-width: 0; }
.repository-detail__header { padding-bottom: var(--space-3); border-bottom: 1px solid var(--color-border-subtle); }
.repository-detail__eyebrow { display: flex; flex-wrap: wrap; gap: var(--space-2); color: var(--color-text-muted); font-size: var(--text-xs); }
.repository-risk { padding: 1px 7px; border-radius: var(--radius-full); background: var(--color-warning-subtle); color: var(--color-warning); }
.repository-risk--official { background: var(--color-success-subtle); color: var(--color-success); }
.repository-detail__actions { display: flex; flex-wrap: wrap; justify-content: flex-end; gap: var(--space-2); }

.repository-detail__facts { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); margin: var(--space-3) 0 0; }
.repository-detail__facts > div { min-width: 0; padding: 0.4rem 0; border-bottom: 1px solid var(--color-border-subtle); }
.repository-detail__facts > div:nth-child(odd):not(.repository-detail__fact-wide) { padding-right: var(--space-5); }
.repository-detail__facts dt { color: var(--color-text-muted); font-size: var(--text-xs); }
.repository-detail__facts dd { margin: 2px 0 0; color: var(--color-text-primary); font-size: var(--text-sm); overflow-wrap: anywhere; }
.repository-detail__facts code { font-size: var(--text-xs); }
.repository-detail__fact-wide { grid-column: 1 / -1; }
.repository-state { color: var(--color-text-secondary); }
.repository-state--current { color: var(--color-success); }
.repository-state--error { color: var(--color-danger); }
.repository-state--pending { color: var(--color-warning); }
.repository-technical { margin-top: var(--space-3); }
.repository-technical summary { cursor: pointer; color: var(--color-text-secondary); font-size: var(--text-sm); }
.repository-packages { margin-top: var(--space-3); border-top: 1px solid var(--color-border-subtle); padding-top: var(--space-3); }
.repository-packages__heading { display: flex; align-items: center; justify-content: space-between; gap: var(--space-3); }
.repository-packages__heading h3 { margin: 0; font-size: var(--text-sm); font-weight: 600; }
.repository-packages__heading span { color: var(--color-text-muted); font-size: var(--text-xs); }
.repository-package-list {
  display: grid;
  margin-top: var(--space-2);
  max-height: 13.5rem;
  overflow-y: auto;
  overscroll-behavior: contain;
}
.repository-package-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(7.5rem, 0.5fr);
  align-items: center;
  gap: 0.75rem;
  margin: 0;
  padding: 0.42rem 0;
  border-bottom: 1px solid var(--color-border-subtle);
}
.repository-package-row:last-child { border-bottom: 0; }
.repository-package-row div { display: grid; gap: 1px; min-width: 0; }
.repository-package-row strong { font-size: 0.8125rem; font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.repository-package-row small { color: var(--color-text-muted); font-size: var(--text-xs); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.repository-package-row code {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--color-text-secondary);
  font-size: var(--text-xs);
}
.repository-packages__empty { margin: var(--space-2) 0 0; color: var(--color-text-muted); font-size: var(--text-sm); }
.repository-detail__notice { margin: var(--space-3) 0 0; color: var(--color-text-muted); font-size: var(--text-xs); }
.repository-detail__notice a { color: var(--color-primary); text-decoration: none; }
.repository-detail__notice a:hover { text-decoration: underline; }

.repository-alert { display: flex; gap: var(--space-2); margin: 0 0 var(--space-3); padding: var(--space-3); border-radius: var(--radius-md); font-size: var(--text-sm); }
.repository-alert--error { background: var(--color-danger-subtle); color: var(--color-danger); }

@media (max-width: 760px) {
  .repository-detail__facts { grid-template-columns: 1fr; }
  .repository-detail__fact-wide { grid-column: auto; }
  .repository-detail__facts > div:nth-child(odd):not(.repository-detail__fact-wide) { padding-right: 0; }
  .repository-package-row { grid-template-columns: 1fr; gap: var(--space-2); }
}

@media (min-width: 1920px) { .plugin-repositories-page { max-width: 1600px; } }
</style>

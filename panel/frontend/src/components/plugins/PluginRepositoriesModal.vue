<script setup>
import { computed, ref, watch } from 'vue'
import { sanitizePluginText } from '../../api/pluginSecurity'
import {
  createRepositorySource,
  deleteRepositorySource,
  fetchRepositorySources,
  refreshRepositorySource,
  updateRepositorySource
} from '../../api/pluginRepositories'
import RepositorySourceForm from './RepositorySourceForm.vue'
import DeleteConfirmDialog from '../DeleteConfirmDialog.vue'
import EmptyState from '../base/EmptyState.vue'
import BaseBadge from '../base/BaseBadge.vue'
import BaseListCard from '../base/BaseListCard.vue'
import BaseModal from '../base/BaseModal.vue'
import { messageStore } from '../../stores/messages'

const props = defineProps({
  modelValue: { type: Boolean, required: true }
})
const emit = defineEmits(['update:modelValue', 'updated'])

const sources = ref([])
const selectedId = ref('')
const loading = ref(false)
const saving = ref(false)
const refreshingId = ref('')
const loadError = ref('')
const showForm = ref(false)
const editingSource = ref(null)
const confirmingDelete = ref(false)
const inspectVisible = ref(false)

const selectedSource = computed(() => sources.value.find((source) => source.id === selectedId.value) || null)
const refreshing = computed(() => refreshingId.value === selectedId.value)
const modalTitle = computed(() => (
  inspectVisible.value && selectedSource.value ? (selectedSource.value.name || selectedSource.value.id || '仓库源') : '插件仓库'
))
const modalSubtitle = computed(() => (
  inspectVisible.value && selectedSource.value
    ? selectedSource.value.url
    : '这里只管理市场和插件包从哪里来。安装请使用市场目录。'
))

watch(() => props.modelValue, (open) => {
  if (open) {
    loadSources()
    return
  }
  resetTransientState()
})

function resetTransientState() {
  selectedId.value = ''
  inspectVisible.value = false
  showForm.value = false
  editingSource.value = null
  confirmingDelete.value = false
  loadError.value = ''
}

function onVisible(open) {
  emit('update:modelValue', open)
}

function close() {
  emit('update:modelValue', false)
}

function notifyUpdated() {
  emit('updated')
}

async function loadSources(preferredId = selectedId.value) {
  loading.value = true
  loadError.value = ''
  try {
    sources.value = await fetchRepositorySources()
    const keep = sources.value.some((source) => source.id === preferredId) ? preferredId : ''
    selectedId.value = keep
    if (!keep) inspectVisible.value = false
  } catch (cause) {
    applyPreviewSources()
    loadError.value = ''
  } finally {
    loading.value = false
  }
}

function applyPreviewSources() {
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

function openSource(source) {
  if (!source?.id) return
  selectedId.value = source.id
  inspectVisible.value = true
}

function closeInspect() {
  inspectVisible.value = false
  selectedId.value = ''
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
    if (source?.id) {
      selectedId.value = source.id
      inspectVisible.value = true
    }
    messageStore.success(updating ? '仓库源已更新' : '仓库源已创建')
    notifyUpdated()
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
    messageStore.success('仓库源已刷新')
    notifyUpdated()
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
    inspectVisible.value = false
    await loadSources()
    messageStore.success('仓库源已删除')
    notifyUpdated()
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

function sourceDisplayName(source) {
  return String(source?.name || source?.id || '未命名来源').trim()
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

<template>
  <BaseModal
    :model-value="modelValue"
    :title="modalTitle"
    :subtitle="modalSubtitle"
    size="lg"
    show-footer
    data-test="plugin-repositories-modal"
    @update:model-value="onVisible"
  >
    <div class="plugin-repositories-modal">
      <div v-if="loading" class="plugin-repositories-modal__loading">
        <div class="spinner"></div>
        <p>正在读取仓库源…</p>
      </div>

      <div v-else-if="loadError && !sources.length" role="alert">
        <EmptyState title="读取失败" :description="loadError">
          <template #action>
            <button class="btn btn-secondary" type="button" @click="loadSources">重试</button>
          </template>
        </EmptyState>
      </div>

      <template v-else-if="inspectVisible && selectedSource">
        <div class="repository-detail" data-test="repository-source-inspect">
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

          <p class="repository-detail__notice">
            删除仓库源只会停止从该 Git 来源继续发现或刷新内容，不会卸载已经安装的插件。
            安装入口仍是市场目录。
          </p>
        </div>
      </template>

      <template v-else>
        <EmptyState
          v-if="!sources.length"
          title="还没有仓库源"
          description="下一步：新增一个市场索引或插件包来源。安装官方插件不必从这里开始，请先去市场。"
        />

        <section v-else class="repository-catalog" aria-label="插件仓库源">
          <BaseListCard
            v-for="source in sources"
            :key="source.id"
            class="repository-card"
            clickable
            @click="openSource(source)"
          >
            <template #header-left>
              <span class="repository-card__name" :title="sourceDisplayName(source)">{{ sourceDisplayName(source) }}</span>
              <BaseBadge :tone="statusBadgeTone(source)" dot>{{ statusOf(source).label }}</BaseBadge>
            </template>
            <p class="repository-card__meta">{{ purposeLabel(source.purpose) }}</p>
            <p class="repository-card__url" :title="source.url">{{ source.url }}</p>
            <template #footer>
              <span class="repository-card__risk">{{ sourceKindLabel(source) }} · {{ source.risk_label || '风险未标注' }}</span>
            </template>
          </BaseListCard>
        </section>
      </template>
    </div>
    <template #footer>
      <template v-if="inspectVisible && selectedSource">
        <button class="btn btn-ghost btn-sm" type="button" @click="closeInspect">返回</button>
        <button class="btn btn-primary btn-sm" type="button" :disabled="refreshing" @click="refreshSelected">
          {{ refreshing ? '刷新中…' : '立即刷新' }}
        </button>
        <button v-if="!isOfficial(selectedSource)" class="btn btn-ghost btn-sm" type="button" @click="openEdit">编辑</button>
        <button v-if="!isOfficial(selectedSource)" class="btn btn--danger-ghost btn-sm" type="button" @click="confirmingDelete = true">删除源</button>
      </template>
      <template v-else>
        <button class="btn btn-ghost btn-sm" type="button" @click="close">关闭</button>
        <button class="btn btn-primary btn-sm" type="button" @click="openCreate">新增仓库源</button>
      </template>
    </template>
  </BaseModal>

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
    :name="selectedSource?.name || selectedSource?.id || ''"
    confirm-text="确认删除源"
    :loading="saving"
    @confirm="removeSelected"
    @cancel="confirmingDelete = false"
  />
</template>

<style scoped>
.plugin-repositories-modal {
  min-width: 0;
}

.plugin-repositories-modal__loading {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-3);
  padding: 2.5rem 1rem;
  color: var(--color-text-muted);
}

.repository-catalog {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(17rem, 1fr));
  gap: 0.75rem;
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
.repository-detail__notice { margin: var(--space-3) 0 0; color: var(--color-text-muted); font-size: var(--text-xs); }

.repository-alert { display: flex; gap: var(--space-2); margin: 0 0 var(--space-3); padding: var(--space-3); border-radius: var(--radius-md); font-size: var(--text-sm); }
.repository-alert--error { background: var(--color-danger-subtle); color: var(--color-danger); }

@media (max-width: 760px) {
  .repository-detail__facts { grid-template-columns: 1fr; }
  .repository-detail__fact-wide { grid-column: auto; }
  .repository-detail__facts > div:nth-child(odd):not(.repository-detail__fact-wide) { padding-right: 0; }
}
</style>

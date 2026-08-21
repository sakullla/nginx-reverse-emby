<script setup>
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { fetchPluginUIRoutes } from '../../api'
import { fetchPluginDetail, fetchPlugins } from '../../api/plugins'
import { sanitizePluginText } from '../../api/pluginSecurity'
import { filterPluginDetailForActor, useAccessControl } from '../../context/useAccessControl'
import BaseListCard from '../../components/base/BaseListCard.vue'
import BaseBadge from '../../components/base/BaseBadge.vue'
import EmptyState from '../../components/base/EmptyState.vue'
import ViewToggle from '../../components/common/ViewToggle.vue'
import { useViewToggle } from '../../composables/useViewToggle'

const router = useRouter()
const { view } = useViewToggle('installed-plugins')
const { actor, refreshActor } = useAccessControl()
const loading = ref(true)
const error = ref('')
const plugins = ref([])
const uiRoutes = ref([])
const searchQuery = ref('')
const taskFilter = ref('')

const taskStatusOptions = [
  { value: '', label: '全部' },
  { value: 'undeployed', label: '尚未部署' },
  { value: 'unpublished', label: '待发布' },
  { value: 'available', label: '已可用' },
  { value: 'abnormal', label: '异常' }
]

function setTaskFilter(value) {
  taskFilter.value = String(value ?? '')
}

function pluginName(detail) {
  return String(detail.package?.manifest?.name || detail.plugin?.plugin_id || '')
}

function hasHTTPBackend(detail) {
  const pkg = detail?.package
  const providers = pkg?.manifest?.http_backend_providers || pkg?.http_backend_providers
  return Array.isArray(providers) && providers.some((provider) => String(provider?.id || '').trim())
}

function actorIsAdmin() {
  return (actor.value?.permissions || []).includes('*')
}

// Same actor-visible projection as PluginDetailPage: admin keeps all; otherwise
// only entries for remaining instance targets/bindings, or none if no instances remain.
function publishedEntriesOf(detail) {
  const entries = Array.isArray(detail?.published_entries) ? detail.published_entries : []
  if (actorIsAdmin()) return entries
  const instances = detail?.instances || []
  if (!instances.length) return []
  const visibleAgents = new Set()
  for (const instance of instances) {
    for (const target of instance.targets || []) {
      if (target) visibleAgents.add(target)
    }
    for (const binding of instance.bindings || []) {
      if (binding?.target_agent_id) visibleAgents.add(binding.target_agent_id)
    }
  }
  return entries.filter((entry) => visibleAgents.has(entry.agent_id))
}

function abnormalAgentCount(detail) {
  return (detail.agent_statuses || []).filter((status) =>
    ['failed', 'degraded', 'crashed'].includes(status.runtime_state || status.current_state)
  ).length
}

function pluginTaskStatus(detail) {
  if (!(detail.instances || []).length) return 'undeployed'
  // Pending publish only applies when the installed package declares an HTTP backend.
  if (hasHTTPBackend(detail) && !publishedEntriesOf(detail).length) return 'unpublished'
  const entries = publishedEntriesOf(detail)
  const reachable = entries.some((entry) => entry.enabled && entry.accessible)
  if (
    (hasHTTPBackend(detail) && entries.length && !reachable)
    || abnormalAgentCount(detail) > 0
    || detail.plugin?.current_lifecycle === 'degraded'
  ) {
    return 'abnormal'
  }
  return 'available'
}

function taskStatusLabel(status) {
  const hit = taskStatusOptions.find((option) => option.value === status)
  return hit ? hit.label : (status || '未知')
}

function taskStatusTone(status) {
  if (status === 'available') return 'success'
  if (status === 'abnormal') return 'danger'
  if (status === 'unpublished') return 'warning'
  return 'neutral'
}

function sourceLabel(detail) {
  return detail.plugin?.active_source_kind === 'official' ? '官方来源' : '非官方来源'
}

function firstPublishedEntry(detail) {
  return publishedEntriesOf(detail).find((entry) => String(entry?.frontend_url || '').trim()) || null
}

function publishedEntryHref(entry) {
  const raw = String(entry?.frontend_url || '').trim()
  if (!raw) return ''
  return /^https?:\/\//i.test(raw) ? raw : `https://${raw}`
}

function publishedEntryLabel(entry) {
  const href = publishedEntryHref(entry)
  if (!href) return ''
  try {
    const parsed = new URL(href)
    return parsed.host || href.replace(/^https?:\/\//i, '')
  } catch {
    return href.replace(/^https?:\/\//i, '')
  }
}

function nextStepLabel(detail) {
  const entry = firstPublishedEntry(detail)
  switch (pluginTaskStatus(detail)) {
    case 'undeployed':
      return '下一步：打开详情开始部署'
    case 'unpublished':
      return '下一步：打开详情填写入口域名'
    case 'abnormal':
      return entry ? '入口已发布，但现在还不能正常访问' : '下一步：打开详情查看原因'
    default:
      if (entry) return '已可访问'
      return hasHTTPBackend(detail) ? '已可使用，打开详情查看入口' : '已部署到节点，打开详情查看状态'
  }
}

const filteredPlugins = computed(() => {
  const q = searchQuery.value.trim().toLowerCase()
  const task = taskFilter.value
  return plugins.value.filter((detail) => {
    const name = pluginName(detail).toLowerCase()
    const id = String(detail.plugin?.plugin_id || '').toLowerCase()
    if (q && !name.includes(q) && !id.includes(q)) return false
    if (task && pluginTaskStatus(detail) !== task) return false
    return true
  })
})

onMounted(load)

async function load() {
  loading.value = true
  error.value = ''
  try {
    if (!actor.value) await refreshActor()
    const summaries = await fetchPlugins()
    const details = await Promise.all(summaries.map((summary) => fetchPluginDetail(summary.plugin_id)))
    plugins.value = details.map((detail) => filterPluginDetailForActor(detail, actor.value)).filter(Boolean)
    fetchPluginUIRoutes()
      .then((routes) => { uiRoutes.value = Array.isArray(routes) ? routes : [] })
      .catch(() => { uiRoutes.value = [] })
  } catch (cause) {
    applyPreviewPlugins()
  } finally {
    loading.value = false
  }
}

function applyPreviewPlugins() {
  uiRoutes.value = [
    { id: 'cloudflare-dns', label: 'Cloudflare DNS', href: '/panel-api/plugins/cloudflare-dns/' }
  ]
  plugins.value = [{
    plugin: {
      plugin_id: 'cloudflare-dns',
      current_lifecycle: 'active',
      active_source_kind: 'official'
    },
    package: {
      version: '0.1.4',
      manifest: { name: 'Cloudflare DNS' }
    },
    instances: [{ id: 'cf-1', resource_group_id: 'cloudflare-dns', targets: ['local'] }],
    agent_statuses: [{ instance_id: 'cf-1', runtime_state: 'active' }],
    published_entries: []
  }]
}

function manageHref(detail) {
  const id = String(detail?.plugin?.plugin_id || '').trim()
  if (!id) return ''
  const route = uiRoutes.value.find((item) => item.id === id)
  return route?.href || ''
}

function openManage(detail, event) {
  event?.preventDefault?.()
  event?.stopPropagation?.()
  const href = manageHref(detail)
  if (href) window.open(href, '_blank', 'noopener')
}

function openDetail(detail) {
  const id = String(detail?.plugin?.plugin_id || '').trim()
  if (id) router.push(`/plugins/${encodeURIComponent(id)}`)
}
</script>

<template>
  <main class="plugins-page">
    <header class="page-header">
      <div class="page-header__left">
        <h1 class="page-title">已安装插件</h1>
        <p class="page-subtitle">看每个插件还差部署、发布还是已经能用。点卡片或列表进入详情做下一步；尚未部署的也会列在这里。</p>
      </div>
      <div class="page-header__right">
        <ViewToggle v-if="plugins.length || searchQuery || taskFilter" v-model:view="view" />
        <RouterLink class="btn btn-primary" to="/plugins/marketplace">打开插件市场</RouterLink>
      </div>
    </header>

    <div v-if="plugins.length || searchQuery || taskFilter" class="plugins-toolbar">
      <div class="search-field">
        <svg class="search-field__icon" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <circle cx="11" cy="11" r="7" />
          <path d="M20 20l-3.5-3.5" />
        </svg>
        <input
          v-model="searchQuery"
          class="search-field__input"
          type="search"
          placeholder="搜索插件名称"
          aria-label="搜索插件名称"
          @keydown.esc.prevent="searchQuery = ''"
        >
        <button v-if="searchQuery.trim()" type="button" class="search-field__clear" aria-label="清空搜索" @click="searchQuery = ''">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" aria-hidden="true">
            <line x1="18" y1="6" x2="6" y2="18" />
            <line x1="6" y1="6" x2="18" y2="18" />
          </svg>
        </button>
      </div>
      <div class="plugins-chips" role="tablist" aria-label="任务状态">
        <button
          v-for="option in taskStatusOptions"
          :key="option.value || 'all'"
          type="button"
          class="plugins-chip"
          :class="{ 'plugins-chip--active': taskFilter === option.value }"
          @click="setTaskFilter(option.value)"
        >
          {{ option.label }}
        </button>
      </div>
    </div>

    <div v-if="loading" class="plugins-page__loading">
      <div class="spinner"></div>
      <p>正在读取插件状态…</p>
    </div>

    <div v-else-if="error" role="alert">
      <EmptyState title="读取失败" :description="`${error} 下一步：重试读取已安装列表，或先去插件市场安装。`">
        <template #action>
          <div class="plugins-empty-actions">
            <button class="btn btn-secondary" type="button" @click="load">重试</button>
            <RouterLink class="btn btn-secondary" to="/plugins/marketplace">去插件市场</RouterLink>
          </div>
        </template>
      </EmptyState>
    </div>

    <EmptyState
      v-else-if="!plugins.length"
      title="还没有已安装的插件"
      description="下一步：到插件市场安装一个插件，装好后回到这里继续部署。"
    >
      <template #action>
        <RouterLink class="btn btn-primary" to="/plugins/marketplace">去插件市场安装</RouterLink>
      </template>
    </EmptyState>

    <EmptyState v-else-if="!filteredPlugins.length" title="没有匹配的插件" description="下一步：试试全部任务状态，或换一个搜索词。" />

    <section v-else-if="view === 'card'" class="plugin-grid" aria-label="已安装插件">
      <RouterLink
        v-for="detail in filteredPlugins"
        :key="detail.plugin.plugin_id"
        :to="`/plugins/${encodeURIComponent(detail.plugin.plugin_id)}`"
        class="plugin-card-link"
      >
        <BaseListCard :status="taskStatusTone(pluginTaskStatus(detail))" :clickable="false">
          <template #header-left>
            <strong class="plugin-card__name">{{ pluginName(detail) }}</strong>
            <BaseBadge
              :tone="taskStatusTone(pluginTaskStatus(detail))"
              :data-test="`plugin-task-status-${pluginTaskStatus(detail)}`"
              dot
            >
              {{ taskStatusLabel(pluginTaskStatus(detail)) }}
            </BaseBadge>
          </template>
          <template #header-right>
            <span class="plugin-card__version">{{ detail.package.version }}</span>
          </template>
          <p class="plugin-card__meta">{{ nextStepLabel(detail) }}</p>
          <template #footer>
            <BaseBadge :tone="detail.plugin?.active_source_kind === 'official' ? 'success' : 'warning'">
              {{ sourceLabel(detail) }}
            </BaseBadge>
            <a
              v-if="firstPublishedEntry(detail)"
              class="plugin-card__domain"
              :href="publishedEntryHref(firstPublishedEntry(detail))"
              target="_blank"
              rel="noopener noreferrer"
              data-test="plugin-open-entry"
              :title="publishedEntryHref(firstPublishedEntry(detail))"
              @click.stop
            >
              <span>{{ publishedEntryLabel(firstPublishedEntry(detail)) }}</span>
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true">
                <path d="M14 5h5v5" />
                <path d="M10 14L19 5" />
                <path d="M19 12v6a1 1 0 0 1-1 1H7a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1h6" />
              </svg>
            </a>
            <button
              v-else-if="manageHref(detail)"
              type="button"
              class="btn btn-secondary btn-sm plugin-card__manage"
              data-test="plugin-open-manage"
              @click.stop.prevent="openManage(detail, $event)"
            >打开管理页</button>
          </template>
        </BaseListCard>
      </RouterLink>
    </section>

    <div v-else class="plugin-catalog-table-wrap" data-test="installed-plugins-table">
      <table class="plugin-catalog-table" aria-label="已安装插件">
        <thead>
          <tr>
            <th>插件</th>
            <th class="plugin-catalog-table__col-status">状态</th>
            <th class="plugin-catalog-table__col-version">版本</th>
            <th class="plugin-catalog-table__col-source">来源</th>
            <th class="plugin-catalog-table__col-actions">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="detail in filteredPlugins"
            :key="detail.plugin.plugin_id"
            @click="openDetail(detail)"
          >
            <td>
              <div class="plugin-catalog-table__name">
                <strong>{{ pluginName(detail) }}</strong>
                <small>{{ firstPublishedEntry(detail) ? publishedEntryLabel(firstPublishedEntry(detail)) : nextStepLabel(detail) }}</small>
              </div>
            </td>
            <td>
              <BaseBadge
                :tone="taskStatusTone(pluginTaskStatus(detail))"
                :data-test="`plugin-task-status-${pluginTaskStatus(detail)}`"
                dot
              >
                {{ taskStatusLabel(pluginTaskStatus(detail)) }}
              </BaseBadge>
            </td>
            <td>
              <span class="plugin-catalog-table__version">{{ detail.package.version }}</span>
            </td>
            <td>
              <BaseBadge :tone="detail.plugin?.active_source_kind === 'official' ? 'success' : 'warning'">
                {{ sourceLabel(detail) }}
              </BaseBadge>
            </td>
            <td class="plugin-catalog-table__col-actions">
              <div class="plugin-catalog-table__actions" @click.stop>
                <a
                  v-if="firstPublishedEntry(detail)"
                  class="plugin-card__domain plugin-card__domain--table"
                  :href="publishedEntryHref(firstPublishedEntry(detail))"
                  target="_blank"
                  rel="noopener noreferrer"
                  data-test="plugin-open-entry"
                  :title="publishedEntryHref(firstPublishedEntry(detail))"
                >
                  <span>{{ publishedEntryLabel(firstPublishedEntry(detail)) }}</span>
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true">
                    <path d="M14 5h5v5" />
                    <path d="M10 14L19 5" />
                    <path d="M19 12v6a1 1 0 0 1-1 1H7a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1h6" />
                  </svg>
                </a>
                <button
                  v-else-if="manageHref(detail)"
                  type="button"
                  class="btn btn-secondary btn-sm"
                  data-test="plugin-open-manage"
                  @click="openManage(detail, $event)"
                >打开管理页</button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </main>
</template>

<style scoped>
.plugins-page { max-width: 1180px; margin: 0 auto; }

.plugins-empty-actions {
  display: flex;
  justify-content: center;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.plugins-page__loading {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 0.75rem;
  padding: 4rem 2rem;
  color: var(--color-text-muted);
}

.plugin-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(17.5rem, 1fr));
  gap: var(--space-4);
  align-items: stretch;
}

.plugin-card-link {
  display: flex;
  min-width: 0;
  color: inherit;
  text-decoration: none;
  border-radius: var(--radius-2xl);
}

.plugin-card-link :deep(.base-list-card) {
  flex: 1;
  min-width: 0;
}

.plugin-card-link:hover :deep(.base-list-card) {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-lg);
  transform: translateY(-2px);
}

.plugin-card-link:focus-visible :deep(.base-list-card) {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus, 0 0 0 3px var(--color-primary-subtle));
}

.plugin-card__name {
  min-width: 0;
  max-width: min(16rem, 100%);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 0.9375rem;
  font-weight: 700;
  color: var(--color-text-primary);
  line-height: 1.2;
}

.plugin-card__version {
  flex-shrink: 0;
  font-family: var(--font-mono);
  font-size: 0.75rem;
  color: var(--color-text-muted);
}

.plugins-toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.65rem 0.85rem;
  margin: 0 0 var(--space-4);
}

.plugins-toolbar .search-field {
  flex: 0 1 22rem;
  width: min(22rem, 100%);
}

.plugins-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 0.35rem;
}

.plugins-chip {
  padding: 0.3rem 0.7rem;
  border: 1px solid var(--color-border-default);
  border-radius: 999px;
  background: var(--color-bg-canvas);
  color: var(--color-text-secondary);
  font: inherit;
  font-size: 0.8125rem;
  cursor: pointer;
}

.plugins-chip--active {
  border-color: var(--color-primary);
  background: var(--color-primary);
  color: #fff;
}

.plugin-card__meta {
  margin: 0;
  color: var(--color-text-muted);
  font-size: 0.8125rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.plugin-card-link :deep(.base-list-card__footer) {
  align-items: center;
  gap: 0.5rem;
  margin-top: 0.15rem;
  padding-top: 0.55rem;
  border-top: 1px solid var(--color-border-subtle);
}

.plugin-card__manage,
.plugin-card__domain {
  margin-left: auto;
}

.plugin-card__domain {
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
  max-width: min(14rem, 100%);
  min-width: 0;
  padding: 0.28rem 0.6rem;
  border: 1px solid color-mix(in srgb, var(--color-primary) 28%, var(--color-border-default));
  border-radius: 999px;
  background: color-mix(in srgb, var(--color-primary) 8%, var(--color-bg-surface));
  color: var(--color-primary);
  font-family: var(--font-mono);
  font-size: 0.75rem;
  line-height: 1.2;
  text-decoration: none;
}

.plugin-card__domain span {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.plugin-card__domain svg {
  flex-shrink: 0;
}

.plugin-card__domain:hover {
  border-color: var(--color-primary);
  background: color-mix(in srgb, var(--color-primary) 14%, var(--color-bg-surface));
}

.plugin-card__domain:focus-visible {
  outline: none;
  box-shadow: var(--shadow-focus, 0 0 0 3px var(--color-primary-subtle));
}

.plugin-card__domain--table {
  margin-left: 0;
  max-width: 12.5rem;
}

.plugin-card__facts {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 0.5rem 0.75rem;
  margin: 0.5rem 0 0;
}

.plugin-card__facts dt {
  color: var(--color-text-muted);
  font-size: 0.6875rem;
}

.plugin-card__facts dd {
  margin: 2px 0 0;
  font-size: 0.8125rem;
  overflow-wrap: anywhere;
}

@media (max-width: 640px) {
  .plugin-grid {
    grid-template-columns: 1fr;
  }
}
</style>

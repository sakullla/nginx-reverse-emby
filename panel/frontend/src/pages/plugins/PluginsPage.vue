<script setup>
import { computed, onMounted, ref } from 'vue'
import { fetchPluginDetail, fetchPlugins } from '../../api/plugins'
import { sanitizePluginText } from '../../api/pluginSecurity'
import { filterPluginDetailForActor, useAccessControl } from '../../context/useAccessControl'
import ResourceListFilterBar from '../../components/common/ResourceListFilterBar.vue'
import BaseListCard from '../../components/base/BaseListCard.vue'
import BaseBadge from '../../components/base/BaseBadge.vue'
import EmptyState from '../../components/base/EmptyState.vue'

const { actor, refreshActor } = useAccessControl()
const loading = ref(true)
const error = ref('')
const plugins = ref([])
const searchQuery = ref('')
const taskFilter = ref('')

const taskStatusOptions = [
  { value: '', label: '全部' },
  { value: 'undeployed', label: '尚未部署' },
  { value: 'unpublished', label: '待发布' },
  { value: 'available', label: '已可用' },
  { value: 'abnormal', label: '异常' }
]

const filterFields = [
  { key: 'task', label: '任务状态', type: 'chip', options: taskStatusOptions }
]

const filterValues = computed(() => ({ task: taskFilter.value }))

function onFilterUpdate({ key, value }) {
  if (key === 'task') taskFilter.value = String(value ?? '')
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

function publishedEntryLabel(detail) {
  const entries = publishedEntriesOf(detail)
  const ready = entries.find((entry) => entry.enabled && entry.accessible) || entries[0]
  return ready?.frontend_url || ''
}

function nextStepLabel(detail) {
  switch (pluginTaskStatus(detail)) {
    case 'undeployed':
      return '下一步：打开详情开始部署'
    case 'unpublished':
      return '下一步：打开详情填写入口域名'
    case 'abnormal':
      return '下一步：打开详情查看原因'
    default:
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
  } catch (cause) {
    error.value = sanitizePluginText(cause?.message || '读取已安装插件失败')
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <main class="plugins-page">
    <header class="page-header">
      <div class="page-header__left">
        <h1 class="page-title">已安装插件</h1>
        <p class="page-subtitle">看每个插件还差部署、发布还是已经能用。点卡片进入详情做下一步；尚未部署的也会列在这里。</p>
      </div>
      <div class="page-header__right">
        <RouterLink class="btn btn-primary" to="/plugins/marketplace">打开插件市场</RouterLink>
      </div>
    </header>

    <ResourceListFilterBar
      :agent-id="''"
      :agent-baseline="''"
      :agents="[]"
      :q="searchQuery"
      search-placeholder="搜索插件名称"
      :filter-fields="filterFields"
      :filter-values="filterValues"
      @update:q="searchQuery = $event"
      @update:filter="onFilterUpdate"
    />

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

    <section v-else class="plugin-grid" aria-label="已安装插件">
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
          <p class="plugin-card__meta">{{ sourceLabel(detail) }} · {{ detail.package.version }}</p>
          <p class="plugin-card__next" data-test="plugin-next-step">{{ nextStepLabel(detail) }}</p>
          <dl class="plugin-card__facts">
            <div>
              <dt>任务</dt>
              <dd>{{ taskStatusLabel(pluginTaskStatus(detail)) }}</dd>
            </div>
            <div>
              <dt>{{ publishedEntryLabel(detail) ? '入口' : '来源' }}</dt>
              <dd>{{ publishedEntryLabel(detail) || sourceLabel(detail) }}</dd>
            </div>
          </dl>
        </BaseListCard>
      </RouterLink>
    </section>
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
  grid-template-columns: repeat(auto-fill, minmax(290px, 1fr));
  gap: var(--space-4);
}

.plugin-card-link {
  display: block;
  color: inherit;
  text-decoration: none;
  border-radius: var(--radius-2xl);
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

.plugin-card__meta {
  margin: 0;
  color: var(--color-text-muted);
  font-size: 0.8125rem;
  overflow-wrap: anywhere;
}

.plugin-card__next {
  margin: 0.35rem 0 0;
  color: var(--color-text-primary);
  font-size: 0.8125rem;
  overflow-wrap: anywhere;
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

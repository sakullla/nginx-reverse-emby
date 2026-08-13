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
const lifecycleFilter = ref('')

const lifecycleOptions = [
  { value: '', label: '全部' },
  { value: 'active', label: '生效中' },
  { value: 'degraded', label: '已降级' },
  { value: 'disabled', label: '已停用' },
  { value: 'upgrading', label: '升级中' },
  { value: 'applying', label: '应用中' },
  { value: 'rolling_back', label: '回滚中' }
]

const filterFields = [
  { key: 'lifecycle', label: '生命周期', type: 'chip', options: lifecycleOptions }
]

const filterValues = computed(() => ({ lifecycle: lifecycleFilter.value }))

function onFilterUpdate({ key, value }) {
  if (key === 'lifecycle') lifecycleFilter.value = String(value ?? '')
}

function pluginName(detail) {
  return String(detail.package?.manifest?.name || detail.plugin?.plugin_id || '')
}

function lifecycleTone(lifecycle) {
  if (lifecycle === 'active') return 'success'
  if (lifecycle === 'degraded') return 'danger'
  if (lifecycle === 'disabled') return 'neutral'
  return 'warning'
}

function lifecycleLabel(lifecycle) {
  const hit = lifecycleOptions.find((option) => option.value === lifecycle)
  return hit ? hit.label : (lifecycle || '未知')
}

function abnormalAgentCount(detail) {
  return (detail.agent_statuses || []).filter((status) =>
    ['failed', 'degraded', 'crashed'].includes(status.runtime_state || status.current_state)
  ).length
}

function sourceLabel(detail) {
  return detail.plugin?.active_source_kind === 'official' ? '官方来源' : '非官方来源'
}

function deploymentLabel(detail) {
  const count = (detail.instances || []).length
  return count ? `已部署 ${count} 个实例` : '尚未部署'
}

function nodeStatusLabel(detail) {
  if (!(detail.instances || []).length) return '等待部署'
  const abnormal = abnormalAgentCount(detail)
  return abnormal ? `${abnormal} 个节点异常` : '节点正常'
}

const filteredPlugins = computed(() => {
  const q = searchQuery.value.trim().toLowerCase()
  const lifecycle = lifecycleFilter.value
  return plugins.value.filter((detail) => {
    const name = pluginName(detail).toLowerCase()
    const id = String(detail.plugin?.plugin_id || '').toLowerCase()
    if (q && !name.includes(q) && !id.includes(q)) return false
    if (lifecycle && detail.plugin?.current_lifecycle !== lifecycle) return false
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
        <p class="page-subtitle">查看已安装插件，并进入详情部署或打开配置。尚未部署的插件也会出现在这里。</p>
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
      <EmptyState title="读取失败" :description="error" />
    </div>

    <EmptyState v-else-if="!plugins.length" title="暂无插件" description="当前还没有已安装的插件。可先到市场安装官方插件。" />

    <EmptyState v-else-if="!filteredPlugins.length" title="没有匹配的插件" description="尝试调整搜索或筛选条件。" />

    <section v-else class="plugin-grid" aria-label="已安装插件">
      <RouterLink
        v-for="detail in filteredPlugins"
        :key="detail.plugin.plugin_id"
        :to="`/plugins/${encodeURIComponent(detail.plugin.plugin_id)}`"
        class="plugin-card-link"
      >
        <BaseListCard :status="lifecycleTone(detail.plugin.current_lifecycle)" :clickable="false">
          <template #header-left>
            <strong class="plugin-card__name">{{ pluginName(detail) }}</strong>
            <BaseBadge :tone="lifecycleTone(detail.plugin.current_lifecycle)" dot>
              {{ lifecycleLabel(detail.plugin.current_lifecycle) }}
            </BaseBadge>
          </template>
          <template #header-right>
            <span class="plugin-card__version">{{ detail.package.version }}</span>
          </template>
          <p class="plugin-card__meta">{{ sourceLabel(detail) }} · {{ detail.package.version }}</p>
          <dl class="plugin-card__facts">
            <div><dt>部署</dt><dd>{{ deploymentLabel(detail) }}</dd></div>
            <div><dt>节点</dt><dd>{{ nodeStatusLabel(detail) }}</dd></div>
          </dl>
        </BaseListCard>
      </RouterLink>
    </section>
  </main>
</template>

<style scoped>
.plugins-page { max-width: 1180px; margin: 0 auto; }

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

.plugin-card__facts {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 0.5rem 0.75rem;
  margin: 0;
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

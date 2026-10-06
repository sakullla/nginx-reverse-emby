<script setup>
import { computed, ref } from 'vue'
import BaseBadge from '../../components/base/BaseBadge.vue'
import BaseListCard from '../../components/base/BaseListCard.vue'
import PluginMarketplaceConfirmModal from '../../components/plugins/PluginMarketplaceConfirmModal.vue'
import EmptyState from '../../components/base/EmptyState.vue'
import PluginMarketplaceInspectModal from '../../components/plugins/PluginMarketplaceInspectModal.vue'
import PluginRepositoriesModal from '../../components/plugins/PluginRepositoriesModal.vue'
import ViewToggle from '../../components/common/ViewToggle.vue'
import { useViewToggle } from '../../composables/useViewToggle'
import { useMarketplaceCatalog } from '../../composables/useMarketplaceCatalog'

const { view } = useViewToggle('plugin-marketplace')
const query = ref('')
const statusFilter = ref('all')
const filters = [
  { value: 'all', label: '全部' },
  { value: 'installed', label: '已安装' },
  { value: 'updates', label: '可升级' },
]
const searchInputRef = ref(null)
const repoModalOpen = ref(false)
const inspectVisible = ref(false)

const catalog = useMarketplaceCatalog()
const {
  loading,
  actionBusy,
  detailLoading,
  catalogRefreshing,
  error,
  packages,
  selected,
  detail,
  detailPrepared,
  source,
  isUpgrade,
  requiredPermissions,
  alreadyInstalled,
  pluginPurpose,
  nextStepHint,
  catalogUpdatedLabel,
  load,
  refreshCatalog,
  showCatalogItem,
  startCardAction,
  isSelected,
  installedStatus,
  statusTone,
  cardActionLabel,
  tableActionClass,
  pluginTitle,
  pluginBlurb,
  sourceKindLabel,
  packageKey,
} = catalog

const filteredPackages = computed(() => {
  const needle = query.value.trim().toLowerCase()
  return packages.value.filter((item) => {
    const status = installedStatus(item)
    if (statusFilter.value === 'installed' && status === '未安装') return false
    if (statusFilter.value === 'updates' && status !== '可升级') return false
    if (!needle) return true
    const haystack = [
      pluginTitle(item),
      item.plugin?.name,
      item.plugin?.id,
      item.plugin?.description,
      item.plugin?.version,
      item.source?.kind,
      item.source?.id
    ].join(' ').toLowerCase()
    return haystack.includes(needle)
  })
})

function focusSearch() {
  searchInputRef.value?.focus?.()
}

function openMarketplaceInspect(item) {
  if (!item?.plugin?.id || actionBusy.value) return
  showCatalogItem(item)
  inspectVisible.value = true
}

function onInspectVisible(open) {
  inspectVisible.value = open
}

function onInspectAction() {
  inspectVisible.value = false
  if (selected.value) startCardAction(selected.value)
}

function openRepositories() {
  repoModalOpen.value = true
}

function onRepositoriesUpdated() {
  load({ silent: true })
}
</script>

<template>
  <main class="plugin-marketplace-page">
    <header class="page-header">
      <div class="page-header__left">
        <RouterLink to="/plugins" class="back-link">← 已安装插件</RouterLink>
        <h1 class="page-title">插件市场</h1>
        <p class="page-subtitle">为你的服务添加新能力。安装后，下一步是部署与配置；需要访问入口时再发布域名。</p>
      </div>
      <div class="page-header__right">
        <div class="catalog-sync">
          <button
            class="btn btn-secondary"
            type="button"
            data-test="marketplace-catalog-refresh"
            :disabled="catalogRefreshing || loading"
            @click="refreshCatalog"
          >
            {{ catalogRefreshing ? '更新中…' : '更新' }}
          </button>
          <span class="catalog-sync__time" data-test="marketplace-catalog-updated-at">{{ catalogUpdatedLabel }}</span>
        </div>
        <button
          class="btn btn-secondary"
          type="button"
          data-test="marketplace-repositories"
          @click="openRepositories"
        >
          插件仓库
        </button>
      </div>
    </header>

    <section v-if="packages.length" class="marketplace-toolbar" aria-label="搜索和筛选插件">
      <div v-if="packages.length" class="search-field" @click="focusSearch">
        <svg class="search-field__icon" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <circle cx="11" cy="11" r="7" />
          <path d="M20 20l-3.5-3.5" />
        </svg>
        <input
          ref="searchInputRef"
          v-model="query"
          class="search-field__input"
          type="search"
          placeholder="搜索插件名称 / 来源"
          aria-label="搜索插件"
          @keydown.esc.prevent="query = ''"
        >
        <button
          v-if="query.trim()"
          type="button"
          class="search-field__clear"
          aria-label="清空搜索"
          @click.stop="query = ''"
        >
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" aria-hidden="true">
            <line x1="18" y1="6" x2="6" y2="18" />
            <line x1="6" y1="6" x2="18" y2="18" />
          </svg>
        </button>
      </div>
      <ViewToggle v-if="packages.length" v-model:view="view" />
      <div class="marketplace-filters" aria-label="安装状态">
        <button v-for="filter in filters" :key="filter.value" type="button" :aria-pressed="statusFilter === filter.value" :data-test="`marketplace-filter-${filter.value}`" @click="statusFilter = filter.value">{{ filter.label }}</button>
      </div>
      <span class="marketplace-count" role="status">{{ filteredPackages.length }} 个插件</span>
    </section>

    <div v-if="loading" class="plugin-marketplace-page__loading">
      <div class="spinner"></div>
      <p>正在读取已验证市场快照…</p>
    </div>

    <div v-else-if="!packages.length && error" role="alert">
      <EmptyState title="读取失败" :description="error">
        <template #action>
          <button class="btn btn-secondary" type="button" @click="load">重试</button>
        </template>
      </EmptyState>
    </div>

    <EmptyState v-else-if="!packages.length" icon="🧩" title="暂无插件" description="当前市场没有可安装的插件。下一步：到仓库检查来源是否刷新成功。">
      <template #action>
        <button class="btn btn-secondary" type="button" data-test="marketplace-repositories-empty" @click="openRepositories">插件仓库</button>
      </template>
    </EmptyState>

    <template v-else>
      <p v-if="error" class="marketplace-load-error" role="alert">{{ error }} <button class="btn btn-secondary btn-sm" type="button" @click="load({ silent: true })">重试</button></p>
      <div v-if="!filteredPackages.length" class="plugin-marketplace-empty">
        <p>没有匹配的插件</p>
        <button class="btn btn-secondary" type="button" @click="query = ''; statusFilter = 'all'">清除筛选</button>
      </div>

      <section v-else-if="view === 'card'" class="plugin-marketplace-catalog" aria-label="可安装插件">
        <BaseListCard
          v-for="item in filteredPackages"
          :key="packageKey(item)"
          class="marketplace-card"
          :class="{ 'marketplace-card--active': isSelected(item) }"
          clickable
          :data-test="`marketplace-package-${item.plugin.id}`"
          @click="openMarketplaceInspect(item)"
        >
          <template #header-left>
            <span class="marketplace-card__icon" aria-hidden="true"><svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6"><rect x="4" y="4" width="16" height="16" rx="5" /><path d="M9 8v8M15 8v8M8 12h8" /></svg></span>
            <span class="marketplace-card__heading"><span class="marketplace-card__name" :title="pluginTitle(item)">{{ pluginTitle(item) }}</span><span class="marketplace-card__version">v{{ item.plugin.version }}</span></span>
          </template>
          <template #header-right>
            <BaseBadge :tone="statusTone(item)" dot>{{ installedStatus(item) }}</BaseBadge>
          </template>
          <p v-if="pluginBlurb(item)" class="marketplace-card__blurb">{{ pluginBlurb(item) }}</p>
          <template #footer>
            <BaseBadge :tone="item.source.kind === 'official' ? 'success' : 'warning'">
              {{ sourceKindLabel(item.source.kind) }}
            </BaseBadge>
            <div class="marketplace-card__actions">
            <button type="button" class="btn btn-ghost btn-sm" :aria-label="`查看 ${pluginTitle(item)} 的详情`" @click.stop="openMarketplaceInspect(item)">详情</button>
            <button
              type="button"
              :class="tableActionClass(item)"
              :data-test="`marketplace-card-action-${item.plugin.id}`"
              :disabled="actionBusy || detailLoading"
              @click.stop.prevent="startCardAction(item)"
            >
              {{ detailLoading && isSelected(item) ? '下载中…' : cardActionLabel(item) }}
            </button>
            </div>
          </template>
        </BaseListCard>
      </section>

      <div v-else class="plugin-catalog-table-wrap" data-test="marketplace-table">
        <table class="plugin-catalog-table" aria-label="可安装插件">
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
              v-for="item in filteredPackages"
              :key="packageKey(item)"
              :class="{ 'plugin-catalog-table__row--active': isSelected(item) }"
              :data-test="`marketplace-package-${item.plugin.id}`"
              @click="openMarketplaceInspect(item)"
            >
              <td>
                <div class="plugin-catalog-table__name">
                  <button class="marketplace-name-button" type="button" @click.stop="openMarketplaceInspect(item)">{{ pluginTitle(item) }}</button>
                  <small v-if="pluginBlurb(item)">{{ pluginBlurb(item) }}</small>
                </div>
              </td>
              <td data-label="状态">
                <BaseBadge :tone="statusTone(item)" dot>{{ installedStatus(item) }}</BaseBadge>
              </td>
              <td data-label="版本">
                <span class="plugin-catalog-table__version">{{ item.plugin.version }}</span>
              </td>
              <td data-label="来源">
                <BaseBadge :tone="item.source.kind === 'official' ? 'success' : 'warning'">
                  {{ sourceKindLabel(item.source.kind) }}
                </BaseBadge>
              </td>
              <td class="plugin-catalog-table__col-actions">
                <div class="plugin-catalog-table__actions" @click.stop>
                  <button
                    type="button"
                    :class="tableActionClass(item)"
                    :data-test="`marketplace-card-action-${item.plugin.id}`"
                    :disabled="actionBusy || detailLoading"
                    @click.stop="startCardAction(item)"
                  >
                    {{ detailLoading && isSelected(item) ? '下载中…' : cardActionLabel(item) }}
                  </button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>

    <PluginMarketplaceInspectModal
      :model-value="inspectVisible"
      :item="selected"
      :detail="detail"
      :detail-prepared="detailPrepared"
      :source="source"
      :title="pluginTitle(selected)"
      :version="selected?.plugin?.version || ''"
      :status="selected ? installedStatus(selected) : ''"
      :status-tone="selected ? statusTone(selected) : 'neutral'"
      :source-label="sourceKindLabel(source.kind)"
      :purpose="pluginPurpose"
      :next-step="nextStepHint"
      :already-installed="alreadyInstalled"
      :is-upgrade="isUpgrade"
      :required-permissions="requiredPermissions"
      :action-busy="actionBusy"
      :detail-loading="detailLoading"
      :action-label="selected ? cardActionLabel(selected) : '安装'"
      @update:model-value="onInspectVisible"
      @action="onInspectAction"
    />

    <PluginRepositoriesModal
      v-model="repoModalOpen"
      @updated="onRepositoriesUpdated"
    />

    <PluginMarketplaceConfirmModal :catalog="catalog" />
  </main>
</template>

<style scoped>
.plugin-marketplace-page {
  max-width: 1180px;
  margin: 0 auto;
}

.plugin-marketplace-page__loading {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-3);
  padding: 4rem 2rem;
  color: var(--color-text-muted);
}

.page-header__right {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  flex-wrap: wrap;
  gap: 0.5rem;
  min-width: 0;
}

.page-header__right .search-field {
  flex: 1 1 12rem;
  min-width: 0;
  max-width: 22rem;
}

.catalog-sync {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  min-width: 0;
}

.catalog-sync__time {
  color: var(--color-text-muted);
  font-size: var(--text-xs);
  white-space: nowrap;
}

.back-link {
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
  text-decoration: none;
}

.back-link:hover {
  color: var(--color-primary);
}

.plugin-marketplace-catalog {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 19rem), 1fr));
  gap: 0.85rem;
  padding: var(--space-1) var(--space-1) var(--space-3);
  margin: calc(-1 * var(--space-1)) calc(-1 * var(--space-1)) calc(-1 * var(--space-1));
  align-items: stretch;
}

.plugin-marketplace-empty {
  grid-column: 1 / -1;
  margin: 0;
  padding: 2rem 1rem;
  color: var(--color-text-muted);
  text-align: center;
}

.marketplace-card :deep(.base-list-card__header-left) {
  flex-wrap: nowrap;
  min-width: 0;
  flex: 1;
}

.marketplace-card--active {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-md);
}

.marketplace-card__heading {
  display: grid;
  gap: 0.2rem;
  min-width: 0;
}


.marketplace-card__name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 0.9375rem;
  font-weight: 700;
  color: var(--color-text-primary);
}

.marketplace-card__version {
  flex-shrink: 0;
  font-family: var(--font-mono);
  font-size: 0.75rem;
  color: var(--color-text-muted);
}

.marketplace-card__blurb {
  margin: 0;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  color: var(--color-text-secondary);
  font-size: 0.875rem;
  line-height: 1.65;
  min-height: 2.9em;
}

.marketplace-card :deep(.base-list-card__footer) {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
  padding-top: 0.45rem;
  border-top: 1px solid var(--color-border-subtle);
}


.page-header {
  flex-wrap: nowrap;
  align-items: center;
}

.page-header__left {
  flex: 1 1 auto;
  min-width: 0;
}

.page-subtitle {
  max-width: 38rem;
  line-height: 1.65;
}

.page-header__right {
  flex-shrink: 0;
}

.catalog-sync {
  flex-wrap: wrap;
}

.catalog-sync__time {
  width: 100%;
  order: 2;
  font-size: 0.6875rem;
  text-align: center;
}

.marketplace-toolbar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 0.75rem;
  margin-bottom: 1.25rem;
}

.marketplace-toolbar .search-field {
  flex: 1 1 16rem;
  max-width: 28rem;
  min-width: 0;
  height: 44px;
}

.marketplace-filters {
  display: flex;
  gap: 0.25rem;
  padding: var(--space-1);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-lg);
  background: var(--color-bg-subtle);
}

.marketplace-filters button {
  border: 0;
  padding: 0.45rem 0.8rem;
  border-radius: var(--radius-md);
  background: transparent;
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
  cursor: pointer;
  white-space: nowrap;
}

.marketplace-filters button[aria-pressed="true"] {
  background: var(--color-bg-surface);
  color: var(--color-primary);
  box-shadow: var(--shadow-xs);
}

.marketplace-count {
  margin-left: auto;
  color: var(--color-text-muted);
  font-size: var(--text-xs);
  white-space: nowrap;
}

.marketplace-card {
  padding: 1.2rem;
  gap: 0.9rem;
}

.marketplace-card__icon {
  display: grid;
  place-items: center;
  width: 42px;
  height: 42px;
  flex-shrink: 0;
  border-radius: var(--radius-lg);
  color: var(--color-primary);
  background: var(--color-primary-subtle);
}

.marketplace-card :deep(.base-list-card__header-left) {
  gap: 0.65rem;
}

.marketplace-card :deep(.base-list-card__header-right) {
  margin-left: 0;
}

.marketplace-card__actions {
  display: flex;
  align-items: center;
  gap: 0.25rem;
}

.marketplace-card__actions .btn {
  min-height: 40px;
  min-width: 56px;
}

.marketplace-name-button {
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--color-text-primary);
  text-align: left;
  font-size: var(--text-sm);
  font-weight: 650;
  cursor: pointer;
  overflow-wrap: anywhere;
}

.marketplace-name-button:hover {
  color: var(--color-primary);
}

.marketplace-load-error {
  color: var(--color-danger);
  font-size: var(--text-sm);
}

@media (max-width: 800px) {
  .page-header {
    flex-direction: column;
    align-items: stretch;
    gap: 1rem;
  }

  .page-header__left {
    flex: none;
    min-width: 0;
  }

  .page-header__right {
    justify-content: flex-start;
    flex-wrap: nowrap;
  }

  .page-header__right > .btn {
    flex: none;
    min-height: 44px;
  }

  .catalog-sync {
    flex-wrap: nowrap;
    flex: 1;
  }

  .catalog-sync__time {
    width: auto;
    text-align: left;
    white-space: normal;
  }

  .marketplace-toolbar .search-field {
    flex: 1 1 calc(100% - 5rem);
    max-width: none;
  }

  .marketplace-toolbar :deep(.view-toggle) {
    margin-left: auto;
  }

  .marketplace-toolbar :deep(.view-toggle__btn) {
    width: 38px;
    height: 38px;
  }

  .marketplace-filters button {
    min-height: 38px;
  }

}
@media (max-width: 640px) {
  .plugin-marketplace-catalog {
    grid-template-columns: 1fr;
  }

  .marketplace-card :deep(.base-list-card__header) {
    flex-wrap: nowrap;
  }

  .marketplace-card__actions .btn {
    min-height: 44px;
  }

  .plugin-catalog-table-wrap {
    border: 0;
    background: transparent;
    overflow: visible;
  }

  .plugin-catalog-table, .plugin-catalog-table tbody {
    display: block;
    width: 100%;
    min-width: 0;
  }

  .plugin-catalog-table thead {
    display: none;
  }

  .plugin-catalog-table tbody {
    display: grid;
    gap: 0.75rem;
  }

  .plugin-catalog-table tbody tr {
    display: grid;
    grid-template-columns: 1fr auto;
    padding: 1rem;
    border: 1px solid var(--color-border-subtle);
    border-radius: var(--radius-xl);
    background: var(--color-bg-surface);
  }

  .plugin-catalog-table td {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    width: auto;
    padding: 0.35rem 0;
    border: 0;
  }

  .plugin-catalog-table td:first-child {
    grid-column: 1 / -1;
    width: auto;
    padding: 0 0 0.5rem;
  }

  .plugin-catalog-table td[data-label="状态"] {
    grid-column: 2;
    grid-row: 2;
    justify-content: flex-end;
  }

  .plugin-catalog-table td[data-label="版本"] {
    grid-column: 1;
    grid-row: 2;
  }

  .plugin-catalog-table td[data-label="来源"] {
    grid-column: 1;
    grid-row: 3;
  }

  .plugin-catalog-table td:last-child {
    grid-column: 2;
    grid-row: 3;
    justify-content: flex-end;
  }

  .plugin-catalog-table__name small {
    white-space: normal;
    line-height: 1.6;
  }

  .plugin-catalog-table__actions .btn {
    min-height: 44px;
  }

}

</style>

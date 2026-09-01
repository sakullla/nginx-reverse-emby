<script setup>
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import BaseBadge from '../../components/base/BaseBadge.vue'
import BaseListCard from '../../components/base/BaseListCard.vue'
import BaseModal from '../../components/base/BaseModal.vue'
import EmptyState from '../../components/base/EmptyState.vue'
import ViewToggle from '../../components/common/ViewToggle.vue'
import { useViewToggle } from '../../composables/useViewToggle'
import { useMarketplaceCatalog } from '../../composables/useMarketplaceCatalog'

const router = useRouter()
const { view } = useViewToggle('plugin-marketplace')
const query = ref('')
const searchInputRef = ref(null)

const {
  loading,
  actionBusy,
  detailLoading,
  error,
  actionError,
  packages,
  selected,
  confirmVisible,
  downloadElapsedSec,
  downloadSteps,
  downloadPhaseLabel,
  downloadHint,
  source,
  isUpgrade,
  requiredPermissions,
  selectedDetailPath,
  hasPendingDetailLink,
  nextStepHint,
  load,
  startCardAction,
  cancelConfirm,
  onConfirmVisible,
  applyPackage,
  isSelected,
  installedStatus,
  statusTone,
  cardActionLabel,
  tableActionClass,
  pluginTitle,
  pluginBlurb,
  sourceKindLabel,
  packageKey,
  marketplaceDetailHref,
} = useMarketplaceCatalog()

const filteredPackages = computed(() => {
  const needle = query.value.trim().toLowerCase()
  if (!needle) return packages.value
  return packages.value.filter((item) => {
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

function openMarketplaceDetail(item) {
  const href = marketplaceDetailHref(item)
  if (href) router.push(href)
}
</script>

<template>
  <main class="plugin-marketplace-page">
    <header class="page-header">
      <div class="page-header__left">
        <RouterLink to="/plugins" class="back-link">← 已安装插件</RouterLink>
        <h1 class="page-title">插件市场</h1>
        <p class="page-subtitle">选一个插件安装或升级。成功后会进入详情，下一步是部署；提供访问入口的插件还要发布域名。</p>
      </div>
      <div class="page-header__right">
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
        <RouterLink class="btn btn-secondary" to="/plugins/repositories">插件仓库</RouterLink>
      </div>
    </header>

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
        <RouterLink class="btn btn-secondary" to="/plugins/repositories">插件仓库</RouterLink>
      </template>
    </EmptyState>

    <template v-else>
      <p v-if="query.trim() && !filteredPackages.length" class="plugin-marketplace-empty">没有匹配的插件</p>

      <section v-else-if="view === 'card'" class="plugin-marketplace-catalog" aria-label="可安装插件">
        <RouterLink
          v-for="item in filteredPackages"
          :key="packageKey(item)"
          class="marketplace-card-link"
          :to="marketplaceDetailHref(item)"
          :data-test="`marketplace-detail-link-${item.plugin.id}`"
        >
          <BaseListCard
            class="marketplace-card"
            :class="{ 'marketplace-card--active': isSelected(item) }"
            :clickable="false"
          >
            <template #header-left>
              <span class="marketplace-card__name" :title="pluginTitle(item)">{{ pluginTitle(item) }}</span>
              <BaseBadge :tone="statusTone(item)" dot>{{ installedStatus(item) }}</BaseBadge>
            </template>
            <template #header-right>
              <span class="marketplace-card__version">{{ item.plugin.version }}</span>
            </template>
            <p v-if="pluginBlurb(item)" class="marketplace-card__blurb">{{ pluginBlurb(item) }}</p>
            <template #footer>
              <BaseBadge :tone="item.source.kind === 'official' ? 'success' : 'warning'">
                {{ sourceKindLabel(item.source.kind) }}
              </BaseBadge>
              <button
                type="button"
                :class="tableActionClass(item)"
                :data-test="`marketplace-card-action-${item.plugin.id}`"
                :disabled="actionBusy || detailLoading"
                @click.stop.prevent="startCardAction(item)"
              >
                {{ detailLoading && isSelected(item) ? '下载中…' : cardActionLabel(item) }}
              </button>
            </template>
          </BaseListCard>
        </RouterLink>
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
              @click="openMarketplaceDetail(item)"
            >
              <td>
                <div class="plugin-catalog-table__name">
                  <RouterLink
                    class="plugin-catalog-table__name-link"
                    :to="marketplaceDetailHref(item)"
                    :data-test="`marketplace-detail-link-${item.plugin.id}`"
                    @click.stop
                  >
                    <strong :title="pluginTitle(item)">{{ pluginTitle(item) }}</strong>
                  </RouterLink>
                  <small v-if="pluginBlurb(item)">{{ pluginBlurb(item) }}</small>
                </div>
              </td>
              <td>
                <BaseBadge :tone="statusTone(item)" dot>{{ installedStatus(item) }}</BaseBadge>
              </td>
              <td>
                <span class="plugin-catalog-table__version">{{ item.plugin.version }}</span>
              </td>
              <td>
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
                    @click="startCardAction(item)"
                  >
                    {{ detailLoading && isSelected(item) ? '下载中…' : cardActionLabel(item) }}
                  </button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <BaseModal
        :model-value="confirmVisible"
        :title="isUpgrade ? '确认升级插件' : '确认安装插件'"
        :subtitle="pluginTitle(selected)"
        size="sm"
        :close-on-click-modal="!actionBusy"
        show-footer
        @update:model-value="onConfirmVisible"
      >
        <div class="confirm-permissions">
          <p v-if="hasPendingDetailLink" class="confirm-pending-next">
            <RouterLink :to="selectedDetailPath" data-test="marketplace-pending-detail">打开详情查看进行中的操作</RouterLink>
          </p>
          <div v-if="detailLoading" class="package-download-progress" data-test="marketplace-detail-loading">
            <p class="package-download-progress__title">{{ downloadPhaseLabel }}</p>
            <p class="package-download-progress__hint">{{ downloadHint }}</p>
            <div
              class="package-download-progress__track"
              role="progressbar"
              aria-valuemin="0"
              aria-valuemax="100"
              :aria-valuetext="downloadPhaseLabel"
            >
              <div class="package-download-progress__fill"></div>
            </div>
            <ol class="package-download-progress__steps">
              <li
                v-for="step in downloadSteps"
                :key="step.id"
                :class="{ 'is-current': step.id === 'download' }"
              >
                {{ step.label }}
              </li>
            </ol>
            <p class="package-download-progress__elapsed">已等待 {{ downloadElapsedSec }} 秒</p>
          </div>
          <template v-else>
            <p v-if="requiredPermissions.length">安装将授予以下宿主能力：</p>
            <p v-else>此包未请求宿主能力。</p>
            <ul v-if="requiredPermissions.length" class="permission-list">
              <li v-for="permission in requiredPermissions" :key="permission"><code>{{ permission }}</code></li>
            </ul>
            <p v-if="!actionError" class="confirm-next" data-test="marketplace-confirm-next">{{ nextStepHint }}</p>
            <p v-if="source.kind !== 'official'" class="confirm-risk">我已复核非官方来源、签名指纹、checksum、权限差异和宿主风险。</p>
          </template>
        </div>
        <template #footer>
          <button class="btn btn-secondary" type="button" :disabled="actionBusy" @click="cancelConfirm">取消</button>
          <button class="btn btn-primary" type="button" data-test="marketplace-confirm-submit" :disabled="actionBusy || detailLoading" @click="applyPackage">
            {{ actionBusy ? '提交中…' : detailLoading ? '下载中…' : actionError ? (isUpgrade ? '重试升级' : '重试安装') : isUpgrade ? '确认升级' : '确认安装' }}
          </button>
        </template>
      </BaseModal>
    </template>
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
  grid-template-columns: repeat(auto-fit, minmax(17.5rem, 1fr));
  gap: 0.85rem;
  padding: 4px 4px 12px;
  margin: -4px -4px -4px;
  align-items: stretch;
}

.plugin-marketplace-empty {
  grid-column: 1 / -1;
  margin: 0;
  padding: 2rem 1rem;
  color: var(--color-text-muted);
  text-align: center;
}

.marketplace-card-link {
  display: flex;
  min-width: 0;
  color: inherit;
  text-decoration: none;
  border-radius: var(--radius-2xl);
}

.marketplace-card-link :deep(.base-list-card) {
  flex: 1;
  min-width: 0;
}

.marketplace-card-link:hover :deep(.base-list-card) {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-lg);
  transform: translateY(-2px);
}

.marketplace-card-link:focus-visible :deep(.base-list-card) {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus, 0 0 0 3px var(--color-primary-subtle));
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
  white-space: nowrap;
  color: var(--color-text-muted);
  font-size: 0.8125rem;
  line-height: 1.4;
}

.marketplace-card :deep(.base-list-card__footer) {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
  padding-top: 0.45rem;
  border-top: 1px solid var(--color-border-subtle);
}

.plugin-catalog-table__name-link {
  color: inherit;
  text-decoration: none;
}

.plugin-catalog-table__name-link:hover {
  color: var(--color-primary);
}

.permission-list {
  display: grid;
  gap: var(--space-2);
  margin: 0;
  padding-left: 1.2rem;
}

.permission-list code {
  font-size: var(--text-xs);
  overflow-wrap: anywhere;
}

.confirm-permissions {
  display: grid;
  gap: var(--space-3);
}

.confirm-permissions > p {
  margin: 0;
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
}

.confirm-next {
  color: var(--color-text-primary);
}

.confirm-pending-next {
  margin: 0;
  font-size: var(--text-sm);
}

.confirm-pending-next a {
  color: var(--color-primary);
  text-decoration: none;
}

.confirm-pending-next a:hover {
  text-decoration: underline;
}

.confirm-risk {
  color: var(--color-warning);
}

.package-download-progress {
  display: grid;
  gap: 0.55rem;
}

.package-download-progress__title {
  margin: 0;
  color: var(--color-text-primary);
  font-size: 0.9375rem;
  font-weight: 650;
}

.package-download-progress__hint,
.package-download-progress__elapsed {
  margin: 0;
  color: var(--color-text-muted);
  font-size: 0.8125rem;
  line-height: 1.45;
}

.package-download-progress__track {
  position: relative;
  overflow: hidden;
  height: 0.4rem;
  border-radius: 999px;
  background: var(--color-bg-subtle);
}

.package-download-progress__fill {
  position: absolute;
  inset: 0 auto 0 0;
  width: 36%;
  border-radius: inherit;
  background: var(--color-primary);
  animation: package-download-indeterminate 1.35s ease-in-out infinite;
}

.package-download-progress__steps {
  display: grid;
  gap: 0.3rem;
  margin: 0.15rem 0 0;
  padding: 0;
  list-style: none;
}

.package-download-progress__steps li {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  color: var(--color-text-tertiary);
  font-size: 0.8125rem;
}

.package-download-progress__steps li::before {
  content: '';
  width: 0.45rem;
  height: 0.45rem;
  border-radius: 50%;
  background: currentColor;
  flex-shrink: 0;
}

.package-download-progress__steps li.is-current {
  color: var(--color-primary);
  font-weight: 650;
}

@keyframes package-download-indeterminate {
  0% { transform: translateX(-120%); }
  100% { transform: translateX(340%); }
}

@media (max-width: 800px) {
  .page-header__right .search-field {
    flex: 1 1 100%;
    max-width: none;
  }
}
</style>

<script setup>
import { computed, watch } from 'vue'
import { useRoute } from 'vue-router'
import BaseBadge from '../../components/base/BaseBadge.vue'
import PluginMarketplaceConfirmModal from '../../components/plugins/PluginMarketplaceConfirmModal.vue'
import EmptyState from '../../components/base/EmptyState.vue'
import PluginPackageSummary from '../../components/plugins/PluginPackageSummary.vue'
import PluginRiskNotices from '../../components/plugins/PluginRiskNotices.vue'
import {
  resolveMarketplacePackage,
  useMarketplaceCatalog,
} from '../../composables/useMarketplaceCatalog'

const route = useRoute()
const catalog = useMarketplaceCatalog()
const {
  loading,
  error,
  actionBusy,
  detailLoading,
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
  load,
  showCatalogItem,
  startCardAction,
  installedStatus,
  statusTone,
  cardActionLabel,
  pluginTitle,
  sourceKindLabel,
} = catalog

const missing = computed(() => !loading.value && !selected.value)

watch(
  () => [packages.value, String(route.params.pluginId || ''), route.query.source],
  () => {
    showCatalogItem(resolveMarketplacePackage(packages.value, route.params.pluginId, route.query.source))
  },
  { immediate: true }
)
</script>

<template>
  <main class="plugin-marketplace-detail-page">
    <div v-if="loading" class="plugin-marketplace-detail-page__loading">
      <div class="spinner"></div>
      <p>正在读取市场目录…</p>
    </div>

    <div v-else-if="missing" role="alert">
      <EmptyState :title="error ? '读取失败' : '没有找到这个插件'" :description="error || '市场目录里没有对应条目。下一步：返回市场重新选择，或到仓库检查来源是否刷新成功。'">
        <template #action>
          <div class="plugin-marketplace-detail-empty-actions">
            <button class="btn btn-secondary" type="button" @click="load">重试</button>
            <RouterLink class="btn btn-secondary" to="/plugins/marketplace">返回插件市场</RouterLink>
          </div>
        </template>
      </EmptyState>
    </div>

    <template v-else-if="selected && detail">
      <header class="page-header">
        <div class="page-header__left">
          <RouterLink to="/plugins/marketplace" class="back-link">← 插件市场</RouterLink>
          <h1 class="page-title">{{ pluginTitle(selected) }}</h1>
          <p class="page-subtitle">{{ selected.plugin.version }} · {{ installedStatus(selected) }}</p>
        </div>
        <div class="page-header__right">
          <button
            type="button"
            class="btn btn-primary"
            data-test="marketplace-detail-action"
            :disabled="actionBusy || detailLoading"
            @click="startCardAction(selected)"
          >
            {{ detailLoading ? '下载中…' : cardActionLabel(selected) }}
          </button>
        </div>
      </header>

      <div class="plugin-marketplace-detail">
        <section class="marketplace-primary">
          <p class="marketplace-primary__source">
            <BaseBadge :tone="source.kind === 'official' ? 'success' : 'warning'">
              {{ sourceKindLabel(source.kind) }}
            </BaseBadge>
            <BaseBadge :tone="statusTone(selected)" dot>{{ installedStatus(selected) }}</BaseBadge>
          </p>
          <p class="marketplace-primary__purpose">{{ pluginPurpose }}</p>
          <p class="marketplace-primary__next" data-test="marketplace-next-step">{{ nextStepHint }}</p>
          <p v-if="alreadyInstalled">当前版本已安装，可打开详情继续部署或配置。</p>
          <p v-else-if="isUpgrade" class="upgrade-notice">升级将先验证候选版本；失败时保留当前已安装版本。</p>
        </section>
        <section class="permission-review">
          <h3>安装权限</h3>
          <p v-if="!detailPrepared">确认安装前会下载并校验插件包，再逐项展示需要授予的能力。</p>
          <p v-else-if="!requiredPermissions.length">此包未请求宿主能力。</p>
          <ul v-else class="permission-list">
            <li v-for="permission in requiredPermissions" :key="permission"><code>{{ permission }}</code></li>
          </ul>
        </section>
        <details class="marketplace-technical">
          <summary>来源、签名与运行限制</summary>
          <PluginRiskNotices :package-detail="detail" :source="source" />
          <PluginPackageSummary :detail="detail" :source="source" :show-identity="false" :collapsible="false" />
        </details>
      </div>
    </template>

    <PluginMarketplaceConfirmModal :catalog="catalog" />
  </main>
</template>

<style scoped>
.plugin-marketplace-detail-page {
  max-width: 1180px;
  margin: 0 auto;
}

.plugin-marketplace-detail-page__loading {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-3);
  padding: 4rem 2rem;
  color: var(--color-text-muted);
}

.plugin-marketplace-detail-empty-actions {
  display: flex;
  justify-content: center;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.back-link {
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
  text-decoration: none;
}

.back-link:hover {
  color: var(--color-primary);
}

.plugin-marketplace-detail {
  min-width: 0;
  display: grid;
  align-content: start;
  gap: 1rem;
}

.marketplace-primary {
  display: grid;
  gap: 0.35rem;
}

.marketplace-primary p {
  margin: 0;
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.marketplace-primary__source {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.4rem;
  color: var(--color-text-secondary);
  font-size: var(--text-xs);
}

.marketplace-primary__purpose {
  color: var(--color-text-secondary);
}

.marketplace-primary__next {
  color: var(--color-text-primary);
}

.marketplace-technical {
  border-top: 1px solid var(--color-border-subtle);
  padding-top: 0.5rem;
}

.marketplace-technical summary {
  padding: 0.6rem 0;
}

.marketplace-technical :deep(.plugin-risks) {
  margin: 0.6rem 0 1rem;
}


.marketplace-technical summary {
  cursor: pointer;
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
}

.permission-review {
  display: grid;
  gap: 0.4rem;
}

.permission-review h3 {
  font-size: var(--text-sm);
  color: var(--color-text-primary);
}

.permission-review p {
  font-size: var(--text-sm);
  color: var(--color-text-secondary);
  line-height: 1.65;
}


.permission-review h3,
.permission-review p {
  margin: 0;
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

.upgrade-notice {
  color: var(--color-warning);
}


.page-header__left {
  flex: 1 1 auto;
  min-width: 0;
}

@media (max-width: 640px) {
  .page-header__left {
    flex: none;
  }

  .page-header__right .btn {
    min-height: 44px;
  }

}
</style>

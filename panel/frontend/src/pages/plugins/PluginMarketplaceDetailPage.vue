<script setup>
import { computed, watch } from 'vue'
import { useRoute } from 'vue-router'
import BaseBadge from '../../components/base/BaseBadge.vue'
import BaseModal from '../../components/base/BaseModal.vue'
import EmptyState from '../../components/base/EmptyState.vue'
import PluginPackageSummary from '../../components/plugins/PluginPackageSummary.vue'
import PluginRiskNotices from '../../components/plugins/PluginRiskNotices.vue'
import {
  resolveMarketplacePackage,
  useMarketplaceCatalog,
} from '../../composables/useMarketplaceCatalog'

const route = useRoute()
const {
  loading,
  actionBusy,
  detailLoading,
  actionError,
  packages,
  selected,
  detail,
  detailPrepared,
  confirmVisible,
  downloadElapsedSec,
  downloadSteps,
  downloadPhaseLabel,
  downloadHint,
  source,
  isUpgrade,
  requiredPermissions,
  alreadyInstalled,
  selectedDetailPath,
  hasPendingDetailLink,
  pluginPurpose,
  nextStepHint,
  load,
  showCatalogItem,
  startCardAction,
  cancelConfirm,
  onConfirmVisible,
  applyPackage,
  installedStatus,
  statusTone,
  cardActionLabel,
  pluginTitle,
  sourceKindLabel,
} = useMarketplaceCatalog()

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
      <EmptyState title="没有找到这个插件" description="市场目录里没有对应条目。下一步：返回市场重新选择，或到仓库检查来源是否刷新成功。">
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
        <PluginRiskNotices :package-detail="detail" :source="source" />
        <section class="permission-review">
          <h3>精确权限确认</h3>
          <p v-if="!detailPrepared">市场快照只展示已签名的索引信息；点击安装或升级后，会校验完整包并显示精确权限。</p>
          <p v-else-if="!requiredPermissions.length">此包未请求宿主能力。</p>
          <ul v-else class="permission-list">
            <li v-for="permission in requiredPermissions" :key="permission"><code>{{ permission }}</code></li>
          </ul>
        </section>
        <details class="marketplace-technical">
          <summary>技术详情</summary>
          <PluginPackageSummary :detail="detail" :source="source" :show-identity="false" :collapsible="false" />
        </details>
      </div>
    </template>

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
  display: grid;
  gap: var(--space-4);
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
</style>

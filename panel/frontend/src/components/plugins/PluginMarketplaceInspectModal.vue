<script setup>
import BaseBadge from '../base/BaseBadge.vue'
import BaseModal from '../base/BaseModal.vue'
import PluginPackageSummary from './PluginPackageSummary.vue'
import PluginRiskNotices from './PluginRiskNotices.vue'

defineProps({
  modelValue: { type: Boolean, required: true },
  item: { type: Object, default: null },
  detail: { type: Object, default: null },
  detailPrepared: { type: Boolean, default: false },
  source: { type: Object, default: () => ({}) },
  title: { type: String, default: '' },
  version: { type: String, default: '' },
  status: { type: String, default: '' },
  statusTone: { type: String, default: 'neutral' },
  sourceLabel: { type: String, default: '' },
  purpose: { type: String, default: '' },
  nextStep: { type: String, default: '' },
  alreadyInstalled: { type: Boolean, default: false },
  isUpgrade: { type: Boolean, default: false },
  requiredPermissions: { type: Array, default: () => [] },
  actionBusy: { type: Boolean, default: false },
  detailLoading: { type: Boolean, default: false },
  actionLabel: { type: String, default: '安装' }
})

const emit = defineEmits(['update:modelValue', 'action'])

function onVisible(open) {
  emit('update:modelValue', open)
}

function onAction(event) {
  event?.stopPropagation?.()
  emit('action')
}
</script>

<template>
  <BaseModal
    :model-value="modelValue && !!item"
    :title="title || '插件详情'"
    :subtitle="[version, status].filter(Boolean).join(' · ')"
    size="lg"
    show-footer
    data-test="marketplace-inspect-modal"
    @update:model-value="onVisible"
  >
    <div v-if="item && detail" class="marketplace-inspect">
      <section class="marketplace-primary">
        <p class="marketplace-primary__source">
          <BaseBadge :tone="source.kind === 'official' ? 'success' : 'warning'">
            {{ sourceLabel }}
          </BaseBadge>
          <BaseBadge :tone="statusTone" dot>{{ status }}</BaseBadge>
          <span data-test="marketplace-inspect-version">{{ version }}</span>
        </p>
        <p class="marketplace-primary__purpose">{{ purpose }}</p>
        <p class="marketplace-primary__next" data-test="marketplace-inspect-next">{{ nextStep }}</p>
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
    <template #footer>
      <RouterLink
        class="btn btn-secondary"
        to="/plugins"
        data-test="marketplace-inspect-installed"
        @click.stop
      >
        已安装插件
      </RouterLink>
      <button
        type="button"
        class="btn btn-primary"
        data-test="marketplace-inspect-action"
        :disabled="actionBusy || detailLoading"
        @click.stop="onAction"
      >
        {{ detailLoading ? '下载中…' : actionLabel }}
      </button>
    </template>
  </BaseModal>
</template>

<style scoped>
.marketplace-inspect {
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

.marketplace-primary__source [data-test='marketplace-inspect-version'] {
  font-family: var(--font-mono);
  color: var(--color-text-muted);
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
</style>

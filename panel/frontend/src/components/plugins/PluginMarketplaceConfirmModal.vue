<script setup>
import { computed, proxyRefs } from 'vue'
import BaseModal from '../base/BaseModal.vue'

const props = defineProps({ catalog: { type: Object, required: true } })
const state = proxyRefs(props.catalog)
const permissionLabels = {
  'http.inspect': '检查 HTTP 请求', 'http.respond': '返回 HTTP 响应',
  'http.outbound': '访问外部 HTTP 服务', 'network.full': '完整网络访问',
  'agent.read': '读取节点信息', 'agent.configure': '修改节点配置',
  'event.emit': '发送事件', 'secret.use': '使用已授权的密钥',
  'storage.read': '读取插件存储', 'storage.write': '写入插件存储',
  'policy.read': '读取策略', 'policy.write': '修改策略',
  'l4.inspect': '检查网络连接', 'l4.respond': '处理网络连接',
  'dns.manage': '管理 DNS 记录',
}
const submitLabel = computed(() => {
  if (state.actionBusy) return '提交中…'
  if (state.detailLoading) return '下载中…'
  if (state.statusRefreshing) return '刷新中…'
  if (state.alreadyInstalled) return '打开插件'
  if (!state.detailPrepared) return '重试下载'
  return `${state.actionError ? '重试' : '确认'}${state.isUpgrade ? '升级' : '安装'}`
})
</script>

<template>
  <BaseModal
    :model-value="state.confirmVisible"
    :title="state.hasPendingDetailLink ? '操作进行中' : state.alreadyInstalled ? '插件已安装' : state.isUpgrade ? '确认升级插件' : '确认安装插件'"
    :subtitle="state.pluginTitle(state.selected)"
    size="sm"
    fit-content-on-mobile
    :close-on-click-modal="!state.actionBusy"
    show-footer
    data-test="marketplace-confirm-modal"
    @update:model-value="state.onConfirmVisible"
  >
    <div class="confirmation">
      <section v-if="state.hasPendingDetailLink" class="confirmation__status" role="status" data-test="marketplace-pending-status">
        <span class="confirmation__eyebrow">已提交 · 等待完成</span>
        <h4>{{ state.pendingOperationLabel }}正在处理</h4>
        <p>该插件有未完成的操作。查看进度可了解节点执行结果；结束后刷新状态再继续。</p>
        <button class="btn btn-secondary" type="button" data-test="marketplace-pending-refresh" :disabled="state.statusRefreshing || state.actionBusy" @click="state.refreshOperationStatus">
          {{ state.statusRefreshing ? '刷新中…' : '刷新状态' }}
        </button>
      </section>
      <div v-if="state.actionError" class="confirmation__error" role="alert" data-test="marketplace-action-error">
        <p>{{ state.actionError }}</p>
        <button v-if="!state.hasPendingDetailLink" class="btn btn-secondary btn-sm" type="button" data-test="marketplace-error-refresh" :disabled="state.statusRefreshing || state.actionBusy || state.detailLoading" @click="state.refreshOperationStatus">{{ state.statusRefreshing ? '刷新中…' : '刷新状态' }}</button>
      </div>
      <div v-if="state.detailLoading" class="package-download-progress" data-test="marketplace-detail-loading" role="status">
        <strong>{{ state.downloadPhaseLabel }}</strong>
        <p>{{ state.downloadHint }}</p>
        <div class="package-download-progress__track" role="progressbar" :aria-valuetext="state.downloadPhaseLabel"><span /></div>
        <ol><li v-for="step in state.downloadSteps" :key="step.id" :class="{ 'is-current': step.id === 'download' }">{{ step.label }}</li></ol>
        <small>已等待 {{ state.downloadElapsedSec }} 秒</small>
      </div>
      <template v-else-if="!state.hasPendingDetailLink">
        <div class="confirmation__identity"><strong>{{ state.pluginTitle(state.selected) }}</strong><span>v{{ state.selected?.plugin?.version }} · {{ state.sourceKindLabel(state.source.kind) }}</span></div>
        <section v-if="state.detailPrepared" class="confirmation__permissions">
          <h4>{{ state.requiredPermissions.length ? '需要授予的能力' : '无需额外宿主能力' }}</h4>
          <ul v-if="state.requiredPermissions.length" class="permission-list">
            <li v-for="permission in state.requiredPermissions" :key="permission"><strong>{{ permissionLabels[permission] || '插件声明的宿主能力' }}</strong><code>{{ permission }}</code></li>
          </ul>
        </section>
        <p v-if="!state.actionError" data-test="marketplace-confirm-next">{{ state.nextStepHint }}</p>
        <p v-if="state.source.kind !== 'official'" class="confirmation__risk">此插件来自非官方仓库。确认即同意授予上列能力，请先复核来源与签名信息。</p>
      </template>
    </div>
    <template #footer>
      <div class="confirmation__actions">
        <button class="btn btn-secondary" type="button" :disabled="state.actionBusy" @click="state.cancelConfirm">{{ state.hasPendingDetailLink ? '关闭' : '取消' }}</button>
        <RouterLink v-if="state.hasPendingDetailLink" class="btn btn-primary" :to="state.selectedDetailPath" data-test="marketplace-pending-detail">查看进度</RouterLink>
        <button v-else class="btn btn-primary" type="button" data-test="marketplace-confirm-submit" :disabled="state.actionBusy || state.detailLoading || state.statusRefreshing" @click="state.applyPackage">{{ submitLabel }}</button>
      </div>
    </template>
  </BaseModal>
</template>

<style scoped>
.confirmation {
  display: grid;
  gap: 1rem;
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
  line-height: 1.6;
}

.confirmation p, .confirmation h4 {
  margin: 0;
}

.confirmation h4 {
  color: var(--color-text-primary);
  font-size: var(--text-sm);
}

.confirmation__identity {
  display: grid;
  gap: 0.25rem;
  padding-bottom: 1rem;
  border-bottom: 1px solid var(--color-border-subtle);
}

.confirmation__identity strong {
  color: var(--color-text-primary);
  font-size: var(--text-base);
  overflow-wrap: anywhere;
}

.confirmation__identity span, .confirmation__eyebrow {
  color: var(--color-text-muted);
  font-size: var(--text-xs);
}

.confirmation__status {
  display: grid;
  gap: 0.6rem;
  padding: 1rem;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  background: var(--color-primary-subtle);
}

.confirmation__status .btn {
  justify-self: start;
}

.confirmation__error {
  display: grid;
  gap: 0.6rem;
  padding: 0.8rem 1rem;
  color: var(--color-danger);
  background: var(--color-danger-subtle);
  border-radius: var(--radius-lg);
  overflow-wrap: anywhere;
}

.confirmation__error .btn {
  justify-self: start;
  min-height: 44px;
}

.confirmation__permissions {
  display: grid;
  gap: 0.6rem;
}

.permission-list {
  margin: 0;
  padding: 0;
  list-style: none;
  display: grid;
  gap: 0.5rem;
}

.permission-list li {
  display: grid;
  gap: 0.1rem;
  padding: 0.65rem 0.8rem;
  background: var(--color-bg-subtle);
  border-radius: var(--radius-md);
}

.permission-list strong {
  font-weight: 500;
  color: var(--color-text-primary);
}

.permission-list code {
  font-size: var(--text-xs);
  color: var(--color-text-muted);
  overflow-wrap: anywhere;
}

.confirmation__risk {
  color: var(--color-warning);
  font-size: var(--text-xs);
}

.confirmation__actions {
  width: 100%;
  display: flex;
  justify-content: flex-end;
  gap: 0.6rem;
}

.confirmation__actions .btn {
  min-height: 44px;
}

.package-download-progress {
  display: grid;
  gap: 0.6rem;
}

.package-download-progress strong {
  color: var(--color-text-primary);
}

.package-download-progress small {
  color: var(--color-text-muted);
}

.package-download-progress__track {
  height: 5px;
  overflow: hidden;
  border-radius: var(--radius-md);
  background: var(--color-bg-subtle);
}

.package-download-progress__track span {
  display: block;
  width: 36%;
  height: 100%;
  background: var(--color-primary);
  animation: download 1.35s ease-in-out infinite;
}

.package-download-progress ol {
  display: flex;
  flex-wrap: wrap;
  gap: 0.4rem 1.5rem;
  margin: 0;
  padding-left: 1.25rem;
  color: var(--color-text-muted);
  font-size: var(--text-xs);
}

.package-download-progress .is-current {
  color: var(--color-primary);
}

@keyframes download { from { transform: translateX(-110%); } to { transform: translateX(290%); } }
@media (prefers-reduced-motion: reduce) { .package-download-progress__track span { animation: none; } }
@media (max-width: 640px) { .confirmation__actions .btn { flex: 1; } }
</style>

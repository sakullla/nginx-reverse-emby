<template>
  <div class="settings-backup">
    <header class="task-header">
      <div>
        <h2 class="task-header__title">备份恢复</h2>
        <p class="task-header__desc">导出当前配置做备份，或导入备份完成恢复；内部 PKI 必须使用独立的加密受保护备份</p>
      </div>
      <div class="task-header__meta" v-if="totalResources > 0">
        <span class="task-header__meta-label">当前资源</span>
        <strong class="task-header__meta-value">{{ totalResources }}</strong>
      </div>
    </header>

    <div class="pki-backup-boundary" role="note">
      普通配置备份不替代内部 PKI 的可恢复备份，也不会保存备份口令。
      <a :href="pkiHref">前往内部 PKI 受保护备份</a>
    </div>

    <section class="settings-section config-io-card">
      <div class="config-io-tabs" role="tablist" aria-label="备份恢复操作">
        <button
          id="tab-export"
          class="config-io-tab"
          :class="{ active: activeTab === 'export' }"
          role="tab"
          :aria-selected="activeTab === 'export'"
          aria-controls="panel-export"
          @click="activeTab = 'export'"
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/></svg>
          导出备份
        </button>
        <button
          id="tab-import"
          class="config-io-tab"
          :class="{ active: activeTab === 'import' }"
          role="tab"
          :aria-selected="activeTab === 'import'"
          aria-controls="panel-import"
          @click="activeTab = 'import'"
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="17 8 12 3 7 8"/><line x1="12" y1="3" x2="12" y2="15"/></svg>
          导入恢复
        </button>
      </div>

      <div
        id="panel-export"
        class="config-io-panel"
        role="tabpanel"
        aria-labelledby="tab-export"
        :hidden="activeTab !== 'export'"
      >
        <p class="config-io-hint">下载包含节点、规则、证书等配置的备份文件，用于迁移或灾备。</p>
        <ExportPanel :counts="counts" />
      </div>
      <div
        id="panel-import"
        class="config-io-panel"
        role="tabpanel"
        aria-labelledby="tab-import"
        :hidden="activeTab !== 'import'"
      >
        <p class="config-io-hint">上传备份文件后先预览变更，确认再写入，避免覆盖关键配置。</p>
        <ImportWizard />
      </div>
    </section>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import ExportPanel from './data-mgmt/ExportPanel.vue'
import ImportWizard from './data-mgmt/ImportWizard.vue'
import { fetchBackupResourceCounts } from '../../api'

const counts = ref({ agents: 0, http_rules: 0, l4_rules: 0, relay_listeners: 0, certificates: 0, version_policies: 0 })
const activeTab = ref('export')

const pkiHref = computed(() => {
  const configuredBase = typeof window !== 'undefined' ? window.__NRE_PANEL_BASE__ : ''
  let base = String(configuredBase || import.meta.env.BASE_URL || '/').trim()
  if (!base.startsWith('/')) base = `/${base}`
  if (!base.endsWith('/')) base = `${base}/`
  return `${base}pki`
})

const totalResources = computed(() => {
  const c = counts.value || {}
  return Object.values(c).reduce((sum, n) => sum + (Number(n) || 0), 0)
})

onMounted(() => {
  fetchBackupResourceCounts()
    .then(d => { counts.value = d.counts })
    .catch(() => {})
})
</script>

<style scoped>
.settings-backup {
  display: flex;
  flex-direction: column;
  gap: var(--space-5);
}

.task-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-4);
  flex-wrap: wrap;
}

.task-header__title {
  margin: 0 0 0.25rem;
  font-size: var(--text-lg);
  font-weight: 700;
  color: var(--color-text-primary);
}

.task-header__desc {
  margin: 0;
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
}

.task-header__meta {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 0.15rem;
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-lg);
  background: var(--color-bg-subtle);
  border: 1px solid var(--color-border-subtle);
}

.task-header__meta-label {
  font-size: var(--text-xs);
  color: var(--color-text-tertiary);
}

.task-header__meta-value {
  font-size: var(--text-lg);
  font-weight: 700;
  font-variant-numeric: tabular-nums;
  color: var(--color-text-primary);
}

.config-io-card {
  display: flex;
  flex-direction: column;
  padding: 0;
  overflow: hidden;
}

.pki-backup-boundary {
  padding: var(--space-3) var(--space-4);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  background: var(--color-bg-subtle);
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
}

.pki-backup-boundary a {
  margin-left: var(--space-2);
  color: var(--color-primary);
  font-weight: var(--font-medium);
}

.config-io-tabs {
  display: flex;
  gap: var(--space-1);
  padding: var(--space-3);
  border-bottom: 1px solid var(--color-border-subtle);
  background: var(--color-bg-subtle);
}

.config-io-tab {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-4);
  border: 1px solid transparent;
  border-radius: var(--radius-lg);
  background: transparent;
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-default);
  font-family: inherit;
}

.config-io-tab:hover {
  background: var(--color-bg-surface);
  color: var(--color-text-primary);
}

.config-io-tab.active {
  background: var(--color-bg-surface);
  border-color: var(--color-border-default);
  color: var(--color-text-primary);
  box-shadow: var(--shadow-sm);
}

.config-io-panel {
  padding: var(--space-5);
}

.config-io-panel[hidden] {
  display: none;
}

.config-io-hint {
  margin: 0 0 var(--space-4);
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
  line-height: 1.5;
}

@media (max-width: 640px) {
  .config-io-tabs {
    flex-direction: column;
  }
  .task-header__meta {
    align-items: flex-start;
  }
}
</style>

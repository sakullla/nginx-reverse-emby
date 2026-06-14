<template>
  <div class="settings-data-mgmt">
    <div class="data-mgmt-header">
      <div class="data-mgmt-header__text">
        <h1 class="data-mgmt-header__title">数据管理</h1>
        <p class="data-mgmt-header__desc">导出或导入面板配置</p>
      </div>
    </div>

    <section class="settings-section config-io-card">
      <div class="config-io-tabs" role="tablist">
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
          导出配置
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
          导入配置
        </button>
      </div>

      <div
        id="panel-export"
        class="config-io-panel"
        role="tabpanel"
        aria-labelledby="tab-export"
        :hidden="activeTab !== 'export'"
      >
        <ExportPanel :counts="counts" />
      </div>
      <div
        id="panel-import"
        class="config-io-panel"
        role="tabpanel"
        aria-labelledby="tab-import"
        :hidden="activeTab !== 'import'"
      >
        <ImportWizard />
      </div>
    </section>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import ExportPanel from './data-mgmt/ExportPanel.vue'
import ImportWizard from './data-mgmt/ImportWizard.vue'
import { fetchBackupResourceCounts } from '../../api'

const counts = ref({ agents: 0, http_rules: 0, l4_rules: 0, relay_listeners: 0, certificates: 0, version_policies: 0 })
const activeTab = ref('export')

onMounted(() => {
  fetchBackupResourceCounts()
    .then(d => { counts.value = d.counts })
    .catch(() => {})
})
</script>

<style scoped>
.settings-data-mgmt {
  display: flex;
  flex-direction: column;
  gap: var(--space-5);
}

.data-mgmt-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-4);
  padding: var(--space-5);
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-xl);
}

.data-mgmt-header__title {
  font-size: var(--text-xl);
  font-weight: var(--font-bold);
  color: var(--color-text-primary);
  margin: 0 0 var(--space-1);
}

.data-mgmt-header__desc {
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
  margin: 0;
}

.config-io-card {
  display: flex;
  flex-direction: column;
  padding: 0;
  overflow: hidden;
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

@media (max-width: 640px) {
  .config-io-tabs {
    flex-direction: column;
  }
}
</style>

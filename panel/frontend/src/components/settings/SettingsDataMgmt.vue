<template>
  <div class="settings-data-mgmt">
    <div class="data-mgmt-header">
      <div class="data-mgmt-header__text">
        <h1 class="data-mgmt-header__title">数据管理</h1>
        <p class="data-mgmt-header__desc">备份或恢复面板配置</p>
      </div>
      <div class="data-actions">
        <button class="btn btn--primary" @click="scrollTo(exportPanelRef)">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/></svg>
          备份
        </button>
        <button class="btn btn--primary" @click="scrollTo(importWizardRef)">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="17 8 12 3 7 8"/><line x1="12" y1="3" x2="12" y2="15"/></svg>
          恢复
        </button>
      </div>
    </div>

    <section ref="exportPanelRef" class="settings-section">
      <ExportPanel :counts="counts" />
    </section>
    <section ref="importWizardRef" class="settings-section">
      <ImportWizard />
    </section>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import ExportPanel from './data-mgmt/ExportPanel.vue'
import ImportWizard from './data-mgmt/ImportWizard.vue'
import { fetchBackupResourceCounts } from '../../api'

// 数据管理容器：统一获取资源 counts，传给导出/导入子组件
const counts = ref({ agents: 0, http_rules: 0, l4_rules: 0, relay_listeners: 0, certificates: 0, version_policies: 0 })
const exportPanelRef = ref(null)
const importWizardRef = ref(null)

function scrollTo(elRef) {
  elRef.value?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

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

.data-actions {
  display: flex;
  gap: var(--space-3);
  flex-wrap: wrap;
  flex-shrink: 0;
}

.settings-placeholder {
  margin: 0;
  padding: var(--space-4);
  color: var(--color-text-tertiary);
  font-size: var(--text-sm);
  text-align: center;
  border: 1px dashed var(--color-border-default);
  border-radius: var(--radius-md);
}

@media (max-width: 640px) {
  .data-mgmt-header {
    flex-direction: column;
    align-items: stretch;
  }
  .data-actions {
    justify-content: flex-start;
  }
}
</style>

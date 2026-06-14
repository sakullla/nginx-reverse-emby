<template>
  <div class="settings-data-mgmt">
    <ExportPanel :counts="counts" />
    <ImportWizard />
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import ExportPanel from './data-mgmt/ExportPanel.vue'
import ImportWizard from './data-mgmt/ImportWizard.vue'
import { fetchBackupResourceCounts } from '../../api'

// 数据管理容器：统一获取资源 counts，传给导出/导入子组件；导入向导由 T6 提供
const counts = ref({ agents: 0, http_rules: 0, l4_rules: 0, relay_listeners: 0, certificates: 0, version_policies: 0 })

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
.settings-placeholder {
  margin: 0;
  padding: var(--space-4);
  color: var(--color-text-tertiary);
  font-size: var(--text-sm);
  text-align: center;
  border: 1px dashed var(--color-border-default);
  border-radius: var(--radius-md);
}
</style>

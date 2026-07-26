<template>
  <div class="settings-page">
    <div class="settings-page__header">
      <h1 class="settings-page__title">系统设置</h1>
      <p class="settings-page__desc">按任务管理偏好、备份恢复、网络出口与系统信息</p>
    </div>
    <div class="settings-layout">
      <SettingsNav v-model:activeTab="activeTab" :tabs="tabs" />
      <div class="settings-content">
        <SettingsGeneral v-if="activeTab === 'preferences'" />
        <SettingsDataMgmt v-else-if="activeTab === 'backup'" />
        <SettingsNetworkEgress v-else-if="activeTab === 'egress'" />
        <SettingsAbout v-else-if="activeTab === 'about'" />
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import SettingsNav from '../components/settings/SettingsNav.vue'
import SettingsGeneral from '../components/settings/SettingsGeneral.vue'
import SettingsDataMgmt from '../components/settings/SettingsDataMgmt.vue'
import SettingsNetworkEgress from '../components/settings/SettingsNetworkEgress.vue'
import SettingsAbout from '../components/settings/SettingsAbout.vue'
import '../components/settings/design-language.css'

const activeTab = ref('preferences')

// 任务四区：偏好 / 备份恢复 / 网络出口 / 系统关于
const tabs = [
  { id: 'preferences', icon: '⚙️', label: '偏好' },
  { id: 'backup', icon: '💾', label: '备份恢复' },
  { id: 'egress', icon: '↗️', label: '网络出口' },
  { id: 'about', icon: 'ℹ️', label: '系统关于' }
]
</script>

<style scoped>
.settings-page {
  max-width: 960px;
  margin: 0 auto;
}
.settings-page__header {
  margin-bottom: var(--space-6);
  padding-bottom: var(--space-4);
  border-bottom: 1px solid var(--color-border-subtle);
}
.settings-page__title {
  font-size: var(--text-2xl);
  font-weight: var(--font-bold);
  margin: 0 0 var(--space-1);
  color: var(--color-text-primary);
}
.settings-page__desc {
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
  margin: 0;
}

.settings-layout {
  display: flex;
  gap: 0;
  background: var(--color-bg-subtle);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-2xl);
  overflow: hidden;
  min-height: 28rem;
}

.settings-content {
  flex: 1;
  min-width: 0;
  padding: var(--space-6) var(--space-8) var(--space-8);
  background: var(--color-bg-canvas);
}

@media (max-width: 767px) {
  .settings-page { max-width: 100%; }
  .settings-layout {
    flex-direction: column;
    min-height: 0;
  }
  .settings-content { padding: var(--space-5); }
}
</style>

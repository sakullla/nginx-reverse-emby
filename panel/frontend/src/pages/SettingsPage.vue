<template>
  <div class="settings-page">
    <div class="settings-page__header">
      <h1 class="settings-page__title">系统设置</h1>
      <p class="settings-page__desc">按任务管理偏好、备份恢复、网络出口与系统信息</p>
    </div>
    <div class="settings-layout">
      <SettingsNav v-model:activeTab="activeTab" :tabs="tabs">
        <template #tab-icon="{ tab }">
          <svg v-if="tab.id === 'preferences'" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
            <circle cx="12" cy="12" r="3" />
            <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09a1.65 1.65 0 0 0-1-1.51 1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09a1.65 1.65 0 0 0 1.51-1 1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33h0a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51h0a1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82v0a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" />
          </svg>
          <svg v-else-if="tab.id === 'backup'" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
            <ellipse cx="12" cy="5" rx="8" ry="3" />
            <path d="M4 5v14c0 1.66 3.58 3 8 3s8-1.34 8-3V5" />
            <path d="M4 12c0 1.66 3.58 3 8 3s8-1.34 8-3" />
          </svg>
          <svg v-else-if="tab.id === 'egress'" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
            <path d="M7 17L17 7" />
            <path d="M8 7h9v9" />
          </svg>
          <svg v-else width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
            <circle cx="12" cy="12" r="9" />
            <line x1="12" y1="11" x2="12" y2="16" />
            <line x1="12" y1="8" x2="12.01" y2="8" />
          </svg>
        </template>
      </SettingsNav>
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

// 任务四区：偏好 / 备份恢复 / 网络出口 / 系统关于（图标由 tab-icon 插槽渲染内联 SVG）
const tabs = [
  { id: 'preferences', label: '偏好' },
  { id: 'backup', label: '备份恢复' },
  { id: 'egress', label: '网络出口' },
  { id: 'about', label: '系统关于' }
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

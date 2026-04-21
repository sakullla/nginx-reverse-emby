<template>
  <div class="settings-page">
    <div class="settings-page__header">
      <h1 class="settings-page__title">系统设置</h1>
      <p class="settings-page__desc">管理面板偏好与系统信息</p>
    </div>
    <div class="settings-layout">
      <SettingsNav v-model:activeTab="activeTab" />
      <div class="settings-content">
        <SettingsGeneral v-if="activeTab === 'general'" />
        <SettingsDataMgmt v-else-if="activeTab === 'data'" />
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
import SettingsAbout from '../components/settings/SettingsAbout.vue'

const activeTab = ref('general')
</script>

<style scoped>
.settings-page {
  max-width: 900px;
  margin: 0 auto;
}
.settings-page__header {
  margin-bottom: 1.5rem;
  padding-bottom: 1rem;
  border-bottom: 1px solid var(--color-border-subtle);
}
.settings-page__title { font-size: 1.5rem; font-weight: 700; margin: 0 0 0.25rem; color: var(--color-text-primary); }
.settings-page__desc { font-size: 0.875rem; color: var(--color-text-tertiary); margin: 0; }

.settings-layout {
  display: flex;
  gap: 0;
  background: var(--color-bg-subtle);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-2xl);
  overflow: hidden;
}

.settings-content {
  flex: 1;
  padding: 1.5rem 2rem 2rem;
  min-width: 0;
}

@media (max-width: 767px) {
  .settings-page { max-width: 100%; }
  .settings-layout { flex-direction: column; }
  .settings-content { padding: 1.25rem; }
}
</style>

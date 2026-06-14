<template>
  <div class="settings-page">
    <div class="settings-page__header">
      <h1 class="settings-page__title">系统设置</h1>
      <p class="settings-page__desc">管理面板偏好与系统信息</p>
    </div>
    <div class="settings-layout">
      <SettingsNav v-model:activeTab="activeTab" :tabs="tabs" />
      <div class="settings-content">
        <SettingsGeneral v-if="activeTab === 'appearance'" />
        <section v-else-if="activeTab === 'system'" class="settings-section">
          <div class="settings-section__header">
            <h2 class="settings-section__title">系统信息</h2>
            <p class="settings-section__desc">角色、本地 Agent、节点与运行状态</p>
          </div>
          <div class="settings-section__body">
            <p class="settings-placeholder">系统信息分区由后续任务提供（占位）。</p>
          </div>
        </section>
        <SettingsDataMgmt v-else-if="activeTab === 'data'" />
        <EgressProfilesPage v-else-if="activeTab === 'egress'" />
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
import EgressProfilesPage from './EgressProfilesPage.vue'
// 设置页设计语言范式（新设计语言先行）：.btn--* 与 .settings-section，复用 themes.css token
import '../components/settings/design-language.css'

const activeTab = ref('appearance')

// 分区配置化：外观主题 / 系统信息 / 数据管理 / Egress Profiles / 关于
// 各分区组件由 T2-T6 提供；system 分区当前为占位，由 T3（SettingsSystemInfo）替换
const tabs = [
  { id: 'appearance', icon: '🎨', label: '外观主题' },
  { id: 'system', icon: '🖥️', label: '系统信息' },
  { id: 'data', icon: '💾', label: '数据管理' },
  { id: 'egress', icon: '↗️', label: 'Egress Profiles' },
  { id: 'about', icon: 'ℹ️', label: '关于' }
]
</script>

<style scoped>
.settings-page {
  max-width: 900px;
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
}

.settings-content {
  flex: 1;
  min-width: 0;
  padding: var(--space-6) var(--space-8) var(--space-8);
  background: var(--color-bg-canvas);
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

@media (max-width: 767px) {
  .settings-page { max-width: 100%; }
  .settings-layout { flex-direction: column; }
  .settings-content { padding: var(--space-5); }
}
</style>

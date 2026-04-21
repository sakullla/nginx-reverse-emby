<template>
  <div class="settings-general">
    <section class="settings-section">
      <div class="settings-section__header">
        <h2 class="settings-section__title">外观主题</h2>
        <p class="settings-section__desc">选择面板的外观风格</p>
      </div>
      <div class="settings-section__body">
        <div class="theme-grid">
          <button
            v-for="theme in themes"
            :key="theme.id"
            class="theme-card"
            :class="{ active: currentTheme === theme.id }"
            @click="setTheme(theme.id)"
          >
            <div class="theme-card__preview" :class="'theme-preview--' + theme.id"></div>
            <span class="theme-card__label">{{ theme.label }}</span>
            <div v-if="currentTheme === theme.id" class="theme-card__indicator"></div>
          </button>
        </div>
      </div>
    </section>

    <section class="settings-section">
      <div class="settings-section__header">
        <h2 class="settings-section__title">部署模式</h2>
        <p class="settings-section__desc">查看当前系统的运行角色与本地 Agent 状态</p>
      </div>
      <div class="settings-section__body">
        <div class="info-row">
          <span class="info-label"><span class="info-icon">🖥️</span> 角色</span>
          <span class="info-value">{{ systemInfo?.role || '—' }}</span>
        </div>
        <div class="info-row">
          <span class="info-label"><span class="info-icon">⚡</span> 本地 Agent</span>
          <span class="info-value" :class="systemInfo?.local_agent_enabled ? 'status-ok' : ''">
            {{ systemInfo?.local_agent_enabled ? '● 已启用' : '未启用' }}
          </span>
        </div>
      </div>
    </section>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { useTheme } from '../../context/ThemeContext'
import { fetchSystemInfo } from '../../api'

const { currentThemeId: currentTheme, setTheme, themes } = useTheme()
const systemInfo = ref(null)

onMounted(() => {
  fetchSystemInfo().then(i => { systemInfo.value = i }).catch(() => {})
})
</script>

<style scoped>
.settings-general { display: flex; flex-direction: column; gap: 1.25rem; }
.settings-section {
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-xl);
  overflow: hidden;
  transition: box-shadow 0.2s var(--ease-default);
}
.settings-section:hover {
  box-shadow: 0 1px 4px color-mix(in srgb, var(--color-border-default) 30%, transparent);
}
.settings-section__header {
  padding: 1rem 1.25rem;
  border-bottom: 1px solid var(--color-border-subtle);
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.settings-section__title { font-size: 1rem; font-weight: 600; margin: 0 0 0.25rem; color: var(--color-text-primary); }
.settings-section__desc { font-size: 0.8rem; color: var(--color-text-tertiary); margin: 0; }
.settings-section__body { padding: 1.25rem; display: flex; flex-direction: column; gap: 1rem; }

.theme-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(100px, 1fr)); gap: 0.75rem; }
.theme-card {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 0.5rem;
  padding: 0.75rem;
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  background: var(--color-bg-subtle);
  cursor: pointer;
  transition: all 0.2s var(--ease-default);
}
.theme-card:hover { border-color: var(--color-primary); transform: translateY(-1px); }
.theme-card.active {
  border-color: var(--color-primary);
  background: var(--color-primary-subtle);
  box-shadow: 0 2px 8px color-mix(in srgb, var(--color-primary) 15%, transparent);
  transform: translateY(-1px);
}
.theme-card__preview {
  width: 100%;
  height: 32px;
  border-radius: var(--radius-md);
  border: 1px solid var(--color-border-subtle);
}
.theme-card__label { font-size: 0.875rem; font-weight: 500; color: var(--color-text-primary); }
.theme-card__indicator {
  width: 20px;
  height: 3px;
  border-radius: 2px;
  background: var(--color-primary);
}
.theme-preview--light { background: linear-gradient(135deg, #fff 50%, #f5f5f5 50%); }
.theme-preview--dark { background: linear-gradient(135deg, #1a1a2e 50%, #16213e 50%); }
.theme-preview--auto { background: linear-gradient(135deg, #fff 50%, #1a1a2e 50%); }

.info-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.6rem 0;
  border-bottom: 1px solid var(--color-border-subtle);
}
.info-row:last-child { border-bottom: none; }
.info-label { font-size: 0.875rem; color: var(--color-text-secondary); display: flex; align-items: center; }
.info-value { font-size: 0.875rem; color: var(--color-text-primary); font-weight: 500; }
.info-icon { margin-right: 0.4rem; font-size: 0.9rem; }
.status-ok { color: #16a34a; }
</style>

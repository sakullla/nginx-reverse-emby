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
            class="theme-option"
            :class="{ active: currentTheme === theme.id }"
            @click="setTheme(theme.id)"
          >
            <span class="theme-option__label">{{ theme.label }}</span>
            <svg v-if="currentTheme === theme.id" class="theme-option__check" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3">
              <polyline points="20 6 9 17 4 12"/>
            </svg>
          </button>
        </div>
      </div>
    </section>

    <section class="settings-section">
      <div class="settings-section__header">
        <h2 class="settings-section__title">部署模式</h2>
        <p class="settings-section__desc">当前面板的运行模式</p>
      </div>
      <div class="settings-section__body">
        <div class="info-row">
          <span class="info-label">角色</span>
          <span class="info-value">{{ systemInfo?.role || '—' }}</span>
        </div>
        <div class="info-row">
          <span class="info-label">本地 Agent</span>
          <span class="info-value">{{ systemInfo?.local_agent_enabled ? '已启用' : '未启用' }}</span>
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
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-2xl);
  overflow: hidden;
}
.settings-section__header { padding: 1rem 1.25rem; border-bottom: 1px solid var(--color-border-subtle); }
.settings-section__title { font-size: 1rem; font-weight: 600; margin: 0 0 0.25rem; color: var(--color-text-primary); }
.settings-section__desc { font-size: 0.875rem; color: var(--color-text-tertiary); margin: 0; }
.settings-section__body { padding: 1.25rem; display: flex; flex-direction: column; gap: 1rem; }

.theme-grid { display: flex; gap: 0.5rem; flex-wrap: wrap; }
.theme-option {
  display: flex; align-items: center; gap: 0.5rem;
  padding: 0.5rem 0.75rem;
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  background: var(--color-bg-subtle);
  cursor: pointer;
  transition: all 0.2s var(--ease-default);
}
.theme-option:hover { border-color: var(--color-primary); transform: translateY(-1px); }
.theme-option.active {
  border-color: var(--color-primary);
  background: var(--color-primary-subtle);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--color-primary) 20%, transparent);
  transform: translateY(-1px);
}
.theme-option__label { font-size: 0.875rem; font-weight: 500; color: var(--color-text-primary); }
.theme-option__check { color: var(--color-primary); animation: checkPop 0.3s var(--ease-bounce); }
@keyframes checkPop {
  0% { transform: scale(0); opacity: 0; }
  100% { transform: scale(1); opacity: 1; }
}

.info-row { display: flex; align-items: center; justify-content: space-between; padding: 0.5rem 0; border-bottom: 1px solid var(--color-border-subtle); }
.info-row:last-child { border-bottom: none; }
.info-label { font-size: 0.875rem; color: var(--color-text-secondary); }
.info-value { font-size: 0.875rem; color: var(--color-text-primary); font-weight: 500; }
</style>

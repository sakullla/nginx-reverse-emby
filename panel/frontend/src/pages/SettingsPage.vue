<template>
  <div class="settings-page">
    <div class="settings-page__header">
      <h1 class="settings-page__title">系统设置</h1>
    </div>

    <!-- Theme -->
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
            <span class="theme-option__emoji">{{ theme.emoji }}</span>
            <span class="theme-option__label">{{ theme.label }}</span>
            <svg v-if="currentTheme === theme.id" class="theme-option__check" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3">
              <polyline points="20 6 9 17 4 12"/>
            </svg>
          </button>
        </div>
      </div>
    </section>

    <!-- Deploy Mode -->
    <section class="settings-section">
      <div class="settings-section__header">
        <h2 class="settings-section__title">部署模式</h2>
        <p class="settings-section__desc">当前面板的运行模式</p>
      </div>
      <div class="settings-section__body">
        <div class="info-row">
          <span class="info-label">角色</span>
          <span class="info-value">{{ systemInfo.role || '—' }}</span>
        </div>
        <div class="info-row">
          <span class="info-label">部署方式</span>
          <span class="info-value">{{ systemInfo.deployMode || '—' }}</span>
        </div>
        <div class="info-row">
          <span class="info-label">本地 Agent</span>
          <span class="info-value">{{ systemInfo.local_agent_enabled ? '已启用' : '未启用' }}</span>
        </div>
      </div>
    </section>

    <!-- About -->
    <section class="settings-section">
      <div class="settings-section__header">
        <h2 class="settings-section__title">关于</h2>
      </div>
      <div class="settings-section__body">
        <div class="info-row">
          <span class="info-label">版本</span>
          <span class="info-value">1.0.0</span>
        </div>
        <div class="info-row">
          <span class="info-label">项目</span>
          <span class="info-value">nginx-reverse-emby</span>
        </div>
      </div>
    </section>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import { useTheme } from '../context/ThemeContext'

const { currentThemeId: currentTheme, setTheme, themes } = useTheme()

const systemInfo = ref({ role: 'master', deployMode: 'direct', local_agent_enabled: true })
</script>

<style scoped>
.settings-page { max-width: 700px; margin: 0 auto; }
.settings-page__header { margin-bottom: 2rem; }
.settings-page__title { font-size: 1.5rem; font-weight: 700; margin: 0; color: var(--color-text-primary); }
.settings-section { background: var(--color-bg-surface); border: 1.5px solid var(--color-border-default); border-radius: var(--radius-2xl); margin-bottom: 1.25rem; overflow: hidden; }
.settings-section__header { padding: 1rem 1.25rem; border-bottom: 1px solid var(--color-border-subtle); }
.settings-section__title { font-size: 1rem; font-weight: 600; margin: 0 0 0.25rem; color: var(--color-text-primary); }
.settings-section__desc { font-size: 0.875rem; color: var(--color-text-tertiary); margin: 0; }
.settings-section__body { padding: 1.25rem; display: flex; flex-direction: column; gap: 1rem; }
.theme-grid { display: flex; gap: 0.75rem; }
.theme-option { display: flex; flex-direction: column; align-items: center; gap: 0.5rem; padding: 1rem; border: 1.5px solid var(--color-border-default); border-radius: var(--radius-xl); background: var(--color-bg-subtle); cursor: pointer; transition: all 0.15s; position: relative; min-width: 100px; }
.theme-option:hover { border-color: var(--color-primary); }
.theme-option.active { border-color: var(--color-primary); background: var(--color-primary-subtle); }
.theme-option__emoji { font-size: 1.5rem; }
.theme-option__label { font-size: 0.875rem; font-weight: 500; color: var(--color-text-primary); }
.theme-option__check { position: absolute; top: 0.5rem; right: 0.5rem; color: var(--color-primary); }
.info-row { display: flex; align-items: center; justify-content: space-between; padding: 0.5rem 0; border-bottom: 1px solid var(--color-border-subtle); }
.info-row:last-child { border-bottom: none; }
.info-label { font-size: 0.875rem; color: var(--color-text-secondary); }
.info-value { font-size: 0.875rem; color: var(--color-text-primary); font-weight: 500; }
.input-base { width: 100%; padding: 0.5rem 0.75rem; border-radius: var(--radius-lg); border: 1.5px solid var(--color-border-default); background: var(--color-bg-subtle); font-size: 0.875rem; color: var(--color-text-primary); outline: none; font-family: inherit; transition: border-color 0.15s; }
.input-base:focus { border-color: var(--color-primary); }
.btn { padding: 0.5rem 1rem; border-radius: var(--radius-lg); font-size: 0.875rem; font-weight: 500; cursor: pointer; transition: all 0.15s; border: none; font-family: inherit; display: inline-flex; align-items: center; gap: 0.375rem; }
.btn-primary { background: var(--gradient-primary); color: white; }
.btn-secondary { background: var(--color-bg-subtle); color: var(--color-text-primary); border: 1px solid var(--color-border-default); }
</style>

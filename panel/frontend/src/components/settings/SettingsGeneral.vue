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
            <span class="theme-card__icon">{{ theme.emoji || '✨' }}</span>
            <span class="theme-card__label">{{ theme.label }}</span>
            <div v-if="currentTheme === theme.id" class="theme-card__indicator"></div>
          </button>
        </div>
      </div>
    </section>
  </div>
</template>

<script setup>
import { useTheme } from '../../context/ThemeContext'

const { currentThemeId: currentTheme, setTheme, themes } = useTheme()
</script>

<style scoped>
.settings-general {
  display: flex;
  flex-direction: column;
  gap: var(--space-5);
}

.theme-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(100px, 1fr));
  gap: var(--space-3);
}
.theme-card {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-3);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  background: var(--color-bg-subtle);
  cursor: pointer;
  font-family: inherit;
  transition: border-color var(--duration-fast) var(--ease-default),
              background-color var(--duration-fast) var(--ease-default),
              transform var(--duration-fast) var(--ease-default),
              box-shadow var(--duration-fast) var(--ease-default);
}
.theme-card:hover {
  border-color: var(--color-primary);
  transform: translateY(-1px);
}
.theme-card.active {
  border-color: var(--color-primary);
  background: var(--color-primary-subtle);
  box-shadow: 0 2px 8px color-mix(in srgb, var(--color-primary) 15%, transparent);
  transform: translateY(-1px);
}
.theme-card__icon { font-size: var(--text-xl); }
.theme-card__label {
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  color: var(--color-text-primary);
}
.theme-card__indicator {
  width: 20px;
  height: 3px;
  border-radius: var(--radius-full);
  background: var(--color-primary);
}
</style>

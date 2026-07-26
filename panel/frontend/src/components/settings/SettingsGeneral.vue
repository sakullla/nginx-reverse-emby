<template>
  <div class="settings-general">
    <header class="task-header">
      <div>
        <h2 class="task-header__title">偏好</h2>
        <p class="task-header__desc">本地外观与面板行为，修改后即时生效</p>
      </div>
    </header>

    <section class="settings-section">
      <div class="settings-section__header">
        <h3 class="settings-section__title">外观主题</h3>
        <p class="settings-section__desc">选择面板配色风格</p>
      </div>
      <div class="settings-section__body">
        <div class="theme-grid" data-testid="pref-theme">
          <button
            v-for="theme in themes"
            :key="theme.id"
            type="button"
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

    <section class="settings-section">
      <div class="settings-section__header">
        <h3 class="settings-section__title">面板行为</h3>
        <p class="settings-section__desc">可扩展本地偏好，后续功能可继续挂载到此区</p>
      </div>
      <div class="settings-section__body settings-prefs">
        <div
          v-for="pref in preferenceDefs"
          :key="pref.key"
          class="settings-pref"
          :data-testid="`pref-${pref.key}`"
        >
          <div class="settings-pref__text">
            <h4 class="settings-pref__label">{{ pref.label }}</h4>
            <p v-if="pref.description" class="settings-pref__desc">{{ pref.description }}</p>
          </div>
          <div v-if="pref.type === 'enum'" class="settings-pref__control" role="group" :aria-label="pref.label">
            <button
              v-for="option in pref.options"
              :key="option.value"
              type="button"
              class="settings-pref__option"
              :class="{ 'settings-pref__option--active': preferenceValues[pref.key] === option.value }"
              @click="preferenceValues[pref.key] = option.value"
            >
              {{ option.label }}
            </button>
          </div>
        </div>
      </div>
    </section>
  </div>
</template>

<script setup>
import { reactive } from 'vue'
import { useTheme } from '../../context/ThemeContext'
import { usePreference } from '../../hooks/usePreference'
import { PREFERENCE_DEFINITIONS, normalizeTrafficView } from '../../preferences/definitions'

const { currentThemeId: currentTheme, setTheme, themes } = useTheme()

const preferenceDefs = PREFERENCE_DEFINITIONS
const preferenceValues = reactive(
  Object.fromEntries(
    preferenceDefs.map((pref) => {
      const value = usePreference(pref.key, pref.defaultValue)
      if (pref.key === 'dashboard.trafficView') {
        value.value = normalizeTrafficView(value.value, pref.defaultValue)
      }
      return [pref.key, value]
    })
  )
)
</script>

<style scoped>
.settings-general {
  display: flex;
  flex-direction: column;
  gap: var(--space-5);
}

.task-header__title {
  margin: 0 0 0.25rem;
  font-size: var(--text-lg);
  font-weight: 700;
  color: var(--color-text-primary);
}

.task-header__desc {
  margin: 0;
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
}

.settings-prefs {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.settings-pref {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-4);
  flex-wrap: wrap;
  padding: var(--space-3) var(--space-4);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-xl);
  background: var(--color-bg-subtle);
}

.settings-pref__text {
  min-width: 0;
  flex: 1 1 12rem;
}

.settings-pref__label {
  margin: 0 0 0.25rem;
  font-size: var(--text-sm);
  font-weight: 600;
  color: var(--color-text-primary);
}

.settings-pref__desc {
  margin: 0;
  font-size: var(--text-xs);
  color: var(--color-text-tertiary);
  line-height: 1.45;
}

.settings-pref__control {
  display: inline-flex;
  gap: 2px;
  padding: 2px;
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  flex-shrink: 0;
}

.settings-pref__option {
  min-width: 4.5rem;
  padding: 0.35rem 0.75rem;
  border: 0;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--color-text-tertiary);
  font-size: 0.8125rem;
  font-weight: 600;
  cursor: pointer;
  font-family: inherit;
}

.settings-pref__option--active {
  background: var(--color-bg-surface);
  color: var(--color-primary);
  box-shadow: var(--shadow-sm);
}

.theme-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(96px, max-content));
  grid-auto-rows: max-content;
  justify-content: start;
  align-items: start;
  align-content: start;
  gap: var(--space-3);
}

.theme-card {
  display: inline-flex;
  flex-direction: column;
  align-items: center;
  justify-content: flex-start;
  gap: var(--space-2);
  width: 100%;
  max-width: 120px;
  height: auto !important;
  min-height: 0;
  align-self: start;
  padding: var(--space-3);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  background: var(--color-bg-surface);
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

.theme-card__icon {
  font-size: var(--text-xl);
  line-height: 1;
}

.theme-card__label {
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  color: var(--color-text-primary);
  line-height: 1.2;
}

.theme-card__indicator {
  width: 20px;
  height: 3px;
  border-radius: var(--radius-full);
  background: var(--color-primary);
}
</style>

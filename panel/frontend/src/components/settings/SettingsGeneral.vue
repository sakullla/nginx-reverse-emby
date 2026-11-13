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
        <p class="settings-section__desc">选择面板配色；“跟随系统”会按系统明暗在昼樱与夜樱之间自动切换</p>
      </div>
      <div class="settings-section__body">
        <div class="theme-grid" data-testid="pref-theme">
          <button
            v-for="theme in themes"
            :key="theme.id"
            type="button"
            class="theme-card"
            :class="{ active: currentTheme === theme.id }"
            :aria-pressed="currentTheme === theme.id"
            @click="setTheme(theme.id)"
          >
            <span class="theme-card__preview" aria-hidden="true">
              <span
                v-for="paneTheme in previewThemes(theme.id)"
                :key="paneTheme"
                class="theme-card__pane"
                :data-theme="paneTheme"
              >
                <span class="theme-card__side"><span class="theme-card__logo"></span><span class="theme-card__nav"></span><span class="theme-card__nav"></span></span>
                <span class="theme-card__main">
                  <span class="theme-card__line theme-card__line--title"></span>
                  <span class="theme-card__panel"><span class="theme-card__line"></span><span class="theme-card__line theme-card__line--short"></span></span>
                  <span class="theme-card__btn"></span>
                </span>
              </span>
            </span>
            <span class="theme-card__label">
              <span class="theme-card__radio" aria-hidden="true"></span>
              {{ theme.label }}
            </span>
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
              :aria-pressed="preferenceValues[pref.key] === option.value"
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
import { SYSTEM_DARK_THEME, SYSTEM_LIGHT_THEME, useTheme } from '../../context/ThemeContext'
import { usePreference } from '../../hooks/usePreference'
import { PREFERENCE_DEFINITIONS, normalizeTrafficView } from '../../preferences/definitions'

const { currentThemeId: currentTheme, setTheme, themes } = useTheme()

function previewThemes(id) {
  return id === 'system' ? [SYSTEM_LIGHT_THEME, SYSTEM_DARK_THEME] : [id]
}

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
  gap: var(--space-0-5);
  padding: var(--space-0-5);
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
  grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
  gap: var(--space-3);
  max-width: 760px;
}

.theme-card {
  display: flex;
  flex-direction: column;
  gap: var(--space-2-5);
  padding: var(--space-1-5) var(--space-1-5) var(--space-2-5);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  background: var(--color-bg-surface);
  cursor: pointer;
  font-family: inherit;
  text-align: left;
  transition: border-color var(--duration-fast) var(--ease-default), box-shadow var(--duration-fast) var(--ease-default);
}

.theme-card:hover {
  border-color: var(--color-border-strong);
}

.theme-card.active {
  border-color: var(--color-primary);
  box-shadow: 0 0 0 3px var(--color-primary-100);
}

.theme-card__preview {
  display: flex;
  height: 76px;
  border-radius: var(--radius-md);
  overflow: hidden;
  border: 1px solid var(--color-border-subtle);
}

/* Each pane carries data-theme, so it paints with that theme's tokens. */
.theme-card__pane {
  flex: 1;
  display: flex;
  min-width: 0;
  background: var(--color-bg-atmosphere);
}

.theme-card__side {
  display: flex;
  flex-direction: column;
  gap: 4px;
  width: 26%;
  padding: 7px 5px;
  border-right: 1px solid var(--color-border-subtle);
  box-sizing: border-box;
}

.theme-card__logo {
  width: 10px;
  height: 10px;
  margin-bottom: 2px;
  border-radius: 3px;
  background: var(--color-brand-bg);
}

.theme-card__nav {
  height: 4px;
  border-radius: 2px;
  background: var(--color-border-strong);
}

.theme-card__nav:first-of-type {
  background: var(--color-primary);
}

.theme-card__main {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 5px;
  padding: 7px;
  min-width: 0;
}

.theme-card__panel {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 5px;
  border-radius: 4px;
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
}

.theme-card__line {
  height: 4px;
  border-radius: 2px;
  background: var(--color-border-strong);
}

.theme-card__line--title {
  width: 50%;
  height: 5px;
  background: var(--color-text-primary);
  opacity: 0.8;
}

.theme-card__line--short {
  width: 55%;
}

.theme-card__btn {
  align-self: flex-end;
  width: 22px;
  height: 7px;
  border-radius: 3px;
  background: var(--color-button-bg);
}

.theme-card__label {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: 0 var(--space-1);
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  color: var(--color-text-primary);
}

.theme-card__radio {
  width: 14px;
  height: 14px;
  border-radius: 50%;
  border: 1.5px solid var(--color-border-strong);
  box-sizing: border-box;
  flex-shrink: 0;
}

.theme-card.active .theme-card__radio {
  border: 4px solid var(--color-primary);
}

@media (max-width: 520px) {
  .theme-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: var(--space-2);
  }
}
</style>

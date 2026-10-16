<template>
  <section
    v-if="visible"
    class="onboarding"
    data-testid="onboarding-checklist"
    aria-label="上手引导"
  >
    <header class="onboarding__header">
      <div class="onboarding__heading">
        <h2 class="onboarding__title">三步完成上手</h2>
        <p class="onboarding__desc">跟着清单完成第一条规则与访问验证，随时可以关闭。</p>
      </div>
      <button
        type="button"
        class="onboarding__close"
        data-testid="onboarding-dismiss"
        title="关闭上手清单"
        aria-label="关闭上手清单"
        @click="dismiss"
      >
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/>
        </svg>
      </button>
    </header>

    <ol class="onboarding__steps">
      <li
        class="onboarding__step"
        :class="{ 'onboarding__step--done': hasRules }"
        data-testid="onboarding-step-rules"
      >
        <span class="onboarding__marker" aria-hidden="true">
          <svg v-if="hasRules" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3">
            <path d="M20 6 9 17l-5-5"/>
          </svg>
          <template v-else>1</template>
        </span>
        <div class="onboarding__body">
          <p class="onboarding__step-title">添加第一条规则</p>
          <p class="onboarding__step-hint">HTTP 反代或 L4 转发，任选一种创建</p>
          <div v-if="!hasRules" class="onboarding__actions">
            <RouterLink
              :to="{ path: '/rules', query: { create: '1' } }"
              class="btn btn-sm"
              data-testid="onboarding-add-http"
            >
              添加 HTTP 规则
            </RouterLink>
            <RouterLink
              :to="{ path: '/l4', query: { create: '1' } }"
              class="btn btn--secondary btn-sm"
              data-testid="onboarding-add-l4"
            >
              添加 L4 规则
            </RouterLink>
          </div>
          <p v-else class="onboarding__done" data-testid="onboarding-rules-done">已完成</p>
        </div>
      </li>

      <li
        class="onboarding__step"
        :class="{ 'onboarding__step--done': state.verifyDone }"
        data-testid="onboarding-step-verify"
      >
        <span class="onboarding__marker" aria-hidden="true">
          <svg v-if="state.verifyDone" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3">
            <path d="M20 6 9 17l-5-5"/>
          </svg>
          <template v-else>2</template>
        </span>
        <div class="onboarding__body">
          <p class="onboarding__step-title">验证访问</p>
          <p class="onboarding__step-hint">用规则入口访问一次，确认后端可达</p>
          <div v-if="!state.verifyDone" class="onboarding__actions">
            <button
              type="button"
              class="btn btn--secondary btn-sm"
              data-testid="onboarding-verify-done"
              @click="markVerifyDone"
            >
              标记完成
            </button>
          </div>
          <p v-else class="onboarding__done" data-testid="onboarding-verify-check">已完成</p>
        </div>
      </li>

      <li
        class="onboarding__step"
        :class="{ 'onboarding__step--done': state.httpsDone }"
        data-testid="onboarding-step-https"
      >
        <span class="onboarding__marker" aria-hidden="true">
          <svg v-if="state.httpsDone" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3">
            <path d="M20 6 9 17l-5-5"/>
          </svg>
          <template v-else>3</template>
        </span>
        <div class="onboarding__body">
          <p class="onboarding__step-title">按需启用 HTTPS</p>
          <p class="onboarding__step-hint">在证书中心申请证书并绑定到规则</p>
          <div v-if="!state.httpsDone" class="onboarding__actions">
            <button
              type="button"
              class="btn btn--secondary btn-sm"
              data-testid="onboarding-https-done"
              @click="markHttpsDone"
            >
              标记完成
            </button>
          </div>
          <p v-else class="onboarding__done" data-testid="onboarding-https-check">已完成</p>
        </div>
      </li>
    </ol>
  </section>
</template>

<script setup>
import { computed, watch } from 'vue'
import { usePreference } from '../../hooks/usePreference'

// R2: first-run checklist for fresh instances. The preference also drives the
// "重新打开上手引导" entry in SettingsAbout, so keep the shape in sync there.
const ONBOARDING_CHECKLIST_KEY = 'onboarding.checklist.v1'
const DEFAULT_ONBOARDING_STATE = {
  started: false,
  dismissed: false,
  verifyDone: false,
  httpsDone: false
}

const props = defineProps({
  // Agents carry http_rules_count / l4_rules_count; any rule completes step 1.
  agents: { type: Array, default: () => [] },
  // True once the agents query has settled, so first-run detection is not
  // triggered from an empty loading state.
  loaded: { type: Boolean, default: false }
})

const pref = usePreference(ONBOARDING_CHECKLIST_KEY, DEFAULT_ONBOARDING_STATE)
const state = computed(() => {
  const raw = pref.value && typeof pref.value === 'object' ? pref.value : {}
  return {
    started: raw.started === true,
    dismissed: raw.dismissed === true,
    verifyDone: raw.verifyDone === true,
    httpsDone: raw.httpsDone === true
  }
})

const ruleCount = computed(() => (Array.isArray(props.agents) ? props.agents : []).reduce(
  (sum, agent) => sum + (Number(agent?.http_rules_count) || 0) + (Number(agent?.l4_rules_count) || 0),
  0
))
const hasRules = computed(() => ruleCount.value > 0)
const allDone = computed(() => hasRules.value && state.value.verifyDone && state.value.httpsDone)
const visible = computed(() => state.value.started && !state.value.dismissed && !allDone.value)

function updateState(patch) {
  pref.value = { ...state.value, ...patch }
}

// First visit on an instance without any rule: show the checklist once.
// Instances that already have rules never auto-start.
watch(
  () => [props.loaded, hasRules.value],
  () => {
    if (props.loaded && !state.value.started && !hasRules.value) {
      updateState({ started: true })
    }
  },
  { immediate: true }
)

function dismiss() {
  updateState({ dismissed: true })
}

function markVerifyDone() {
  updateState({ verifyDone: true })
}

function markHttpsDone() {
  updateState({ httpsDone: true })
}
</script>

<style scoped>
.onboarding {
  background: var(--color-bg-panel, var(--color-bg-surface));
  border: 1px solid color-mix(in srgb, var(--color-primary) 22%, var(--color-border-subtle));
  border-radius: var(--radius-2xl);
  box-shadow: var(--shadow-xs);
  padding: var(--space-4) var(--space-5);
}

.onboarding__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-3);
  margin-bottom: var(--space-4);
}

.onboarding__heading {
  min-width: 0;
}

.onboarding__title {
  margin: 0 0 0.2rem;
  font-size: var(--text-base);
  font-weight: 650;
  color: var(--color-text-primary);
}

.onboarding__desc {
  margin: 0;
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
}

.onboarding__close {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 1.75rem;
  height: 1.75rem;
  flex-shrink: 0;
  border: 1px solid transparent;
  border-radius: var(--radius-md);
  background: transparent;
  color: var(--color-text-tertiary);
  cursor: pointer;
  transition: color var(--duration-fast) var(--ease-default),
    background var(--duration-fast) var(--ease-default);
}

.onboarding__close:hover {
  color: var(--color-text-primary);
  background: var(--color-bg-hover, var(--color-bg-subtle));
}

.onboarding__close:focus-visible {
  outline: none;
  box-shadow: var(--shadow-focus, 0 0 0 3px var(--color-primary-subtle));
}

.onboarding__steps {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: var(--space-3);
  margin: 0;
  padding: 0;
  list-style: none;
}

.onboarding__step {
  display: flex;
  gap: var(--space-3);
  min-width: 0;
  padding: var(--space-3);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-lg);
  background: var(--color-bg-subtle);
}

.onboarding__step--done {
  border-color: color-mix(in srgb, var(--color-success) 30%, var(--color-border-subtle));
  background: color-mix(in srgb, var(--color-success-subtle) 55%, var(--color-bg-surface));
}

.onboarding__marker {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 1.65rem;
  height: 1.65rem;
  flex-shrink: 0;
  margin-top: 1px;
  border-radius: var(--radius-full);
  background: var(--color-primary-subtle);
  color: var(--color-primary);
  font-size: var(--text-sm);
  font-weight: 700;
}

.onboarding__step--done .onboarding__marker {
  background: var(--color-success-subtle);
  color: var(--color-success);
}

.onboarding__body {
  min-width: 0;
}

.onboarding__step-title {
  margin: 0;
  font-size: var(--text-sm);
  font-weight: 600;
  color: var(--color-text-primary);
}

.onboarding__step-hint {
  margin: 0.15rem 0 0;
  font-size: var(--text-xs);
  color: var(--color-text-tertiary);
  line-height: 1.45;
}

.onboarding__actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  margin-top: var(--space-2-5);
}

.onboarding__done {
  display: inline-flex;
  align-items: center;
  gap: 0.25rem;
  margin: var(--space-2-5) 0 0;
  font-size: var(--text-xs);
  font-weight: 600;
  color: var(--color-success);
}

@media (max-width: 768px) {
  .onboarding__steps {
    grid-template-columns: 1fr;
  }
}
</style>

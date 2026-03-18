<template>
  <div
    class="rule-card"
    :class="{ 'rule-card--disabled': !rule.enabled }"
  >
    <!-- Header: Status & Actions -->
    <div class="rule-card__header">
      <div class="rule-card__status">
        <span class="status-dot" :class="rule.enabled ? 'status-dot--active' : 'status-dot--inactive'"></span>
        <span class="status-text">{{ rule.enabled ? '启用中' : '已停用' }}</span>
      </div>
      <div class="rule-card__actions">
        <button
          class="btn btn--icon btn--ghost tooltip"
          @click="toggleStatus"
        >
          <svg v-if="rule.enabled" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <rect x="6" y="4" width="4" height="16"/>
            <rect x="14" y="4" width="4" height="16"/>
          </svg>
          <svg v-else width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <polygon points="5 3 19 12 5 21 5 3"/>
          </svg>
          <span class="tooltip__content">{{ rule.enabled ? '停用' : '启用' }}</span>
        </button>

        <button
          class="btn btn--icon btn--ghost tooltip"
          @click="$emit('edit', rule)"
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
            <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
          </svg>
          <span class="tooltip__content">编辑</span>
        </button>

        <button
          class="btn btn--icon btn--ghost tooltip text-danger-hover"
          @click="$emit('delete', rule)"
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <polyline points="3 6 5 6 21 6"/>
            <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
          </svg>
          <span class="tooltip__content">删除</span>
        </button>
      </div>
    </div>

    <!-- Body: URLs -->
    <div class="rule-card__body">
      <div class="rule-card__url-row">
        <span class="rule-card__label">前端</span>
        <code class="rule-card__url">{{ rule.frontend_url }}</code>
      </div>
      <div class="rule-card__arrow">
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M12 5v14"/>
          <path d="M19 12l-7 7-7-7"/>
        </svg>
      </div>
      <div class="rule-card__url-row">
        <span class="rule-card__label">后端</span>
        <code class="rule-card__url">{{ rule.backend_url }}</code>
      </div>
    </div>

    <!-- Footer: Tags -->
    <div v-if="rule.tags?.length" class="rule-card__footer">
      <span
        v-for="tag in rule.tags"
        :key="tag"
        class="tag"
      >
        {{ tag }}
      </span>
    </div>
  </div>
</template>

<script setup>
import { useRuleStore } from '../stores/rules'

const props = defineProps({
  rule: { type: Object, required: true }
})

defineEmits(['edit', 'delete'])

const ruleStore = useRuleStore()

const toggleStatus = async () => {
  try {
    await ruleStore.toggleRule(props.rule.id, !props.rule.enabled)
  } catch (err) {
    // Error handled by store
  }
}
</script>

<style scoped>
.rule-card {
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  overflow: hidden;
  transition: all var(--duration-fast) var(--ease-default);
}

.rule-card:hover {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-sm);
}

.rule-card--disabled {
  opacity: 0.7;
  background: var(--color-bg-subtle);
}

.rule-card--disabled .rule-card__url {
  color: var(--color-text-secondary);
}

/* Header */
.rule-card__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-3) var(--space-4);
  background: var(--color-bg-subtle);
  border-bottom: 1px solid var(--color-border-subtle);
}

.rule-card__status {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.status-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
}

.status-dot--active {
  background: var(--color-success);
}

.status-dot--inactive {
  background: var(--color-text-muted);
}

.status-text {
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  color: var(--color-text-secondary);
}

.rule-card__actions {
  display: flex;
  align-items: center;
  gap: var(--space-1);
}

/* Body */
.rule-card__body {
  padding: var(--space-4);
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.rule-card__url-row {
  display: flex;
  flex-direction: column;
  gap: var(--space-1-5);
}

.rule-card__label {
  font-size: var(--text-xs);
  font-weight: var(--font-semibold);
  color: var(--color-text-muted);
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.rule-card__url {
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  color: var(--color-text-primary);
  word-break: break-all;
  line-height: 1.5;
}

.rule-card__arrow {
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--color-primary);
  padding: var(--space-1) 0;
}

/* Footer */
.rule-card__footer {
  display: flex;
  gap: var(--space-2);
  flex-wrap: wrap;
  padding: var(--space-3) var(--space-4);
  border-top: 1px solid var(--color-border-subtle);
  background: var(--color-bg-subtle);
}

.tag {
  display: inline-flex;
  align-items: center;
  padding: var(--space-1) var(--space-2);
  font-size: var(--text-xs);
  background: var(--color-bg-surface);
  color: var(--color-text-secondary);
  border-radius: var(--radius-full);
  border: 1px solid var(--color-border-default);
  font-weight: var(--font-medium);
}

.btn--icon {
  padding: var(--space-1-5);
  width: 28px;
  height: 28px;
}

.text-danger-hover:hover {
  color: var(--color-danger) !important;
}
</style>

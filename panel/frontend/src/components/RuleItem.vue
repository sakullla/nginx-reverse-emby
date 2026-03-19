<template>
  <div
    class="rule-card"
    :class="{ 'rule-card--disabled': !rule.enabled }"
  >
    <!-- Gradient top accent bar -->
    <div class="rule-card__accent" :class="rule.enabled ? 'rule-card__accent--active' : 'rule-card__accent--inactive'"></div>

    <div class="rule-card__body">
      <!-- Header: Status & Actions -->
      <div class="rule-card__header">
        <div class="rule-card__status">
          <span class="rule-card__status-dot" :class="rule.enabled ? 'rule-card__status-dot--on' : 'rule-card__status-dot--off'"></span>
          <span class="rule-card__status-text">{{ rule.enabled ? '启用中' : '已停用' }}</span>
        </div>
        <div class="rule-card__actions">
          <button
            class="rule-card__action"
            :class="rule.enabled ? 'rule-card__action--pause' : 'rule-card__action--play'"
            @click="toggleStatus"
            :title="rule.enabled ? '停用' : '启用'"
          >
            <svg v-if="rule.enabled" width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
              <rect x="6" y="4" width="4" height="16" rx="1"/>
              <rect x="14" y="4" width="4" height="16" rx="1"/>
            </svg>
            <svg v-else width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
              <polygon points="5 3 19 12 5 21 5 3"/>
            </svg>
          </button>
          <button
            class="rule-card__action rule-card__action--edit"
            @click="$emit('edit', rule)"
            title="编辑"
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
              <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
            </svg>
          </button>
          <button
            class="rule-card__action rule-card__action--delete"
            @click="$emit('delete', rule)"
            title="删除"
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <polyline points="3 6 5 6 21 6"/>
              <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
            </svg>
          </button>
        </div>
      </div>

      <!-- URL Mapping -->
      <div class="rule-card__mapping">
        <div class="rule-card__endpoint">
          <div class="rule-card__endpoint-label">前端入口</div>
          <div class="rule-card__endpoint-value">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <circle cx="12" cy="12" r="10"/>
              <line x1="2" y1="12" x2="22" y2="12"/>
              <path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/>
            </svg>
            <code>{{ rule.frontend_url }}</code>
          </div>
        </div>
        <div class="rule-card__arrow">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <line x1="5" y1="12" x2="19" y2="12"/>
            <polyline points="12 5 19 12 12 19"/>
          </svg>
        </div>
        <div class="rule-card__endpoint">
          <div class="rule-card__endpoint-label">后端目标</div>
          <div class="rule-card__endpoint-value">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <rect x="2" y="2" width="20" height="8" rx="2" ry="2"/>
              <rect x="2" y="14" width="20" height="8" rx="2" ry="2"/>
              <line x1="6" y1="6" x2="6.01" y2="6"/>
              <line x1="6" y1="18" x2="6.01" y2="18"/>
            </svg>
            <code>{{ rule.backend_url }}</code>
          </div>
        </div>
      </div>
    </div>

    <!-- Footer: Tags -->
    <div v-if="rule.tags?.length" class="rule-card__footer">
      <span v-for="tag in rule.tags" :key="tag" class="rule-card__tag">
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
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-2xl);
  overflow: hidden;
  transition: all var(--duration-normal) var(--ease-bounce);
  backdrop-filter: blur(12px);
  position: relative;
}

.rule-card:hover {
  border-color: var(--color-border-strong);
  box-shadow: var(--shadow-md);
  transform: translateY(-3px);
}

.rule-card--disabled {
  opacity: 0.6;
}

.rule-card--disabled:hover {
  transform: none;
  box-shadow: none;
}

/* Top Accent Bar */
.rule-card__accent {
  height: 3px;
  transition: opacity var(--duration-normal) var(--ease-default);
}

.rule-card__accent--active {
  background: var(--gradient-primary);
}

.rule-card__accent--inactive {
  background: var(--color-border-default);
}

/* Body */
.rule-card__body {
  padding: var(--space-4);
}

/* Header */
.rule-card__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: var(--space-4);
}

.rule-card__status {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.rule-card__status-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
}

.rule-card__status-dot--on {
  background: var(--color-success);
  box-shadow: 0 0 0 3px var(--color-success-50);
  animation: pulse 2s ease-in-out infinite;
}

.rule-card__status-dot--off {
  background: var(--color-text-muted);
}

.rule-card__status-text {
  font-size: var(--text-xs);
  font-weight: var(--font-semibold);
  color: var(--color-text-secondary);
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.rule-card__actions {
  display: flex;
  align-items: center;
  gap: var(--space-1);
}

.rule-card__action {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: all var(--duration-normal) var(--ease-bounce);
  border: none;
  background: transparent;
  color: var(--color-text-muted);
}

.rule-card__action:hover {
  transform: scale(1.15);
}

.rule-card__action--pause:hover {
  color: var(--color-warning);
  background: var(--color-warning-50);
}

.rule-card__action--play:hover {
  color: var(--color-success);
  background: var(--color-success-50);
}

.rule-card__action--edit:hover {
  color: var(--color-primary);
  background: var(--color-primary-subtle);
}

.rule-card__action--delete:hover {
  color: var(--color-danger);
  background: var(--color-danger-50);
}

/* URL Mapping */
.rule-card__mapping {
  display: flex;
  align-items: center;
  gap: var(--space-3);
}

.rule-card__endpoint {
  flex: 1;
  min-width: 0;
}

.rule-card__endpoint-label {
  font-size: var(--text-xs);
  font-weight: var(--font-semibold);
  color: var(--color-text-muted);
  text-transform: uppercase;
  letter-spacing: 0.05em;
  margin-bottom: var(--space-1-5);
}

.rule-card__endpoint-value {
  display: flex;
  align-items: flex-start;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  background: var(--color-bg-subtle);
  border-radius: var(--radius-lg);
  border: 1px solid var(--color-border-subtle);
  min-height: 40px;
}

.rule-card__endpoint-value svg {
  color: var(--color-text-muted);
  flex-shrink: 0;
}

.rule-card__endpoint-value code {
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  color: var(--color-text-primary);
  overflow-wrap: break-word;
  word-break: break-word;
  line-height: 1.5;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.rule-card__arrow {
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--color-primary);
  flex-shrink: 0;
  margin-top: var(--space-5);
  animation: float 3s ease-in-out infinite;
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

.rule-card__tag {
  font-size: var(--text-xs);
  font-weight: var(--font-medium);
  padding: var(--space-1) var(--space-2-5);
  background: var(--color-bg-surface);
  color: var(--color-primary);
  border-radius: var(--radius-full);
  border: 1px solid var(--color-border-default);
}

@keyframes float {
  0%, 100% { transform: translateY(0) translateX(0); }
  50% { transform: translateY(-3px) translateX(2px); }
}

@keyframes pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.6; }
}
</style>

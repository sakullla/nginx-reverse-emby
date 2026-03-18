<template>
  <div 
    class="rule-item"
    :class="{ 'rule-item--disabled': !rule.enabled }"
  >
    <div class="rule-item__content">
      <div class="rule-item__urls">
        <div class="rule-item__url-group">
          <span class="rule-item__label">前端</span>
          <code 
            class="rule-item__url"
            @click="copy(rule.frontend_url)"
          >{{ rule.frontend_url }}</code>
        </div>
        <div class="rule-item__arrow">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <line x1="5" y1="12" x2="19" y2="12"/>
            <polyline points="12 5 19 12 12 19"/>
          </svg>
        </div>
        <div class="rule-item__url-group">
          <span class="rule-item__label">后端</span>
          <code 
            class="rule-item__url"
            @click="copy(rule.backend_url)"
          >{{ rule.backend_url }}</code>
        </div>
      </div>
      
      <div v-if="rule.tags?.length" class="rule-item__tags">
        <span 
          v-for="tag in rule.tags" 
          :key="tag" 
          class="tag"
        >
          {{ tag }}
        </span>
      </div>
    </div>

    <div class="rule-item__actions">
      <button 
        class="btn btn--icon btn--ghost tooltip"
        :class="{ 'text-success': rule.enabled }"
        @click="toggleStatus"
        :title="rule.enabled ? '点击停用' : '点击启用'"
      >
        <svg v-if="rule.enabled" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M18.36 6.64a9 9 0 1 1-12.73 0"/>
          <line x1="12" y1="2" x2="12" y2="12"/>
        </svg>
        <svg v-else width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <rect x="6" y="4" width="4" height="16"/>
          <rect x="14" y="4" width="4" height="16"/>
        </svg>
        <span class="tooltip__content">{{ rule.enabled ? '停用' : '启用' }}</span>
      </button>

      <button 
        class="btn btn--icon btn--ghost tooltip"
        @click="$emit('edit', rule)"
        title="编辑规则"
      >
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
          <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
        </svg>
        <span class="tooltip__content">编辑</span>
      </button>

      <button 
        class="btn btn--icon btn--ghost tooltip text-danger-hover"
        @click="$emit('delete', rule)"
        title="删除规则"
      >
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <polyline points="3 6 5 6 21 6"/>
          <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
        </svg>
        <span class="tooltip__content">删除</span>
      </button>
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

const copy = (text) => {
  navigator.clipboard.writeText(text)
}

const toggleStatus = async () => {
  try {
    await ruleStore.toggleRule(props.rule.id, !props.rule.enabled)
  } catch (err) {
    // Error handled by store
  }
}
</script>

<style scoped>
.rule-item {
  display: flex;
  align-items: center;
  gap: var(--space-4);
  padding: var(--space-4);
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  transition: all var(--duration-fast) var(--ease-default);
}

.rule-item:hover {
  border-color: var(--color-border-strong);
  box-shadow: var(--shadow-sm);
}

.rule-item--disabled {
  opacity: 0.6;
  background: var(--color-bg-subtle);
}

.rule-item__content {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.rule-item__urls {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  flex-wrap: wrap;
}

.rule-item__url-group {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  min-width: 0;
}

.rule-item__label {
  font-size: var(--text-xs);
  font-weight: var(--font-medium);
  color: var(--color-text-muted);
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.rule-item__url {
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  color: var(--color-text-primary);
  background: var(--color-bg-subtle);
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-default);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 280px;
}

.rule-item__url:hover {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
}

.rule-item__arrow {
  color: var(--color-text-muted);
  display: flex;
  align-items: center;
  padding: 0 var(--space-2);
}

.rule-item__tags {
  display: flex;
  gap: var(--space-2);
  flex-wrap: wrap;
}

.rule-item__actions {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  flex-shrink: 0;
}

.text-success {
  color: var(--color-success) !important;
}

.text-danger-hover:hover {
  color: var(--color-danger) !important;
  background: var(--color-danger-50) !important;
}

@media (max-width: 768px) {
  .rule-item {
    flex-direction: column;
    align-items: flex-start;
  }

  .rule-item__actions {
    width: 100%;
    justify-content: flex-end;
    border-top: 1px solid var(--color-border-subtle);
    padding-top: var(--space-3);
    margin-top: var(--space-2);
  }

  .rule-item__urls {
    flex-direction: column;
    align-items: flex-start;
    width: 100%;
  }

  .rule-item__arrow {
    transform: rotate(90deg);
    padding: var(--space-2) 0;
  }

  .rule-item__url {
    max-width: 100%;
    width: 100%;
  }
}
</style>

<template>
  <tr class="rule-row">
    <td class="col-id" data-label="ID">{{ rule.id }}</td>
    <td class="col-url" data-label="前端 URL">
      <div class="url-display">
        <span class="url-text">{{ rule.frontend_url }}</span>
      </div>
    </td>
    <td class="col-url" data-label="后端 URL">
      <div class="url-display">
        <span class="url-text">{{ rule.backend_url }}</span>
      </div>
    </td>
    <td class="col-actions">
      <div class="action-buttons">
        <button @click="handleDelete" class="btn-icon danger" title="删除规则">
          <svg viewBox="0 0 24 24"><path d="M3 6h18m-2 0v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6m3 0V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2"/></svg>
        </button>
      </div>
    </td>
  </tr>
</template>

<script setup>
import { useRuleStore } from '../stores/rules'

const props = defineProps({
  rule: {
    type: Object,
    required: true
  }
})

const ruleStore = useRuleStore()

const handleDelete = async () => {
  if (confirm(`确定要删除规则 ${props.rule.id} 吗？`)) {
    try {
      await ruleStore.removeRule(props.rule.id)
    } catch (err) {
      // 错误已由 store 处理
    }
  }
}
</script>

<style scoped>
.url-text {
  font-family: var(--font-family-mono);
  font-size: 0.9rem;
  word-break: break-all;
}

.action-buttons {
  display: flex;
  gap: 8px;
}

.btn-icon {
  width: 36px;
  height: 36px;
  padding: 0;
  border-radius: var(--radius-md);
  background: var(--color-bg-secondary);
  color: var(--color-text-secondary);
  border: 1px solid var(--color-border);
}

.btn-icon svg {
  width: 18px;
  height: 18px;
  stroke: currentColor;
  stroke-width: 2;
  fill: none;
}

.btn-icon.danger:hover {
  background: var(--color-danger);
  color: white;
  border-color: var(--color-danger);
}

/* Responsive Mobile Card Style */
@media (max-width: 768px) {
  .rule-row {
    display: block;
    background: var(--color-bg-card);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-md);
    margin-bottom: var(--spacing-md);
    padding: var(--spacing-md);
    box-shadow: var(--shadow-sm);
  }

  td {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: var(--spacing-xs) 0 !important;
    border: none !important;
    text-align: right;
  }

  td::before {
    content: attr(data-label);
    font-weight: var(--font-weight-bold);
    color: var(--color-text-muted);
    font-size: 0.8rem;
    text-align: left;
  }

  .url-display {
    max-width: 70%;
  }

  .col-actions {
    margin-top: var(--spacing-sm);
    padding-top: var(--spacing-sm) !important;
    border-top: 1px solid var(--color-border-light) !important;
  }
}
</style>

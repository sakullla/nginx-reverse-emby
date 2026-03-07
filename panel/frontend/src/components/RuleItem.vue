<template>
  <tr>
    <td class="id-cell">{{ rule.id }}</td>
    <td class="url-cell">
      <input
        type="text"
        v-model="frontendUrl"
        :disabled="ruleStore.loading"
        @blur="handleUpdate"
        @keyup.enter="handleUpdate"
        placeholder="前端 URL"
      />
    </td>
    <td class="url-cell">
      <input
        type="text"
        v-model="backendUrl"
        :disabled="ruleStore.loading"
        @blur="handleUpdate"
        @keyup.enter="handleUpdate"
        placeholder="后端 URL"
      />
    </td>
    <td class="action-cell">
      <div class="button-group">
        <button
          @click="handleUpdate"
          class="btn-small btn-save"
          :disabled="!hasChanges || ruleStore.loading"
          title="保存修改"
        >
          💾
        </button>
        <button
          @click="showDeleteModal = true"
          class="btn-small btn-delete"
          :disabled="ruleStore.loading"
          title="删除规则"
        >
          🗑️
        </button>
      </div>
    </td>
  </tr>

  <!-- 删除确认对话框 -->
  <BaseModal
    v-model="showDeleteModal"
    title="确认删除"
    confirm-text="删除"
    confirm-variant="danger"
    :loading="ruleStore.loading"
    @confirm="handleDelete"
  >
    <p>确定要删除规则 <strong>{{ rule.id }}</strong> 吗？</p>
    <div style="margin-top: var(--spacing-md); padding: var(--spacing-md); background: var(--color-bg-secondary); border-radius: var(--radius-md);">
      <p style="margin-bottom: var(--spacing-xs);"><strong>前端:</strong> {{ rule.frontend_url }}</p>
      <p><strong>后端:</strong> {{ rule.backend_url }}</p>
    </div>
    <p style="margin-top: var(--spacing-md); color: var(--color-text-muted); font-size: var(--font-size-sm);">
      此操作无法撤销
    </p>
  </BaseModal>
</template>

<script setup>
import { ref, computed, watch } from 'vue'
import { useRuleStore } from '../stores/rules'
import BaseModal from './base/BaseModal.vue'

const props = defineProps({
  rule: {
    type: Object,
    required: true
  }
})

const ruleStore = useRuleStore()
const frontendUrl = ref(props.rule.frontend_url)
const backendUrl = ref(props.rule.backend_url)
const showDeleteModal = ref(false)

const hasChanges = computed(() => {
  return frontendUrl.value !== props.rule.frontend_url ||
         backendUrl.value !== props.rule.backend_url
})

watch(() => props.rule, (newRule) => {
  frontendUrl.value = newRule.frontend_url
  backendUrl.value = newRule.backend_url
}, { deep: true })

async function handleUpdate() {
  if (!hasChanges.value) return

  try {
    await ruleStore.modifyRule(
      props.rule.id,
      frontendUrl.value.trim(),
      backendUrl.value.trim()
    )
  } catch (err) {
    frontendUrl.value = props.rule.frontend_url
    backendUrl.value = props.rule.backend_url
  }
}

async function handleDelete() {
  try {
    await ruleStore.removeRule(props.rule.id)
    showDeleteModal.value = false
  } catch (err) {
    // Error handled by store
  }
}
</script>

<style scoped>
.id-cell {
  width: 60px;
  text-align: center;
  font-weight: 600;
  color: var(--color-primary);
  font-size: var(--font-size-base);
}

.url-cell {
  padding: var(--spacing-sm) var(--spacing-md) !important;
}

.url-cell input {
  width: 100%;
  padding: var(--spacing-sm) var(--spacing-md);
  border: 2px solid var(--color-border);
  border-radius: var(--radius-md);
  font-size: var(--font-size-sm);
  color: var(--color-text-primary);
  transition: all var(--transition-base);
  background: var(--color-bg-secondary);
  box-sizing: border-box;
  line-height: var(--line-height-normal);
  font-family: var(--font-family-mono);
}

.url-cell input:hover {
  border-color: var(--color-border-dark);
}

.url-cell input:focus {
  background: var(--color-bg-primary);
  border-color: var(--color-primary);
  box-shadow: 0 0 0 4px rgba(99, 102, 241, 0.1);
  outline: none;
}

.url-cell input:disabled {
  background: var(--color-bg-disabled);
  color: var(--color-text-disabled);
  cursor: not-allowed;
  opacity: 0.6;
}

.action-cell {
  width: 140px;
  padding: var(--spacing-sm) var(--spacing-md) !important;
}

.button-group {
  display: flex;
  gap: var(--spacing-sm);
  justify-content: center;
  align-items: center;
}

.btn-small {
  padding: var(--spacing-sm) var(--spacing-md);
  font-size: var(--font-size-sm);
  min-width: 60px;
  border-radius: var(--radius-md);
  line-height: 1;
  font-weight: var(--font-weight-semibold);
  transition: all var(--transition-base);
  border: none;
  cursor: pointer;
  color: white;
}

.btn-save {
  background: var(--gradient-success);
  box-shadow: var(--shadow-success);
}

.btn-save:hover:not(:disabled) {
  transform: translateY(-2px);
  box-shadow: var(--shadow-lg);
}

.btn-save:disabled {
  background: var(--color-border-dark);
  box-shadow: none;
  cursor: not-allowed;
  opacity: 0.5;
}

.btn-delete {
  background: var(--gradient-danger);
  box-shadow: var(--shadow-danger);
}

.btn-delete:hover:not(:disabled) {
  transform: translateY(-2px);
  box-shadow: var(--shadow-lg);
}

.btn-delete:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
</style>

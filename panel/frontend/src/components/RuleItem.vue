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
          @click="handleDelete"
          class="btn-small btn-delete"
          :disabled="ruleStore.loading"
          title="删除规则"
        >
          🗑️
        </button>
      </div>
    </td>
  </tr>
</template>

<script setup>
import { ref, computed, watch } from 'vue'
import { useRuleStore } from '../stores/rules'

const props = defineProps({
  rule: {
    type: Object,
    required: true
  }
})

const ruleStore = useRuleStore()
const frontendUrl = ref(props.rule.frontend_url)
const backendUrl = ref(props.rule.backend_url)

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
  if (!confirm(`确定要删除规则 ${props.rule.id} 吗？\n\n前端: ${props.rule.frontend_url}\n后端: ${props.rule.backend_url}`)) return

  try {
    await ruleStore.removeRule(props.rule.id)
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
  color: #8b5cf6;
  font-size: 0.95rem;
}

.url-cell {
  padding: 0.5rem 1rem !important;
}

.url-cell input {
  width: 100%;
  padding: 0.5rem 0.875rem;
  border: 1.5px solid #e5e7eb;
  border-radius: 6px;
  font-size: 0.875rem;
  color: #111827;
  transition: all 0.2s ease;
  background: #fafafa;
  box-sizing: border-box;
  line-height: 1.5;
}

.url-cell input:focus {
  background: white;
  color: #111827;
  border-color: #8b5cf6;
  box-shadow: 0 0 0 3px rgba(139, 92, 246, 0.08);
  outline: none;
}

.url-cell input:disabled {
  background: #f9fafb;
  color: #6b7280;
  cursor: not-allowed;
}

.action-cell {
  width: 140px;
  padding: 0.5rem 1rem !important;
}

.button-group {
  display: flex;
  gap: 0.5rem;
  justify-content: center;
  align-items: center;
}

.btn-small {
  padding: 0.5rem 1rem;
  font-size: 0.875rem;
  min-width: 60px;
  border-radius: 6px;
  line-height: 1.5;
  font-weight: 600;
  transition: all 0.2s ease;
  border: none;
  cursor: pointer;
  color: white;
}

.btn-save {
  background: linear-gradient(135deg, #10b981 0%, #059669 100%);
  box-shadow: 0 1px 3px rgba(16, 185, 129, 0.3);
}

.btn-save:hover:not(:disabled) {
  transform: translateY(-1px);
  box-shadow: 0 2px 6px rgba(16, 185, 129, 0.4);
}

.btn-save:disabled {
  background: #d1d5db;
  box-shadow: none;
  cursor: not-allowed;
  opacity: 0.6;
}

.btn-delete {
  background: linear-gradient(135deg, #ef4444 0%, #dc2626 100%);
  box-shadow: 0 1px 3px rgba(239, 68, 68, 0.3);
}

.btn-delete:hover:not(:disabled) {
  transform: translateY(-1px);
  box-shadow: 0 2px 6px rgba(239, 68, 68, 0.4);
}

.btn-delete:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}
</style>

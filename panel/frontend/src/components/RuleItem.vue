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
}

.url-cell input {
  width: 100%;
  padding: 0.5rem 0.75rem;
  border: 1.5px solid #e2e8f0;
  border-radius: 6px;
  font-size: 0.85rem;
  color: #1a202c;
  transition: all 0.2s ease;
  background: #fafafa;
  box-sizing: border-box;
}

.url-cell input:focus {
  background: white;
  color: #1a202c;
  border-color: #667eea;
  box-shadow: 0 0 0 2px rgba(102, 126, 234, 0.1);
}

.url-cell input:disabled {
  background: #f7fafc;
  color: #718096;
  cursor: not-allowed;
}

.action-cell {
  width: 120px;
}

.button-group {
  display: flex;
  gap: 0.5rem;
  justify-content: center;
}

.btn-small {
  padding: 0.5rem 0.75rem;
  font-size: 1rem;
  min-width: 44px;
  border-radius: 6px;
  line-height: 1;
}

.btn-save {
  background: linear-gradient(135deg, #48bb78 0%, #38a169 100%);
  box-shadow: 0 2px 6px rgba(72, 187, 120, 0.25);
}

.btn-save:hover:not(:disabled) {
  box-shadow: 0 3px 10px rgba(72, 187, 120, 0.35);
}

.btn-save:disabled {
  background: #e2e8f0;
  box-shadow: none;
}

.btn-delete {
  background: linear-gradient(135deg, #f56565 0%, #e53e3e 100%);
  box-shadow: 0 2px 6px rgba(245, 101, 101, 0.25);
}

.btn-delete:hover:not(:disabled) {
  box-shadow: 0 3px 10px rgba(245, 101, 101, 0.35);
}
</style>

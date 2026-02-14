<template>
  <tr>
    <td><strong>{{ rule.id }}</strong></td>
    <td>
      <input
        v-model="frontendUrl"
        :disabled="ruleStore.loading"
        @blur="handleUpdate"
        @keyup.enter="handleUpdate"
      />
    </td>
    <td>
      <input
        v-model="backendUrl"
        :disabled="ruleStore.loading"
        @blur="handleUpdate"
        @keyup.enter="handleUpdate"
      />
    </td>
    <td>
      <div class="button-group">
        <button
          @click="handleUpdate"
          class="small"
          :disabled="!hasChanges || ruleStore.loading"
        >
          💾 保存
        </button>
        <button
          @click="handleDelete"
          class="small danger"
          :disabled="ruleStore.loading"
        >
          🗑️ 删除
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

<template>
  <div>
    <h2>🎀 新增反向代理规则</h2>
    <form @submit.prevent="handleSubmit">
      <div class="input-group vertical">
        <BaseInput
          v-model="frontendUrl"
          placeholder="✨ 前端 URL (例如: https://example.com)"
          :disabled="ruleStore.loading"
          required
        />
        <BaseInput
          v-model="backendUrl"
          placeholder="🌸 后端 URL (例如: http://backend:8080)"
          :disabled="ruleStore.loading"
          required
        />
      </div>
      <BaseButton
        type="submit"
        :disabled="!isValid"
        :loading="ruleStore.loading"
      >
        <span v-if="ruleStore.loading">添加中...</span>
        <span v-else>💫 添加规则</span>
      </BaseButton>
    </form>
  </div>
</template>

<script setup>
import { ref, computed } from 'vue'
import { useRuleStore } from '../stores/rules'
import BaseInput from './base/BaseInput.vue'
import BaseButton from './base/BaseButton.vue'

const ruleStore = useRuleStore()
const frontendUrl = ref('')
const backendUrl = ref('')

const isValid = computed(() => {
  return frontendUrl.value.trim() && backendUrl.value.trim()
})

async function handleSubmit() {
  if (!isValid.value) return

  try {
    await ruleStore.addRule(frontendUrl.value.trim(), backendUrl.value.trim())
    frontendUrl.value = ''
    backendUrl.value = ''
  } catch (err) {
    // Error handled by store
  }
}
</script>

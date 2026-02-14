<template>
  <div>
    <h2>⚙️ 配置管理</h2>
    <div class="input-group">
      <input
        v-model="apiToken"
        type="password"
        placeholder="🔑 API Token (可选)"
        @blur="saveToken"
      />
      <button @click="handleApply" :disabled="ruleStore.loading" class="secondary">
        <span v-if="ruleStore.loading">⏳ 应用中...</span>
        <span v-else>🚀 应用配置</span>
      </button>
    </div>
    <p v-if="apiToken" class="token-badge">Token 已配置</p>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { useRuleStore } from '../stores/rules'
import { setApiToken } from '../api'

const ruleStore = useRuleStore()
const apiToken = ref('')

onMounted(() => {
  const saved = localStorage.getItem('panel_api_token')
  if (saved) {
    apiToken.value = saved
    setApiToken(saved)
  }
})

function saveToken() {
  const token = apiToken.value.trim()
  if (token) {
    localStorage.setItem('panel_api_token', token)
    setApiToken(token)
    ruleStore.showInfo('Token 已保存')
  } else {
    localStorage.removeItem('panel_api_token')
    setApiToken('')
  }
}

async function handleApply() {
  try {
    await ruleStore.applyNginxConfig()
  } catch (err) {
    // Error handled by store
  }
}
</script>

<template>
  <div>
    <h2>🔑 API Token 配置</h2>
    <div class="input-group">
      <input
        v-model="apiToken"
        type="password"
        placeholder="输入 API Token (可选)"
        @blur="saveToken"
      />
    </div>
    <p v-if="apiToken" class="token-badge">Token 已配置并保存</p>
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
    ruleStore.showInfo('✨ Token 已保存')
  } else {
    localStorage.removeItem('panel_api_token')
    setApiToken('')
    ruleStore.showInfo('Token 已清除')
  }
}
</script>

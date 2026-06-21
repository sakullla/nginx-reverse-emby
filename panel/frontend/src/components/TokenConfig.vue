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
import { setAuthToken } from '../api/authState'

const ruleStore = useRuleStore()
const apiToken = ref('')

onMounted(() => {
  if (typeof localStorage !== 'undefined') {
    localStorage.removeItem('panel_api_token')
  }
})

function saveToken() {
  const token = apiToken.value.trim()
  if (token) {
    setAuthToken(token)
    ruleStore.showInfo('✨ Token 已保存')
  } else {
    setAuthToken('')
    ruleStore.showInfo('Token 已清除')
  }
}
</script>

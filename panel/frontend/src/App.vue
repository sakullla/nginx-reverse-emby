<template>
  <AgentProvider>
    <ThemeProvider>
      <RouterView />
    </ThemeProvider>
  </AgentProvider>
  <!-- 全局消息提醒组件 -->
  <StatusMessage />
</template>

<script setup>
import { watch } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { AgentProvider } from './context/AgentContext.js'
import { ThemeProvider } from './context/ThemeContext.js'
import { useAuthState } from './context/useAuthState.js'
import StatusMessage from './components/StatusMessage.vue'

const router = useRouter()
const route = useRoute()
const { token } = useAuthState()

// If token is cleared (401, logout), redirect to login immediately
watch(token, (val) => {
  if (val === null && route.name !== 'login') {
    router.replace({ name: 'login' })
  }
})
</script>

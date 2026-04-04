<template>
  <AgentProvider>
    <ThemeProvider>
      <RouterView />
    </ThemeProvider>
  </AgentProvider>
</template>

<script setup>
import { watch } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { AgentProvider } from './context/AgentContext.js'
import { ThemeProvider } from './context/ThemeContext.js'
import { useAuthState } from './context/useAuthState.js'

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

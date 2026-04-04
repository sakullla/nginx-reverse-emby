<template>
  <AgentProvider>
    <ThemeProvider>
      <RouterView />
    </ThemeProvider>
  </AgentProvider>
</template>

<script setup>
import { AgentProvider } from './context/AgentContext.js'
import { ThemeProvider } from './context/ThemeContext.js'
</script>

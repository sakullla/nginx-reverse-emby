<template>
  <button class="theme-toggle" data-testid="theme-toggle" @click="cycleTheme" :title="themeTitle">
    <span v-if="currentTheme === 'light'">☀️</span>
    <span v-else-if="currentTheme === 'dark'">🌙</span>
    <span v-else-if="currentTheme === 'anime'">🌸</span>
  </button>
</template>

<script setup>
import { ref, onMounted, computed } from 'vue'

const themes = ['light', 'dark', 'anime']
const currentTheme = ref('light')

const themeTitle = computed(() => {
  const titles = {
    light: '切换到暗色模式',
    dark: '切换到二次元模式',
    anime: '切换到亮色模式'
  }
  return titles[currentTheme.value]
})

const cycleTheme = () => {
  const currentIndex = themes.indexOf(currentTheme.value)
  const nextIndex = (currentIndex + 1) % themes.length
  const nextTheme = themes[nextIndex]

  applyTheme(nextTheme)
}

const applyTheme = (theme) => {
  currentTheme.value = theme
  document.documentElement.setAttribute('data-theme', theme)
  localStorage.setItem('theme', theme)
}

onMounted(() => {
  const savedTheme = localStorage.getItem('theme') || 'light'
  applyTheme(savedTheme)
})
</script>

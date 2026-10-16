<template>
  <svg class="brand-mark" viewBox="0 0 40 40" aria-hidden="true">
    <g class="brand-mark__sakura" fill="currentColor">
      <path
        v-for="angle in [0, 72, 144, 216, 288]"
        :key="angle"
        :transform="`rotate(${angle} 20 20)`"
        d="M20 18C15 15 12 8 16 4L20 7L24 4C28 8 25 15 20 18Z"
      />
      <circle cx="20" cy="20" r="2" />
    </g>
    <g class="brand-mark__fallback" transform="translate(8 8)" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
      <template v-if="fallback === 'lock'">
        <rect x="3" y="11" width="18" height="11" rx="2" />
        <path d="M7 11V7a5 5 0 0 1 10 0v4" />
      </template>
      <polygon v-else points="13 2 3 14 12 14 11 22 21 10 12 10 13 2" />
    </g>
  </svg>
</template>

<script setup>
defineProps({
  fallback: { type: String, default: 'bolt' }
})
</script>

<style scoped>
.brand-mark {
  display: block;
  width: 100%;
  height: 100%;
}

.brand-mark__sakura {
  display: none;
}

[data-theme="sakura-day"] .brand-mark__sakura,
[data-theme="sakura-night"] .brand-mark__sakura {
  display: block;
}

[data-theme="sakura-day"] .brand-mark__fallback,
[data-theme="sakura-night"] .brand-mark__fallback {
  display: none;
}
</style>

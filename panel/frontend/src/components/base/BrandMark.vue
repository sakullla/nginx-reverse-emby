<template>
  <svg class="brand-mark" viewBox="0 0 32 32" fill="none" aria-hidden="true">
    <!-- Sakura themes keep the blossom mark -->
    <g class="brand-mark__sakura" fill="currentColor">
      <path
        v-for="angle in [0, 72, 144, 216, 288]"
        :key="angle"
        :transform="`rotate(${angle} 16 16)`"
        d="M16 14.6C12.4 12.5 10.3 7.4 13.1 4.6L16 6.8L18.9 4.6C21.7 7.4 19.6 12.5 16 14.6Z"
      />
      <circle cx="16" cy="16" r="1.5" />
    </g>
    <!-- Routing glyph: one entry fanning out to upstream nodes -->
    <g class="brand-mark__route">
      <path
        d="M7 16h6.5c2.2 0 3.3-1 4.4-3l1.2-2.2c1-1.8 2.1-2.8 4.4-2.8H25M13.5 16c2.2 0 3.3 1 4.4 3l1.2 2.2c1 1.8 2.1 2.8 4.4 2.8H25M13.5 16H25"
        stroke="currentColor"
        stroke-width="2.4"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
      <circle cx="7" cy="16" r="2.6" fill="currentColor" />
    </g>
  </svg>
</template>

<script setup>
// `fallback` was used by the retired per-theme marks; accepted for compatibility.
defineProps({
  fallback: { type: String, default: '' }
})
</script>

<style scoped>
.brand-mark {
  display: block;
  width: 62%;
  height: 62%;
  margin: auto;
}

.brand-mark__sakura {
  display: none;
}

[data-theme="sakura-day"] .brand-mark,
[data-theme="sakura-night"] .brand-mark {
  width: 74%;
  height: 74%;
}

[data-theme="sakura-day"] .brand-mark__sakura,
[data-theme="sakura-night"] .brand-mark__sakura {
  display: block;
}

[data-theme="sakura-day"] .brand-mark__route,
[data-theme="sakura-night"] .brand-mark__route {
  display: none;
}
</style>

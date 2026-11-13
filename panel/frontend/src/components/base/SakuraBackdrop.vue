<template>
  <!-- Ambient glow behind page chrome; purely decorative. -->
  <div class="ambient-backdrop" aria-hidden="true"></div>
</template>

<style scoped>
.ambient-backdrop {
  position: absolute;
  inset: 0 0 auto 0;
  height: 320px;
  pointer-events: none;
  background: var(--color-bg-atmosphere);
  -webkit-mask-image: linear-gradient(180deg, #000 30%, transparent);
  mask-image: linear-gradient(180deg, #000 30%, transparent);
  z-index: 0;
}
</style>

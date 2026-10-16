<template>
  <svg class="sakura-backdrop" viewBox="0 0 320 180" fill="none" aria-hidden="true">
    <g stroke="currentColor" stroke-width="0.8" stroke-linecap="round">
      <path d="M40 148C104 160 212 122 286 42" />
      <path d="M76 165C151 172 243 137 308 77" opacity="0.4" />
      <path d="M164 124C190 87 234 65 272 64" opacity="0.5" />
    </g>
    <g fill="currentColor">
      <path d="M0 0C-12-3-18-18-10-25L-5-20L0-24C8-14 5-5 0 0Z" transform="translate(238 71) rotate(30)" />
      <path d="M0 0C-12-3-18-18-10-25L-5-20L0-24C8-14 5-5 0 0Z" transform="translate(183 111) rotate(-30) scale(.6)" />
      <path d="M0 0C-12-3-18-18-10-25L-5-20L0-24C8-14 5-5 0 0Z" transform="translate(295 107) rotate(70) scale(.5)" />
      <path d="M0-6L1.5-1.5L6 0L1.5 1.5L0 6L-1.5 1.5L-6 0L-1.5-1.5Z" transform="translate(126 111)" />
      <path d="M0-6L1.5-1.5L6 0L1.5 1.5L0 6L-1.5 1.5L-6 0L-1.5-1.5Z" transform="translate(278 28) scale(.6)" />
      <circle cx="205" cy="44" r="1.5" />
      <circle cx="92" cy="135" r="1" />
    </g>
  </svg>
</template>

<style scoped>
.sakura-backdrop {
  display: var(--sakura-decoration-display, none);
  position: absolute;
  top: var(--space-2);
  right: var(--space-6);
  width: min(30vw, 320px);
  height: 150px;
  color: var(--color-accent);
  opacity: var(--sakura-decoration-opacity, 0);
  pointer-events: none;
}

@media (max-width: 640px) {
  .sakura-backdrop {
    width: 160px;
    height: 100px;
    right: var(--space-2);
    top: 0;
  }
}
</style>

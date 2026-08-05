<template>
  <PkiSection
    title="CA generations"
    description="活动状态优先，仅显示最近 5 个内部信任根。轮转请使用上方「日常 / 紧急 CA 轮转」。"
    eyebrow="信任根"
    aria-label="CA generations"
    collapsible
    storage-key="nre.pki.section.authority"
  >
    <div class="authority-list">
      <article
        v-for="authority in authorities"
        :key="authority.id"
        data-test="authority-row"
        class="authority-card"
      >
        <div class="authority-card__main">
          <div class="authority-card__title">
            <strong>Generation {{ authority.generation }}</strong>
            <PkiStatusBadge :status="authority.status" :label="authorityStatusLabel(authority.status)" />
          </div>
          <span class="mono authority-card__fp" :title="authority.fingerprint_sha256 || ''">
            {{ authority.fingerprint_sha256 || '—' }}
          </span>
        </div>
        <div class="authority-card__meta">
          <span class="authority-card__meta-label">有效期至</span>
          <span>{{ formatDate(authority.not_after) }}</span>
        </div>
      </article>

      <div v-if="!authorities.length" class="pki-empty">
        <div class="pki-empty__icon" aria-hidden="true">∅</div>
        <p>暂无 CA 记录。</p>
      </div>
    </div>
  </PkiSection>
</template>

<script setup>
import PkiSection from './PkiSection.vue'
import PkiStatusBadge from './PkiStatusBadge.vue'

defineProps({
  authorities: { type: Array, default: () => [] },
  authorityStatusLabel: { type: Function, required: true },
  formatDate: { type: Function, required: true },
})
</script>

<style scoped>
.authority-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.authority-card {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-4);
  padding: var(--space-3) var(--space-4);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-xl);
  background: color-mix(in srgb, var(--color-bg-subtle) 35%, var(--color-bg-surface));
}

.authority-card__main {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 0.3rem;
}

.authority-card__title {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  flex-wrap: wrap;
}

.authority-card__title strong {
  color: var(--color-text-primary);
}

.authority-card__fp {
  color: var(--color-text-secondary);
  font-size: var(--text-xs);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: min(560px, 70vw);
}

.authority-card__meta {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 0.15rem;
  color: var(--color-text-secondary);
  font-size: var(--text-xs);
  flex-shrink: 0;
}

.authority-card__meta-label {
  color: var(--color-text-tertiary);
  font-weight: 600;
}

.pki-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  padding: var(--space-8) var(--space-4);
  color: var(--color-text-tertiary);
  text-align: center;
}

.pki-empty__icon {
  width: 2rem;
  height: 2rem;
  border-radius: var(--radius-full);
  display: grid;
  place-items: center;
  background: var(--color-bg-subtle);
  font-weight: 700;
}

.pki-empty p {
  margin: 0;
  font-size: var(--text-sm);
}

.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}

@media (max-width: 680px) {
  .authority-card {
    flex-direction: column;
    align-items: flex-start;
  }

  .authority-card__meta {
    align-items: flex-start;
  }
}
</style>

<template>
  <header class="cert-center">
    <div class="cert-center__top">
      <div class="cert-center__titles">
        <div class="cert-center__eyebrow">证书中心</div>
        <h1 class="cert-center__title">{{ title }}</h1>
        <p v-if="subtitle" class="cert-center__subtitle">{{ subtitle }}</p>
      </div>
      <div v-if="$slots.actions" class="cert-center__actions">
        <slot name="actions" />
      </div>
    </div>

    <div class="cert-center__nav">
      <BaseTabs
        class="cert-center__tabs"
        :tabs="tabs"
        :model-value="activeDomain"
        @update:model-value="onDomainChange"
      />
      <p class="cert-center__hint" role="note">
        <template v-if="activeDomain === 'public'">
          当前为公网业务证书域 · 网站 ACME / 手动上传。内部 relay mTLS 请切换到「内部 PKI」。
        </template>
        <template v-else>
          当前为内部 PKI 域 · relay 身份 / CA / 撤销 / 备份。公网 ACME 请切换到「公网证书」。
        </template>
      </p>
    </div>
  </header>
</template>

<script setup>
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import BaseTabs from '../base/BaseTabs.vue'

const props = defineProps({
  domain: {
    type: String,
    required: true,
    validator: (value) => ['public', 'internal'].includes(value),
  },
  title: {
    type: String,
    required: true,
  },
  subtitle: {
    type: String,
    default: '',
  },
})

const router = useRouter()

const tabs = [
  { id: 'public', label: '公网证书' },
  { id: 'internal', label: '内部 PKI' },
]

const activeDomain = computed(() => props.domain)

function onDomainChange(next) {
  if (next === props.domain) return
  router.push(next === 'internal' ? '/pki' : '/certs')
}
</script>

<style scoped>
.cert-center {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  margin-bottom: var(--space-3);
}

.cert-center__top {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: var(--space-4);
  flex-wrap: wrap;
}

.cert-center__titles {
  min-width: 0;
  flex: 1;
}

.cert-center__eyebrow {
  color: var(--color-primary);
  font-size: var(--text-xs);
  font-weight: 700;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  margin-bottom: 0.15rem;
}

.cert-center__title {
  margin: 0 0 0.15rem;
  font-size: 1.3125rem;
  font-weight: 700;
  letter-spacing: -0.02em;
  line-height: 1.25;
  color: var(--color-text-primary);
}

.cert-center__subtitle {
  margin: 0;
  font-size: 0.75rem;
  line-height: 1.35;
  color: var(--color-text-tertiary);
  font-variant-numeric: tabular-nums;
}

.cert-center__actions {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  flex-shrink: 0;
  flex-wrap: wrap;
}

.cert-center__nav {
  display: flex;
  flex-direction: column;
  gap: 0.45rem;
}

.cert-center__tabs {
  margin: 0;
}

.cert-center__hint {
  margin: 0;
  padding: 0 0.15rem;
  color: var(--color-text-tertiary);
  font-size: 0.72rem;
  line-height: 1.45;
  font-weight: 400;
}

@media (max-width: 640px) {
  .cert-center {
    gap: var(--space-3);
    margin-bottom: var(--space-3);
  }

  .cert-center__top {
    flex-direction: column;
    align-items: stretch;
    gap: var(--space-3);
  }

  .cert-center__title {
    font-size: 1.2rem;
  }

  .cert-center__actions {
    width: 100%;
  }

  .cert-center__actions :deep(.btn),
  .cert-center__actions .btn {
    flex: 1 1 auto;
    min-height: 2.4rem;
    justify-content: center;
  }

  .cert-center__hint {
    font-size: 0.7rem;
  }
}
</style>

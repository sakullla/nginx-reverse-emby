<template>
  <BaseListCard
    :status="profile.enabled === false ? 'neutral' : 'success'"
    :disabled="profile.enabled === false"
    :clickable="true"
    @click="navigateToClients"
  >
    <template #header-left>
      <BaseBadge tone="neutral" subtone="secondary" mono>#{{ profile.id }}</BaseBadge>
      <BaseBadge :tone="profile.enabled === false ? 'neutral' : 'success'" dot>
        {{ profile.enabled === false ? '停用' : '启用' }}
      </BaseBadge>
    </template>

    <template #header-right>
      <BaseIconButton
        :tone="profile.enabled === false ? 'success' : 'warning'"
        :title="profile.enabled === false ? '启用' : '停用'"
        @click.stop="$emit('toggle', profile)"
      >
        <svg v-if="profile.enabled === false" width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
          <polygon points="5 3 19 12 5 21 5 3"/>
        </svg>
        <svg v-else width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
          <rect x="6" y="4" width="4" height="16" rx="1"/>
          <rect x="14" y="4" width="4" height="16" rx="1"/>
        </svg>
      </BaseIconButton>
      <BaseIconButton title="编辑" @click.stop="$emit('edit', profile)">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
          <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
        </svg>
      </BaseIconButton>
      <BaseIconButton title="管理客户端" @click.stop="navigateToClients">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/>
          <circle cx="9" cy="7" r="4"/>
          <path d="M23 21v-2a4 4 0 0 0-3-3.87"/>
          <path d="M16 3.13a4 4 0 0 1 0 7.75"/>
        </svg>
      </BaseIconButton>
      <BaseIconButton tone="danger" title="删除" @click.stop="$emit('delete', profile)">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <polyline points="3 6 5 6 21 6"/>
          <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
        </svg>
      </BaseIconButton>
    </template>

    <div class="profile-card__body">
      <h3 class="profile-card__name">{{ profile.name || `Profile ${profile.id}` }}</h3>

      <div v-if="profile.public_endpoint" class="profile-card__endpoint">
        <svg class="endpoint-icon" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <circle cx="12" cy="12" r="10"/>
          <line x1="2" y1="12" x2="22" y2="12"/>
          <path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/>
        </svg>
        <span class="endpoint-value">{{ profile.public_endpoint }}</span>
      </div>

      <div class="profile-card__info-bar">
        <span v-if="hasAddresses" class="info-item" :title="formatList(profile.addresses)">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
            <rect x="2" y="2" width="20" height="8" rx="2"/>
            <rect x="2" y="14" width="20" height="8" rx="2"/>
          </svg>
          {{ formatList(profile.addresses) }}
        </span>
        <span v-if="profile.listen_port" class="info-item">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
            <path d="M5 12h14M12 5l7 7-7 7"/>
          </svg>
          {{ profile.listen_port }}
        </span>
        <span class="info-item">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
            <path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/>
            <circle cx="9" cy="7" r="4"/>
            <path d="M23 21v-2a4 4 0 0 0-3-3.87"/>
          </svg>
          {{ clientCount }} 客户端
        </span>
        <span v-if="profile.mtu" class="info-item">
          MTU {{ profile.mtu }}
        </span>
      </div>
    </div>

    <template v-if="hasTags" #footer>
      <BaseBadge v-for="tag in profile.tags" :key="tag" tone="primary" size="sm">{{ tag }}</BaseBadge>
    </template>
  </BaseListCard>
</template>

<script setup>
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import BaseListCard from '../base/BaseListCard.vue'
import BaseBadge from '../base/BaseBadge.vue'
import BaseIconButton from '../base/BaseIconButton.vue'

const props = defineProps({
  profile: { type: Object, required: true },
  clientCount: { type: Number, default: 0 }
})

const emit = defineEmits(['toggle', 'edit', 'delete'])

const router = useRouter()

const hasTags = computed(() => Array.isArray(props.profile.tags) && props.profile.tags.length > 0)
const hasAddresses = computed(() => Array.isArray(props.profile.addresses) && props.profile.addresses.length > 0)

function formatList(items) {
  return Array.isArray(items) ? items.join(', ') : ''
}

function navigateToClients() {
  router.push(`/wireguard-profiles/${props.profile.id}`)
}
</script>

<style scoped>
.profile-card__body {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.profile-card__name {
  margin: 0;
  font-size: var(--text-base);
  font-weight: var(--font-semibold);
  color: var(--color-text-primary);
  word-break: break-all;
}

.profile-card__endpoint {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-md);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  color: var(--color-text-primary);
}

.endpoint-icon {
  color: var(--color-primary);
  flex-shrink: 0;
}

.endpoint-value {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}

.profile-card__info-bar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-1) var(--space-3);
}

.info-item {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: var(--text-xs);
  color: var(--color-text-secondary);
  white-space: nowrap;
}

.info-item svg {
  flex-shrink: 0;
  opacity: 0.7;
}
</style>

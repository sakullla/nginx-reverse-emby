<template>
  <BaseListCard
    :status="profile.enabled === false ? 'neutral' : 'success'"
    :disabled="profile.enabled === false"
    :clickable="true"
    @click="$emit('manageClients', profile)"
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
        @click="$emit('toggle', profile)"
      >
        <svg v-if="profile.enabled === false" width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
          <polygon points="5 3 19 12 5 21 5 3"/>
        </svg>
        <svg v-else width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
          <rect x="6" y="4" width="4" height="16" rx="1"/>
          <rect x="14" y="4" width="4" height="16" rx="1"/>
        </svg>
      </BaseIconButton>
      <BaseIconButton title="管理客户端" @click="$emit('manageClients', profile)">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/>
          <circle cx="9" cy="7" r="4"/>
          <path d="M23 21v-2a4 4 0 0 0-3-3.87"/>
          <path d="M16 3.13a4 4 0 0 1 0 7.75"/>
        </svg>
      </BaseIconButton>
      <BaseIconButton title="编辑" @click="$emit('edit', profile)">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
          <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
        </svg>
      </BaseIconButton>
      <BaseIconButton tone="danger" title="删除" @click="$emit('delete', profile)">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <polyline points="3 6 5 6 21 6"/>
          <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
        </svg>
      </BaseIconButton>
    </template>

    <div class="profile-card__body">
      <h3 class="profile-card__name">{{ profile.name || `Profile ${profile.id}` }}</h3>
      <div class="profile-card__meta">
        <div class="meta-item">
          <span class="meta-label">端口</span>
          <span class="meta-value">{{ profile.listen_port || '-' }}</span>
        </div>
        <div class="meta-item">
          <span class="meta-label">地址</span>
          <span class="meta-value">{{ formatList(profile.addresses) || '-' }}</span>
        </div>
        <div class="meta-item">
          <span class="meta-label">客户端</span>
          <span class="meta-value">{{ clientCount }} 个</span>
        </div>
        <div class="meta-item">
          <span class="meta-label">Endpoint</span>
          <span class="meta-value">{{ profile.public_endpoint || '-' }}</span>
        </div>
        <div class="meta-item">
          <span class="meta-label">MTU</span>
          <span class="meta-value">{{ profile.mtu || '-' }}</span>
        </div>
      </div>
    </div>

    <template v-if="hasTags" #footer>
      <BaseBadge v-for="tag in profile.tags" :key="tag" tone="primary">{{ tag }}</BaseBadge>
    </template>
  </BaseListCard>
</template>

<script setup>
import { computed } from 'vue'
import BaseListCard from '../base/BaseListCard.vue'
import BaseBadge from '../base/BaseBadge.vue'
import BaseIconButton from '../base/BaseIconButton.vue'

const props = defineProps({
  profile: { type: Object, required: true },
  clientCount: { type: Number, default: 0 }
})

defineEmits(['manageClients', 'edit', 'delete', 'toggle'])

const hasTags = computed(() => Array.isArray(props.profile.tags) && props.profile.tags.length > 0)

function formatList(items) {
  return Array.isArray(items) ? items.join(', ') : ''
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

.profile-card__meta {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: var(--space-2);
}

.meta-item {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.meta-label {
  font-size: var(--text-xs);
  color: var(--color-text-muted);
}

.meta-value {
  font-size: var(--text-sm);
  color: var(--color-text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

@media (max-width: 640px) {
  .profile-card__meta {
    grid-template-columns: 1fr;
  }
}
</style>

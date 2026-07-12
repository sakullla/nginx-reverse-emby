<template>
  <BaseListCard
    :status="statusTone"
    :disabled="profile.enabled === false"
    :clickable="true"
    :title="titleText"
    @click="navigateToClients"
  >
    <template #header-left>
      <BaseBadge tone="neutral" subtone="secondary" mono>#{{ profile.id }}</BaseBadge>
      <BaseBadge :tone="statusTone" dot>
        {{ statusLabel }}
      </BaseBadge>
      <AgentBadge :item="profile" />
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
      <BaseIconButton title="管理客户端" @click.stop="navigateToClients">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/>
          <circle cx="9" cy="7" r="4"/>
          <path d="M23 21v-2a4 4 0 0 0-3-3.87"/>
          <path d="M16 3.13a4 4 0 0 1 0 7.75"/>
        </svg>
      </BaseIconButton>
      <BaseActionMenu :items="moreItems" @select="onMoreSelect" />
    </template>

    <div class="profile-card__body">
      <div v-if="profile.public_endpoint" class="profile-card__endpoint">
        <svg class="endpoint-icon" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <circle cx="12" cy="12" r="10"/>
          <line x1="2" y1="12" x2="22" y2="12"/>
          <path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/>
        </svg>
        <span class="endpoint-value">{{ profile.public_endpoint }}</span>
      </div>

      <div class="profile-card__info-bar">
        <span v-if="hasInterfaceAddresses" class="info-item" :title="formatList(profile.interface_addresses)">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
            <path d="M12 2v20M2 12h20"/>
          </svg>
          {{ formatList(profile.interface_addresses) }}
        </span>
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
import { useRoute, useRouter } from 'vue-router'
import BaseListCard from '../base/BaseListCard.vue'
import BaseBadge from '../base/BaseBadge.vue'
import BaseActionMenu from '../base/BaseActionMenu.vue'
import AgentBadge from '../common/AgentBadge.vue'
import BaseIconButton from '../base/BaseIconButton.vue'
import { enabledStatusLabel, enabledStatusTone } from '../../utils/resourceCardStatus.js'

const props = defineProps({
  profile: { type: Object, required: true },
  clientCount: { type: Number, default: 0 }
})

const emit = defineEmits(['toggle', 'edit', 'delete'])

const route = useRoute()
const router = useRouter()

const isEnabled = computed(() => props.profile.enabled !== false)
const statusTone = computed(() => enabledStatusTone(isEnabled.value))
const statusLabel = computed(() => enabledStatusLabel(isEnabled.value))
const titleText = computed(() => props.profile.name || `Profile ${props.profile.id}`)

const hasTags = computed(() => Array.isArray(props.profile.tags) && props.profile.tags.length > 0)
const hasAddresses = computed(() => Array.isArray(props.profile.addresses) && props.profile.addresses.length > 0)
const hasInterfaceAddresses = computed(() => Array.isArray(props.profile.interface_addresses) && props.profile.interface_addresses.length > 0)

const moreItems = computed(() => [
  { id: 'edit', label: '编辑' },
  { id: 'delete', label: '删除', tone: 'danger' },
])

function formatList(items) {
  return Array.isArray(items) ? items.join(', ') : ''
}

function navigateToClients() {
  const agentId = typeof route.query.agentId === 'string' ? route.query.agentId : ''
  const target = {
    path: `/wireguard-profiles/${props.profile.id}`
  }
  if (agentId) {
    target.query = { agentId }
  }
  router.push(target)
}

function onMoreSelect(item) {
  if (item.id === 'edit') emit('edit', props.profile)
  else if (item.id === 'delete') emit('delete', props.profile)
}
</script>

<style scoped>
.profile-card__body {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
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

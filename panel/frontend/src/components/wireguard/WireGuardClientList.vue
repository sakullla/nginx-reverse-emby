<template>
  <div class="client-list">
    <div v-if="!clients.length" class="empty-inline">暂无 Client</div>
    <div
      v-for="client in clients"
      :key="client.id"
      class="client-row"
      :class="{ 'client-row--pending': isPending(client) }"
    >
      <div class="client-row__info">
        <div class="client-row__name">
          <strong>{{ client.name || `client-${client.id}` }}</strong>
          <BaseBadge
            :tone="client.enabled === false ? 'neutral' : 'success'"
            size="sm"
            dot
          >
            {{ client.enabled === false ? '停用' : '启用' }}
          </BaseBadge>
        </div>
        <div class="client-row__meta">
          <span>{{ client.address || '-' }}</span>
          <span class="client-row__pubkey" :title="client.public_key">{{ client.public_key || '-' }}</span>
        </div>
      </div>

      <div class="client-row__actions">
        <BaseIconButton title="编辑" :disabled="isPending(client)" @click="$emit('edit', client)">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
            <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
          </svg>
        </BaseIconButton>
        <BaseIconButton
          :tone="client.enabled === false ? 'success' : 'warning'"
          :title="client.enabled === false ? '启用' : '停用'"
          :disabled="isPending(client)"
          @click="$emit('toggle', client)"
        >
          <svg v-if="client.enabled === false" width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
            <polygon points="5 3 19 12 5 21 5 3"/>
          </svg>
          <svg v-else width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
            <rect x="6" y="4" width="4" height="16" rx="1"/>
            <rect x="14" y="4" width="4" height="16" rx="1"/>
          </svg>
        </BaseIconButton>
        <BaseIconButton title="下载配置" :disabled="isPending(client)" @click="$emit('download', client)">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/>
            <polyline points="7 10 12 15 17 10"/>
            <line x1="12" y1="15" x2="12" y2="3"/>
          </svg>
        </BaseIconButton>
        <BaseIconButton title="二维码" :disabled="isPending(client)" @click="$emit('qr', client)">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <rect x="3" y="3" width="7" height="7"/>
            <rect x="14" y="3" width="7" height="7"/>
            <rect x="14" y="14" width="7" height="7"/>
            <rect x="3" y="14" width="7" height="7"/>
          </svg>
        </BaseIconButton>
        <BaseIconButton title="复制 URI" :disabled="isPending(client)" @click="$emit('copyURI', client)">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>
            <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>
          </svg>
        </BaseIconButton>
        <BaseIconButton tone="danger" title="删除" :disabled="isPending(client)" @click="$emit('delete', client)">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <polyline points="3 6 5 6 21 6"/>
            <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
          </svg>
        </BaseIconButton>
      </div>
    </div>
  </div>
</template>

<script setup>
import BaseBadge from '../base/BaseBadge.vue'
import BaseIconButton from '../base/BaseIconButton.vue'

const props = defineProps({
  clients: { type: Array, required: true },
  profileId: { type: [String, Number], default: null },
  pendingClientIds: { type: Object, default: () => new Set() }
})

defineEmits(['edit', 'toggle', 'download', 'qr', 'copyURI', 'delete'])

function clientRowKey(profileID, clientOrID) {
  return `${String(profileID)}:${String(typeof clientOrID === 'object' ? clientOrID?.id : clientOrID)}`
}

function isPending(client) {
  return props.pendingClientIds.has(clientRowKey(props.profileId, client))
}
</script>

<style scoped>
.client-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.client-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-3);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-lg);
  background: var(--color-bg-surface);
  transition: opacity 150ms ease;
}

.client-row--pending {
  opacity: 0.6;
}

.client-row__info {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  min-width: 0;
  overflow: hidden;
}

.client-row__name {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-width: 0;
}

.client-row__name strong {
  font-size: var(--text-sm);
  color: var(--color-text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.client-row__meta {
  display: grid;
  grid-template-columns: minmax(80px, 0.4fr) minmax(0, 1fr);
  gap: var(--space-3);
  font-size: var(--text-xs);
  color: var(--color-text-muted);
  min-width: 0;
}

.client-row__pubkey {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: var(--font-mono);
}

.client-row__actions {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  flex-shrink: 0;
}

.empty-inline {
  padding: var(--space-3);
  border: 1px dashed var(--color-border-default);
  border-radius: var(--radius-md);
  color: var(--color-text-muted);
  font-size: var(--text-sm);
  text-align: center;
}

@media (max-width: 720px) {
  .client-row {
    grid-template-columns: 1fr;
    align-items: stretch;
  }

  .client-row__actions {
    justify-content: flex-start;
    flex-wrap: wrap;
  }

  .client-row__meta {
    grid-template-columns: 1fr;
  }
}
</style>

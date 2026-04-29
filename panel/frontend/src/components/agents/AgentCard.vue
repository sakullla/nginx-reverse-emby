<template>
  <BaseListCard :status="statusTone" :title="agent.name" @click="$emit('click')">
    <template #header-left>
      <AgentStatusBadge :agent="agent" />
      <BaseBadge tone="primary">{{ modeLabel }}</BaseBadge>
    </template>
    <template #header-right>
      <BaseIconButton title="重命名" @click="$emit('rename')">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
          <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
        </svg>
      </BaseIconButton>
      <BaseIconButton v-if="!agent.is_local" tone="danger" title="删除" @click="$emit('delete')">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <polyline points="3 6 5 6 21 6"/>
          <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
        </svg>
      </BaseIconButton>
    </template>

    <div class="agent-card__url">{{ displayUrl }}</div>
    <div class="agent-card__stats">
      <span class="agent-card__stat">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>
        </svg>
        HTTP {{ agent.http_rules_count || 0 }}
      </span>
      <span class="agent-card__stat">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <rect x="2" y="2" width="20" height="8" rx="2"/>
          <rect x="2" y="14" width="20" height="8" rx="2"/>
        </svg>
        L4 {{ agent.l4_rules_count || 0 }}
      </span>
      <span class="agent-card__last-seen">{{ timeAgo(agent.last_seen_at) }}</span>
    </div>

    <template v-if="hasTags" #footer>
      <BaseBadge v-for="tag in agent.tags" :key="tag" tone="primary">{{ tag }}</BaseBadge>
    </template>
  </BaseListCard>
</template>

<script setup>
import { computed } from 'vue'
import AgentStatusBadge from './AgentStatusBadge.vue'
import BaseListCard from '../base/BaseListCard.vue'
import BaseBadge from '../base/BaseBadge.vue'
import BaseIconButton from '../base/BaseIconButton.vue'
import { getAgentStatus, getModeLabel, getHostname, timeAgo } from '../../utils/agentHelpers.js'

const props = defineProps({
  agent: { type: Object, required: true }
})

defineEmits(['click', 'rename', 'delete'])

const STATUS_TONE = {
  online: 'success',
  offline: 'neutral',
  failed: 'danger',
  pending: 'warning',
}

const statusTone = computed(() => STATUS_TONE[getAgentStatus(props.agent)] || 'neutral')
const modeLabel = computed(() => getModeLabel(props.agent.mode))
const displayUrl = computed(() => props.agent.agent_url ? getHostname(props.agent.agent_url) : (props.agent.last_seen_ip || '—'))
const hasTags = computed(() => Array.isArray(props.agent.tags) && props.agent.tags.length > 0)
</script>

<style scoped>
.agent-card__url {
  font-size: 0.8125rem;
  color: var(--color-text-tertiary);
  font-family: var(--font-mono);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.agent-card__stats {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  margin-top: 0.25rem;
}
.agent-card__stat {
  display: flex;
  align-items: center;
  gap: 0.25rem;
  font-size: 0.75rem;
  color: var(--color-text-tertiary);
}
.agent-card__last-seen {
  font-size: 0.75rem;
  color: var(--color-text-muted);
  margin-left: auto;
}
</style>

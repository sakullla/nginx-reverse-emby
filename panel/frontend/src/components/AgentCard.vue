<template>
  <div class="agent-card" @click="$emit('click')">
    <div class="agent-card__header">
      <div class="agent-card__badges">
        <AgentStatusBadge :agent="agent" />
        <span class="agent-card__mode-badge">{{ modeLabel }}</span>
      </div>
      <div class="agent-card__actions" @click.stop>
        <button class="agent-card__action" title="重命名" @click="$emit('rename')">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
            <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
          </svg>
        </button>
        <button v-if="!agent.is_local" class="agent-card__action agent-card__action--delete" title="删除" @click="$emit('delete')">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <polyline points="3 6 5 6 21 6"/>
            <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
          </svg>
        </button>
      </div>
    </div>
    <div class="agent-card__name">{{ agent.name }}</div>
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
    <div v-if="hasTags" class="agent-card__tags">
      <span v-for="tag in agent.tags" :key="tag" class="agent-card__tag">{{ tag }}</span>
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import AgentStatusBadge from './AgentStatusBadge.vue'
import { getModeLabel, getHostname, timeAgo } from '../utils/agentHelpers.js'

const props = defineProps({
  agent: { type: Object, required: true }
})

defineEmits(['click', 'rename', 'delete'])

const modeLabel = computed(() => getModeLabel(props.agent.mode))
const displayUrl = computed(() => props.agent.agent_url ? getHostname(props.agent.agent_url) : (props.agent.last_seen_ip || '—'))
const hasTags = computed(() => Array.isArray(props.agent.tags) && props.agent.tags.length > 0)
</script>

<style scoped>
.agent-card {
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  padding: 1.125rem 1.25rem;
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  cursor: pointer;
  transition: border-color 0.15s, transform 0.1s;
}
.agent-card:hover {
  border-color: var(--color-primary);
  transform: translateY(-1px);
}
.agent-card__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 0.125rem;
}
.agent-card__badges {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}
.agent-card__mode-badge {
  font-size: 0.75rem;
  padding: 1px 6px;
  background: var(--color-primary-subtle);
  color: var(--color-primary);
  border-radius: var(--radius-full);
  font-weight: 500;
}
.agent-card__name {
  font-size: 1rem;
  font-weight: 600;
  color: var(--color-text-primary);
}
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
.agent-card__actions {
  display: flex;
  gap: 0.25rem;
}
.agent-card__action {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: var(--radius-md);
  border: none;
  background: transparent;
  color: var(--color-text-tertiary);
  cursor: pointer;
  transition: all 0.15s;
}
.agent-card__action:hover {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}
.agent-card__action--delete:hover {
  background: var(--color-danger-50);
  color: var(--color-danger);
}
.agent-card__tags {
  display: flex;
  gap: 0.375rem;
  flex-wrap: wrap;
  margin-top: 0.25rem;
}
.agent-card__tag {
  font-size: 0.7rem;
  padding: 2px 8px;
  background: var(--color-primary-subtle);
  color: var(--color-primary);
  border-radius: var(--radius-full);
  font-weight: 500;
}
</style>

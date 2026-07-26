<template>
  <div class="agent-table-wrap">
    <table class="agent-table">
      <thead>
        <tr>
          <th>节点</th>
          <th>状态</th>
          <th>模式</th>
          <th>HTTP</th>
          <th>L4</th>
          <th>最后活跃</th>
          <th v-if="showActions">操作</th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="agent in agents"
          :key="agent.id"
          class="agent-table__row"
          :class="{ 'agent-table__row--clickable': clickable }"
          @click="handleRowClick(agent)"
        >
          <td>
            <div class="agent-cell">
              <span class="agent-cell__name">{{ agent.name }}</span>
              <span class="agent-cell__url">{{ getAgentEndpointLabel(agent) }}</span>
            </div>
          </td>
          <td><AgentStatusBadge :agent="agent" /></td>
          <td>
            <BaseBadge tone="primary" size="sm">{{ getModeLabel(agent.mode) }}</BaseBadge>
          </td>
          <td>{{ agent.http_rules_count || 0 }}</td>
          <td>{{ agent.l4_rules_count || 0 }}</td>
          <td>{{ timeAgo(agent.last_seen_at) }}</td>
          <td v-if="showActions" @click.stop>
            <div class="agent-table__actions">
              <button class="agent-table__action" title="重命名" @click="$emit('rename', agent)">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
                  <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
                </svg>
              </button>
              <button v-if="!agent.is_local" class="agent-table__action agent-table__action--delete" title="删除" @click="$emit('delete', agent)">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <polyline points="3 6 5 6 21 6"/>
                  <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
                </svg>
              </button>
            </div>
          </td>
        </tr>
      </tbody>
    </table>

    <!-- Narrow-screen form: stacked cards keep mode / rule counts / last-seen reachable
         instead of hiding table columns. -->
    <div class="agent-cards">
      <div
        v-for="agent in agents"
        :key="agent.id"
        class="agent-cards__item"
        :class="{ 'agent-cards__item--clickable': clickable }"
        @click="handleRowClick(agent)"
      >
        <div class="agent-cards__top">
          <AgentStatusBadge :agent="agent" />
          <span class="agent-cards__name">{{ agent.name }}</span>
          <div v-if="showActions" class="agent-table__actions" @click.stop>
            <button class="agent-table__action" title="重命名" @click="$emit('rename', agent)">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
                <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
              </svg>
            </button>
            <button v-if="!agent.is_local" class="agent-table__action agent-table__action--delete" title="删除" @click="$emit('delete', agent)">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <polyline points="3 6 5 6 21 6"/>
                <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
              </svg>
            </button>
          </div>
        </div>
        <div class="agent-cards__url">{{ getAgentEndpointLabel(agent) }}</div>
        <div class="agent-cards__meta">
          <BaseBadge tone="primary" size="sm">{{ getModeLabel(agent.mode) }}</BaseBadge>
          <span class="agent-cards__stat">HTTP {{ agent.http_rules_count || 0 }}</span>
          <span class="agent-cards__stat">L4 {{ agent.l4_rules_count || 0 }}</span>
          <span class="agent-cards__time">{{ timeAgo(agent.last_seen_at) }}</span>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import AgentStatusBadge from './AgentStatusBadge.vue'
import BaseBadge from './base/BaseBadge.vue'
import { getModeLabel, getAgentEndpointLabel, timeAgo } from '../utils/agentHelpers.js'

const props = defineProps({
  agents: { type: Array, default: () => [] },
  showActions: { type: Boolean, default: true },
  clickable: { type: Boolean, default: false }
})

const emit = defineEmits(['click', 'rename', 'delete'])

function handleRowClick(agent) {
  if (props.clickable) {
    emit('click', agent)
  }
}
</script>

<style scoped>
.agent-table-wrap {
  overflow-x: auto;
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-2xl);
  box-shadow: var(--shadow-xs);
}
.agent-table {
  width: 100%;
  border-collapse: collapse;
}
.agent-table th {
  text-align: left;
  padding: var(--space-4) var(--space-5);
  font-size: var(--text-xs);
  font-weight: var(--font-semibold);
  letter-spacing: 0.03em;
  color: var(--color-text-muted);
  border-bottom: 1px solid var(--color-border-subtle);
  white-space: nowrap;
}
.agent-table td {
  padding: var(--space-4) var(--space-5);
  border-bottom: 1px solid var(--color-border-subtle);
  font-size: var(--text-sm);
  vertical-align: middle;
}
.agent-table tr:last-child td {
  border-bottom: none;
}
.agent-table__row {
  transition: background-color var(--duration-fast) var(--ease-default);
}
.agent-table__row--clickable {
  cursor: pointer;
}
.agent-table__row--clickable:hover {
  background: var(--color-bg-hover);
}
.agent-cell__name {
  display: block;
  font-weight: var(--font-medium);
  color: var(--color-text-primary);
}
.agent-cell__url {
  display: block;
  margin-top: 0.125rem;
  font-size: var(--text-xs);
  color: var(--color-text-tertiary);
  font-family: var(--font-mono);
}
.agent-table__actions {
  display: flex;
  gap: 0.25rem;
}
.agent-table__action {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 30px;
  height: 30px;
  border-radius: var(--radius-full);
  border: none;
  background: transparent;
  color: var(--color-text-tertiary);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-default);
}
.agent-table__action:hover {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}
.agent-table__action--delete:hover {
  background: var(--color-danger-50);
  color: var(--color-danger);
}

/* Stacked cards are the phone-only form of the list view. */
.agent-cards {
  display: none;
}

@media (max-width: 640px) {
  .agent-table {
    display: none;
  }
  .agent-table-wrap {
    background: transparent;
    border: none;
    border-radius: 0;
    box-shadow: none;
    overflow: visible;
  }
  .agent-cards {
    display: flex;
    flex-direction: column;
    gap: var(--space-2-5);
  }
  .agent-cards__item {
    display: flex;
    flex-direction: column;
    gap: var(--space-1-5);
    padding: var(--space-3) var(--space-4);
    background: var(--color-bg-surface);
    border: 1px solid var(--color-border-subtle);
    border-radius: var(--radius-xl);
    box-shadow: var(--shadow-xs);
    transition: background-color var(--duration-fast) var(--ease-default),
      transform var(--duration-fast) var(--ease-default);
  }
  .agent-cards__item--clickable {
    cursor: pointer;
  }
  .agent-cards__item--clickable:active {
    background: var(--color-bg-hover);
    transform: scale(0.99);
  }
  .agent-cards__top {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    min-width: 0;
  }
  .agent-cards__name {
    flex: 1;
    min-width: 0;
    font-weight: var(--font-medium);
    color: var(--color-text-primary);
    font-size: var(--text-sm);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .agent-cards__top .agent-table__actions {
    flex-shrink: 0;
  }
  .agent-cards__url {
    font-size: var(--text-xs);
    color: var(--color-text-tertiary);
    font-family: var(--font-mono);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .agent-cards__meta {
    display: flex;
    align-items: center;
    gap: var(--space-2-5);
    min-width: 0;
  }
  .agent-cards__stat {
    font-size: var(--text-xs);
    color: var(--color-text-secondary);
    white-space: nowrap;
  }
  .agent-cards__time {
    margin-left: auto;
    font-size: var(--text-xs);
    color: var(--color-text-muted);
    white-space: nowrap;
  }
}
</style>

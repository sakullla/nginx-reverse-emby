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
              <span class="agent-cell__url">{{ agent.agent_url ? getHostname(agent.agent_url) : (agent.last_seen_ip || '—') }}</span>
            </div>
          </td>
          <td><AgentStatusBadge :agent="agent" /></td>
          <td>
            <span class="mode-badge">{{ getModeLabel(agent.mode) }}</span>
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
  </div>
</template>

<script setup>
import AgentStatusBadge from './AgentStatusBadge.vue'
import { getModeLabel, getHostname, timeAgo } from '../utils/agentHelpers.js'

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
}
.agent-table {
  width: 100%;
  border-collapse: collapse;
}
.agent-table th {
  text-align: left;
  padding: 0.6rem 1rem;
  font-size: 0.7rem;
  font-weight: 600;
  color: var(--color-text-tertiary);
  border-bottom: 1px solid var(--color-border-subtle);
  white-space: nowrap;
}
.agent-table td {
  padding: 0.75rem 1rem;
  border-bottom: 1px solid var(--color-border-subtle);
  font-size: 0.875rem;
  vertical-align: middle;
}
.agent-table tr:last-child td {
  border-bottom: none;
}
.agent-table__row--clickable {
  cursor: pointer;
}
.agent-table__row--clickable:hover {
  background: var(--color-bg-hover);
}
.agent-cell__name {
  display: block;
  font-weight: 500;
  color: var(--color-text-primary);
}
.agent-cell__url {
  display: block;
  font-size: 0.75rem;
  color: var(--color-text-tertiary);
  font-family: var(--font-mono);
}
.mode-badge {
  font-size: 0.75rem;
  padding: 1px 6px;
  background: var(--color-primary-subtle);
  color: var(--color-primary);
  border-radius: var(--radius-full);
  font-weight: 500;
}
.agent-table__actions {
  display: flex;
  gap: 0.25rem;
}
.agent-table__action {
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
.agent-table__action:hover {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}
.agent-table__action--delete:hover {
  background: var(--color-danger-50);
  color: var(--color-danger);
}
</style>

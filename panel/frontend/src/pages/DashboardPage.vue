<template>
  <div class="dashboard">
    <div class="dashboard__header">
      <h1 class="dashboard__title">集群概览</h1>
      <p class="dashboard__subtitle">实时监控所有节点状态</p>
    </div>

    <div class="stats-grid">
      <div class="stat-card">
        <div class="stat-card__icon stat-card__icon--agents">
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <ellipse cx="12" cy="5" rx="9" ry="3"/>
            <path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3"/>
            <path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5"/>
          </svg>
        </div>
        <div class="stat-card__data">
          <div class="stat-card__value">{{ agents?.length || 0 }}</div>
          <div class="stat-card__label">总节点数</div>
        </div>
      </div>

      <div class="stat-card">
        <div class="stat-card__icon stat-card__icon--online">
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M22 11.08V12a10 10 0 1 1-5.93-9.14"/>
            <polyline points="22 4 12 14.01 9 11.01"/>
          </svg>
        </div>
        <div class="stat-card__data">
          <div class="stat-card__value">{{ onlineCount }}</div>
          <div class="stat-card__label">在线节点</div>
        </div>
      </div>

      <div class="stat-card">
        <div class="stat-card__icon stat-card__icon--rules">
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>
            <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>
          </svg>
        </div>
        <div class="stat-card__data">
          <div class="stat-card__value">{{ rulesCount }}</div>
          <div class="stat-card__label">HTTP 规则</div>
        </div>
      </div>

      <div class="stat-card">
        <div class="stat-card__icon stat-card__icon--l4">
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <rect x="2" y="2" width="20" height="8" rx="2" ry="2"/>
            <rect x="2" y="14" width="20" height="8" rx="2" ry="2"/>
          </svg>
        </div>
        <div class="stat-card__data">
          <div class="stat-card__value">{{ l4Count }}</div>
          <div class="stat-card__label">L4 规则</div>
        </div>
      </div>
    </div>

    <div v-if="agents?.length" class="dashboard-section">
      <div class="dashboard-section__header">
        <h2 class="dashboard-section__title">节点状态</h2>
        <RouterLink to="/agents" class="dashboard-section__link">查看全部 →</RouterLink>
      </div>
      <div class="dashboard-table-wrap">
        <table class="dashboard-table">
          <thead>
            <tr>
              <th>节点</th>
              <th>状态</th>
              <th>HTTP 规则</th>
              <th>L4 规则</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="agent in displayedAgents" :key="agent.id">
              <td>
                <div class="agent-cell">
                  <span class="agent-cell__name">{{ agent.name }}</span>
                  <span class="agent-cell__url">{{ agent.agent_url ? getHostname(agent.agent_url) : (agent.last_seen_ip || '—') }}</span>
                </div>
              </td>
              <td>
                <span class="status-dot" :class="`status-dot--${getStatus(agent)}`"></span>
              </td>
              <td>
                <span class="tag">{{ agent.http_rules_count || 0 }}</span>
              </td>
              <td>
                <span class="tag">{{ agent.l4_rules_count || 0 }}</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- Loading state -->
    <div v-if="isLoading" class="dashboard__loading">
      <div class="spinner"></div>
      <span>加载中...</span>
    </div>

    <!-- Empty state -->
    <div v-else-if="!agents?.length" class="dashboard__empty">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <ellipse cx="12" cy="5" rx="9" ry="3"/>
        <path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3"/>
        <path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5"/>
      </svg>
      <p>暂无节点</p>
      <p class="dashboard__empty-hint">点击顶部导航栏「加入节点」来添加第一个 Agent</p>
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { useAgents } from '../hooks/useAgents'
import { useAgent } from '../context/AgentContext'

const { data: agents, isLoading } = useAgents()
const { selectedAgentId } = useAgent()

const onlineCount = computed(() => agents.value?.filter(a => a.status === 'online').length || 0)

const rulesCount = computed(() => {
  return agents.value?.reduce((sum, a) => sum + (a.http_rules_count || 0), 0) || 0
})

const l4Count = computed(() => {
  return agents.value?.reduce((sum, a) => sum + (a.l4_rules_count || 0), 0) || 0
})

const displayedAgents = computed(() => (agents.value || []).slice(0, 8))

function getHostname(url) {
  try { return url ? new URL(url).hostname : '' } catch { return '' }
}

function getStatus(agent) {
  if (agent.status === 'offline') return 'offline'
  if (agent.last_apply_status === 'failed') return 'failed'
  if (agent.desired_revision > agent.current_revision) return 'pending'
  return 'online'
}
</script>

<style scoped>
.dashboard {
  max-width: 1200px;
  margin: 0 auto;
}
.dashboard__header {
  margin-bottom: 2.5rem;
}
.dashboard__title {
  font-size: 1.5rem;
  font-weight: 700;
  color: var(--color-text-primary);
  margin: 0 0 0.25rem;
}
.dashboard__subtitle {
  font-size: 0.875rem;
  color: var(--color-text-tertiary);
  margin: 0;
}
.stats-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
  gap: 1rem;
  margin-bottom: 2.5rem;
}
.stat-card {
  display: flex;
  align-items: center;
  gap: 1rem;
  padding: 1.25rem;
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-xl);
}
.stat-card__icon {
  width: 48px;
  height: 48px;
  border-radius: var(--radius-lg);
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}
.stat-card__icon--agents {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
}
.stat-card__icon--online {
  background: var(--color-success-50);
  color: var(--color-success);
}
.stat-card__icon--rules {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
}
.stat-card__icon--l4 {
  background: var(--color-warning-50);
  color: var(--color-warning);
}
.stat-card__data {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.stat-card__value {
  font-size: 1.75rem;
  font-weight: 700;
  color: var(--color-text-primary);
  line-height: 1;
}
.stat-card__label {
  font-size: 0.875rem;
  color: var(--color-text-tertiary);
}
.dashboard__loading {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 0.75rem;
  padding: 3rem;
  color: var(--color-text-secondary);
}
.spinner {
  width: 24px;
  height: 24px;
  border: 2px solid var(--color-border-default);
  border-top-color: var(--color-primary);
  border-radius: 50%;
  animation: spin 1s linear infinite;
}
@keyframes spin { to { transform: rotate(360deg); } }
.dashboard__empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 0.75rem;
  padding: 4rem 2rem;
  color: var(--color-text-muted);
  text-align: center;
}
.dashboard__empty p {
  margin: 0;
  font-size: 1rem;
}
.dashboard__empty-hint {
  font-size: 0.875rem !important;
  color: var(--color-text-tertiary) !important;
}
.dashboard-section { background: var(--color-bg-surface); border: 1.5px solid var(--color-border-default); border-radius: var(--radius-2xl); overflow: hidden; margin-bottom: 2.5rem; }
.dashboard-section__header { display: flex; align-items: center; justify-content: space-between; padding: 1rem 1.25rem; border-bottom: 1px solid var(--color-border-subtle); }
.dashboard-section__title { font-size: 0.875rem; font-weight: 600; color: var(--color-text-primary); margin: 0; }
.dashboard-section__link { font-size: 0.8rem; color: var(--color-primary); text-decoration: none; }
.dashboard-table-wrap { overflow-x: auto; }
.dashboard-table { width: 100%; border-collapse: collapse; }
.dashboard-table th { text-align: left; padding: 0.6rem 1rem; font-size: 0.7rem; font-weight: 600; color: var(--color-text-tertiary); border-bottom: 1px solid var(--color-border-subtle); }
.dashboard-table td { padding: 0.75rem 1rem; border-bottom: 1px solid var(--color-border-subtle); }
.dashboard-table tr:last-child td { border-bottom: none; }
.agent-cell__name { display: block; font-size: 0.875rem; font-weight: 500; color: var(--color-text-primary); }
.agent-cell__url { display: block; font-size: 0.75rem; color: var(--color-text-tertiary); font-family: var(--font-mono); }
.status-dot { display: inline-block; width: 8px; height: 8px; border-radius: 50%; }
.status-dot--online { background: var(--color-success); }
.status-dot--offline { background: var(--color-text-muted); }
.status-dot--failed { background: var(--color-danger); }
.status-dot--pending { background: var(--color-warning); }
.tag { display: inline-block; font-size: 0.75rem; padding: 2px 6px; background: var(--color-primary-subtle); color: var(--color-primary); border-radius: var(--radius-full); font-weight: 500; }
</style>

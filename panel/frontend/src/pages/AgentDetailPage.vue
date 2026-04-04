<template>
  <div class="agent-detail" v-if="agent">
    <div class="agent-detail__back">
      <RouterLink to="/agents" class="back-link">← 返回节点管理</RouterLink>
    </div>

    <div class="agent-detail__header">
      <div>
        <h1 class="agent-detail__name">{{ agent.name }}</h1>
        <p class="agent-detail__url">{{ agent.agent_url || agent.last_seen_ip || '—' }}</p>
      </div>
      <div class="agent-detail__status" :class="`agent-detail__status--${getStatus(agent)}`">
        {{ getStatusLabel(agent) }}
      </div>
    </div>

    <div class="agent-detail__stats">
      <div class="stat-mini">
        <span class="stat-mini__value">{{ httpRulesCount }}</span>
        <span class="stat-mini__label">HTTP 规则</span>
      </div>
      <div class="stat-mini">
        <span class="stat-mini__value">{{ l4RulesCount }}</span>
        <span class="stat-mini__label">L4 规则</span>
      </div>
      <div class="stat-mini">
        <span class="stat-mini__value">{{ agent.last_seen_at ? timeAgo(agent.last_seen_at) : '—' }}</span>
        <span class="stat-mini__label">最后活跃</span>
      </div>
    </div>

    <div class="agent-detail__tabs">
      <button v-for="tab in tabs" :key="tab.id" class="tab-btn" :class="{ active: activeTab === tab.id }" @click="activeTab = tab.id">{{ tab.label }}</button>
    </div>

    <div class="agent-detail__tab-content">
      <div v-if="activeTab === 'http'" class="tab-panel">
        <div class="tab-panel__header">
          <button class="btn btn-primary" @click="router.push({ path: '/rules', query: { agentId } })">查看全部规则</button>
        </div>
        <div class="rules-preview">
          <div v-for="rule in httpRules.slice(0, 5)" :key="rule.id" class="rule-preview-item">
            <span class="rule-preview-item__url">{{ rule.frontend_url }}</span>
            <span class="rule-preview-item__backend">→ {{ rule.backend_url }}</span>
          </div>
          <p v-if="!httpRules.length" class="empty-hint">暂无 HTTP 规则</p>
        </div>
      </div>

      <div v-if="activeTab === 'l4'" class="tab-panel">
        <div class="tab-panel__header">
          <button class="btn btn-primary" @click="router.push({ path: '/l4', query: { agentId } })">查看全部规则</button>
        </div>
        <div class="rules-preview">
          <div v-for="rule in l4Rules.slice(0, 5)" :key="rule.id" class="rule-preview-item">
            <span class="rule-preview-item__url">{{ rule.listen_host }}:{{ rule.listen_port }}</span>
            <span class="rule-preview-item__backend">→ {{ rule.upstream_host }}:{{ rule.upstream_port }}</span>
          </div>
          <p v-if="!l4Rules.length" class="empty-hint">暂无 L4 规则</p>
        </div>
      </div>

      <div v-if="activeTab === 'info'" class="tab-panel">
        <div class="info-grid">
          <div class="info-row"><span>版本</span><span>{{ agent.version || '—' }}</span></div>
          <div class="info-row"><span>角色</span><span>{{ getModeLabel(agent.mode) }}</span></div>
          <div class="info-row"><span>IP</span><span>{{ agent.last_seen_ip || '—' }}</span></div>
          <div class="info-row"><span>最后活跃</span><span>{{ agent.last_seen_at ? new Date(agent.last_seen_at).toLocaleString() : '—' }}</span></div>
          <div class="info-row"><span>同步状态</span><span>{{ agent.last_apply_status || '—' }}</span></div>
        </div>
      </div>
    </div>
  </div>
  <div v-else-if="isLoading" class="agent-detail__loading">
    <div class="spinner"></div>
  </div>
  <div v-else class="agent-detail__not-found">
    <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
      <circle cx="12" cy="12" r="10"/>
      <line x1="12" y1="8" x2="12" y2="12"/>
      <line x1="12" y1="16" x2="12.01" y2="16"/>
    </svg>
    <p>节点不存在或已删除</p>
    <RouterLink to="/agents" class="btn btn-secondary">返回节点管理</RouterLink>
  </div>
</template>

<script setup>
import { ref, computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useRules } from '../hooks/useRules'
import { useL4Rules } from '../hooks/useL4Rules'
import { useAgents } from '../hooks/useAgents'

const route = useRoute()
const router = useRouter()
const agentId = computed(() => route.params.id)

const { data: agentsData, isLoading } = useAgents()
const agent = computed(() => agentsData.value?.find(a => a.id === agentId.value))

const { data: httpRulesData } = useRules(agentId)
const httpRules = computed(() => httpRulesData.value ?? [])
const httpRulesCount = computed(() => httpRules.value.length)

const { data: l4RulesData } = useL4Rules(agentId)
const l4Rules = computed(() => l4RulesData.value ?? [])
const l4RulesCount = computed(() => l4Rules.value.length)

const activeTab = ref('http')
const tabs = [
  { id: 'http', label: 'HTTP 规则' },
  { id: 'l4', label: 'L4 规则' },
  { id: 'info', label: '系统信息' }
]

function getStatus(agent) {
  if (agent.status === 'offline') return 'offline'
  if (agent.last_apply_status === 'failed') return 'failed'
  if (agent.desired_revision > agent.current_revision) return 'pending'
  return 'online'
}

function getStatusLabel(agent) {
  const map = { online: '在线', offline: '离线', failed: '失败', pending: '同步中' }
  return map[getStatus(agent)] || '—'
}

function getModeLabel(mode) {
  return { local: '本机', master: '主控' }[mode] || '拉取'
}

function timeAgo(date) {
  const seconds = Math.floor((Date.now() - new Date(date)) / 1000)
  if (seconds < 60) return '刚刚'
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes} 分钟前`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours} 小时前`
  return `${Math.floor(hours / 24)} 天前`
}
</script>

<style scoped>
.agent-detail { max-width: 900px; margin: 0 auto; }
.agent-detail__back { margin-bottom: 1.5rem; }
.back-link { color: var(--color-text-secondary); font-size: 0.875rem; text-decoration: none; }
.back-link:hover { color: var(--color-primary); }
.agent-detail__header { display: flex; align-items: flex-start; justify-content: space-between; margin-bottom: 1.5rem; }
.agent-detail__name { font-size: 1.5rem; font-weight: 700; margin: 0 0 0.25rem; color: var(--color-text-primary); }
.agent-detail__url { font-size: 0.875rem; color: var(--color-text-tertiary); font-family: var(--font-mono); margin: 0; }
.agent-detail__status { font-size: 0.8rem; font-weight: 600; padding: 0.25rem 0.75rem; border-radius: var(--radius-full); }
.agent-detail__status--online { background: var(--color-success-50); color: var(--color-success); }
.agent-detail__status--offline { background: var(--color-bg-subtle); color: var(--color-text-muted); }
.agent-detail__status--failed { background: var(--color-danger-50); color: var(--color-danger); }
.agent-detail__status--pending { background: var(--color-warning-50); color: var(--color-warning); }
.agent-detail__stats { display: flex; gap: 1rem; margin-bottom: 1.5rem; }
.stat-mini { flex: 1; background: var(--color-bg-surface); border: 1.5px solid var(--color-border-default); border-radius: var(--radius-xl); padding: 1rem; text-align: center; }
.stat-mini__value { display: block; font-size: 1.5rem; font-weight: 700; color: var(--color-text-primary); }
.stat-mini__label { font-size: 0.75rem; color: var(--color-text-tertiary); }
.agent-detail__tabs { display: flex; gap: 0.25rem; border-bottom: 1.5px solid var(--color-border-default); margin-bottom: 1.5rem; }
.tab-btn { padding: 0.5rem 1rem; border: none; background: transparent; color: var(--color-text-secondary); font-size: 0.875rem; cursor: pointer; border-bottom: 2px solid transparent; margin-bottom: -1.5px; transition: all 0.15s; font-family: inherit; }
.tab-btn.active { color: var(--color-primary); border-bottom-color: var(--color-primary); font-weight: 500; }
.tab-panel__header { display: flex; justify-content: flex-end; margin-bottom: 1rem; }
.rules-preview { display: flex; flex-direction: column; gap: 0.5rem; }
.rule-preview-item { display: flex; gap: 0.75rem; padding: 0.75rem 1rem; background: var(--color-bg-surface); border: 1px solid var(--color-border-subtle); border-radius: var(--radius-lg); font-size: 0.8125rem; }
.rule-preview-item__url { flex: 1; color: var(--color-text-primary); font-family: var(--font-mono); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.rule-preview-item__backend { color: var(--color-text-tertiary); font-family: var(--font-mono); }
.empty-hint { text-align: center; color: var(--color-text-muted); padding: 2rem; font-size: 0.875rem; }
.info-grid { display: flex; flex-direction: column; gap: 0.5rem; }
.info-row { display: flex; justify-content: space-between; padding: 0.75rem 1rem; background: var(--color-bg-surface); border-radius: var(--radius-lg); font-size: 0.875rem; }
.info-row span:first-child { color: var(--color-text-secondary); }
.info-row span:last-child { color: var(--color-text-primary); font-weight: 500; }
.agent-detail__loading { display: flex; justify-content: center; padding: 3rem; }
.agent-detail__not-found { display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 1rem; padding: 4rem 2rem; color: var(--color-text-muted); text-align: center; }
.agent-detail__not-found p { margin: 0; font-size: 1rem; }
.spinner { width: 24px; height: 24px; border: 2px solid var(--color-border-default); border-top-color: var(--color-primary); border-radius: 50%; animation: spin 1s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
.btn { padding: 0.5rem 1rem; border-radius: var(--radius-lg); font-size: 0.875rem; font-weight: 500; cursor: pointer; transition: all 0.15s; border: none; font-family: inherit; display: inline-flex; align-items: center; gap: 0.375rem; }
.btn-primary { background: var(--gradient-primary); color: white; }
</style>

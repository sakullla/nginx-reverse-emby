<template>
  <div class="agents-page">
    <div class="agents-page__header">
      <div>
        <h1 class="agents-page__title">节点管理</h1>
        <p class="agents-page__subtitle">{{ agents.length }} 个节点 · {{ onlineCount }} 在线</p>
      </div>
      <button class="btn btn-primary" @click="showJoinModal = true">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
          <line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/>
        </svg>
        加入节点
      </button>
    </div>

    <!-- Agent List -->
    <div class="agents-list">
      <div v-for="agent in agents" :key="agent.id" class="agent-card">
        <div class="agent-card__status" :class="`agent-card__status--${getStatus(agent)}`"></div>
        <div class="agent-card__info">
          <div class="agent-card__name">{{ agent.name }}</div>
          <div class="agent-card__meta">
            <span class="agent-card__mode-badge">{{ getModeLabel(agent.mode) }}</span>
            <span class="agent-card__url">{{ agent.agent_url ? getHostname(agent.agent_url) : (agent.last_seen_ip || '—') }}</span>
          </div>
        </div>
        <div class="agent-card__actions">
          <button v-if="!agent.is_local" class="btn btn-danger btn-sm" @click="startDelete(agent)">删除</button>
        </div>
      </div>

      <div v-if="!agents.length && !isLoading" class="agents-page__empty">
        <p>暂无节点</p>
      </div>
    </div>

    <!-- Loading -->
    <div v-if="isLoading" class="agents-page__loading">
      <div class="spinner"></div>
    </div>

    <!-- Join Modal -->
    <Teleport to="body">
      <div v-if="showJoinModal" class="modal-overlay" @click.self="showJoinModal = false">
        <div class="modal modal--lg">
          <div class="modal__header">
            <span>加入 Agent 节点</span>
            <button class="modal__close" @click="showJoinModal = false">✕</button>
          </div>
          <div class="modal__body">
            <!-- Platform tabs -->
            <div class="join-tabs">
              <button v-for="p in platforms" :key="p.id" class="join-tab" :class="{ active: selectedPlatform === p.id }" @click="selectedPlatform = p.id">{{ p.label }}</button>
            </div>
            <!-- Command block -->
            <div class="join-command">
              <code>{{ getCurrentCommand() }}</code>
              <button class="btn btn-primary btn-sm" @click="copyCommand">复制</button>
            </div>
            <ol class="join-steps">
              <li v-for="step in getCurrentSteps()" :key="step">{{ step }}</li>
            </ol>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- Delete Modal -->
    <Teleport to="body">
      <div v-if="deletingAgent" class="modal-overlay" @click.self="deletingAgent = null">
        <div class="modal">
          <div class="modal__header">确认删除</div>
          <div class="modal__body">
            <p>确定删除节点 <strong>{{ deletingAgent.name }}</strong>？此操作无法撤销。</p>
          </div>
          <div class="modal__footer">
            <button class="btn btn-secondary" @click="deletingAgent = null">取消</button>
            <button class="btn btn-danger" @click="confirmDelete">删除</button>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<script setup>
import { ref, computed } from 'vue'
import { useAgents } from '../hooks/useAgents'

const { data, isLoading, refetch: loadAgents } = useAgents()
const agents = computed(() => data.value ?? [])

const showJoinModal = ref(false)
const selectedPlatform = ref('linux')
const deletingAgent = ref(null)

const platforms = [
  { id: 'linux', label: 'Linux', command: 'curl -fsSL https://your-domain.com/panel-api/public/join-agent.sh | bash -s -- --register-token YOUR_TOKEN --install-systemd', steps: ['在目标主机上以 root 执行', '脚本自动安装 Node.js、Nginx、light-agent', '注册 systemd 开机自启服务', '节点自动出现在列表'] },
  { id: 'macos', label: 'macOS', command: 'curl -fsSL https://your-domain.com/panel-api/public/join-agent.sh | bash -s -- --register-token YOUR_TOKEN --install-launchd', steps: ['以非 root 用户执行（避免 Homebrew 权限问题）', '脚本安装 Homebrew、Node.js、Nginx', '注册 launchd 开机自启'] },
  { id: 'windows', label: 'Windows', command: '请使用 WSL2 环境安装', steps: ['需要 WSL2 环境', '在 PowerShell 中执行 WSL 命令安装'] }
]

const onlineCount = computed(() => agents.value.filter(a => a.status === 'online').length)

function getStatus(agent) {
  if (agent.status === 'offline') return 'offline'
  if (agent.last_apply_status === 'failed') return 'failed'
  if (agent.desired_revision > agent.current_revision) return 'pending'
  return 'online'
}

function getHostname(url) {
  try { return url ? new URL(url).hostname : '' } catch { return '' }
}

function getModeLabel(mode) {
  if (mode === 'local') return '本机'
  if (mode === 'master') return '主控'
  return '拉取'
}

function getCurrentCommand() {
  return platforms.find(p => p.id === selectedPlatform.value)?.command || ''
}

function getCurrentSteps() {
  return platforms.find(p => p.id === selectedPlatform.value)?.steps || []
}

async function copyCommand() {
  await navigator.clipboard.writeText(getCurrentCommand())
}

function startDelete(agent) {
  deletingAgent.value = agent
}

function confirmDelete() {
  // delete logic here
  deletingAgent.value = null
}
</script>

<style scoped>
.agents-page {
  max-width: 800px;
  margin: 0 auto;
}
.agents-page__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  margin-bottom: 1.5rem;
  gap: 1rem;
}
.agents-page__title {
  font-size: 1.5rem;
  font-weight: 700;
  margin: 0 0 0.25rem;
  color: var(--color-text-primary);
}
.agents-page__subtitle {
  font-size: 0.875rem;
  color: var(--color-text-tertiary);
  margin: 0;
}
.agents-list {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}
.agent-card {
  display: flex;
  align-items: center;
  gap: 1rem;
  padding: 1rem 1.25rem;
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-xl);
}
.agent-card__status {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  flex-shrink: 0;
}
.agent-card__status--online { background: var(--color-primary); }
.agent-card__status--pending { background: var(--color-warning); }
.agent-card__status--failed { background: var(--color-danger); }
.agent-card__status--offline { background: var(--color-text-muted); }
.agent-card__info { flex: 1; min-width: 0; }
.agent-card__name { font-size: 0.9375rem; font-weight: 600; color: var(--color-text-primary); }
.agent-card__meta { display: flex; align-items: center; gap: 0.5rem; margin-top: 2px; }
.agent-card__mode-badge { font-size: 0.75rem; padding: 1px 6px; background: var(--color-primary-subtle); color: var(--color-primary); border-radius: var(--radius-full); font-weight: 500; }
.agent-card__url { font-size: 0.75rem; color: var(--color-text-tertiary); font-family: var(--font-mono); }
.agent-card__actions { display: flex; gap: 0.5rem; }
/* Modals */
.modal-overlay { position: fixed; inset: 0; background: rgba(0,0,0,0.5); backdrop-filter: blur(4px); z-index: var(--z-modal); display: flex; align-items: center; justify-content: center; }
.modal { background: var(--color-bg-surface); border: 1.5px solid var(--color-border-default); border-radius: var(--radius-2xl); box-shadow: var(--shadow-xl); width: min(500px, 90vw); overflow: hidden; }
.modal--lg { width: min(640px, 92vw); }
.modal__header { padding: 1rem 1.25rem; font-weight: 600; font-size: 1rem; border-bottom: 1px solid var(--color-border-subtle); display: flex; justify-content: space-between; align-items: center; }
.modal__close { background: none; border: none; font-size: 1rem; cursor: pointer; color: var(--color-text-muted); }
.modal__body { padding: 1.25rem; display: flex; flex-direction: column; gap: 1rem; }
.modal__footer { padding: 1rem 1.25rem; display: flex; justify-content: flex-end; gap: 0.75rem; border-top: 1px solid var(--color-border-subtle); }
/* Join modal */
.join-tabs { display: flex; gap: 0.5rem; }
.join-tab { flex: 1; padding: 0.5rem; border: none; border-radius: var(--radius-lg); background: var(--color-bg-subtle); color: var(--color-text-secondary); font-size: 0.875rem; cursor: pointer; transition: all 0.15s; font-family: inherit; }
.join-tab.active { background: var(--color-primary); color: white; }
.join-command { display: flex; align-items: center; gap: 0.75rem; padding: 0.75rem 1rem; background: var(--color-bg-subtle); border-radius: var(--radius-lg); font-family: var(--font-mono); font-size: 0.8125rem; }
.join-command code { flex: 1; overflow-x: auto; color: var(--color-text-primary); }
.join-steps { list-style: none; padding: 0; margin: 0; display: flex; flex-direction: column; gap: 0.5rem; }
.join-steps li { font-size: 0.875rem; color: var(--color-text-secondary); padding-left: 1.25rem; position: relative; }
.join-steps li::before { content: counter(step) "."; counter-increment: step; position: absolute; left: 0; color: var(--color-primary); font-weight: 600; }
.agents-page__loading, .agents-page__empty { display: flex; align-items: center; justify-content: center; padding: 3rem; color: var(--color-text-muted); }
.btn { padding: 0.5rem 1rem; border-radius: var(--radius-lg); font-size: 0.875rem; font-weight: 500; cursor: pointer; transition: all 0.15s; border: none; font-family: inherit; display: inline-flex; align-items: center; gap: 0.375rem; }
.btn-primary { background: var(--gradient-primary); color: white; }
.btn-secondary { background: var(--color-bg-subtle); color: var(--color-text-primary); border: 1px solid var(--color-border-default); }
.btn-danger { background: var(--color-danger); color: white; }
.btn-sm { padding: 0.25rem 0.75rem; font-size: 0.8125rem; }
.spinner { width: 24px; height: 24px; border: 2px solid var(--color-border-default); border-top-color: var(--color-primary); border-radius: 50%; animation: spin 1s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
</style>

<template>
  <section class="settings-section">
    <div class="settings-section__header">
      <h2 class="settings-section__title">系统信息</h2>
      <p class="settings-section__desc">角色、本地 Agent、节点与运行状态</p>
    </div>
    <div class="settings-section__body">
      <div v-if="isLoading" class="settings-placeholder">加载中…</div>
      <div v-else-if="!info" class="settings-placeholder">系统信息暂不可用。</div>
      <template v-else>
        <div class="info-row">
          <span class="info-label"><span class="info-icon">🖥️</span> 角色</span>
          <span class="info-value">{{ info.role || '—' }}</span>
        </div>
        <div class="info-row">
          <span class="info-label"><span class="info-icon">⚡</span> 本地 Agent</span>
          <span class="info-value" :class="info.local_agent_enabled ? 'status-ok' : ''">
            <span v-if="info.local_agent_enabled" class="status-dot"></span>
            {{ info.local_agent_enabled ? '已启用' : '未启用' }}
          </span>
        </div>
        <div class="info-row">
          <span class="info-label"><span class="info-icon">🌐</span> 在线节点</span>
          <span class="info-value">{{ info.online_agents ?? '—' }} 在线 / {{ info.total_agents ?? '—' }} 总计</span>
        </div>
        <div class="info-row">
          <span class="info-label"><span class="info-icon">⏱️</span> 运行时长</span>
          <span class="info-value">{{ formatUptime(info.started_at) }}</span>
        </div>
        <div class="info-row">
          <span class="info-label"><span class="info-icon">📁</span> 数据目录</span>
          <span class="info-value info-value--mono">{{ info.data_dir || '—' }}</span>
        </div>
        <div class="info-row">
          <span class="info-label"><span class="info-icon">🏗️</span> 部署架构</span>
          <span class="info-value">{{ info.local_apply_runtime || '—' }}</span>
        </div>
      </template>
    </div>
  </section>
</template>

<script setup>
import { useSystemInfo } from '../../hooks/useSystemInfo'

const { data: info, isLoading } = useSystemInfo()

function formatUptime(startedAt) {
  if (!startedAt) return '—'
  const start = new Date(startedAt)
  if (Number.isNaN(start.getTime())) return '—'
  const diff = Date.now() - start.getTime()
  if (diff < 0) return '—'
  const seconds = Math.floor(diff / 1000)
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  if (days > 0) return `${days} 天 ${hours} 小时`
  if (hours > 0) return `${hours} 小时 ${minutes} 分钟`
  return `${minutes} 分钟`
}
</script>

<style scoped>
.info-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-2-5) 0;
  border-bottom: 1px solid var(--color-border-subtle);
}
.info-row:last-child { border-bottom: none; }
.info-label {
  font-size: var(--text-sm);
  color: var(--color-text-secondary);
  display: flex;
  align-items: center;
}
.info-value {
  font-size: var(--text-sm);
  color: var(--color-text-primary);
  font-weight: var(--font-medium);
}
.info-value--mono {
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  word-break: break-all;
}
.info-icon {
  margin-right: var(--space-1);
  font-size: var(--text-sm);
}
.status-ok {
  color: var(--color-success);
  display: flex;
  align-items: center;
}
.status-dot {
  display: inline-block;
  width: 7px;
  height: 7px;
  border-radius: var(--radius-full);
  background: var(--color-success);
  margin-right: var(--space-1-5);
  animation: system-info-pulse 2s ease-in-out infinite;
}
@keyframes system-info-pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.5; }
}
.settings-placeholder {
  margin: 0;
  padding: var(--space-4);
  color: var(--color-text-tertiary);
  font-size: var(--text-sm);
  text-align: center;
  border: 1px dashed var(--color-border-default);
  border-radius: var(--radius-md);
}
</style>

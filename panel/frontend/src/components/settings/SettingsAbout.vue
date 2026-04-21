<template>
  <div class="settings-about">
    <div class="about-identity">
      <h2 class="about-identity__name">Nginx Reverse Emby</h2>
      <p class="about-identity__tagline">Nginx 反向代理 &amp; Emby 媒体管理控制面板</p>
    </div>

    <section class="settings-section">
      <div class="settings-section__header">
        <h3 class="settings-section__title">版本信息</h3>
      </div>
      <div class="settings-section__body">
        <div class="info-row">
          <span class="info-label">当前版本</span>
          <span class="info-value">{{ info?.app_version || 'dev' }}</span>
        </div>
        <div class="info-row">
          <span class="info-label">构建时间</span>
          <span class="info-value">{{ info?.build_time || '—' }}</span>
        </div>
        <div class="info-row">
          <span class="info-label">架构</span>
          <span class="info-value">{{ info?.local_apply_runtime || '—' }}</span>
        </div>
        <div class="info-row">
          <span class="info-label">Go 版本</span>
          <span class="info-value">{{ info?.go_version || '—' }}</span>
        </div>
      </div>
    </section>

    <section v-if="info?.project_url" class="settings-section">
      <div class="settings-section__header">
        <h3 class="settings-section__title">项目地址</h3>
      </div>
      <div class="settings-section__body">
        <div class="info-row">
          <span class="info-label">GitHub</span>
          <a :href="info.project_url" target="_blank" rel="noopener" class="info-link">{{ info.project_url }} ↗</a>
        </div>
        <div class="info-row">
          <span class="info-label">问题反馈</span>
          <a :href="info.project_url + '/issues'" target="_blank" rel="noopener" class="info-link">Issues ↗</a>
        </div>
      </div>
    </section>

    <section class="settings-section">
      <div class="settings-section__header">
        <h3 class="settings-section__title">系统状态</h3>
      </div>
      <div class="settings-section__body">
        <div class="info-row">
          <span class="info-label">角色</span>
          <span class="info-value">{{ info?.role || '—' }}</span>
        </div>
        <div class="info-row">
          <span class="info-label">本地 Agent</span>
          <span class="info-value" :class="info?.local_agent_enabled ? 'status-ok' : ''">
            {{ info?.local_agent_enabled ? '● 已启用' : '未启用' }}
          </span>
        </div>
        <div class="info-row">
          <span class="info-label">在线节点</span>
          <span class="info-value">{{ info?.online_agents ?? '—' }} / {{ info?.total_agents ?? '—' }}</span>
        </div>
        <div class="info-row">
          <span class="info-label">运行时长</span>
          <span class="info-value">{{ formatUptime(info?.started_at) }}</span>
        </div>
        <div class="info-row">
          <span class="info-label">数据目录</span>
          <span class="info-value info-value--mono">{{ info?.data_dir || '—' }}</span>
        </div>
      </div>
    </section>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { fetchSystemInfo } from '../../api'

const info = ref(null)

onMounted(() => {
  fetchSystemInfo().then(d => { info.value = d }).catch(() => {})
})

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
.settings-about { display: flex; flex-direction: column; gap: 1.25rem; }

.about-identity {
  text-align: center;
  padding: 1.5rem 0;
}
.about-identity__name {
  font-size: 1.8rem;
  font-weight: 700;
  margin: 0 0 0.3rem;
  color: var(--color-text-primary);
}
.about-identity__tagline {
  font-size: 0.85rem;
  color: var(--color-text-tertiary);
  margin: 0;
}

.settings-section {
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-2xl);
  overflow: hidden;
}
.settings-section__header { padding: 1rem 1.25rem; border-bottom: 1px solid var(--color-border-subtle); }
.settings-section__title { font-size: 1rem; font-weight: 600; margin: 0; color: var(--color-text-primary); }
.settings-section__body { padding: 0.25rem 1.25rem; }

.info-row { display: flex; align-items: center; justify-content: space-between; padding: 0.5rem 0; border-bottom: 1px solid var(--color-border-subtle); }
.info-row:last-child { border-bottom: none; }
.info-label { font-size: 0.875rem; color: var(--color-text-secondary); }
.info-value { font-size: 0.875rem; color: var(--color-text-primary); font-weight: 500; }
.info-value--mono { font-family: monospace; font-size: 0.8rem; }
.info-link {
  font-size: 0.85rem;
  color: var(--color-primary);
  text-decoration: none;
  word-break: break-all;
}
.info-link:hover { text-decoration: underline; }
.status-ok { color: #16a34a; }
</style>

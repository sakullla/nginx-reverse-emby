<template>
  <div class="settings-about">
    <div class="about-identity">
      <h2 class="about-identity__name">Nginx Reverse Emby</h2>
      <div class="about-identity__divider"></div>
      <p class="about-identity__tagline">Nginx 反向代理 &amp; Emby 媒体管理控制面板</p>
    </div>

    <section class="settings-section">
      <div class="settings-section__header">
        <h3 class="settings-section__title">版本信息</h3>
      </div>
      <div class="settings-section__body">
        <div class="info-row">
          <span class="info-label"><span class="info-icon">🏷️</span> 当前版本</span>
          <span class="info-value info-value--highlight">{{ info?.app_version || 'dev' }}</span>
        </div>
        <div class="info-row">
          <span class="info-label"><span class="info-icon">🕐</span> 构建时间</span>
          <span class="info-value">{{ info?.build_time || '—' }}</span>
        </div>
        <div class="info-row">
          <span class="info-label"><span class="info-icon">🏗️</span> 架构</span>
          <span class="info-value">{{ info?.local_apply_runtime || '—' }}</span>
        </div>
        <div class="info-row">
          <span class="info-label"><span class="info-icon">&lt;/&gt;</span> Go 版本</span>
          <span class="info-value info-value--highlight">{{ info?.go_version || '—' }}</span>
        </div>
      </div>
    </section>

    <section class="settings-section">
      <div class="settings-section__header">
        <h3 class="settings-section__title">项目地址</h3>
      </div>
      <div class="settings-section__body">
        <div class="project-links">
          <a href="https://github.com/sakullla/nginx-reverse-emby" target="_blank" rel="noopener" class="project-link">
            <span class="project-link__icon">
              <svg width="18" height="18" viewBox="0 0 24 24" fill="currentColor"><path d="M12 0c-6.626 0-12 5.373-12 12 0 5.302 3.438 9.8 8.207 11.387.599.111.793-.261.793-.577v-2.234c-3.338.726-4.033-1.416-4.033-1.416-.546-1.387-1.333-1.756-1.333-1.756-1.089-.745.083-.729.083-.729 1.205.084 1.839 1.237 1.839 1.237 1.07 1.834 2.807 1.304 3.492.997.107-.775.418-1.305.762-1.604-2.665-.305-5.467-1.334-5.467-5.931 0-1.311.469-2.381 1.236-3.221-.124-.303-.535-1.524.117-3.176 0 0 1.008-.322 3.301 1.23.957-.266 1.983-.399 3.003-.404 1.02.005 2.047.138 3.006.404 2.291-1.552 3.297-1.23 3.297-1.23.653 1.653.242 2.874.118 3.176.77.84 1.235 1.911 1.235 3.221 0 4.609-2.807 5.624-5.479 5.921.43.372.823 1.102.823 2.222v3.293c0 .319.192.694.801.576 4.765-1.589 8.199-6.086 8.199-11.386 0-6.627-5.373-12-12-12z"/></svg>
            </span>
            <span class="project-link__text">GitHub</span>
            <span class="project-link__arrow">↗</span>
          </a>
          <a href="https://github.com/sakullla/nginx-reverse-emby/issues" target="_blank" rel="noopener" class="project-link">
            <span class="project-link__icon">🐛</span>
            <span class="project-link__text">问题反馈</span>
            <span class="project-link__arrow">↗</span>
          </a>
        </div>
      </div>
    </section>

    <section class="settings-section">
      <div class="settings-section__header">
        <h3 class="settings-section__title">系统状态</h3>
      </div>
      <div class="settings-section__body">
        <div class="info-row">
          <span class="info-label"><span class="info-icon">🖥️</span> 角色</span>
          <span class="info-value">{{ info?.role || '—' }}</span>
        </div>
        <div class="info-row">
          <span class="info-label"><span class="info-icon">⚡</span> 本地 Agent</span>
          <span class="info-value" :class="info?.local_agent_enabled ? 'status-ok' : ''">
            <span v-if="info?.local_agent_enabled" class="status-dot"></span>
            {{ info?.local_agent_enabled ? '已启用' : '未启用' }}
          </span>
        </div>
        <div class="info-row">
          <span class="info-label"><span class="info-icon">🌐</span> 在线节点</span>
          <span class="info-value">{{ info?.online_agents ?? '—' }} 在线 / {{ info?.total_agents ?? '—' }} 总计</span>
        </div>
        <div class="info-row">
          <span class="info-label"><span class="info-icon">⏱️</span> 运行时长</span>
          <span class="info-value">{{ formatUptime(info?.started_at) }}</span>
        </div>
        <div class="info-row">
          <span class="info-label"><span class="info-icon">📁</span> 数据目录</span>
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
  margin: 0 0 0.5rem;
  color: var(--color-text-primary);
}
.about-identity__divider {
  width: 80px;
  height: 3px;
  margin: 0 auto 0.5rem;
  border-radius: 2px;
  background: linear-gradient(90deg, transparent, var(--color-primary), transparent);
}
.about-identity__tagline {
  font-size: 0.9rem;
  color: var(--color-text-secondary);
  margin: 0;
}

.settings-section {
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-xl);
  overflow: hidden;
  transition: box-shadow 0.2s var(--ease-default);
}
.settings-section:hover {
  box-shadow: 0 1px 4px color-mix(in srgb, var(--color-border-default) 30%, transparent);
}
.settings-section__header {
  padding: 1rem 1.25rem;
  border-bottom: 1px solid var(--color-border-subtle);
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.settings-section__title { font-size: 1rem; font-weight: 600; margin: 0; color: var(--color-text-primary); }
.settings-section__body { padding: 0.25rem 1.25rem; }

.info-row { display: flex; align-items: center; justify-content: space-between; padding: 0.6rem 0; border-bottom: 1px solid var(--color-border-subtle); }
.info-row:last-child { border-bottom: none; }
.info-label { font-size: 0.875rem; color: var(--color-text-secondary); display: flex; align-items: center; }
.info-value { font-size: 0.875rem; color: var(--color-text-primary); font-weight: 500; }
.info-value--highlight { font-family: monospace; color: var(--color-primary); }
.info-value--mono { font-family: monospace; font-size: 0.8rem; }
.info-icon { margin-right: 0.4rem; font-size: 0.9rem; }

.project-links { display: flex; flex-direction: column; gap: 0.5rem; }
.project-link {
  display: flex;
  align-items: center;
  gap: 0.6rem;
  padding: 0.75rem 1rem;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  background: var(--color-bg-subtle);
  color: var(--color-text-primary);
  text-decoration: none;
  transition: all 0.2s var(--ease-default);
}
.project-link:hover {
  border-color: var(--color-primary);
  box-shadow: 0 2px 8px color-mix(in srgb, var(--color-primary) 10%, transparent);
  transform: translateY(-1px);
}
.project-link__icon { display: flex; align-items: center; color: var(--color-primary); }
.project-link__text { flex: 1; font-size: 0.9rem; font-weight: 500; }
.project-link__arrow { font-size: 0.85rem; color: var(--color-text-tertiary); }

.status-dot {
  display: inline-block;
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: #16a34a;
  margin-right: 0.35rem;
  animation: pulse 2s ease-in-out infinite;
}
@keyframes pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.5; }
}
.status-ok { color: #16a34a; display: flex; align-items: center; }
</style>

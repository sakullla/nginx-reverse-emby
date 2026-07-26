<template>
  <div class="settings-about">
    <header class="task-header">
      <div>
        <h2 class="task-header__title">系统关于</h2>
        <p class="task-header__desc">版本、运行状态与项目信息</p>
      </div>
    </header>

    <section class="settings-section about-identity-card">
      <div class="about-identity">
        <h3 class="about-identity__name">Nginx Reverse Emby</h3>
        <div class="about-identity__divider"></div>
        <p class="about-identity__tagline">Nginx 反向代理 &amp; Emby 媒体管理控制面板</p>
        <p class="about-identity__version">
          版本
          <strong>{{ info?.app_version || 'dev' }}</strong>
        </p>
      </div>
    </section>

    <div class="about-grid">
      <section class="settings-section">
        <div class="settings-section__header">
          <h3 class="settings-section__title">版本信息</h3>
          <p class="settings-section__desc">构建与运行时</p>
        </div>
        <div class="settings-section__body">
          <div class="info-row">
            <span class="info-label">当前版本</span>
            <span class="info-value info-value--highlight">{{ info?.app_version || 'dev' }}</span>
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
            <span class="info-value info-value--highlight">{{ info?.go_version || '—' }}</span>
          </div>
        </div>
      </section>

      <section class="settings-section">
        <div class="settings-section__header">
          <h3 class="settings-section__title">运行状态</h3>
          <p class="settings-section__desc">角色、节点与运行时长</p>
        </div>
        <div class="settings-section__body">
          <div v-if="isLoading" class="settings-placeholder">加载中…</div>
          <div v-else-if="!info" class="settings-placeholder">系统信息暂不可用。</div>
          <template v-else>
            <div class="info-row">
              <span class="info-label">角色</span>
              <span class="info-value">{{ info.role || '—' }}</span>
            </div>
            <div class="info-row">
              <span class="info-label">本地 Agent</span>
              <span class="info-value" :class="info.local_agent_enabled ? 'status-ok' : ''">
                <span v-if="info.local_agent_enabled" class="status-dot"></span>
                {{ info.local_agent_enabled ? '已启用' : '未启用' }}
              </span>
            </div>
            <div class="info-row">
              <span class="info-label">在线节点</span>
              <span class="info-value">{{ info.online_agents ?? '—' }} / {{ info.total_agents ?? '—' }}</span>
            </div>
            <div class="info-row">
              <span class="info-label">运行时长</span>
              <span class="info-value">{{ formatUptime(info.started_at) }}</span>
            </div>
            <div class="info-row">
              <span class="info-label">数据目录</span>
              <span class="info-value info-value--mono">{{ info.data_dir || '—' }}</span>
            </div>
          </template>
        </div>
      </section>
    </div>

    <section class="settings-section">
      <div class="settings-section__header">
        <h3 class="settings-section__title">项目地址</h3>
        <p class="settings-section__desc">源码与反馈入口</p>
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
  </div>
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
.settings-about {
  display: flex;
  flex-direction: column;
  gap: var(--space-5);
}

.task-header__title {
  margin: 0 0 0.25rem;
  font-size: var(--text-lg);
  font-weight: 700;
  color: var(--color-text-primary);
}

.task-header__desc {
  margin: 0;
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
}

.about-identity {
  text-align: center;
  padding: var(--space-4) 0;
}
.about-identity__name {
  font-size: var(--text-2xl);
  font-weight: var(--font-bold);
  margin: 0 0 var(--space-2);
  color: var(--color-text-primary);
}
.about-identity__divider {
  width: 72px;
  height: 3px;
  margin: 0 auto var(--space-2);
  border-radius: var(--radius-full);
  background: linear-gradient(90deg, transparent, var(--color-primary), transparent);
}
.about-identity__tagline {
  font-size: var(--text-sm);
  color: var(--color-text-secondary);
  margin: 0 0 var(--space-2);
}
.about-identity__version {
  margin: 0;
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
}
.about-identity__version strong {
  margin-left: 0.35rem;
  font-family: var(--font-mono);
  color: var(--color-primary);
}

.about-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-4);
}

.info-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  padding: var(--space-2-5) 0;
  border-bottom: 1px solid var(--color-border-subtle);
}
.info-row:last-child { border-bottom: none; }
.info-label {
  font-size: var(--text-sm);
  color: var(--color-text-secondary);
}
.info-value {
  font-size: var(--text-sm);
  color: var(--color-text-primary);
  font-weight: var(--font-medium);
  text-align: right;
}
.info-value--highlight { font-family: var(--font-mono); color: var(--color-primary); }
.info-value--mono {
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  word-break: break-all;
}

.status-ok {
  color: var(--color-success);
  display: inline-flex;
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

.project-links { display: flex; flex-direction: column; gap: var(--space-2); }
.project-link {
  display: flex;
  align-items: center;
  gap: var(--space-2-5);
  padding: var(--space-3) var(--space-4);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  background: var(--color-bg-subtle);
  color: var(--color-text-primary);
  text-decoration: none;
  transition: border-color var(--duration-fast) var(--ease-default),
              box-shadow var(--duration-fast) var(--ease-default),
              transform var(--duration-fast) var(--ease-default);
}
.project-link:hover {
  border-color: var(--color-primary);
  box-shadow: 0 2px 8px color-mix(in srgb, var(--color-primary) 10%, transparent);
  transform: translateY(-1px);
}
.project-link__icon { display: flex; align-items: center; color: var(--color-primary); }
.project-link__text { flex: 1; font-size: var(--text-sm); font-weight: var(--font-medium); }
.project-link__arrow { font-size: var(--text-xs); color: var(--color-text-tertiary); }

@media (max-width: 720px) {
  .about-grid {
    grid-template-columns: 1fr;
  }
}
</style>

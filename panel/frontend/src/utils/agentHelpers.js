export function getAgentStatus(agent) {
  if (!agent) return 'offline'
  if (agent.status === 'offline') return 'offline'

  const desired = normalizeRevision(agent.desired_revision)
  const current = normalizeRevision(agent.current_revision)
  const lastApplyRevision = normalizeRevision(agent.last_apply_revision, current)
  const applyStatus = agent.last_apply_status
  const applyFailed = applyStatus !== null && applyStatus !== undefined && applyStatus !== 'success'

  if (desired > current) {
    if (applyFailed && lastApplyRevision >= desired) return 'failed'
    return 'pending'
  }

  if (applyFailed) return 'failed'
  return 'online'
}

function normalizeRevision(value, fallback = 0) {
  const parsed = Number(value)
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : fallback
}

export function getAgentStatusLabel(status) {
  const map = { online: '在线', offline: '离线', failed: '失败', pending: '同步中' }
  return map[status] || '—'
}

export function getModeLabel(mode) {
  if (mode === 'local') return '本机'
  if (mode === 'master') return '主控'
  return '拉取'
}

export function getHostname(url) {
  try { return url ? new URL(url).hostname : '' } catch { return '' }
}

export function timeAgo(date) {
  if (!date) return '—'
  const seconds = Math.floor((Date.now() - new Date(date)) / 1000)
  if (seconds < 60) return '刚刚'
  const m = Math.floor(seconds / 60)
  if (m < 60) return `${m}m`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h`
  return `${Math.floor(h / 24)}d`
}

export function timeAgoLong(date) {
  if (!date) return '—'
  const seconds = Math.floor((Date.now() - new Date(date)) / 1000)
  if (seconds < 60) return '刚刚'
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes} 分钟前`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours} 小时前`
  return `${Math.floor(hours / 24)} 天前`
}

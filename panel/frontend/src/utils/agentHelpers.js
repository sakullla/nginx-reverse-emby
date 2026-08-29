export function isRevisionReportAckFailure(message) {
  const text = String(message || '').toLowerCase()
  return text.includes('/api/agent-revisions/') && text.includes('/report failed')
}

export function agentApplyFailed(agent) {
  if (!agent) return false
  const applyStatus = agent.last_apply_status
  if (applyStatus == null || applyStatus === '' || applyStatus === 'success') return false
  if (isRevisionReportAckFailure(agent.last_apply_message)) {
    const current = normalizeRevision(agent.current_revision)
    const lastApply = normalizeRevision(agent.last_apply_revision, current)
    if (lastApply <= current) return false
  }
  return true
}

export function getAgentStatus(agent) {
  if (!agent) return 'offline'
  if (agent.status === 'offline') return 'offline'

  const desired = normalizeRevision(agent.desired_revision)
  const current = normalizeRevision(agent.current_revision)
  const lastApplyRevision = normalizeRevision(agent.last_apply_revision, current)
  const applyFailed = agentApplyFailed(agent)

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

/**
 * Display address for agent list/cards.
 * Prefer configured DDNS domain over agent_url hostname / last_seen IP,
 * so nodes with DDNS keep showing the domain even when agent_url is an IP URL.
 */
export function getAgentEndpointLabel(agent) {
  if (!agent) return '—'
  const domain = typeof agent.ddns_domain === 'string' ? agent.ddns_domain.trim() : ''
  if (domain) return domain
  const host = getHostname(agent.agent_url)
  if (host) return host
  const ip = typeof agent.last_seen_ip === 'string' ? agent.last_seen_ip.trim() : ''
  if (ip) return ip
  return '—'
}

/**
 * Split the ddns_domain text field into individual domains.
 * Accepts comma / Chinese comma / newline separators, mirroring the backend.
 */
export function splitDdnsDomains(value) {
  if (typeof value !== 'string') return []
  const seen = new Set()
  const domains = []
  for (const part of value.split(/[,，\n\r]/)) {
    const domain = part.trim()
    if (!domain || seen.has(domain)) continue
    seen.add(domain)
    domains.push(domain)
  }
  return domains
}

/**
 * Compact endpoint display for dense tiles: the first domain plus the count
 * of additional ones, so multi-domain DDNS configs stay on a single line.
 * `full` keeps the complete label for tooltips.
 */
export function getAgentEndpointDisplay(agent) {
  const full = getAgentEndpointLabel(agent)
  const domains = splitDdnsDomains(agent && agent.ddns_domain)
  if (domains.length <= 1) return { primary: full, extraCount: 0, full }
  return { primary: domains[0], extraCount: domains.length - 1, full }
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

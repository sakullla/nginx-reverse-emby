function normalizeRevision(value, fallback = 0) {
  const parsed = Number(value)
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : fallback
}

export function getAgentSyncStatus(agent) {
  if (!agent) return 'online'
  if (agent.status !== 'online') return 'offline'

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

export function getRuleEffectiveStatus(rule, agent) {
  if (!rule?.enabled) return 'disabled'
  if (!agent) return 'active'

  const targetRevision = normalizeRevision(rule.revision)
  const currentRevision = normalizeRevision(agent.current_revision)
  const lastApplyRevision = normalizeRevision(agent.last_apply_revision, currentRevision)
  const applyStatus = agent.last_apply_status
  const applyFailed = applyStatus !== null && applyStatus !== undefined && applyStatus !== 'success'

  if (targetRevision <= currentRevision) return 'active'
  if (applyFailed && lastApplyRevision >= targetRevision) return 'failed'
  return 'pending'
}

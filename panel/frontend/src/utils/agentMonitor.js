export function createNDJSONParser(onMessage) {
  let buffer = ''

  function emit(line) {
    const trimmed = line.trim()
    if (!trimmed) return
    onMessage(JSON.parse(trimmed))
  }

  return {
    push(chunk) {
      buffer += String(chunk || '')
      const lines = buffer.split(/\r?\n/)
      buffer = lines.pop() || ''
      lines.forEach(emit)
    },
    flush() {
      if (!buffer.trim()) {
        buffer = ''
        return
      }
      emit(buffer)
      buffer = ''
    }
  }
}

export function quantizeLastSeenAt(agent) {
  if (!agent?.last_seen_at) return agent
  const date = new Date(agent.last_seen_at)
  if (Number.isNaN(date.getTime())) return agent
  if (date.getSeconds() === 0 && date.getMilliseconds() === 0) return agent
  date.setSeconds(0, 0)
  return { ...agent, last_seen_at: date.toISOString() }
}

function packageSha(value) {
  return String(value || '').trim()
}

function samePackageSha(left, right) {
  const a = packageSha(left)
  const b = packageSha(right)
  return Boolean(a) && Boolean(b) && a.toLowerCase() === b.toLowerCase()
}

// Heartbeats stay alive while a new package is only staged. A monitor payload
// that copies the target digest into runtime_* would make the panel show the
// candidate as already running. Keep the last known running identity until
// the durable agent record itself has switched.
export function preserveRunningPackage(agent, next) {
  if (!agent || !next) return next
  const runningSha = packageSha(agent.runtime_package_sha256)
  const desiredSha = packageSha(agent.desired_package_sha256 || next.desired_package_sha256)
  const nextRuntimeSha = packageSha(next.runtime_package_sha256)
  if (runningSha && desiredSha && !samePackageSha(runningSha, desiredSha) && samePackageSha(nextRuntimeSha, desiredSha)) {
    return {
      ...next,
      runtime_package_sha256: runningSha,
      runtime_package_version: agent.runtime_package_version,
      runtime_package_platform: agent.runtime_package_platform,
      runtime_package_arch: agent.runtime_package_arch,
      version: agent.version || agent.runtime_package_version,
      package_sync_status: 'pending'
    }
  }
  if (runningSha && desiredSha && !samePackageSha(runningSha, desiredSha)) {
    return { ...next, package_sync_status: 'pending' }
  }
  return next
}

export function overlayMonitorOnAgent(agent, monitor) {
  if (!agent || !monitor) return agent
  return preserveRunningPackage(agent, { ...agent, ...monitor })
}

export function mergeFetchedAgents(previous, next) {
  if (!Array.isArray(next)) return Array.isArray(previous) ? previous : []
  // The list endpoint is backed by the durable agent record. The control plane
  // already rejects staging heartbeat package identities before persisting
  // them, so a successful refetch must replace the cached running identity.
  // Reusing the monitor-overlay guard here would keep the old package forever
  // after the durable record reports a completed switch.
  return next
}

export function mergeMonitorAgents(previous = [], update) {
  const nextAgent = quantizeLastSeenAt(update?.agent || update)
  if (!nextAgent?.id) return Array.isArray(previous) ? previous : []
  const agents = Array.isArray(previous) ? [...previous] : []
  const index = agents.findIndex((agent) => agent?.id === nextAgent.id)
  if (index >= 0) {
    agents[index] = preserveRunningPackage(agents[index], { ...agents[index], ...nextAgent })
    return agents
  }
  agents.push(nextAgent)
  return agents
}

export function mergeAgentsWithMonitor(agents, monitorAgents, options = {}) {
  const baseAgents = agents || []
  // Cached monitor snapshots are useful across reconnects, but they are not
  // authoritative once the stream is inactive. In that state the durable
  // /agents result must win without an inline or cached monitor overlay.
  if (options.active === false) return baseAgents
  const monitorById = new Map((monitorAgents || []).map(agent => [agent.id, agent]))
  let changed = false
  const merged = baseAgents.map((agent) => {
    const monitor = monitorById.get(agent.id) || agent.monitor
    if (!monitor) return agent
    changed = true
    return { ...overlayMonitorOnAgent(agent, monitor), monitor }
  })
  return changed ? merged : baseAgents
}

export function monitorSnapshotAgents(snapshot) {
  return (Array.isArray(snapshot?.agents) ? snapshot.agents : []).map(quantizeLastSeenAt)
}

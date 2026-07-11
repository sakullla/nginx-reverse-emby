import { isAllAgentsFilter, normalizeAgentFilter } from '../utils/agentFilter.js'

export function reconcileSelectedAgent({
  currentSelectedAgentId,
  agents,
  systemInfo,
  systemInfoAttempted
}) {
  if (agents == null) {
    return {
      nextSelectedAgentId: currentSelectedAgentId,
      persist: false,
      clear: false
    }
  }

  if (agents.length === 0) {
    return {
      nextSelectedAgentId: null,
      persist: false,
      clear: true
    }
  }

  const current = normalizeAgentFilter(currentSelectedAgentId)

  // Explicit "all agents" memory is valid whenever at least one agent exists.
  // Never auto-promote to all; only preserve a user-chosen sentinel.
  if (isAllAgentsFilter(current)) {
    return {
      nextSelectedAgentId: current,
      persist: false,
      clear: false
    }
  }

  const ids = new Set(agents.map(agent => agent.id))
  if (current && ids.has(current)) {
    return {
      nextSelectedAgentId: current,
      persist: false,
      clear: false
    }
  }

  if (!systemInfo && !systemInfoAttempted && !current) {
    return {
      nextSelectedAgentId: currentSelectedAgentId,
      persist: false,
      clear: false
    }
  }

  // Invalid / missing selection falls back to a concrete default node — never all.
  const defaultId = systemInfo?.default_agent_id
    || agents.find(agent => agent.id === 'local')?.id
    || agents[0]?.id
    || null

  return {
    nextSelectedAgentId: defaultId,
    persist: defaultId !== null,
    clear: false
  }
}

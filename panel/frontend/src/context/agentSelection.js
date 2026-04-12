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

  const ids = new Set(agents.map(agent => agent.id))
  if (currentSelectedAgentId && ids.has(currentSelectedAgentId)) {
    return {
      nextSelectedAgentId: currentSelectedAgentId,
      persist: false,
      clear: false
    }
  }

  if (!systemInfo && !systemInfoAttempted && !currentSelectedAgentId) {
    return {
      nextSelectedAgentId: currentSelectedAgentId,
      persist: false,
      clear: false
    }
  }

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

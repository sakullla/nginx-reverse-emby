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

export function mergeMonitorAgents(previous = [], update) {
  const nextAgent = update?.agent || update
  if (!nextAgent?.id) return Array.isArray(previous) ? previous : []
  const agents = Array.isArray(previous) ? [...previous] : []
  const index = agents.findIndex((agent) => agent?.id === nextAgent.id)
  if (index >= 0) {
    agents[index] = { ...agents[index], ...nextAgent }
    return agents
  }
  agents.push(nextAgent)
  return agents
}

export function monitorSnapshotAgents(snapshot) {
  return Array.isArray(snapshot?.agents) ? snapshot.agents : []
}

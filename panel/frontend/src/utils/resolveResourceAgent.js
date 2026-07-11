import { ALL_AGENTS_FILTER, isAllAgentsFilter, normalizeAgentFilter } from './agentFilter.js'

/**
 * Resolve the agent id for create flows under concrete / all filters.
 *
 * @param {string|null|undefined} filter - agent filter (concrete id or ALL_AGENTS_FILTER)
 * @param {Array<{id?: string|number, is_local?: boolean, name?: string}>} agents
 * @param {{ systemInfo?: { default_agent_id?: string|number }|null, explicitAgentId?: string|null }} [options]
 * @returns {{
 *   agentId: string|null,
 *   needsSelection: boolean,
 *   reason: string,
 *   candidates: Array
 * }}
 */
export function resolveCreateAgentId(filter, agents = [], options = {}) {
  const list = Array.isArray(agents) ? agents : []
  const normalized = normalizeAgentFilter(filter)
  const explicit = normalizeAgentFilter(options.explicitAgentId)

  if (explicit && !isAllAgentsFilter(explicit)) {
    return {
      agentId: explicit,
      needsSelection: false,
      reason: 'explicit',
      candidates: list,
    }
  }

  if (!normalized) {
    return {
      agentId: null,
      needsSelection: list.length > 0,
      reason: 'missing_filter',
      candidates: list,
    }
  }

  if (!isAllAgentsFilter(normalized)) {
    return {
      agentId: normalized,
      needsSelection: false,
      reason: 'concrete_filter',
      candidates: list,
    }
  }

  // all agents view: auto-bind only when there is a single candidate agent.
  // "only local" is the single-local-agent case (not "one local among many remotes").
  if (list.length === 1) {
    const only = list[0]
    const id = String(only?.id ?? '').trim()
    if (id) {
      return {
        agentId: id,
        needsSelection: false,
        reason: only?.is_local ? 'single_local' : 'single_agent',
        candidates: list,
      }
    }
  }

  return {
    agentId: null,
    needsSelection: list.length > 0,
    reason: list.length === 0 ? 'no_agents' : 'multi_agent_all',
    candidates: list,
  }
}

/**
 * Resolve agent id for mutations (toggle/update/delete/diagnose/issue/traffic).
 * Prefer resource.agent_id, then concrete filter; never silently no-op.
 *
 * @param {object|null|undefined} resource
 * @param {string|null|undefined} filter
 * @param {{ fallbackAgentId?: string|null }} [options]
 * @returns {{ agentId: string|null, error: string|null, source: string }}
 */
export function resolveMutationAgentId(resource, filter, options = {}) {
  const fromResource = firstNonEmpty(
    resource?.agent_id,
    resource?.agentId,
  )
  if (fromResource) {
    return { agentId: fromResource, error: null, source: 'resource' }
  }

  const fallback = firstNonEmpty(options.fallbackAgentId)
  if (fallback && !isAllAgentsFilter(fallback)) {
    return { agentId: fallback, error: null, source: 'fallback' }
  }

  const normalized = normalizeAgentFilter(filter)
  if (normalized && !isAllAgentsFilter(normalized)) {
    return { agentId: normalized, error: null, source: 'filter' }
  }

  return {
    agentId: null,
    error: '缺少节点归属，无法执行该操作',
    source: 'none',
  }
}

/**
 * Resolve target agent for copy/create-from-existing under all/concrete filters.
 * Same rules as create, but can seed from source resource agent when filter is all
 * only if caller passes preferSource=true (default false: still requires create resolve).
 */
export function resolveCopyTargetAgentId(filter, agents = [], options = {}) {
  return resolveCreateAgentId(filter, agents, options)
}

export { ALL_AGENTS_FILTER, isAllAgentsFilter, normalizeAgentFilter }

function firstNonEmpty(...values) {
  for (const value of values) {
    if (value == null) continue
    const text = String(value).trim()
    if (text && text !== ALL_AGENTS_FILTER && text !== 'all' && text !== '*') {
      return text
    }
  }
  return null
}

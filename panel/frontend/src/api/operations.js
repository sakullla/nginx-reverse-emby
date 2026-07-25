import { api, longRunningRequest } from './client'

const TERMINAL_STATUSES = new Set(['drained', 'forced', 'failed', 'degraded', 'superseded'])

function text(value) {
  return String(value || '').trim()
}

function statusURL(value) {
  const candidate = text(value)
  return /^\/(?:panel-api|api)\/operations\/[^/?#]+$/.test(candidate) ? candidate : ''
}

export function normalizeOperationStatus(payload = {}) {
  const agents = Array.isArray(payload.agents) ? payload.agents.map((agent) => ({ ...agent })) : []
  const applyStatus = text(payload.apply_status || payload.status || 'pending').toLowerCase()
  const drainStatuses = agents.map((agent) => text(agent.drain_status).toLowerCase()).filter(Boolean)
  const primaryAgent = text(payload.agent_id || payload.primary_agent_id)
  const primaryStatus = agents.find((agent) => agent.agent_id === primaryAgent) || agents[0] || {}
  const failedAgent = agents.find((agent) => agent.error_code || agent.error_message) || {}
  let uiStatus = applyStatus
  if (applyStatus === 'applied' && drainStatuses.includes('draining')) uiStatus = 'draining'
  if (
    applyStatus === 'applied'
    && agents.length > 0
    && drainStatuses.length === agents.length
    && drainStatuses.every((status) => ['drained', 'forced'].includes(status))
  ) uiStatus = drainStatuses.includes('forced') ? 'forced' : 'drained'
  if (payload.degraded === true || applyStatus === 'degraded') uiStatus = 'degraded'
  const terminalCompletedProgress = Boolean(payload.completed_at) && ['pending', 'applying'].includes(uiStatus)
  const terminalApplied = uiStatus === 'applied' && (payload.no_op === true || Boolean(payload.completed_at))
  return {
    operation_id: text(payload.operation_id),
    status_url: statusURL(payload.status_url),
    agent_id: primaryAgent,
    desired_revision: Number(payload.desired_revision ?? primaryStatus.desired_revision) || 0,
    apply_status: applyStatus,
    ui_status: uiStatus,
    agents,
    no_op: payload.no_op === true,
    replayed: payload.replayed === true,
    degraded: payload.degraded === true || applyStatus === 'degraded',
    error_code: text(payload.error_code || failedAgent.error_code),
    error_message: text(payload.error_message || failedAgent.error_message),
    updated_at: payload.updated_at || '',
    completed_at: payload.completed_at || '',
    terminal: terminalCompletedProgress || TERMINAL_STATUSES.has(uiStatus) || terminalApplied
  }
}

export function operationFromEnvelope(payload = {}) {
  if (!payload?.operation_id) return null
  return normalizeOperationStatus({ ...payload, apply_status: payload.apply_status || 'pending' })
}

export function preserveMutationEnvelope(payload = {}, resourceKey, normalizeResource = (value) => value) {
  const rawResource = resourceKey ? payload?.[resourceKey] : payload
  const resource = normalizeResource(rawResource)
  const operation = operationFromEnvelope(payload)
  if (resource && typeof resource === 'object' && !Array.isArray(resource)) {
    return operation ? { ...resource, operation } : resource
  }
  return operation ? { value: resource, operation } : resource
}

export async function fetchOperationStatus(statusURL) {
  const safeURL = typeof statusURL === 'string' && (statusURL.startsWith('/panel-api/') || statusURL.startsWith('/api/'))
    ? statusURL
    : ''
  if (!safeURL || !/^\/(?:panel-api|api)\/operations\/[^/?#]+$/.test(safeURL)) {
    throw new Error('operation status URL is invalid')
  }
  const requestURL = safeURL.replace(/^\/(?:panel-api|api)/, '')
  const { data } = await api.get(requestURL)
  return normalizeOperationStatus(data.operation || data)
}

export async function dismissOperationStatus(operationID) {
  const id = text(operationID)
  if (!id) throw new Error('operation id is required')
  const { data } = await api.post(`/operations/${encodeURIComponent(id)}/dismiss`, {})
  return normalizeOperationStatus(data.operation || data)
}

export async function fetchRevisionEvents(after = 0, options = {}) {
  const params = { after: Number(after) || 0, limit: Number(options.limit) || 100 }
  if (options.operationId) params.operation_id = options.operationId
  if (options.agentId) params.agent_id = options.agentId
  const { data } = await api.get('/revision-events', { params })
  return {
    events: Array.isArray(data.events) ? data.events : [],
    next_cursor: Number(data.next_cursor) || params.after,
    has_more: data.has_more === true
  }
}

export async function retryRevision(operation, targetAgent) {
  const failedAgent = targetAgent || operation?.agents?.find((agent) => agent.apply_status === 'failed')
  const agent = failedAgent || operation?.agents?.find((item) => item.agent_id === operation?.agent_id) || operation?.agents?.[0]
  const agentID = agent?.agent_id || operation?.agent_id
  const revision = agent?.desired_revision || operation?.desired_revision
  if (!agentID || !revision) throw new Error('operation is missing retry revision details')
  const { data } = await api.post(
    `/agents/${encodeURIComponent(agentID)}/revisions/${encodeURIComponent(revision)}/retry`,
    {},
    longRunningRequest
  )
  return normalizeOperationStatus(data)
}

export async function rollbackRevision(operation, targetAgent) {
  const failedAgent = targetAgent || operation?.agents?.find((agent) => agent.apply_status === 'failed')
  const agentID = failedAgent?.agent_id || operation?.agent_id || operation?.agents?.[0]?.agent_id
  if (!agentID) throw new Error('operation is missing rollback agent details')
  const { data } = await api.post(
    `/agents/${encodeURIComponent(agentID)}/revisions/rollback`,
    {},
    longRunningRequest
  )
  return normalizeOperationStatus(data)
}

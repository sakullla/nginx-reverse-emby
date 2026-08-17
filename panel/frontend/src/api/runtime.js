import { api, longRunningRequest } from './client'
import { operationFromEnvelope, preserveMutationEnvelope } from './operations'
import { recordAcceptedOperation } from '../stores/operations'
export { consumeAgentMonitorStream } from './agentMonitor'

const SUPPORTED_LOAD_BALANCING_STRATEGIES = new Set(['adaptive', 'round_robin', 'random'])

function mutationResource(data, resourceKey, normalizer) {
  const result = preserveMutationEnvelope(data, resourceKey, normalizer)
  recordAcceptedOperation(result?.operation)
  return result
}

function mutationEnvelope(data) {
  const operation = operationFromEnvelope(data)
  recordAcceptedOperation(operation)
  return operation ? { ...data, operation } : data
}

function normalizeHttpBackends(rule = {}) {
  if (Array.isArray(rule.backends) && rule.backends.length > 0) {
    return rule.backends
      .map((backend) => {
        if (backend?.kind === 'plugin_provider') {
          const instanceId = String(backend?.plugin_provider?.instance_id || '').trim()
          const providerId = String(backend?.plugin_provider?.provider_id || '').trim()
          if (!instanceId || !providerId) return null
          return {
            kind: 'plugin_provider',
            plugin_provider: { instance_id: instanceId, provider_id: providerId }
          }
        }
        const url = String(backend?.url || '').trim()
        return url ? { url } : null
      })
      .filter(Boolean)
  }
  return []
}

function normalizeLoadBalancingStrategy(value) {
  const strategy = String(value || '').trim().toLowerCase()
  return SUPPORTED_LOAD_BALANCING_STRATEGIES.has(strategy) ? strategy : 'adaptive'
}

function normalizeRelayLayers(value) {
  if (!Array.isArray(value)) return []
  return value
    .map((layer) => Array.isArray(layer)
      ? layer.map((id) => Number(id)).filter((id) => Number.isInteger(id) && id > 0)
      : [])
    .filter((layer) => layer.length > 0)
}

function normalizeEgressProfileID(payload = {}) {
  const id = Number(payload.egress_profile_id)
  return Number.isInteger(id) && id > 0 ? id : undefined
}

function normalizeExplicitEgressProfileID(payload = {}) {
  if (!Object.prototype.hasOwnProperty.call(payload, 'egress_profile_id')) return undefined
  if (payload.egress_profile_id === '' || payload.egress_profile_id == null) return undefined
  const id = Number(payload.egress_profile_id)
  return Number.isInteger(id) && id >= 0 ? id : undefined
}

function applyEgressProfileID(normalizedPayload, payload = {}) {
  const explicitID = normalizeExplicitEgressProfileID(payload)
  if (explicitID != null) {
    normalizedPayload.egress_profile_id = explicitID
    return normalizedPayload
  }
  const id = normalizeEgressProfileID(payload)
  if (id) {
    normalizedPayload.egress_profile_id = id
  } else {
    delete normalizedPayload.egress_profile_id
  }
  return normalizedPayload
}

function normalizeEgressProfilePayload(payload = {}) {
  const requestedType = String(payload.type || 'direct').trim().toLowerCase()
  const type = requestedType === 'socks' || requestedType === 'http' ? requestedType : 'direct'
  const normalized = {
    name: String(payload.name || '').trim(),
    type,
    enabled: payload.enabled !== false,
    description: String(payload.description || '').trim(),
    proxy_url: ''
  }
  if (type === 'socks' || type === 'http') {
    normalized.proxy_url = String(payload.proxy_url || '').trim()
  }
  return normalized
}

function normalizeHttpRule(rule = {}) {
  const egressProfileID = normalizeEgressProfileID(rule)
  return {
    ...rule,
    backends: normalizeHttpBackends(rule),
    load_balancing: {
      strategy: normalizeLoadBalancingStrategy(rule.load_balancing?.strategy)
    },
    relay_obfs: rule.relay_obfs === true,
    egress_profile_id: egressProfileID
  }
}

function normalizeL4Backends(rule = {}) {
  if (Array.isArray(rule.backends) && rule.backends.length > 0) {
    return rule.backends
      .map((backend) => ({
        host: String(backend?.host || '').trim(),
        port: Number(backend?.port)
      }))
      .filter((backend) => backend.host && Number.isInteger(backend.port) && backend.port > 0)
  }
  return []
}

function normalizeL4Rule(rule = {}) {
  const listenMode = rule.listen_mode === 'proxy' ? 'proxy' : 'tcp'
  const egressProfileID = normalizeEgressProfileID(rule)
  return {
    ...rule,
    backends: normalizeL4Backends(rule),
    load_balancing: {
      strategy: normalizeLoadBalancingStrategy(rule.load_balancing?.strategy)
    },
    relay_obfs: rule.relay_obfs === true,
    listen_mode: listenMode,
    proxy_entry_auth: listenMode === 'proxy'
      ? {
          enabled: rule.proxy_entry_auth?.enabled === true,
          username: String(rule.proxy_entry_auth?.username || ''),
          password: String(rule.proxy_entry_auth?.password || '')
        }
      : { enabled: false, username: '', password: '' },
    egress_profile_id: egressProfileID
  }
}

function normalizeHttpRulePayloadObject(payload = {}, options = {}) {
  const includeRelayDefaults = options.includeRelayDefaults === true
  const { backend_url, relay_chain, ...rest } = payload
  const normalizedPayload = {
    ...rest,
    frontend_url: String(payload.frontend_url || '').trim(),
    backends: normalizeHttpBackends(payload),
    load_balancing: {
      strategy: normalizeLoadBalancingStrategy(payload.load_balancing?.strategy)
    },
    tags: payload.tags != null ? payload.tags : undefined,
    enabled: payload.enabled !== false,
    proxy_redirect: payload.proxy_redirect !== false,
    pass_proxy_headers: payload.pass_proxy_headers === true,
    user_agent: String(payload.user_agent || ''),
    custom_headers: Array.isArray(payload.custom_headers) ? payload.custom_headers : []
  }
  if (Array.isArray(payload.relay_layers)) {
    normalizedPayload.relay_layers = normalizeRelayLayers(payload.relay_layers)
  } else if (includeRelayDefaults) {
    normalizedPayload.relay_layers = []
  }
  if (payload.relay_obfs != null) {
    normalizedPayload.relay_obfs = payload.relay_obfs === true
  } else if (includeRelayDefaults) {
    normalizedPayload.relay_obfs = false
  }
  return applyEgressProfileID(normalizedPayload, payload)
}

function normalizeL4RulePayload(payload = {}, options = {}) {
  const includeRelayDefaults = options.includeRelayDefaults === true
  const {
    upstream_host,
    upstream_port,
    relay_chain,
    ...rest
  } = payload
  const normalizedPayload = {
    ...rest,
    backends: normalizeL4Backends(payload),
    load_balancing: {
      strategy: normalizeLoadBalancingStrategy(payload.load_balancing?.strategy)
    }
  }
  if (Array.isArray(payload.relay_layers)) {
    normalizedPayload.relay_layers = normalizeRelayLayers(payload.relay_layers)
  } else if (includeRelayDefaults) {
    normalizedPayload.relay_layers = []
  }
  if (payload.relay_obfs != null) {
    normalizedPayload.relay_obfs = payload.relay_obfs === true
  } else if (includeRelayDefaults) {
    normalizedPayload.relay_obfs = false
  }
  return applyEgressProfileID(normalizedPayload, payload)
}

function parseDownloadFilename(contentDisposition, fallback = 'nre-backup.tar.gz') {
  const value = String(contentDisposition || '')
  const encodedMatch = value.match(/filename\*=UTF-8''([^;]+)/i)
  if (encodedMatch?.[1]) {
    try {
      return decodeURIComponent(encodedMatch[1])
    } catch {
      return fallback
    }
  }
  const plainMatch = value.match(/filename="?([^";]+)"?/i)
  return plainMatch?.[1] || fallback
}

/**
 * Build query params for control-plane list endpoints (T1/T2 contract).
 * Empty / all-agents filter omits agent_id so the backend returns every node.
 * page defaults to 1, page_size to 20 (clamped server-side to max 100).
 * Optional enabled (boolean) and status (string) are omitted when unset.
 * Optional filter dimensions (tags, certificate/egress/relay ids, sync,
 * referenced) are only sent when they carry a value.
 */
export function buildListQueryParams({ agentId, agentFilter, page, pageSize, q, enabled, status, tags, certificateId, egressProfileId, relayListenerId, sync, referenced } = {}) {
  const params = {}
  const rawAgent = agentId != null ? agentId : agentFilter
  if (rawAgent != null && rawAgent !== '' && rawAgent !== '__all__' && rawAgent !== 'all' && rawAgent !== '*') {
    params.agent_id = String(rawAgent).trim()
  }
  const pageNum = Number(page)
  params.page = Number.isInteger(pageNum) && pageNum > 0 ? pageNum : 1
  const sizeNum = Number(pageSize)
  params.page_size = Number.isInteger(sizeNum) && sizeNum > 0 ? sizeNum : 20
  const query = q == null ? '' : String(q).trim()
  if (query) params.q = query
  if (typeof enabled === 'boolean') {
    params.enabled = enabled
  }
  const statusValue = status == null ? '' : String(status).trim()
  if (statusValue) params.status = statusValue
  if (Array.isArray(tags) && tags.length) {
    params.tags = tags.map((tag) => String(tag).trim()).filter(Boolean).join(',')
  }
  for (const [key, value] of [['certificate_id', certificateId], ['egress_profile_id', egressProfileId], ['relay_listener_id', relayListenerId]]) {
    const num = Number(value)
    if (Number.isFinite(num) && num > 0) params[key] = String(Math.trunc(num))
  }
  const syncValue = sync == null ? '' : String(sync).trim()
  if (syncValue) params.sync = syncValue
  if (typeof referenced === 'boolean') {
    params.referenced = referenced
  }
  return params
}

/**
 * Normalize a paginated list envelope into { items, total, page, page_size }.
 * collectionKey is the backend array field: rules | certificates | listeners | profiles.
 */
export function normalizeListPageResponse(data = {}, collectionKey, itemNormalizer) {
  const rawItems = Array.isArray(data?.[collectionKey])
    ? data[collectionKey]
    : Array.isArray(data?.items)
      ? data.items
      : []
  const items = typeof itemNormalizer === 'function'
    ? rawItems.map((item) => itemNormalizer(item))
    : rawItems.map((item) => ({ ...item }))
  const total = Number(data?.total)
  const page = Number(data?.page)
  const pageSize = Number(data?.page_size)
  return {
    items,
    total: Number.isFinite(total) && total >= 0 ? total : items.length,
    page: Number.isInteger(page) && page > 0 ? page : 1,
    page_size: Number.isInteger(pageSize) && pageSize > 0 ? pageSize : 20
  }
}

async function fetchResourcePage(path, collectionKey, params = {}, itemNormalizer) {
  const query = buildListQueryParams(params)
  const { data } = await api.get(path, { params: query })
  return normalizeListPageResponse(data, collectionKey, itemNormalizer)
}

export async function fetchHttpRulesPage(params = {}) {
  return fetchResourcePage('/http-rules', 'rules', params, normalizeHttpRule)
}

export async function fetchL4RulesPage(params = {}) {
  return fetchResourcePage('/l4-rules', 'rules', params, normalizeL4Rule)
}

export async function fetchCertificatesPage(params = {}) {
  // Always include page so /certificates uses the paginated ListPage path
  // (legacy full list is only returned when no list query params are present).
  return fetchResourcePage('/certificates', 'certificates', params)
}

export async function fetchRelayListenersPage(params = {}) {
  return fetchResourcePage('/relay-listeners', 'listeners', params)
}

export async function verifyToken(token) {
  const { data } = await api.get('/auth/verify', {
    headers: { 'X-Panel-Token': token }
  })
  return data.ok
}

export async function fetchSystemInfo() {
  const { data } = await api.get('/info')
  return data
}

export async function exportBackup() {
  const response = await api.get('/system/backup/export', {
    responseType: 'blob',
    timeout: 0
  })
  return {
    blob: response.data,
    filename: parseDownloadFilename(response.headers['content-disposition'])
  }
}

export async function importBackup(file) {
  const formData = new FormData()
  formData.append('file', file)
  const { data } = await api.post('/system/backup/import', formData, {
    timeout: 0
  })
  return data
}

export async function fetchAgents() {
  const { data } = await api.get('/agents')
  return data.agents || []
}

export async function fetchEgressProfiles() {
  const { data } = await api.get('/egress-profiles')
  return data.profiles || []
}

export async function createEgressProfile(payload) {
  const { data } = await api.post('/egress-profiles', normalizeEgressProfilePayload(payload))
  return mutationResource(data, 'profile')
}

export async function updateEgressProfile(id, payload) {
  const { data } = await api.put(`/egress-profiles/${encodeURIComponent(id)}`, normalizeEgressProfilePayload(payload))
  return mutationResource(data, 'profile')
}

export async function deleteEgressProfile(id) {
  const { data } = await api.delete(`/egress-profiles/${encodeURIComponent(id)}`)
  return mutationResource(data, 'profile')
}

export async function fetchAgentStats(agentId) {
  const { data } = await api.get(`/agents/${encodeURIComponent(agentId)}/stats`)
  return data.stats
}

export async function updateAgent(agentId, payload) {
  const { data } = await api.patch(`/agents/${encodeURIComponent(agentId)}`, payload)
  return mutationResource(data, 'agent')
}

export async function fetchRules(agentId) {
  const { data } = await api.get(`/agents/${encodeURIComponent(agentId)}/rules`)
  return (data.rules || []).map((rule) => normalizeHttpRule(rule))
}

export async function fetchHTTPBackendProviders(agentId) {
  const { data } = await api.get(`/agents/${encodeURIComponent(agentId)}/http-backend-providers`)
  return data.providers || []
}

export async function createRule(agentId, payloadOrFrontend) {
  const payload = normalizeHttpRulePayloadObject(payloadOrFrontend && typeof payloadOrFrontend === 'object' && !Array.isArray(payloadOrFrontend)
    ? payloadOrFrontend
    : {}, { includeRelayDefaults: true })
  const { data } = await api.post(
    `/agents/${encodeURIComponent(agentId)}/rules`,
    payload,
    longRunningRequest
  )
  return mutationResource(data, 'rule', normalizeHttpRule)
}

export async function updateRule(agentId, id, payloadOrFrontend) {
  const payload = normalizeHttpRulePayloadObject(payloadOrFrontend && typeof payloadOrFrontend === 'object' && !Array.isArray(payloadOrFrontend)
    ? payloadOrFrontend
    : {}, { includeRelayDefaults: false })
  const { data } = await api.put(
    `/agents/${encodeURIComponent(agentId)}/rules/${id}`,
    payload,
    longRunningRequest
  )
  return mutationResource(data, 'rule', normalizeHttpRule)
}

export async function deleteRule(agentId, id) {
  const { data } = await api.delete(
    `/agents/${encodeURIComponent(agentId)}/rules/${id}`,
    longRunningRequest
  )
  return mutationResource(data, 'rule')
}

export async function diagnoseRule(agentId, ruleId) {
  const { data } = await api.post(
    `/agents/${encodeURIComponent(agentId)}/rules/${encodeURIComponent(ruleId)}/diagnose`,
    {},
    longRunningRequest
  )
  return mutationEnvelope(data)
}

export async function fetchAgentTask(agentId, taskId) {
  const { data } = await api.get(`/agents/${encodeURIComponent(agentId)}/tasks/${encodeURIComponent(taskId)}`)
  return data
}

export async function applyConfig(agentId) {
  const { data } = await api.post(
    `/agents/${encodeURIComponent(agentId)}/apply`,
    {},
    longRunningRequest
  )
  return mutationEnvelope(data)
}

export async function deleteAgent(agentId) {
  const { data } = await api.delete(`/agents/${encodeURIComponent(agentId)}`)
  return mutationResource(data, 'agent')
}

export async function renameAgent(agentId, newName) {
  const { data } = await api.patch(`/agents/${encodeURIComponent(agentId)}`, { name: newName })
  return mutationResource(data, 'agent')
}

export async function fetchAllAgentsRules(agentIds) {
  const results = await Promise.allSettled(
    agentIds.map((agentId) =>
      api.get(`/agents/${encodeURIComponent(agentId)}/rules`).then(({ data }) => ({
        agentId,
        rules: (data.rules || []).map((rule) => normalizeHttpRule(rule))
      }))
    )
  )
  return results
    .filter((r) => r.status === 'fulfilled')
    .map((r) => r.value)
}

export async function fetchAllAgentsL4Rules(agentIds) {
  const results = await Promise.allSettled(
    agentIds.map((agentId) =>
      api.get(`/agents/${encodeURIComponent(agentId)}/l4-rules`).then(({ data }) => ({
        agentId,
        l4Rules: (data.rules || []).map((rule) => normalizeL4Rule(rule))
      }))
    )
  )
  return results
    .filter((r) => r.status === 'fulfilled')
    .map((r) => r.value)
}

export async function checkHealth() {
  const { data } = await api.get('/health')
  return data
}

export async function fetchL4Rules(agentId) {
  const { data } = await api.get(`/agents/${encodeURIComponent(agentId)}/l4-rules`)
  return (data.rules || []).map((rule) => normalizeL4Rule(rule))
}

export async function createL4Rule(agentId, payload) {
  const normalizedPayload = normalizeL4RulePayload(payload, { includeRelayDefaults: true })
  const { data } = await api.post(`/agents/${encodeURIComponent(agentId)}/l4-rules`, normalizedPayload, longRunningRequest)
  return mutationResource(data, 'rule', normalizeL4Rule)
}

export async function updateL4Rule(agentId, id, payload) {
  const normalizedPayload = normalizeL4RulePayload(payload)
  const { data } = await api.put(`/agents/${encodeURIComponent(agentId)}/l4-rules/${id}`, normalizedPayload, longRunningRequest)
  return mutationResource(data, 'rule', normalizeL4Rule)
}

export async function deleteL4Rule(agentId, id) {
  const { data } = await api.delete(`/agents/${encodeURIComponent(agentId)}/l4-rules/${id}`, longRunningRequest)
  return mutationResource(data, 'rule')
}

export async function diagnoseL4Rule(agentId, ruleId) {
  const { data } = await api.post(`/agents/${encodeURIComponent(agentId)}/l4-rules/${encodeURIComponent(ruleId)}/diagnose`, {}, longRunningRequest)
  return mutationEnvelope(data)
}

export async function fetchCertificates(agentId) {
  const { data } = await api.get(`/agents/${encodeURIComponent(agentId)}/certificates`)
  return data.certificates || []
}

export async function createCertificate(agentId, payload) {
  const { data } = await api.post(`/agents/${encodeURIComponent(agentId)}/certificates`, payload, longRunningRequest)
  return mutationResource(data, 'certificate')
}

export async function updateCertificate(agentId, id, payload) {
  const { data } = await api.put(`/agents/${encodeURIComponent(agentId)}/certificates/${id}`, payload, longRunningRequest)
  return mutationResource(data, 'certificate')
}

export async function deleteCertificate(agentId, id) {
  const { data } = await api.delete(`/agents/${encodeURIComponent(agentId)}/certificates/${id}`, longRunningRequest)
  return mutationResource(data, 'certificate')
}

export async function issueCertificate(agentId, id) {
  const { data } = await api.post(`/agents/${encodeURIComponent(agentId)}/certificates/${id}/issue`, {}, longRunningRequest)
  return mutationResource(data, 'certificate')
}

export async function fetchAllAgentsCertificates(agentIds) {
  const results = await Promise.allSettled(
    agentIds.map((agentId) =>
      api.get(`/agents/${encodeURIComponent(agentId)}/certificates`).then(({ data }) => ({
        agentId,
        certificates: data.certificates || []
      }))
    )
  )
  return results.filter((r) => r.status === 'fulfilled').map((r) => r.value)
}

export async function fetchAllAgentsRelayListeners(agentIds) {
  const results = await Promise.allSettled(
    agentIds.map((agentId) =>
      api.get(`/agents/${encodeURIComponent(agentId)}/relay-listeners`).then(({ data }) => ({
        agentId,
        listeners: data.listeners || []
      }))
    )
  )
  return results
    .filter((r) => r.status === 'fulfilled')
    .map((r) => r.value)
}

export async function fetchRelayListeners(agentId) {
  const { data } = await api.get(`/agents/${encodeURIComponent(agentId)}/relay-listeners`)
  return data.listeners || []
}

export async function fetchAllRelayListeners() {
  const agents = await fetchAgents()
  const activeAgents = Array.isArray(agents)
    ? agents.filter((agent) => String(agent?.id || '').trim())
    : []
  const agentNameById = new Map(
    activeAgents.map((agent) => [String(agent.id), agent.name || agent.id])
  )
  const results = await Promise.allSettled(
    activeAgents.map((agent) =>
      fetchRelayListeners(agent.id).then((listeners) =>
        (listeners || []).map((listener) => ({
          ...listener,
          id: Number(listener.id),
          agent_id: String(listener.agent_id || agent.id),
          agent_name: agentNameById.get(String(listener.agent_id || agent.id)) || String(listener.agent_id || agent.id)
        }))
      )
    )
  )
  return results
    .filter((item) => item.status === 'fulfilled')
    .flatMap((item) => item.value)
}

export async function createRelayListener(agentId, payload) {
  const { data } = await api.post(
    `/agents/${encodeURIComponent(agentId)}/relay-listeners`,
    payload,
    longRunningRequest
  )
  return mutationResource(data, 'listener')
}

export async function updateRelayListener(agentId, id, payload) {
  const { data } = await api.put(
    `/agents/${encodeURIComponent(agentId)}/relay-listeners/${encodeURIComponent(id)}`,
    payload,
    longRunningRequest
  )
  return mutationResource(data, 'listener')
}

export async function deleteRelayListener(agentId, id) {
  const { data } = await api.delete(
    `/agents/${encodeURIComponent(agentId)}/relay-listeners/${encodeURIComponent(id)}`,
    longRunningRequest
  )
  return mutationResource(data, 'listener')
}

export async function fetchVersionPolicies() {
  const { data } = await api.get('/version-policies')
  return data.policies || []
}

export async function createVersionPolicy(payload) {
  const { data } = await api.post('/version-policies', payload, longRunningRequest)
  return mutationResource(data, 'policy')
}

export async function updateVersionPolicy(id, payload) {
  const { data } = await api.put(`/version-policies/${encodeURIComponent(id)}`, payload, longRunningRequest)
  return mutationResource(data, 'policy')
}

export async function deleteVersionPolicy(id) {
  const { data } = await api.delete(`/version-policies/${encodeURIComponent(id)}`, longRunningRequest)
  return mutationResource(data, 'policy')
}

export async function exportBackupSelective(include) {
  const params = new URLSearchParams()
  params.set('include', include.join(','))
  const response = await api.get(`/system/backup/export?${params.toString()}`, {
    responseType: 'blob',
    timeout: 0
  })
  return {
    blob: response.data,
    filename: parseDownloadFilename(response.headers['content-disposition'])
  }
}

export async function importBackupPreview(file) {
  const formData = new FormData()
  formData.append('file', file)
  const { data } = await api.post('/system/backup/import/preview', formData, {
    timeout: 0
  })
  return data
}

export async function fetchBackupResourceCounts() {
  const { data } = await api.get('/system/backup/counts')
  return data
}

export async function fetchTrafficPolicy(agentId) {
  const { data } = await api.get(`/agents/${encodeURIComponent(agentId)}/traffic-policy`)
  return data.policy
}

export async function updateTrafficPolicy(agentId, patch) {
  const { data } = await api.patch(`/agents/${encodeURIComponent(agentId)}/traffic-policy`, patch)
  return mutationResource(data, 'policy')
}

export async function fetchTrafficSummary(agentId) {
  const { data } = await api.get(`/agents/${encodeURIComponent(agentId)}/traffic-summary`)
  return data.summary
}

export async function fetchTrafficTrend(agentId, params = {}) {
  const query = new URLSearchParams()
  Object.entries(params || {}).forEach(([key, value]) => {
    if (value != null && value !== '') query.set(key, value)
  })
  const suffix = query.toString() ? `?${query.toString()}` : ''
  const { data } = await api.get(`/agents/${encodeURIComponent(agentId)}/traffic-trend${suffix}`)
  return data.points || []
}

export async function calibrateTraffic(agentId, payload) {
  const { data } = await api.post(`/agents/${encodeURIComponent(agentId)}/traffic-calibration`, payload)
  return mutationResource(data, 'summary')
}

export async function cleanupTraffic(agentId) {
  const { data } = await api.post(`/agents/${encodeURIComponent(agentId)}/traffic-cleanup`)
  return mutationResource(data, 'result')
}

export async function fetchTrafficOverview(agentId, granularity) {
  const params = new URLSearchParams()
  if (agentId) params.set('agent_id', agentId)
  if (granularity) params.set('granularity', granularity)
  const suffix = params.toString() ? `?${params.toString()}` : ''
  const { data } = await api.get(`/traffic-overview${suffix}`)
  return data
}

export async function fetchTrafficAggregate(agentId, granularity) {
  const params = new URLSearchParams()
  if (agentId) params.set('agent_id', agentId)
  if (granularity) params.set('granularity', granularity)
  const suffix = params.toString() ? `?${params.toString()}` : ''
  const { data } = await api.get(`/traffic-aggregate${suffix}`)
  return data
}

export async function fetchDashboardAttention() {
  const { data } = await api.get('/dashboard/attention')
  return data
}

export async function fetchPluginUIRoutes() {
  const { data } = await api.get('/plugin-ui-routes')
  return Array.isArray(data?.routes)
    ? data.routes.map((route) => ({
      id: String(route?.id || ''),
      label: String(route?.label || route?.id || ''),
      group: String(route?.group || ''),
      href: String(route?.href || '')
    })).filter((route) => route.id && route.href)
    : []
}

export async function fetchPluginResourceGroups() {
  const { data } = await api.get('/plugin-resource-groups')
  return Array.isArray(data?.groups)
    ? data.groups.map((group) => ({
      id: String(group?.id || ''),
      plugin_id: String(group?.plugin_id || ''),
      ref: String(group?.ref || ''),
      label: String(group?.label || group?.id || ''),
      description: String(group?.description || ''),
      status: String(group?.status || ''),
      ui_route_id: String(group?.ui_route_id || ''),
      ui_href: String(group?.ui_href || '')
    })).filter((group) => group.id && group.ref)
    : []
}

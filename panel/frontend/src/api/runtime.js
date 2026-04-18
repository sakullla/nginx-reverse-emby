import { api, longRunningRequest } from './client'

const SUPPORTED_LOAD_BALANCING_STRATEGIES = new Set(['adaptive', 'round_robin', 'random'])

function normalizeHttpBackends(rule = {}) {
  if (Array.isArray(rule.backends) && rule.backends.length > 0) {
    return rule.backends
      .map((backend) => ({ url: String(backend?.url || '').trim() }))
      .filter((backend) => backend.url)
  }

  const backendUrl = String(rule.backend_url || '').trim()
  return backendUrl ? [{ url: backendUrl }] : []
}

function normalizeLoadBalancingStrategy(value) {
  const strategy = String(value || '').trim().toLowerCase()
  return SUPPORTED_LOAD_BALANCING_STRATEGIES.has(strategy) ? strategy : 'adaptive'
}

function normalizeHttpRule(rule = {}) {
  const backends = normalizeHttpBackends(rule)
  return {
    ...rule,
    backend_url: backends[0]?.url || String(rule.backend_url || '').trim(),
    backends,
    load_balancing: {
      strategy: normalizeLoadBalancingStrategy(rule.load_balancing?.strategy)
    },
    relay_obfs: rule.relay_obfs === true
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

  const host = String(rule.upstream_host || '').trim()
  const port = Number(rule.upstream_port)
  return host && Number.isInteger(port) && port > 0 ? [{ host, port }] : []
}

function normalizeL4Rule(rule = {}) {
  const backends = normalizeL4Backends(rule)
  return {
    ...rule,
    upstream_host: backends[0]?.host || String(rule.upstream_host || '').trim(),
    upstream_port: backends[0]?.port || Number(rule.upstream_port) || 0,
    backends,
    load_balancing: {
      strategy: normalizeLoadBalancingStrategy(rule.load_balancing?.strategy)
    },
    relay_obfs: rule.relay_obfs === true
  }
}

function normalizeHttpRulePayloadObject(payload = {}, options = {}) {
  const includeRelayDefaults = options.includeRelayDefaults === true
  const backends = normalizeHttpBackends(payload)
  const normalizedPayload = {
    ...payload,
    frontend_url: String(payload.frontend_url || '').trim(),
    backend_url: backends[0]?.url || '',
    backends,
    load_balancing: {
      strategy: normalizeLoadBalancingStrategy(payload.load_balancing?.strategy)
    },
    tags: Array.isArray(payload.tags) ? payload.tags : [],
    enabled: payload.enabled !== false,
    proxy_redirect: payload.proxy_redirect !== false,
    pass_proxy_headers: payload.pass_proxy_headers === true,
    user_agent: String(payload.user_agent || ''),
    custom_headers: Array.isArray(payload.custom_headers) ? payload.custom_headers : []
  }
  if (Array.isArray(payload.relay_chain)) {
    normalizedPayload.relay_chain = payload.relay_chain
  } else if (includeRelayDefaults) {
    normalizedPayload.relay_chain = []
  }
  if (payload.relay_obfs != null) {
    normalizedPayload.relay_obfs = payload.relay_obfs === true
  } else if (includeRelayDefaults) {
    normalizedPayload.relay_obfs = false
  }
  return normalizedPayload
}

function normalizeLegacyHttpRulePayload(payloadOrFrontend, legacyArgs = [], options = {}) {
  const [
    backend_url,
    tags,
    enabled,
    proxy_redirect,
    pass_proxy_headers,
    user_agent,
    custom_headers,
    relay_chain,
    relay_obfs
  ] = legacyArgs

  return normalizeHttpRulePayloadObject({
    frontend_url: payloadOrFrontend,
    backend_url,
    tags,
    enabled,
    proxy_redirect,
    pass_proxy_headers,
    user_agent,
    custom_headers,
    relay_chain,
    relay_obfs
  }, options)
}

function normalizeL4RulePayload(payload = {}, options = {}) {
  const includeRelayDefaults = options.includeRelayDefaults === true
  const normalizedPayload = {
    ...payload,
    load_balancing: {
      strategy: normalizeLoadBalancingStrategy(payload.load_balancing?.strategy)
    }
  }
  if (Array.isArray(payload.relay_chain)) {
    normalizedPayload.relay_chain = payload.relay_chain
  } else if (includeRelayDefaults) {
    normalizedPayload.relay_chain = []
  }
  if (payload.relay_obfs != null) {
    normalizedPayload.relay_obfs = payload.relay_obfs === true
  } else if (includeRelayDefaults) {
    normalizedPayload.relay_obfs = false
  }
  return {
    ...normalizedPayload
  }
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

export async function fetchAgentStats(agentId) {
  const { data } = await api.get(`/agents/${encodeURIComponent(agentId)}/stats`)
  return data.stats
}

export async function fetchRules(agentId) {
  const { data } = await api.get(`/agents/${encodeURIComponent(agentId)}/rules`)
  return (data.rules || []).map((rule) => normalizeHttpRule(rule))
}

export async function createRule(agentId, payloadOrFrontend, ...legacyArgs) {
  const payload = payloadOrFrontend && typeof payloadOrFrontend === 'object' && !Array.isArray(payloadOrFrontend)
    ? normalizeHttpRulePayloadObject(payloadOrFrontend, { includeRelayDefaults: true })
    : normalizeLegacyHttpRulePayload(payloadOrFrontend, legacyArgs, { includeRelayDefaults: true })
  const { data } = await api.post(
    `/agents/${encodeURIComponent(agentId)}/rules`,
    payload,
    longRunningRequest
  )
  return normalizeHttpRule(data.rule)
}

export async function updateRule(agentId, id, payloadOrFrontend, ...legacyArgs) {
  const payload = payloadOrFrontend && typeof payloadOrFrontend === 'object' && !Array.isArray(payloadOrFrontend)
    ? normalizeHttpRulePayloadObject(payloadOrFrontend, { includeRelayDefaults: false })
    : normalizeLegacyHttpRulePayload(payloadOrFrontend, legacyArgs, { includeRelayDefaults: false })
  const { data } = await api.put(
    `/agents/${encodeURIComponent(agentId)}/rules/${id}`,
    payload,
    longRunningRequest
  )
  return normalizeHttpRule(data.rule)
}

export async function deleteRule(agentId, id) {
  const { data } = await api.delete(
    `/agents/${encodeURIComponent(agentId)}/rules/${id}`,
    longRunningRequest
  )
  return data.rule
}

export async function diagnoseRule(agentId, ruleId) {
  const { data } = await api.post(
    `/agents/${encodeURIComponent(agentId)}/rules/${encodeURIComponent(ruleId)}/diagnose`,
    {},
    longRunningRequest
  )
  return data
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
  return data
}

export async function deleteAgent(agentId) {
  const { data } = await api.delete(`/agents/${encodeURIComponent(agentId)}`)
  return data.agent
}

export async function renameAgent(agentId, newName) {
  const { data } = await api.patch(`/agents/${encodeURIComponent(agentId)}`, { name: newName })
  return data.agent
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
  return normalizeL4Rule(data.rule)
}

export async function updateL4Rule(agentId, id, payload) {
  const normalizedPayload = normalizeL4RulePayload(payload)
  const { data } = await api.put(`/agents/${encodeURIComponent(agentId)}/l4-rules/${id}`, normalizedPayload, longRunningRequest)
  return normalizeL4Rule(data.rule)
}

export async function deleteL4Rule(agentId, id) {
  const { data } = await api.delete(`/agents/${encodeURIComponent(agentId)}/l4-rules/${id}`, longRunningRequest)
  return data.rule
}

export async function diagnoseL4Rule(agentId, ruleId) {
  const { data } = await api.post(`/agents/${encodeURIComponent(agentId)}/l4-rules/${encodeURIComponent(ruleId)}/diagnose`, {}, longRunningRequest)
  return data
}

export async function fetchCertificates(agentId) {
  const { data } = await api.get(`/agents/${encodeURIComponent(agentId)}/certificates`)
  return data.certificates || []
}

export async function createCertificate(agentId, payload) {
  const { data } = await api.post(`/agents/${encodeURIComponent(agentId)}/certificates`, payload, longRunningRequest)
  return data.certificate
}

export async function updateCertificate(agentId, id, payload) {
  const { data } = await api.put(`/agents/${encodeURIComponent(agentId)}/certificates/${id}`, payload, longRunningRequest)
  return data.certificate
}

export async function deleteCertificate(agentId, id) {
  const { data } = await api.delete(`/agents/${encodeURIComponent(agentId)}/certificates/${id}`, longRunningRequest)
  return data.certificate
}

export async function issueCertificate(agentId, id) {
  const { data } = await api.post(`/agents/${encodeURIComponent(agentId)}/certificates/${id}/issue`, {}, longRunningRequest)
  return data.certificate
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
  return data.listener
}

export async function updateRelayListener(agentId, id, payload) {
  const { data } = await api.put(
    `/agents/${encodeURIComponent(agentId)}/relay-listeners/${encodeURIComponent(id)}`,
    payload,
    longRunningRequest
  )
  return data.listener
}

export async function deleteRelayListener(agentId, id) {
  const { data } = await api.delete(
    `/agents/${encodeURIComponent(agentId)}/relay-listeners/${encodeURIComponent(id)}`,
    longRunningRequest
  )
  return data.listener
}

export async function fetchVersionPolicies() {
  const { data } = await api.get('/version-policies')
  return data.policies || []
}

export async function createVersionPolicy(payload) {
  const { data } = await api.post('/version-policies', payload, longRunningRequest)
  return data.policy
}

export async function updateVersionPolicy(id, payload) {
  const { data } = await api.put(`/version-policies/${encodeURIComponent(id)}`, payload, longRunningRequest)
  return data.policy
}

export async function deleteVersionPolicy(id) {
  const { data } = await api.delete(`/version-policies/${encodeURIComponent(id)}`, longRunningRequest)
  return data.policy
}

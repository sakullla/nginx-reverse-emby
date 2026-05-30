import { api, longRunningRequest } from './client'

const SUPPORTED_LOAD_BALANCING_STRATEGIES = new Set(['adaptive', 'round_robin', 'random'])

function normalizeHttpBackends(rule = {}) {
  if (Array.isArray(rule.backends) && rule.backends.length > 0) {
    return rule.backends
      .map((backend) => ({ url: String(backend?.url || '').trim() }))
      .filter((backend) => backend.url)
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
  const type = String(payload.type || 'direct').trim().toLowerCase()
  const normalized = {
    ...payload,
    name: String(payload.name || '').trim(),
    type,
    enabled: payload.enabled !== false,
    description: String(payload.description || '').trim()
  }
  if (type === 'socks' || type === 'http') {
    normalized.proxy_url = String(payload.proxy_url || '').trim()
    delete normalized.wireguard_config
  } else if (type === 'wireguard') {
    normalized.proxy_url = ''
    normalized.wireguard_config = payload.wireguard_config || {}
  } else {
    normalized.proxy_url = ''
    delete normalized.wireguard_config
  }
  return normalized
}

function normalizeHttpRule(rule = {}) {
  const wireGuardEntryEnabled = rule.wireguard_entry_enabled === true
  const wireGuardProfileID = Number(rule.wireguard_profile_id)
  const wireGuardEntryListenPort = Number(rule.wireguard_entry_listen_port)
  const egressProfileID = normalizeEgressProfileID(rule)
  return {
    ...rule,
    backends: normalizeHttpBackends(rule),
    load_balancing: {
      strategy: normalizeLoadBalancingStrategy(rule.load_balancing?.strategy)
    },
    relay_obfs: rule.relay_obfs === true,
    wireguard_entry_enabled: wireGuardEntryEnabled,
    wireguard_profile_id: wireGuardEntryEnabled && Number.isInteger(wireGuardProfileID) && wireGuardProfileID > 0
      ? wireGuardProfileID
      : undefined,
    wireguard_entry_listen_host: wireGuardEntryEnabled
      ? String(rule.wireguard_entry_listen_host || '').trim()
      : undefined,
    wireguard_entry_listen_port: wireGuardEntryEnabled && Number.isInteger(wireGuardEntryListenPort) && wireGuardEntryListenPort > 0
      ? wireGuardEntryListenPort
      : undefined,
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
  const listenMode = ['proxy', 'wireguard'].includes(rule.listen_mode) ? rule.listen_mode : 'tcp'
  const egressProfileID = normalizeEgressProfileID(rule)
  const wireGuardInboundMode = listenMode === 'wireguard' && rule.wireguard_inbound_mode === 'transparent'
    ? 'transparent'
    : listenMode === 'wireguard'
      ? 'address'
      : ''
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
    wireguard_inbound_mode: wireGuardInboundMode,
    egress_profile_id: egressProfileID
  }
}

function normalizeRelayListenerPayload(payload = {}) {
  if (payload.transport_mode !== 'wireguard') return payload
  const { wireguard_profile_id, ...rest } = payload
  return {
    ...rest,
    transport_mode: 'wireguard',
    obfs_mode: 'off',
    allow_transport_fallback: false
  }
}

function normalizeHttpRulePayloadObject(payload = {}, options = {}) {
  const includeRelayDefaults = options.includeRelayDefaults === true
  const { backend_url, relay_chain, ...rest } = payload
  const wireGuardEntryEnabled = payload.wireguard_entry_enabled === true
  const wireGuardProfileID = Number(payload.wireguard_profile_id)
  const wireGuardEntryListenPort = Number(payload.wireguard_entry_listen_port)
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
    custom_headers: Array.isArray(payload.custom_headers) ? payload.custom_headers : [],
    wireguard_entry_enabled: wireGuardEntryEnabled
  }
  if (wireGuardEntryEnabled) {
    normalizedPayload.wireguard_profile_id = Number.isInteger(wireGuardProfileID) && wireGuardProfileID > 0
      ? wireGuardProfileID
      : undefined
    const wireGuardEntryListenHost = String(payload.wireguard_entry_listen_host || '').trim()
    if (wireGuardEntryListenHost) {
      normalizedPayload.wireguard_entry_listen_host = wireGuardEntryListenHost
    }
    if (Number.isInteger(wireGuardEntryListenPort) && wireGuardEntryListenPort > 0) {
      normalizedPayload.wireguard_entry_listen_port = wireGuardEntryListenPort
    }
  } else {
    delete normalizedPayload.wireguard_profile_id
    delete normalizedPayload.wireguard_entry_listen_host
    delete normalizedPayload.wireguard_entry_listen_port
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
    wireguard_listen_host,
    ...rest
  } = payload
  const listenMode = payload.listen_mode === 'wireguard' ? 'wireguard' : payload.listen_mode
  const wireGuardInboundMode = listenMode === 'wireguard' && payload.wireguard_inbound_mode === 'transparent'
    ? 'transparent'
    : listenMode === 'wireguard'
      ? 'address'
      : ''
  const normalizedPayload = {
    ...rest,
    backends: normalizeL4Backends(payload),
    load_balancing: {
      strategy: normalizeLoadBalancingStrategy(payload.load_balancing?.strategy)
    }
  }
  if (listenMode === 'wireguard') {
    normalizedPayload.proxy_entry_auth = { enabled: false, username: '', password: '' }
  }
  if (listenMode === 'wireguard') {
    normalizedPayload.wireguard_inbound_mode = wireGuardInboundMode
  } else {
    delete normalizedPayload.wireguard_inbound_mode
  }
  if (normalizedPayload.wireguard_profile_id != null && listenMode !== 'wireguard') {
    delete normalizedPayload.wireguard_profile_id
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
  return data.profile
}

export async function updateEgressProfile(id, payload) {
  const { data } = await api.put(`/egress-profiles/${encodeURIComponent(id)}`, normalizeEgressProfilePayload(payload))
  return data.profile
}

export async function deleteEgressProfile(id) {
  const { data } = await api.delete(`/egress-profiles/${encodeURIComponent(id)}`)
  return data.profile
}

export async function fetchAgentStats(agentId) {
  const { data } = await api.get(`/agents/${encodeURIComponent(agentId)}/stats`)
  return data.stats
}

export async function updateAgent(agentId, payload) {
  const { data } = await api.patch(`/agents/${encodeURIComponent(agentId)}`, payload)
  return data.agent
}

export async function fetchRules(agentId) {
  const { data } = await api.get(`/agents/${encodeURIComponent(agentId)}/rules`)
  return (data.rules || []).map((rule) => normalizeHttpRule(rule))
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
  return normalizeHttpRule(data.rule)
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
  const normalizedPayload = normalizeRelayListenerPayload(payload)
  const { data } = await api.post(
    `/agents/${encodeURIComponent(agentId)}/relay-listeners`,
    normalizedPayload,
    longRunningRequest
  )
  return data.listener
}

export async function updateRelayListener(agentId, id, payload) {
  const normalizedPayload = normalizeRelayListenerPayload(payload)
  const { data } = await api.put(
    `/agents/${encodeURIComponent(agentId)}/relay-listeners/${encodeURIComponent(id)}`,
    normalizedPayload,
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

export async function fetchWireGuardProfiles(agentId) {
  const { data } = await api.get(`/agents/${encodeURIComponent(agentId)}/wireguard-profiles`)
  return data.profiles || []
}

export async function createWireGuardProfile(agentId, payload) {
  const { data } = await api.post(
    `/agents/${encodeURIComponent(agentId)}/wireguard-profiles`,
    payload,
    longRunningRequest
  )
  return data.profile
}

export async function updateWireGuardProfile(agentId, id, payload) {
  const { data } = await api.put(
    `/agents/${encodeURIComponent(agentId)}/wireguard-profiles/${encodeURIComponent(id)}`,
    payload,
    longRunningRequest
  )
  return data.profile
}

export async function deleteWireGuardProfile(agentId, id) {
  const { data } = await api.delete(
    `/agents/${encodeURIComponent(agentId)}/wireguard-profiles/${encodeURIComponent(id)}`,
    longRunningRequest
  )
  return data.profile
}

export async function fetchWireGuardClients(agentId, profileId) {
  const { data } = await api.get(
    `/agents/${encodeURIComponent(agentId)}/wireguard-profiles/${encodeURIComponent(profileId)}/clients`
  )
  return data.clients || []
}

export async function createWireGuardClient(agentId, profileId, payload) {
  const { data } = await api.post(
    `/agents/${encodeURIComponent(agentId)}/wireguard-profiles/${encodeURIComponent(profileId)}/clients`,
    payload,
    longRunningRequest
  )
  return data.client
}

export async function updateWireGuardClient(agentId, profileId, clientId, payload) {
  const { data } = await api.patch(
    `/agents/${encodeURIComponent(agentId)}/wireguard-profiles/${encodeURIComponent(profileId)}/clients/${encodeURIComponent(clientId)}`,
    payload,
    longRunningRequest
  )
  return data.client
}

export async function deleteWireGuardClient(agentId, profileId, clientId) {
  const { data } = await api.delete(
    `/agents/${encodeURIComponent(agentId)}/wireguard-profiles/${encodeURIComponent(profileId)}/clients/${encodeURIComponent(clientId)}`,
    longRunningRequest
  )
  return data.client
}

export async function fetchWireGuardClientConfig(agentId, profileId, clientId) {
  const { data } = await api.get(
    `/agents/${encodeURIComponent(agentId)}/wireguard-profiles/${encodeURIComponent(profileId)}/clients/${encodeURIComponent(clientId)}/config`,
    { responseType: 'text' }
  )
  return data
}

export async function fetchWireGuardClientURI(agentId, profileId, clientId, reserved = '') {
  const suffix = reserved ? `?reserved=${encodeURIComponent(reserved)}` : ''
  const { data } = await api.get(
    `/agents/${encodeURIComponent(agentId)}/wireguard-profiles/${encodeURIComponent(profileId)}/clients/${encodeURIComponent(clientId)}/uri${suffix}`,
    { responseType: 'text' }
  )
  return data
}

export async function parseWireGuardURI(uri) {
  const { data } = await api.post('/wireguard/parse-uri', { uri })
  return data
}

export async function importWireGuardURIProfile(agentId, uri, name = '') {
  const payload = { uri }
  if (String(name || '').trim()) payload.name = String(name || '').trim()
  const { data } = await api.post(
    `/agents/${encodeURIComponent(agentId)}/wireguard-profiles/import-uri`,
    payload,
    longRunningRequest
  )
  return data.profile
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
  return data.policy
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
  return data.summary
}

export async function cleanupTraffic(agentId) {
  const { data } = await api.post(`/agents/${encodeURIComponent(agentId)}/traffic-cleanup`)
  return data.result
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

import axios from 'axios'

const isDev = import.meta.env.DEV
const sleep = (ms = 500) => new Promise((resolve) => setTimeout(resolve, ms))

function readDevMockFlags() {
  if (!isDev || typeof window === 'undefined') return {}
  try {
    return JSON.parse(localStorage.getItem('panel_dev_mock_flags') || '{}')
  } catch {
    return {}
  }
}

function createDevUnauthorizedError() {
  return new Error('401 Unauthorized')
}

const api = axios.create({
  baseURL: '/panel-api',
  timeout: 10000,
  headers: {
    'Content-Type': 'application/json'
  }
})

const longRunningRequest = {
  timeout: 0
}

api.interceptors.request.use((config) => {
  // Only inject stored token when caller did not already set X-Panel-Token
  if (!config.headers['X-Panel-Token']) {
    const token = localStorage.getItem('panel_token')
    if (token) {
      config.headers['X-Panel-Token'] = token
    }
  }
  return config
})

import { onAuthChange, notifyAuthChange } from '../utils/authEvents'

// Register so useAuthState can keep its reactive token in sync with 401s
onAuthChange((token) => { _tokenRef = token })
let _tokenRef = null
export function setApiTokenRef(fn) { _tokenRef = fn }

api.interceptors.response.use(
  (response) => response,
  (error) => {
    const status = error.response?.status
    if (status === 401) {
      localStorage.removeItem('panel_token')
      if (_tokenRef) _tokenRef(null)
      notifyAuthChange(null)
    }
    const message = error.response?.data?.message || error.message || '请求失败'
    const details = error.response?.data?.details
    const err = new Error(details ? `${message}: ${details}` : message)
    err.response = error.response
    err.status = status
    return Promise.reject(err)
  }
)

const mockAgentRegions = [
  { prefix: 'hk', name: '香港', domain: 'hk.nodes.example.com' },
  { prefix: 'sg', name: '新加坡', domain: 'sg.nodes.example.com' },
  { prefix: 'jp', name: '日本', domain: 'jp.nodes.example.com' },
  { prefix: 'us-west', name: '美西', domain: 'usw.nodes.example.com' },
  { prefix: 'us-east', name: '美东', domain: 'use.nodes.example.com' },
  { prefix: 'eu', name: '欧洲', domain: 'eu.nodes.example.com' },
  { prefix: 'cn-bj', name: '北京', domain: 'bj.nodes.example.com' },
  { prefix: 'cn-sh', name: '上海', domain: 'sh.nodes.example.com' },
  { prefix: 'au', name: '澳大利亚', domain: 'au.nodes.example.com' },
  { prefix: 'in', name: '印度', domain: 'in.nodes.example.com' },
]

const mockAgents = [
  {
    id: 'local',
    name: '本机 Agent',
    agent_url: '',
    version: '1.0.0',
    tags: ['local'],
    mode: 'local',
    status: 'online',
    is_local: true,
    last_seen_at: new Date().toISOString(),
    http_rules_count: 12,
    l4_rules_count: 3,
    // revisions match + success → 所有规则显示"已生效"
    desired_revision: 5,
    current_revision: 5,
    last_apply_revision: 5,
    last_apply_status: 'success',
    last_apply_message: ''
  },
  {
    id: 'edge-1',
    name: '边缘节点-01',
    agent_url: 'http://edge-1.example.com:8080',
    version: '1.0.0',
    tags: ['edge', 'emby'],
    mode: 'master',
    status: 'online',
    is_local: false,
    last_seen_at: new Date().toISOString(),
    http_rules_count: 8,
    l4_rules_count: 2,
    // desired > current → 所有规则显示"待同步"
    desired_revision: 3,
    current_revision: 2,
    last_apply_revision: 2,
    last_apply_status: null,
    last_apply_message: ''
  },
  {
    id: 'edge-2',
    name: '边缘节点-02',
    agent_url: 'http://edge-2.example.com:8080',
    version: '1.0.0',
    tags: ['edge'],
    mode: 'master',
    status: 'online',
    is_local: false,
    last_seen_at: new Date().toISOString(),
    http_rules_count: 5,
    l4_rules_count: 1,
    // revisions match but apply failed → 所有规则显示"应用失败"
    desired_revision: 2,
    current_revision: 2,
    last_apply_revision: 2,
    last_apply_status: 'failed',
    last_apply_message: 'nginx: configuration file test failed'
  },
  ...Array.from({ length: 30 }, (_, i) => {
    const region = mockAgentRegions[i % mockAgentRegions.length]
    const n = Math.floor(i / mockAgentRegions.length) + 1
    const id = `${region.prefix}-${String(n).padStart(2, '0')}`
    const isMasterMode = i % 5 === 0
    const rev = i + 1
    const applyFailed = i % 11 === 3
    const isPending = i % 7 === 2
    return {
      id,
      name: `${region.name}-${String(n).padStart(2, '0')}`,
      agent_url: isMasterMode ? `http://${id}.${region.domain}:8080` : '',
      version: '1.0.0',
      tags: [region.prefix],
      mode: isMasterMode ? 'master' : 'pull',
      status: i % 6 === 5 ? 'offline' : 'online',
      is_local: false,
      last_seen_at: new Date().toISOString(),
      last_seen_ip: `10.0.${Math.floor(i / 10) + 1}.${(i % 10) + 10}`,
      http_rules_count: (i % 20) + 1,
      l4_rules_count: (i % 8) + 1,
      desired_revision: rev,
      current_revision: isPending ? rev - 1 : rev,
      last_apply_revision: isPending ? rev - 1 : rev,
      last_apply_status: applyFailed ? 'failed' : 'success',
      last_apply_message: applyFailed ? 'nginx config test failed' : ''
    }
  })
]

const serviceTypes = [
  { name: 'emby', port: 8096, tags: ['emby', 'media'] },
  { name: 'jellyfin', port: 8096, tags: ['jellyfin', 'media'] },
  { name: 'plex', port: 32400, tags: ['plex', 'media'] },
  { name: 'navidrome', port: 4533, tags: ['music', 'navidrome'] },
  { name: 'nextcloud', port: 8443, tags: ['cloud', 'nextcloud'] },
  { name: 'gitea', port: 3000, tags: ['git', 'gitea'] },
  { name: 'jenkins', port: 8080, tags: ['ci', 'jenkins'] },
  { name: 'grafana', port: 3000, tags: ['monitor', 'grafana'] },
  { name: 'prometheus', port: 9090, tags: ['monitor', 'prometheus'] },
  { name: 'portainer', port: 9443, tags: ['docker', 'portainer'] },
  { name: 'traefik', port: 8080, tags: ['proxy', 'traefik'] },
  { name: 'minio', port: 9001, tags: ['storage', 'minio'] },
  { name: 'vaultwarden', port: 80, tags: ['password', 'vaultwarden'] },
  { name: 'homeassistant', port: 8123, tags: ['iot', 'home'] },
  { name: 'pihole', port: 80, tags: ['dns', 'pihole'] },
  { name: 'syncthing', port: 8384, tags: ['sync', 'syncthing'] },
  { name: 'code-server', port: 8080, tags: ['ide', 'vscode'] },
  { name: 'uptime-kuma', port: 3001, tags: ['monitor', 'uptime'] },
  { name: 'wikijs', port: 3000, tags: ['wiki', 'docs'] },
  { name: 'bookstack', port: 80, tags: ['wiki', 'bookstack'] },
]

const domains = [
  'homelab.mydomain.com',
  'services.internal.company.io',
  'infra.production.aws.cloud',
  'apps.staging.devops.net',
  'cluster.k8s.platform.local'
]

const envPrefixes = ['prod', 'staging', 'dev', 'test', '']

function generateMockRules(count) {
  const rules = []
  for (let i = 1; i <= count; i++) {
    const svc = serviceTypes[i % serviceTypes.length]
    const domain = domains[i % domains.length]
    const env = envPrefixes[i % envPrefixes.length]
    const instance = Math.floor(i / serviceTypes.length) || ''
    const envPrefix = env ? `${env}.` : ''
    const subdomain = `${svc.name}${instance}.${envPrefix}proxy`
    const ip = `192.168.${Math.floor(i / 50) + 1}.${(i % 50) + 10}`
    rules.push({
      id: i,
      frontend_url: `https://${subdomain}.${domain}`,
      backend_url: `http://${ip}:${svc.port}`,
      enabled: i % 7 !== 0,
      tags: [...svc.tags, i % 3 === 0 ? 'https' : 'http'],
      proxy_redirect: true,
      pass_proxy_headers: true,
      user_agent: '',
      custom_headers: [],
      relay_chain: []
    })
  }
  return rules
}

const mockRulesByAgent = {
  local: generateMockRules(100).map((r, index) => ({
    ...r,
    revision: index < 80 ? 5 : 4
  })),
  'edge-1': generateMockRules(30).map((r, index) => ({
    ...r,
    id: r.id + 1000,
    revision: r.enabled && index < 8 ? 3 : 2
  })),
  'edge-2': generateMockRules(15).map((r, index) => ({
    ...r,
    id: r.id + 2000,
    revision: r.enabled && index < 5 ? 2 : 1
  }))
}

function getMockStats(agentId) {
  return {
    activeConnections: agentId === 'local' ? '12' : '4',
    totalRequests: agentId === 'local' ? '8.4K' : '1.3K',
    status: '正常 (Mock)'
  }
}

export async function verifyToken(token) {
  if (isDev) {
    await sleep()
    const flags = readDevMockFlags()
    if (flags.force401OnVerify || token === 'expired-401') {
      throw createDevUnauthorizedError()
    }
    return token === 'admin'
  }
  const { data } = await api.get('/auth/verify', {
    headers: { 'X-Panel-Token': token }
  })
  return data.ok
}

export async function fetchSystemInfo() {
  if (isDev) {
    await sleep()
    return {
      role: 'master',
      local_apply_runtime: 'go-agent',
      default_agent_id: 'local',
      local_agent_enabled: true,
      proxy_headers_globally_disabled: false
    }
  }
  const { data } = await api.get('/info')
  return data
}

export async function fetchAgents() {
  if (isDev) {
    await sleep()
    return [...mockAgents]
  }
  const { data } = await api.get('/agents')
  return data.agents || []
}

export async function fetchAgentStats(agentId) {
  if (isDev) {
    await sleep()
    return getMockStats(agentId)
  }
  const { data } = await api.get(`/agents/${encodeURIComponent(agentId)}/stats`)
  return data.stats
}

export async function fetchRules(agentId) {
  if (isDev) {
    await sleep()
    return [...(mockRulesByAgent[agentId] || [])]
  }
  const { data } = await api.get(`/agents/${encodeURIComponent(agentId)}/rules`)
  return data.rules || []
}

export async function createRule(
  agentId,
  frontend_url,
  backend_url,
  tags = [],
  enabled = true,
  proxy_redirect = true,
  pass_proxy_headers = true,
  user_agent = '',
  custom_headers = [],
  relay_chain = []
) {
  if (isDev) {
    await sleep()
    const nextRule = {
      id: Date.now(),
      frontend_url,
      backend_url,
      tags,
      enabled,
      proxy_redirect,
      pass_proxy_headers,
      user_agent,
      custom_headers,
      relay_chain
    }
    mockRulesByAgent[agentId] = mockRulesByAgent[agentId] || []
    mockRulesByAgent[agentId].push(nextRule)
    return nextRule
  }
  const { data } = await api.post(
    `/agents/${encodeURIComponent(agentId)}/rules`,
    { frontend_url, backend_url, tags, enabled, proxy_redirect, pass_proxy_headers, user_agent, custom_headers, relay_chain },
    longRunningRequest
  )
  return data.rule
}

export async function updateRule(
  agentId,
  id,
  frontend_url,
  backend_url,
  tags,
  enabled,
  proxy_redirect,
  pass_proxy_headers,
  user_agent,
  custom_headers,
  relay_chain
) {
  if (isDev) {
    await sleep()
    const list = mockRulesByAgent[agentId] || []
    const index = list.findIndex((rule) => rule.id === id)
    if (index !== -1) {
      const nextRule = { ...list[index] }
      if (frontend_url !== undefined) nextRule.frontend_url = frontend_url
      if (backend_url !== undefined) nextRule.backend_url = backend_url
      if (tags !== undefined) nextRule.tags = tags
      if (enabled !== undefined) nextRule.enabled = enabled
      if (proxy_redirect !== undefined) nextRule.proxy_redirect = proxy_redirect
      if (pass_proxy_headers !== undefined) nextRule.pass_proxy_headers = pass_proxy_headers
      if (user_agent !== undefined) nextRule.user_agent = user_agent
      if (custom_headers !== undefined) nextRule.custom_headers = custom_headers
      if (relay_chain !== undefined) nextRule.relay_chain = relay_chain
      list[index] = nextRule
      return nextRule
    }
    return null
  }
  const { data } = await api.put(
    `/agents/${encodeURIComponent(agentId)}/rules/${id}`,
    { frontend_url, backend_url, tags, enabled, proxy_redirect, pass_proxy_headers, user_agent, custom_headers, relay_chain },
    longRunningRequest
  )
  return data.rule
}

export async function deleteRule(agentId, id) {
  if (isDev) {
    await sleep()
    const list = mockRulesByAgent[agentId] || []
    const index = list.findIndex((rule) => rule.id === id)
    if (index === -1) return null
    return list.splice(index, 1)[0]
  }
  const { data } = await api.delete(
    `/agents/${encodeURIComponent(agentId)}/rules/${id}`,
    longRunningRequest
  )
  return data.rule
}

export async function applyConfig(agentId) {
  if (isDev) {
    await sleep(1200)
    if (agentId === 'local') {
      return { ok: true, message: 'applied' }
    }
    return { ok: true, message: 'waiting for heartbeat' }
  }
  const { data } = await api.post(
    `/agents/${encodeURIComponent(agentId)}/apply`,
    {},
    longRunningRequest
  )
  return data
}

export async function deleteAgent(agentId) {
  if (isDev) {
    await sleep()
    const index = mockAgents.findIndex((agent) => agent.id === agentId)
    if (index !== -1) {
      delete mockRulesByAgent[agentId]
      return mockAgents.splice(index, 1)[0]
    }
    return null
  }
  const { data } = await api.delete(`/agents/${encodeURIComponent(agentId)}`)
  return data.agent
}

export async function renameAgent(agentId, newName) {
  if (isDev) {
    await sleep()
    const agent = mockAgents.find((a) => a.id === agentId)
    if (agent) agent.name = newName
    return agent
  }
  const { data } = await api.patch(`/agents/${encodeURIComponent(agentId)}`, { name: newName })
  return data.agent
}

export async function fetchAllAgentsRules(agentIds) {
  if (isDev) {
    await sleep()
    return agentIds.map((agentId) => ({
      agentId,
      rules: [...(mockRulesByAgent[agentId] || [])]
    }))
  }
  const results = await Promise.allSettled(
    agentIds.map((agentId) =>
      api.get(`/agents/${encodeURIComponent(agentId)}/rules`).then(({ data }) => ({
        agentId,
        rules: data.rules || []
      }))
    )
  )
  return results
    .filter((r) => r.status === 'fulfilled')
    .map((r) => r.value)
}

// L4 Rules
const mockL4RulesByAgent = {
  local: [
    { id: 1, protocol: 'tcp', listen_host: '0.0.0.0', listen_port: 25565, upstream_host: '192.168.1.20', upstream_port: 25565, relay_chain: [], enabled: true, tags: ['TCP', ':25565', 'game'] }
  ],
  'edge-1': [
    { id: 1, protocol: 'udp', listen_host: '0.0.0.0', listen_port: 51820, upstream_host: '10.0.0.20', upstream_port: 51820, relay_chain: [], enabled: true, tags: ['UDP', ':51820', 'vpn'] }
  ]
}
let mockL4IdCounter = 1

export async function fetchAllAgentsL4Rules(agentIds) {
  if (isDev) {
    await sleep()
    return agentIds.map((agentId) => ({
      agentId,
      l4Rules: [...(mockL4RulesByAgent[agentId] || [])]
    }))
  }
  const results = await Promise.allSettled(
    agentIds.map((agentId) =>
      api.get(`/agents/${encodeURIComponent(agentId)}/l4-rules`).then(({ data }) => ({
        agentId,
        l4Rules: data.rules || []
      }))
    )
  )
  return results
    .filter((r) => r.status === 'fulfilled')
    .map((r) => r.value)
}

export async function checkHealth() {
  if (isDev) return { ok: true }
  const { data } = await api.get('/health')
  return data
}

export async function fetchL4Rules(agentId) {
  if (isDev) { await sleep(); return [...(mockL4RulesByAgent[agentId] || [])] }
  const { data } = await api.get(`/agents/${encodeURIComponent(agentId)}/l4-rules`)
  return data.rules || []
}

export async function createL4Rule(agentId, payload) {
  if (isDev) {
    await sleep()
    const item = { ...payload, id: ++mockL4IdCounter }
    mockL4RulesByAgent[agentId] = mockL4RulesByAgent[agentId] || []
    mockL4RulesByAgent[agentId].push(item)
    return item
  }
  const { data } = await api.post(`/agents/${encodeURIComponent(agentId)}/l4-rules`, payload, longRunningRequest)
  return data.rule
}

export async function updateL4Rule(agentId, id, payload) {
  if (isDev) {
    await sleep()
    const list = mockL4RulesByAgent[agentId] || []
    const idx = list.findIndex((r) => r.id === id)
    if (idx !== -1) { list[idx] = { ...list[idx], ...payload }; return list[idx] }
    return null
  }
  const { data } = await api.put(`/agents/${encodeURIComponent(agentId)}/l4-rules/${id}`, payload, longRunningRequest)
  return data.rule
}

export async function deleteL4Rule(agentId, id) {
  if (isDev) {
    await sleep()
    const list = mockL4RulesByAgent[agentId] || []
    const idx = list.findIndex((r) => r.id === id)
    if (idx !== -1) return list.splice(idx, 1)[0]
    return null
  }
  const { data } = await api.delete(`/agents/${encodeURIComponent(agentId)}/l4-rules/${id}`, longRunningRequest)
  return data.rule
}

// Certificates (per-agent, like HTTP/L4)
const mockCertsByAgent = {
  local: [
    { id: 1, domain: 'media.example.com', enabled: true, scope: 'domain', issuer_mode: 'master_cf_dns', usage: 'https', certificate_type: 'acme', self_signed: false, status: 'active', last_issue_at: new Date().toISOString(), last_error: '', tags: ['media', 'streaming'] },
    { id: 2, domain: 'pan.example.com', enabled: false, scope: 'domain', issuer_mode: 'master_cf_dns', usage: 'relay_tunnel', certificate_type: 'uploaded', self_signed: true, status: 'active', last_issue_at: new Date().toISOString(), last_error: '', tags: ['cloud'] }
  ],
  'edge-1': [
    { id: 1, domain: 'media.example.com', enabled: true, scope: 'domain', issuer_mode: 'master_cf_dns', usage: 'https', certificate_type: 'acme', self_signed: false, status: 'active', last_issue_at: new Date().toISOString(), last_error: '', tags: ['media'] },
    { id: 2, domain: '192.168.1.100', enabled: true, scope: 'ip', issuer_mode: 'local_http01', usage: 'relay_ca', certificate_type: 'internal_ca', self_signed: true, status: 'pending', last_issue_at: '', last_error: '', tags: ['internal'] }
  ],
  'edge-2': [
    { id: 1, domain: 'media.example.com', enabled: true, scope: 'domain', issuer_mode: 'local_http01', usage: 'mixed', certificate_type: 'acme', self_signed: false, status: 'error', last_issue_at: '', last_error: 'ACME challenge failed', tags: ['media'] }
  ]
}
let mockCertIdCounter = 10

export async function fetchCertificates(agentId) {
  if (isDev) { await sleep(); return [...(mockCertsByAgent[agentId] || [])] }
  const { data } = await api.get(`/agents/${encodeURIComponent(agentId)}/certificates`)
  return data.certificates || []
}

export async function createCertificate(agentId, payload) {
  if (isDev) {
    await sleep()
    const item = { ...payload, id: ++mockCertIdCounter, status: 'pending', last_issue_at: '', last_error: '' }
    mockCertsByAgent[agentId] = mockCertsByAgent[agentId] || []
    mockCertsByAgent[agentId].push(item)
    return item
  }
  const { data } = await api.post(`/agents/${encodeURIComponent(agentId)}/certificates`, payload, longRunningRequest)
  return data.certificate
}

export async function updateCertificate(agentId, id, payload) {
  if (isDev) {
    await sleep()
    const list = mockCertsByAgent[agentId] || []
    const idx = list.findIndex((c) => c.id === id)
    if (idx !== -1) { list[idx] = { ...list[idx], ...payload }; return list[idx] }
    return null
  }
  const { data } = await api.put(`/agents/${encodeURIComponent(agentId)}/certificates/${id}`, payload, longRunningRequest)
  return data.certificate
}

export async function deleteCertificate(agentId, id) {
  if (isDev) {
    await sleep()
    const list = mockCertsByAgent[agentId] || []
    const idx = list.findIndex((c) => c.id === id)
    if (idx !== -1) return list.splice(idx, 1)[0]
    return null
  }
  const { data } = await api.delete(`/agents/${encodeURIComponent(agentId)}/certificates/${id}`, longRunningRequest)
  return data.certificate
}

export async function issueCertificate(agentId, id) {
  if (isDev) {
    await sleep(800)
    const list = mockCertsByAgent[agentId] || []
    const idx = list.findIndex((c) => c.id === id)
    if (idx !== -1) { list[idx] = { ...list[idx], status: 'active', last_issue_at: new Date().toISOString(), last_error: '' }; return list[idx] }
    return null
  }
  const { data } = await api.post(`/agents/${encodeURIComponent(agentId)}/certificates/${id}/issue`, {}, longRunningRequest)
  return data.certificate
}

export async function fetchAllAgentsCertificates(agentIds) {
  if (isDev) {
    await sleep()
    return agentIds.map((agentId) => ({ agentId, certificates: [...(mockCertsByAgent[agentId] || [])] }))
  }
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

const mockRelayListenersByAgent = {
  local: [
    {
      id: 1,
      agent_id: 'local',
      name: '本地中继',
      listen_host: '0.0.0.0',
      listen_port: 9443,
      enabled: true,
      certificate_id: 1,
      tls_mode: 'pin_or_ca',
      pin_set: [{ type: 'spki_sha256', value: 'local-pin-1' }],
      trusted_ca_certificate_ids: [],
      allow_self_signed: false,
      tags: ['relay', 'local'],
      revision: 1
    }
  ],
  'edge-1': [
    {
      id: 2,
      agent_id: 'edge-1',
      name: '边缘中继',
      listen_host: '0.0.0.0',
      listen_port: 10443,
      enabled: true,
      certificate_id: 1,
      tls_mode: 'ca_only',
      pin_set: [{ type: 'spki_sha256', value: 'edge-pin-1' }],
      trusted_ca_certificate_ids: [1],
      allow_self_signed: false,
      tags: ['relay', 'edge'],
      revision: 3
    }
  ]
}

let mockRelayListenerIdCounter = 2

function normalizeMockRelayListenerPayload(payload = {}) {
  const pinSet = Array.isArray(payload.pin_set)
    ? payload.pin_set
      .map((entry) => ({
        type: String(entry?.type || '').trim(),
        value: String(entry?.value || '').trim()
      }))
      .filter((entry) => entry.type && entry.value)
    : []
  const trustedCa = Array.isArray(payload.trusted_ca_certificate_ids)
    ? [...new Set(payload.trusted_ca_certificate_ids
      .map((id) => Number(id))
      .filter((id) => Number.isInteger(id) && id > 0))]
    : []
  if (!pinSet.length && !trustedCa.length) {
    throw new Error('pin_set and trusted_ca_certificate_ids cannot both be empty')
  }
  return {
    ...payload,
    certificate_id: payload.certificate_id == null ? null : Number(payload.certificate_id),
    pin_set: pinSet,
    trusted_ca_certificate_ids: trustedCa,
    tls_mode: String(payload.tls_mode || 'pin_or_ca')
  }
}

function ensureDevRelayAgentExists(agentId) {
  const exists = mockAgents.some((agent) => String(agent.id) === String(agentId))
  if (!exists) {
    throw new Error(`Agent not found: ${agentId}`)
  }
}

export async function fetchRelayListeners(agentId) {
  if (isDev) {
    await sleep()
    ensureDevRelayAgentExists(agentId)
    return [...(mockRelayListenersByAgent[agentId] || [])]
  }
  const { data } = await api.get(`/agents/${encodeURIComponent(agentId)}/relay-listeners`)
  return data.listeners || []
}

export async function fetchAllRelayListeners() {
  if (isDev) {
    await sleep()
    const agentNameById = new Map(mockAgents.map((agent) => [String(agent.id), agent.name || agent.id]))
    return Object.entries(mockRelayListenersByAgent).flatMap(([agentId, listeners]) =>
      (listeners || []).map((listener) => ({
        ...listener,
        id: Number(listener.id),
        agent_id: String(listener.agent_id || agentId),
        agent_name: agentNameById.get(String(listener.agent_id || agentId)) || String(listener.agent_id || agentId)
      }))
    )
  }
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
  if (isDev) {
    await sleep()
    ensureDevRelayAgentExists(agentId)
    const normalizedPayload = normalizeMockRelayListenerPayload(payload)
    const item = {
      id: ++mockRelayListenerIdCounter,
      agent_id: agentId,
      revision: Date.now(),
      ...normalizedPayload
    }
    mockRelayListenersByAgent[agentId] = mockRelayListenersByAgent[agentId] || []
    mockRelayListenersByAgent[agentId].push(item)
    return item
  }
  const { data } = await api.post(
    `/agents/${encodeURIComponent(agentId)}/relay-listeners`,
    payload,
    longRunningRequest
  )
  return data.listener
}

export async function updateRelayListener(agentId, id, payload) {
  if (isDev) {
    await sleep()
    ensureDevRelayAgentExists(agentId)
    const list = mockRelayListenersByAgent[agentId] || []
    const idx = list.findIndex((item) => String(item.id) === String(id))
    if (idx === -1) return null
    const normalizedPayload = normalizeMockRelayListenerPayload({ ...list[idx], ...payload })
    list[idx] = { ...list[idx], ...normalizedPayload }
    return list[idx]
  }
  const { data } = await api.put(
    `/agents/${encodeURIComponent(agentId)}/relay-listeners/${encodeURIComponent(id)}`,
    payload,
    longRunningRequest
  )
  return data.listener
}

export async function deleteRelayListener(agentId, id) {
  if (isDev) {
    await sleep()
    ensureDevRelayAgentExists(agentId)
    const list = mockRelayListenersByAgent[agentId] || []
    const idx = list.findIndex((item) => String(item.id) === String(id))
    if (idx === -1) return null
    return list.splice(idx, 1)[0]
  }
  const { data } = await api.delete(
    `/agents/${encodeURIComponent(agentId)}/relay-listeners/${encodeURIComponent(id)}`,
    longRunningRequest
  )
  return data.listener
}

const mockVersionPolicies = [
  {
    id: 'stable',
    channel: 'stable',
    desired_version: '1.3.0',
    packages: [
      { platform: 'linux-amd64', url: 'https://example.com/stable/linux-amd64.tar.gz', sha256: 'abc123' },
      { platform: 'linux-arm64', url: 'https://example.com/stable/linux-arm64.tar.gz', sha256: 'def456' }
    ],
    tags: ['default']
  },
  {
    id: 'canary',
    channel: 'canary',
    desired_version: '1.4.0-rc1',
    packages: [{ platform: 'linux-amd64', url: 'https://example.com/canary/linux-amd64.tar.gz', sha256: 'ghi789' }],
    tags: ['test']
  }
]

let mockVersionPolicyIdCounter = 10

function normalizeMockVersionPolicyPayload(payload = {}) {
  const desiredVersion = String(payload.desired_version || '').trim()
  if (!desiredVersion) {
    throw new Error('desired_version is required')
  }
  const packages = Array.isArray(payload.packages)
    ? payload.packages.map((item) => ({
      platform: String(item?.platform || '').trim(),
      url: String(item?.url || '').trim(),
      sha256: String(item?.sha256 || '').trim()
    }))
    : []
  const hasPartialPackage = packages.some((item) => !item.platform || !item.url || !item.sha256)
  if (hasPartialPackage) {
    throw new Error('packages entries require platform, url and sha256')
  }
  return {
    ...payload,
    desired_version: desiredVersion,
    packages
  }
}

export async function fetchVersionPolicies() {
  if (isDev) {
    await sleep()
    return [...mockVersionPolicies]
  }
  const { data } = await api.get('/version-policies')
  return data.policies || []
}

export async function createVersionPolicy(payload) {
  if (isDev) {
    await sleep()
    const normalizedPayload = normalizeMockVersionPolicyPayload(payload)
    const item = { id: `vp-${++mockVersionPolicyIdCounter}`, ...normalizedPayload }
    mockVersionPolicies.push(item)
    return item
  }
  const { data } = await api.post('/version-policies', payload, longRunningRequest)
  return data.policy
}

export async function updateVersionPolicy(id, payload) {
  if (isDev) {
    await sleep()
    const idx = mockVersionPolicies.findIndex((item) => String(item.id) === String(id))
    if (idx === -1) return null
    const normalizedPayload = normalizeMockVersionPolicyPayload({ ...mockVersionPolicies[idx], ...payload })
    mockVersionPolicies[idx] = { ...mockVersionPolicies[idx], ...normalizedPayload }
    return mockVersionPolicies[idx]
  }
  const { data } = await api.put(`/version-policies/${encodeURIComponent(id)}`, payload, longRunningRequest)
  return data.policy
}

export async function deleteVersionPolicy(id) {
  if (isDev) {
    await sleep()
    const idx = mockVersionPolicies.findIndex((item) => String(item.id) === String(id))
    if (idx === -1) return null
    return mockVersionPolicies.splice(idx, 1)[0]
  }
  const { data } = await api.delete(`/version-policies/${encodeURIComponent(id)}`, longRunningRequest)
  return data.policy
}

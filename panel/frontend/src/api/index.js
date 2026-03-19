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
  const token = localStorage.getItem('panel_token')
  if (token) {
    config.headers['X-Panel-Token'] = token
  }
  return config
})

api.interceptors.response.use(
  (response) => response,
  (error) => {
    if (error.response?.status === 401) {
      localStorage.removeItem('panel_token')
    }
    const message = error.response?.data?.message || error.message || '请求失败'
    const details = error.response?.data?.details
    return Promise.reject(new Error(details ? `${message}: ${details}` : message))
  }
)

const mockAgents = [
  {
    id: 'local',
    name: '本机 Agent',
    agent_url: 'http://127.0.0.1:8080',
    version: '1.0.0',
    tags: ['local'],
    mode: 'local',
    status: 'online',
    is_local: true,
    last_seen_at: new Date().toISOString()
  },
  {
    id: 'edge-1',
    name: '边缘节点-01',
    agent_url: 'http://edge-1.example.com:8080',
    version: '1.0.0',
    tags: ['edge', 'emby'],
    mode: 'pull',
    status: 'online',
    is_local: false,
    last_seen_at: new Date().toISOString()
  }
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
      proxy_redirect: true
    })
  }
  return rules
}

const mockRulesByAgent = {
  local: generateMockRules(100),
  'edge-1': generateMockRules(30).map(r => ({ ...r, id: r.id + 1000 }))
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
      default_agent_id: 'local',
      local_agent_enabled: true
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
  proxy_redirect = true
) {
  if (isDev) {
    await sleep()
    const nextRule = {
      id: Date.now(),
      frontend_url,
      backend_url,
      tags,
      enabled,
      proxy_redirect
    }
    mockRulesByAgent[agentId] = mockRulesByAgent[agentId] || []
    mockRulesByAgent[agentId].push(nextRule)
    return nextRule
  }
  const { data } = await api.post(
    `/agents/${encodeURIComponent(agentId)}/rules`,
    { frontend_url, backend_url, tags, enabled, proxy_redirect },
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
  proxy_redirect
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
      list[index] = nextRule
      return nextRule
    }
    return null
  }
  const { data } = await api.put(
    `/agents/${encodeURIComponent(agentId)}/rules/${id}`,
    { frontend_url, backend_url, tags, enabled, proxy_redirect },
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

export async function checkHealth() {
  if (isDev) return { ok: true }
  const { data } = await api.get('/health')
  return data
}

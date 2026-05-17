import axios from 'axios'
import { clearAuthToken } from '../authState.js'

const isDev = import.meta.env.DEV
const sleep = (ms = 500) => new Promise((resolve) => setTimeout(resolve, ms))
const SYSTEM_RELAY_CA_TAG = 'system:relay-ca'
const SYSTEM_RELAY_TUNNEL_TAG = 'system:auto-relay-tunnel'
const SUPPORTED_LOAD_BALANCING_STRATEGIES = new Set(['adaptive', 'round_robin', 'random'])

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
  if (!config.headers['X-Panel-Token']) {
    const token = localStorage.getItem('panel_token')
    if (token) {
      config.headers['X-Panel-Token'] = token
    }
  }
  return config
})

api.interceptors.response.use(
  (response) => response,
  (error) => {
    const status = error.response?.status
    if (status === 401) {
      clearAuthToken()
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
    runtime_package_version: '1.0.0',
    runtime_package_platform: 'linux',
    runtime_package_arch: 'amd64',
    runtime_package_sha256: 'local-runtime-sha-1234567890',
    desired_package_sha256: 'local-runtime-sha-1234567890',
    package_sync_status: 'aligned',
    tags: ['local'],
    mode: 'local',
    status: 'online',
    is_local: true,
    outbound_proxy_url: '',
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
    runtime_package_version: '1.0.0',
    runtime_package_platform: 'linux',
    runtime_package_arch: 'amd64',
    runtime_package_sha256: 'edge-runtime-sha-1234567890',
    desired_package_sha256: 'edge-desired-sha-abcdef1234',
    package_sync_status: 'pending',
    tags: ['edge', 'emby'],
    mode: 'master',
    status: 'online',
    is_local: false,
    outbound_proxy_url: 'socks://user:xxxxx@127.0.0.1:1080',
    traffic_stats_interval: '30s',
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
    runtime_package_version: '1.0.0',
    runtime_package_platform: 'linux',
    runtime_package_arch: 'arm64',
    runtime_package_sha256: 'edge2-runtime-sha-1234567890',
    desired_package_sha256: 'edge2-runtime-sha-1234567890',
    package_sync_status: 'aligned',
    tags: ['edge'],
    mode: 'master',
    status: 'online',
    is_local: false,
    outbound_proxy_url: '',
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
      runtime_package_version: '1.0.0',
      runtime_package_platform: i % 2 === 0 ? 'linux' : 'darwin',
      runtime_package_arch: i % 3 === 0 ? 'arm64' : 'amd64',
      runtime_package_sha256: `runtime-sha-${id}`,
      desired_package_sha256: i % 4 === 0 ? `desired-sha-${id}` : `runtime-sha-${id}`,
      package_sync_status: i % 4 === 0 ? 'pending' : 'aligned',
      tags: [region.prefix],
      mode: isMasterMode ? 'master' : 'pull',
      status: i % 6 === 5 ? 'offline' : 'online',
      is_local: false,
      outbound_proxy_url: '',
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
      backends: [{ url: `http://${ip}:${svc.port}` }],
      load_balancing: { strategy: 'adaptive' },
      enabled: i % 7 !== 0,
      tags: [...svc.tags, i % 3 === 0 ? 'https' : 'http'],
      proxy_redirect: true,
      pass_proxy_headers: true,
      user_agent: '',
      custom_headers: [],
      relay_layers: [],
      relay_obfs: false
    })
  }
  return rules
}

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

function normalizeHttpRule(rule = {}) {
  return {
    ...rule,
    backends: normalizeHttpBackends(rule),
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
  return []
}

function normalizeL4Rule(rule = {}) {
  const listenMode = ['proxy', 'wireguard'].includes(rule.listen_mode) ? rule.listen_mode : 'tcp'
  const proxyEgressMode = listenMode === 'proxy'
    ? String(rule.proxy_egress_mode || 'relay')
    : listenMode === 'wireguard'
      ? String(rule.proxy_egress_mode || '')
      : ''
  const proxyEntryMode = listenMode === 'proxy' || (listenMode === 'wireguard' && proxyEgressMode)
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
    proxy_egress_mode: proxyEgressMode,
    proxy_egress_url: proxyEntryMode ? String(rule.proxy_egress_url || '') : '',
    wireguard_egress_uri: proxyEntryMode && proxyEgressMode === 'wireguard'
      ? String(rule.wireguard_egress_uri || '')
      : ''
  }
}

function normalizeRelayListenerPayloadForTransport(payload = {}) {
  if (payload.transport_mode !== 'wireguard') return payload
  return {
    ...payload,
    transport_mode: 'wireguard',
    obfs_mode: 'off',
    allow_transport_fallback: false
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
  return normalizedPayload
}

function normalizeL4RulePayload(payload = {}, options = {}) {
  const includeRelayDefaults = options.includeRelayDefaults === true
  const { upstream_host, upstream_port, relay_chain, ...rest } = payload
  const listenMode = payload.listen_mode === 'wireguard' ? 'wireguard' : payload.listen_mode
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
  return {
    ...normalizedPayload
  }
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
  const rx = agentId === 'local' ? 4_625_219_584 : 712_441_856
  const tx = agentId === 'local' ? 9_219_637_248 : 1_284_505_600
  const httpRules = mockRulesByAgent[agentId] || []
  const l4Rules = mockL4RulesByAgent[agentId] || []
  const relayListeners = mockRelayListenersByAgent[agentId] || []
  const bucketMap = (items, rxBase, txBase) => Object.fromEntries(items.map((item, index) => [
    String(item.id),
    {
      rx_bytes: Math.floor(rxBase / (index + 2)),
      tx_bytes: Math.floor(txBase / (index + 2))
    }
  ]))
  return {
    activeConnections: agentId === 'local' ? '12' : '4',
    totalRequests: agentId === 'local' ? '8.4K' : '1.3K',
    status: '正常 (Mock)',
    traffic: {
      total: { rx_bytes: rx, tx_bytes: tx },
      http: { rx_bytes: Math.floor(rx * 0.62), tx_bytes: Math.floor(tx * 0.66) },
      l4: { rx_bytes: Math.floor(rx * 0.25), tx_bytes: Math.floor(tx * 0.22) },
      relay: { rx_bytes: Math.floor(rx * 0.13), tx_bytes: Math.floor(tx * 0.12) },
      http_rules: bucketMap(httpRules, Math.floor(rx * 0.62), Math.floor(tx * 0.66)),
      l4_rules: bucketMap(l4Rules, Math.floor(rx * 0.25), Math.floor(tx * 0.22)),
      relay_listeners: bucketMap(relayListeners, Math.floor(rx * 0.13), Math.floor(tx * 0.12))
    }
  }
}

const mockTrafficPolicies = Object.fromEntries(mockAgents.map((agent) => [
  agent.id,
  {
    agent_id: agent.id,
    direction: agent.id === 'edge-1' ? 'max' : 'both',
    cycle_start_day: 1,
    monthly_quota_bytes: agent.id === 'edge-1' ? 2 * 1024 * 1024 * 1024 * 1024 : null,
    block_when_exceeded: agent.id === 'edge-1',
    hourly_retention_days: 180,
    daily_retention_months: 24,
    monthly_retention_months: null
  }
]))

function trafficAccountedBytes(bucket, direction = 'both') {
  const rx = Number(bucket?.rx_bytes) || 0
  const tx = Number(bucket?.tx_bytes) || 0
  switch (direction) {
    case 'rx':
      return rx
    case 'tx':
      return tx
    case 'max':
      return Math.max(rx, tx)
    case 'both':
    default:
      return rx + tx
  }
}

function scopeTypeLabel(scopeType) {
  switch (scopeType) {
    case 'http_rule':
      return 'HTTP'
    case 'l4_rule':
      return 'L4'
    case 'relay_listener':
      return 'Relay'
    default:
      return scopeType
  }
}

function ensureMockTrafficPolicy(agentId) {
  const id = String(agentId || 'local')
  if (!mockTrafficPolicies[id]) {
    mockTrafficPolicies[id] = {
      agent_id: id,
      direction: 'both',
      cycle_start_day: 1,
      monthly_quota_bytes: null,
      block_when_exceeded: false,
      hourly_retention_days: 180,
      daily_retention_months: 24,
      monthly_retention_months: null
    }
  }
  return mockTrafficPolicies[id]
}

function buildMockTrafficSummary(agentId) {
  const id = String(agentId || 'local')
  const policy = ensureMockTrafficPolicy(id)
  const stats = getMockStats(id)
  const total = stats.traffic.total
  const usedBytes = trafficAccountedBytes(total, policy.direction)
  const quota = policy.monthly_quota_bytes
  const remainingBytes = quota == null ? null : Math.max(0, quota - usedBytes)
  const toBreakdown = (scopeType, entries) => Object.entries(entries || {}).map(([scopeID, bucket]) => ({
    scope_type: scopeType,
    scope_id: scopeID,
    rx_bytes: bucket.rx_bytes,
    tx_bytes: bucket.tx_bytes,
    accounted_bytes: trafficAccountedBytes(bucket, policy.direction)
  }))
  return {
    agent_id: id,
    policy: { ...policy },
    cycle_start: '2026-05-01T00:00:00Z',
    cycle_end: '2026-06-01T00:00:00Z',
    rx_bytes: total.rx_bytes,
    tx_bytes: total.tx_bytes,
    accounted_bytes: usedBytes,
    used_bytes: usedBytes,
    monthly_quota_bytes: quota,
    quota_percent: quota ? Math.min(100, (usedBytes / quota) * 100) : 0,
    remaining_bytes: remainingBytes,
    over_quota: quota != null && usedBytes > quota,
    blocked: policy.block_when_exceeded && quota != null && usedBytes > quota,
    aggregates: [
      { scope_type: 'total', scope_id: 'total', ...total, accounted_bytes: usedBytes },
      { scope_type: 'http', scope_id: 'http', ...stats.traffic.http, accounted_bytes: trafficAccountedBytes(stats.traffic.http, policy.direction) },
      { scope_type: 'l4', scope_id: 'l4', ...stats.traffic.l4, accounted_bytes: trafficAccountedBytes(stats.traffic.l4, policy.direction) },
      { scope_type: 'relay', scope_id: 'relay', ...stats.traffic.relay, accounted_bytes: trafficAccountedBytes(stats.traffic.relay, policy.direction) }
    ],
    http_rules: toBreakdown('http_rule', stats.traffic.http_rules),
    l4_rules: toBreakdown('l4_rule', stats.traffic.l4_rules),
    relay_listeners: toBreakdown('relay_listener', stats.traffic.relay_listeners)
  }
}

function buildMockTrafficTrend(agentId, params = {}) {
  const policy = ensureMockTrafficPolicy(agentId)
  const granularity = params.granularity || 'day'
  const now = new Date('2026-05-03T00:00:00Z')
  const count = granularity === 'hour' ? 24 : granularity === 'month' ? 6 : 14
  const stepMs = granularity === 'hour'
    ? 60 * 60 * 1000
    : granularity === 'month'
      ? 30 * 24 * 60 * 60 * 1000
      : 24 * 60 * 60 * 1000
  return Array.from({ length: count }, (_, index) => {
    const scale = index + 1
    const bucketStart = new Date(now.getTime() - (count - index - 1) * stepMs).toISOString()
    const point = {
      agent_id: String(agentId || 'local'),
      scope_type: params.scope_type || '',
      scope_id: params.scope_id || '',
      bucket_start: bucketStart,
      rx_bytes: scale * 1024 * 1024 * 17,
      tx_bytes: scale * 1024 * 1024 * 31
    }
    return {
      ...point,
      accounted_bytes: trafficAccountedBytes(point, policy.direction)
    }
  })
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
    const flags = readDevMockFlags()
    return {
      role: 'master',
      local_apply_runtime: 'go-agent',
      default_agent_id: 'local',
      local_agent_enabled: true,
      proxy_headers_globally_disabled: false,
      traffic_stats_enabled: flags.trafficStatsEnabled === false ? false : true
    }
  }
  const { data } = await api.get('/info')
  return data
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

export async function exportBackup() {
  if (isDev) {
    await sleep()
    return {
      filename: `nre-backup-${new Date().toISOString().replace(/[:.]/g, '-')}.tar.gz`,
      blob: new Blob(
        [
          JSON.stringify({
            manifest: {
              package_version: 1,
              source_architecture: 'pure-go',
              exported_at: new Date().toISOString(),
              includes_certificates: true,
              counts: {
                agents: 2,
                http_rules: 4,
                l4_rules: 1,
                relay_listeners: 1,
                certificates: 2,
                version_policies: 1
              }
            }
          })
        ],
        { type: 'application/gzip' }
      )
    }
  }
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
  if (isDev) {
    await sleep(900)
    return {
      manifest: {
        package_version: 1,
        source_architecture: 'main-legacy',
        exported_at: new Date().toISOString(),
        includes_certificates: true,
        counts: {
          agents: 2,
          http_rules: 3,
          l4_rules: 1,
          relay_listeners: 1,
          certificates: 2,
          version_policies: 1
        }
      },
      summary: {
        imported: {
          agents: 1,
          http_rules: 2,
          l4_rules: 1,
          relay_listeners: 1,
          certificates: 1,
          version_policies: 1
        },
        skipped_conflict: {
          agents: 1,
          http_rules: 1,
          l4_rules: 0,
          relay_listeners: 0,
          certificates: 0,
          version_policies: 0
        },
        skipped_invalid: {
          agents: 0,
          http_rules: 0,
          l4_rules: 0,
          relay_listeners: 0,
          certificates: 0,
          version_policies: 0
        },
        skipped_missing_material: {
          agents: 0,
          http_rules: 0,
          l4_rules: 0,
          relay_listeners: 0,
          certificates: 1,
          version_policies: 0
        }
      },
      report: {
        imported: [
          { kind: 'agent', key: 'edge-1' },
          { kind: 'http_rule', key: 'https://media.example.com' }
        ],
        skipped_conflict: [
          { kind: 'agent', key: 'edge-2', reason: 'agent name already exists' }
        ],
        skipped_invalid: [],
        skipped_missing_material: [
          { kind: 'certificate', key: 'relay.example.com', reason: 'certificate material missing from backup' }
        ]
      },
      file_name: file?.name || 'mock-backup.tar.gz'
    }
  }
  const formData = new FormData()
  formData.append('file', file)
  const { data } = await api.post('/system/backup/import', formData, {
    timeout: 0
  })
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

export async function fetchTrafficPolicy(agentId) {
  if (isDev) {
    await sleep()
    return { ...ensureMockTrafficPolicy(agentId) }
  }
  const { data } = await api.get(`/agents/${encodeURIComponent(agentId)}/traffic-policy`)
  return data.policy
}

export async function updateTrafficPolicy(agentId, patch = {}) {
  if (isDev) {
    await sleep()
    const current = ensureMockTrafficPolicy(agentId)
    mockTrafficPolicies[String(agentId || 'local')] = {
      ...current,
      ...patch,
      agent_id: String(agentId || current.agent_id || 'local')
    }
    return { ...mockTrafficPolicies[String(agentId || 'local')] }
  }
  const { data } = await api.patch(`/agents/${encodeURIComponent(agentId)}/traffic-policy`, patch)
  return data.policy
}

export async function fetchTrafficSummary(agentId) {
  if (isDev) {
    await sleep()
    return buildMockTrafficSummary(agentId)
  }
  const { data } = await api.get(`/agents/${encodeURIComponent(agentId)}/traffic-summary`)
  return data.summary
}

export async function fetchTrafficTrend(agentId, params = {}) {
  if (isDev) {
    await sleep()
    return buildMockTrafficTrend(agentId, params)
  }
  const query = new URLSearchParams()
  Object.entries(params || {}).forEach(([key, value]) => {
    if (value != null && value !== '') query.set(key, value)
  })
  const suffix = query.toString() ? `?${query.toString()}` : ''
  const { data } = await api.get(`/agents/${encodeURIComponent(agentId)}/traffic-trend${suffix}`)
  return data.points || []
}

export async function calibrateTraffic(agentId, payload = {}) {
  if (isDev) {
    await sleep()
    const summary = buildMockTrafficSummary(agentId)
    const usedBytes = Number(payload.used_bytes)
    if (Number.isFinite(usedBytes) && usedBytes >= 0) {
      summary.used_bytes = usedBytes
      summary.accounted_bytes = usedBytes
      summary.quota_percent = summary.monthly_quota_bytes ? Math.min(100, (usedBytes / summary.monthly_quota_bytes) * 100) : 0
      summary.remaining_bytes = summary.monthly_quota_bytes == null ? null : Math.max(0, summary.monthly_quota_bytes - usedBytes)
      summary.over_quota = summary.monthly_quota_bytes != null && usedBytes > summary.monthly_quota_bytes
      summary.blocked = summary.policy.block_when_exceeded && summary.over_quota
    }
    return summary
  }
  const { data } = await api.post(`/agents/${encodeURIComponent(agentId)}/traffic-calibration`, payload)
  return data.summary
}

export async function cleanupTraffic(agentId) {
  if (isDev) {
    await sleep()
    return {
      agent_id: String(agentId || 'local'),
      deleted_rows: 42,
      hourly_before: '2026-02-02T00:00:00Z',
      daily_before: '2024-05-01T00:00:00Z'
    }
  }
  const { data } = await api.post(`/agents/${encodeURIComponent(agentId)}/traffic-cleanup`)
  return data.result
}

function createSeededRandom(seed) {
  let s = seed
  return () => {
    s = (s * 16807 + 0) % 2147483647
    return (s - 1) / 2147483646
  }
}

const ALL_MOCK_AGENTS = [
  { agent_id: 'mock-1', name: '节点-A', used_bytes: 1073741824, quota_bytes: 2147483648, remaining_bytes: 1073741824, direction: 'both', cycle_start: '2026-05-01', cycle_end: '2026-06-01', blocked: false },
  { agent_id: 'mock-2', name: '节点-B', used_bytes: 536870912, quota_bytes: 1073741824, remaining_bytes: 536870912, direction: 'both', cycle_start: '2026-05-01', cycle_end: '2026-06-01', blocked: false },
  { agent_id: 'mock-3', name: '节点-C', used_bytes: 3221225472, quota_bytes: 3221225472, remaining_bytes: 0, direction: 'both', cycle_start: '2026-05-01', cycle_end: '2026-06-01', blocked: true }
]

export async function fetchTrafficOverview(agentId, granularity) {
  if (isDev) {
    await sleep()
    const seed = agentId ? agentId.split('').reduce((s, c) => s + c.charCodeAt(0), 0) : 42
    let trend
    if (granularity === 'month') {
      const rng = createSeededRandom(seed)
      trend = Array.from({ length: 12 }, (_, i) => {
        const month = String(i + 1).padStart(2, '0')
        return {
          bucket_start: `2026-${month}-01T00:00:00Z`,
          rx_bytes: Math.round(rng() * 800000000 + 200000000),
          tx_bytes: Math.round(rng() * 600000000 + 100000000),
          accounted_bytes: Math.round(rng() * 1000000000 + 500000000)
        }
      })
    } else if (granularity === 'day') {
      const rng = createSeededRandom(seed)
      trend = Array.from({ length: 30 }, (_, i) => {
        const day = String(i + 1).padStart(2, '0')
        return {
          bucket_start: `2026-05-${day}T00:00:00Z`,
          rx_bytes: Math.round(rng() * 80000000 + 20000000),
          tx_bytes: Math.round(rng() * 60000000 + 10000000),
          accounted_bytes: Math.round(rng() * 100000000 + 50000000)
        }
      })
    } else {
      const rng = createSeededRandom(seed)
      trend = Array.from({ length: 24 }, (_, i) => {
        const hour = String(i).padStart(2, '0')
        return {
          bucket_start: `2026-05-05T${hour}:00:00Z`,
          rx_bytes: Math.round(rng() * 80000000 + 20000000),
          tx_bytes: Math.round(rng() * 60000000 + 10000000),
          accounted_bytes: Math.round(rng() * 100000000 + 50000000)
        }
      })
    }
    const agents = agentId
      ? ALL_MOCK_AGENTS.filter(a => a.agent_id === agentId)
      : ALL_MOCK_AGENTS
    return {
      ok: true,
      agents,
      trend
    }
  }
  const params = new URLSearchParams()
  if (agentId) params.set('agent_id', agentId)
  if (granularity) params.set('granularity', granularity)
  const suffix = params.toString() ? `?${params.toString()}` : ''
  const { data } = await api.get(`/traffic-overview${suffix}`)
  return data
}

export async function fetchTrafficAggregate(agentId, granularity) {
  if (isDev) {
    await sleep()
    const overview = await fetchTrafficOverview(agentId, granularity)
    const selectedAgents = overview.agents || []
    const topRules = selectedAgents.flatMap((agent) => {
      const summary = buildMockTrafficSummary(agent.agent_id)
      return [
        ...(summary.http_rules || []),
        ...(summary.l4_rules || []),
        ...(summary.relay_listeners || [])
      ].map((rule) => {
        const label = `${scopeTypeLabel(rule.scope_type)} #${rule.scope_id}`
        return {
          ...rule,
          key: `${agent.agent_id}:${rule.scope_type}:${rule.scope_id}`,
          agent_id: agent.agent_id,
          label: agentId ? label : `${agent.name || agent.agent_id} / ${label}`
        }
      })
    }).sort((a, b) => (b.accounted_bytes || 0) - (a.accounted_bytes || 0)).slice(0, 10)
    const topNodes = [...selectedAgents]
      .sort((a, b) => (b.used_bytes || 0) - (a.used_bytes || 0))
      .slice(0, 5)
      .map((agent) => ({
        agent_id: agent.agent_id,
        name: agent.name,
        used_bytes: agent.used_bytes,
        quota_bytes: agent.quota_bytes
      }))
    return {
      ...overview,
      top_rules: topRules,
      top_nodes: topNodes
    }
  }
  const params = new URLSearchParams()
  if (agentId) params.set('agent_id', agentId)
  if (granularity) params.set('granularity', granularity)
  const suffix = params.toString() ? `?${params.toString()}` : ''
  const { data } = await api.get(`/traffic-aggregate${suffix}`)
  return data
}

export async function fetchRules(agentId) {
  if (isDev) {
    await sleep()
    return (mockRulesByAgent[agentId] || []).map((rule) => normalizeHttpRule(rule))
  }
  const { data } = await api.get(`/agents/${encodeURIComponent(agentId)}/rules`)
  return (data.rules || []).map((rule) => normalizeHttpRule(rule))
}

export async function createRule(agentId, payloadOrFrontend) {
  const payload = normalizeHttpRulePayloadObject(payloadOrFrontend && typeof payloadOrFrontend === 'object' && !Array.isArray(payloadOrFrontend)
    ? payloadOrFrontend
    : {}, { includeRelayDefaults: true })
  if (isDev) {
    await sleep()
    const nextRule = normalizeHttpRule({
      id: Date.now(),
      ...payload
    })
    mockRulesByAgent[agentId] = mockRulesByAgent[agentId] || []
    mockRulesByAgent[agentId].push(nextRule)
    return nextRule
  }
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
  if (isDev) {
    await sleep()
    const list = mockRulesByAgent[agentId] || []
    const index = list.findIndex((rule) => rule.id === id)
    if (index !== -1) {
      const nextRule = normalizeHttpRule({ ...list[index], ...payload })
      list[index] = nextRule
      return nextRule
    }
    return null
  }
  const { data } = await api.put(
    `/agents/${encodeURIComponent(agentId)}/rules/${id}`,
    payload,
    longRunningRequest
  )
  return normalizeHttpRule(data.rule)
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

const mockTasksByAgent = {}

function ensureMockTaskMap(agentId) {
  const key = String(agentId)
  if (!mockTasksByAgent[key]) mockTasksByAgent[key] = {}
  return mockTasksByAgent[key]
}

function buildMockDiagnosticResult(kind, ruleId) {
  const sent = 10
  const failed = ruleId % 3 === 0 ? 1 : 0
  const succeeded = sent - failed
  const avg = 18 + (ruleId % 7) * 9
  const backendLabels = kind === 'http'
    ? ['http://10.0.0.11:8096/healthz', 'http://10.0.0.12:8096/healthz']
    : ['127.0.0.1:9001', '127.0.0.1:9002']
  const samples = Array.from({ length: sent }, (_, index) => {
    const backend = backendLabels[index % backendLabels.length]
    const sampleFailed = index < failed
    return {
      attempt: index + 1,
      backend,
      success: !sampleFailed,
      latency_ms: sampleFailed ? 0 : avg + index * 2,
      status_code: kind === 'http' && !sampleFailed ? 200 : 0,
      error: sampleFailed ? 'dial timeout' : ''
    }
  })
  const backends = backendLabels.map((backend, index) => {
    const backendSamples = samples.filter((sample) => sample.backend === backend)
    const successful = backendSamples.filter((sample) => sample.success)
    const latencies = successful.map((sample) => sample.latency_ms)
    const total = latencies.reduce((sum, value) => sum + value, 0)
    return {
      backend,
      summary: {
        sent: backendSamples.length,
        succeeded: successful.length,
        failed: backendSamples.length - successful.length,
        loss_rate: Number(((backendSamples.length - successful.length) / backendSamples.length).toFixed(1)),
        avg_latency_ms: successful.length ? Number((total / successful.length).toFixed(1)) : 0,
        min_latency_ms: successful.length ? Math.min(...latencies) : 0,
        max_latency_ms: successful.length ? Math.max(...latencies) : 0,
        quality: successful.length === backendSamples.length ? 'good' : 'fair'
      },
      adaptive: {
        preferred: backend === backendLabels[0],
        reason: backend === backendLabels[0] ? 'performance_higher' : '',
        stability: backend === backendLabels[0] ? 1 : 0.8,
        recent_succeeded: successful.length,
        recent_failed: backendSamples.length - successful.length,
        latency_ms: successful.length ? Number((total / successful.length).toFixed(1)) : 0,
        sustained_throughput_bps: backend === backendLabels[0] ? 4 * 1024 * 1024 : 768 * 1024,
        performance_score: backend === backendLabels[0] ? 0.88 : 0.63,
        state: index === 0 ? 'warm' : 'recovering',
        sample_confidence: index === 0 ? 1 : 0.45,
        slow_start_active: index !== 0,
        outlier: index !== 0,
        traffic_share_hint: index === 0 ? 'normal' : 'recovery'
      },
      children: kind === 'http'
        ? [
            {
              backend: `${backend} [203.0.113.${11 + backendLabels.indexOf(backend)}:8096]`,
              summary: {
                sent: Math.max(1, Math.floor(backendSamples.length / 2)),
                succeeded: Math.max(1, Math.floor(successful.length / 2)),
                failed: Math.max(0, Math.floor((backendSamples.length - successful.length) / 2)),
                loss_rate: 0,
                avg_latency_ms: successful.length ? Number((total / successful.length).toFixed(1)) : 0,
                min_latency_ms: successful.length ? Math.min(...latencies) : 0,
                max_latency_ms: successful.length ? Math.max(...latencies) : 0,
                quality: successful.length === backendSamples.length ? 'good' : 'fair'
              },
              adaptive: {
                preferred: backend === backendLabels[0],
                reason: backend === backendLabels[0] ? 'performance_higher' : '',
                stability: backend === backendLabels[0] ? 1 : 0.8,
                recent_succeeded: successful.length,
                recent_failed: backendSamples.length - successful.length,
                latency_ms: successful.length ? Number((total / successful.length).toFixed(1)) : 0,
                sustained_throughput_bps: backend === backendLabels[0] ? 4 * 1024 * 1024 : 768 * 1024,
                performance_score: backend === backendLabels[0] ? 0.88 : 0.63,
                state: index === 0 ? 'warm' : 'recovering',
                sample_confidence: index === 0 ? 1 : 0.45,
                slow_start_active: index !== 0,
                outlier: index !== 0,
                traffic_share_hint: index === 0 ? 'normal' : 'recovery'
              }
            }
          ]
        : []
    }
  })
  return {
    id: `task-${Date.now()}`,
    agent_id: '',
    state: 'completed',
    type: kind === 'http' ? 'diagnose_http_rule' : 'diagnose_l4_tcp_rule',
    payload: {
      rule_id: ruleId,
      rule_kind: kind
    },
    result: {
      kind,
      rule_id: ruleId,
      summary: {
        sent,
        succeeded,
        failed,
        loss_rate: Number((failed / sent).toFixed(1)),
        avg_latency_ms: avg,
        min_latency_ms: Math.max(5, avg - 8),
        max_latency_ms: avg + 17,
        quality: failed === 0 && avg < 60 ? 'excellent' : failed === 0 ? 'good' : 'fair'
      },
      backends,
      relay_paths: [
        {
          path: [1, 4],
          selected: true,
          success: true,
          latency_ms: Number((avg * 0.7).toFixed(1)),
          adaptive: { preferred: true, state: 'warm', latency_ms: Number((avg * 0.7).toFixed(1)) },
          hops: [
            { from: 'client', to_listener_id: 1, to_listener_name: 'relay-a', to_agent_name: '本机 Agent', latency_ms: 12.1, success: true },
            { from_listener_id: 1, from_listener_name: 'relay-a', from_agent_name: '本机 Agent', to_listener_id: 4, to_listener_name: 'relay-d', to_agent_name: 'edge-1', latency_ms: 15.4, success: true },
            { from_listener_id: 4, from_listener_name: 'relay-d', from_agent_name: 'edge-1', to: backendLabels[0], latency_ms: Number(Math.max(5, avg - 12).toFixed(1)), success: true }
          ]
        },
        {
          path: [2, 4],
          selected: false,
          success: failed === 0,
          latency_ms: failed === 0 ? Number((avg * 1.1).toFixed(1)) : 0,
          error: failed === 0 ? '' : 'relay dial timeout',
          adaptive: { preferred: false, state: failed === 0 ? 'recovering' : 'cold' },
          hops: [
            { from: 'client', to_listener_id: 2, to_listener_name: 'relay-b', to_agent_name: 'edge-1', latency_ms: failed === 0 ? 24.8 : 0, success: failed === 0, error: failed === 0 ? '' : 'timeout' },
            { from_listener_id: 2, from_listener_name: 'relay-b', from_agent_name: 'edge-1', to_listener_id: 4, to_listener_name: 'relay-d', to_agent_name: 'edge-1', latency_ms: failed === 0 ? 18.2 : 0, success: failed === 0, error: failed === 0 ? '' : 'timeout' },
            { from_listener_id: 4, from_listener_name: 'relay-d', from_agent_name: 'edge-1', to: backendLabels[1], latency_ms: failed === 0 ? Number(Math.max(5, avg - 8).toFixed(1)) : 0, success: failed === 0, error: failed === 0 ? '' : 'timeout' }
          ]
        }
      ],
      selected_relay_path: [1, 4],
      samples
    }
  }
}

function queueMockDiagnosticCompletion(agentId, taskRecord) {
  const tasks = ensureMockTaskMap(agentId)
  tasks[taskRecord.id] = {
    ...taskRecord,
    updated_at: new Date().toISOString()
  }
  window.setTimeout(() => {
    const current = tasks[taskRecord.id]
    if (!current) return
    tasks[taskRecord.id] = {
      ...current,
      state: 'completed',
      updated_at: new Date().toISOString(),
      result: taskRecord.result
    }
  }, 900)
}

export async function diagnoseRule(agentId, ruleId) {
  if (isDev) {
    await sleep(250)
    const task = buildMockDiagnosticResult('http', Number(ruleId))
    task.agent_id = String(agentId)
    task.state = 'running'
    queueMockDiagnosticCompletion(agentId, task)
    return { ok: true, task_id: task.id, task }
  }
  const { data } = await api.post(`/agents/${encodeURIComponent(agentId)}/rules/${encodeURIComponent(ruleId)}/diagnose`, {}, longRunningRequest)
  return data
}

export async function fetchAgentTask(agentId, taskId) {
  if (isDev) {
    await sleep(250)
    const tasks = ensureMockTaskMap(agentId)
    const task = tasks[String(taskId)]
    if (!task) throw new Error('task not found')
    return { ok: true, task }
  }
  const { data } = await api.get(`/agents/${encodeURIComponent(agentId)}/tasks/${encodeURIComponent(taskId)}`)
  return data
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

export async function updateAgent(agentId, payload = {}) {
  if (isDev) {
    await sleep()
    const agent = mockAgents.find((a) => a.id === agentId)
    if (!agent) throw new Error('节点不存在')
    if (Object.prototype.hasOwnProperty.call(payload, 'name')) {
      agent.name = String(payload.name || '').trim()
    }
    if (Object.prototype.hasOwnProperty.call(payload, 'outbound_proxy_url')) {
      agent.outbound_proxy_url = String(payload.outbound_proxy_url || '').trim()
    }
    if (Object.prototype.hasOwnProperty.call(payload, 'traffic_stats_interval')) {
      agent.traffic_stats_interval = String(payload.traffic_stats_interval || '').trim()
    }
    return { ...agent }
  }
  const { data } = await api.patch(`/agents/${encodeURIComponent(agentId)}`, payload)
  return data.agent
}

export async function fetchAllAgentsRules(agentIds) {
  if (isDev) {
    await sleep()
    return agentIds.map((agentId) => ({
      agentId,
      rules: (mockRulesByAgent[agentId] || []).map((rule) => normalizeHttpRule(rule))
    }))
  }
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

// L4 Rules
const mockL4RulesByAgent = {
  local: [
    {
      id: 1,
      protocol: 'tcp',
      listen_host: '0.0.0.0',
      listen_port: 25565,
      backends: [
        { host: '192.168.1.20', port: 25565 },
        { host: 'game-backup.ddns.example', port: 25565 }
      ],
      load_balancing: { strategy: 'round_robin' },
      relay_layers: [],
      relay_obfs: false,
      enabled: true,
      tags: ['TCP', ':25565', 'game']
    }
  ],
  'edge-1': [
    {
      id: 1,
      protocol: 'udp',
      listen_host: '0.0.0.0',
      listen_port: 51820,
      backends: [
        { host: '10.0.0.20', port: 51820 },
        { host: 'wireguard-edge.ddns.example', port: 51820 }
      ],
      load_balancing: { strategy: 'random' },
      relay_layers: [],
      relay_obfs: false,
      enabled: true,
      tags: ['UDP', ':51820', 'vpn']
    }
  ]
}
let mockL4IdCounter = 1

export async function fetchAllAgentsL4Rules(agentIds) {
  if (isDev) {
    await sleep()
    return agentIds.map((agentId) => ({
      agentId,
      l4Rules: (mockL4RulesByAgent[agentId] || []).map((rule) => normalizeL4Rule(rule))
    }))
  }
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
  if (isDev) return { ok: true }
  const { data } = await api.get('/health')
  return data
}

export async function fetchL4Rules(agentId) {
  if (isDev) { await sleep(); return (mockL4RulesByAgent[agentId] || []).map((rule) => normalizeL4Rule(rule)) }
  const { data } = await api.get(`/agents/${encodeURIComponent(agentId)}/l4-rules`)
  return (data.rules || []).map((rule) => normalizeL4Rule(rule))
}

export async function createL4Rule(agentId, payload) {
  const normalizedPayload = normalizeL4RulePayload(payload, { includeRelayDefaults: true })
  if (isDev) {
    await sleep()
    const item = normalizeL4Rule({ ...normalizedPayload, id: ++mockL4IdCounter })
    mockL4RulesByAgent[agentId] = mockL4RulesByAgent[agentId] || []
    mockL4RulesByAgent[agentId].push(item)
    return item
  }
  const { data } = await api.post(`/agents/${encodeURIComponent(agentId)}/l4-rules`, normalizedPayload, longRunningRequest)
  return normalizeL4Rule(data.rule)
}

export async function updateL4Rule(agentId, id, payload) {
  const normalizedPayload = normalizeL4RulePayload(payload)
  if (isDev) {
    await sleep()
    const list = mockL4RulesByAgent[agentId] || []
    const idx = list.findIndex((r) => r.id === id)
    if (idx !== -1) {
      list[idx] = normalizeL4Rule({ ...list[idx], ...normalizedPayload })
      return list[idx]
    }
    return null
  }
  const { data } = await api.put(`/agents/${encodeURIComponent(agentId)}/l4-rules/${id}`, normalizedPayload, longRunningRequest)
  return normalizeL4Rule(data.rule)
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

export async function diagnoseL4Rule(agentId, ruleId) {
  if (isDev) {
    await sleep(250)
    const task = buildMockDiagnosticResult('l4_tcp', Number(ruleId))
    task.agent_id = String(agentId)
    task.state = 'running'
    queueMockDiagnosticCompletion(agentId, task)
    return { ok: true, task_id: task.id, task }
  }
  const { data } = await api.post(`/agents/${encodeURIComponent(agentId)}/l4-rules/${encodeURIComponent(ruleId)}/diagnose`, {}, longRunningRequest)
  return data
}

// Certificates (per-agent, like HTTP/L4)
const mockCertsByAgent = {
  local: [
    { id: 1, domain: 'media.example.com', enabled: true, scope: 'domain', issuer_mode: 'master_cf_dns', usage: 'https', certificate_type: 'acme', self_signed: false, status: 'active', last_issue_at: new Date().toISOString(), last_error: '', tags: ['media', 'streaming'] },
    { id: 2, domain: '__relay-ca.internal', enabled: true, scope: 'domain', issuer_mode: 'local_http01', usage: 'relay_ca', certificate_type: 'internal_ca', self_signed: true, status: 'active', last_issue_at: new Date().toISOString(), last_error: '', tags: ['system', SYSTEM_RELAY_CA_TAG] },
    { id: 3, domain: 'relay-local.local.relay.internal', enabled: true, scope: 'domain', issuer_mode: 'local_http01', usage: 'relay_tunnel', certificate_type: 'internal_ca', self_signed: false, status: 'active', last_issue_at: new Date().toISOString(), last_error: '', tags: ['relay', 'listener:1', SYSTEM_RELAY_TUNNEL_TAG] }
  ],
  'edge-1': [
    { id: 1, domain: 'media.example.com', enabled: true, scope: 'domain', issuer_mode: 'master_cf_dns', usage: 'https', certificate_type: 'acme', self_signed: false, status: 'active', last_issue_at: new Date().toISOString(), last_error: '', tags: ['media'] },
    { id: 2, domain: 'relay-edge-1.relay.internal', enabled: true, scope: 'domain', issuer_mode: 'local_http01', usage: 'relay_tunnel', certificate_type: 'internal_ca', self_signed: false, status: 'active', last_issue_at: new Date().toISOString(), last_error: '', tags: ['relay', 'listener:2', SYSTEM_RELAY_TUNNEL_TAG] },
    { id: 3, domain: 'relay-uploaded.example.com', enabled: true, scope: 'domain', issuer_mode: 'local_http01', usage: 'relay_tunnel', certificate_type: 'uploaded', self_signed: true, status: 'active', last_issue_at: new Date().toISOString(), last_error: '', tags: ['relay', 'uploaded'] },
    { id: 4, domain: '__relay-ca.internal', enabled: true, scope: 'domain', issuer_mode: 'local_http01', usage: 'relay_ca', certificate_type: 'internal_ca', self_signed: true, status: 'active', last_issue_at: new Date().toISOString(), last_error: '', tags: ['system', SYSTEM_RELAY_CA_TAG] }
  ],
  'edge-2': [
    { id: 1, domain: 'media.example.com', enabled: true, scope: 'domain', issuer_mode: 'local_http01', usage: 'mixed', certificate_type: 'acme', self_signed: false, status: 'error', last_issue_at: '', last_error: 'ACME challenge failed', tags: ['media'] }
  ]
}
let mockCertIdCounter = 10

function hasMockCertificateTag(certificate, tag) {
  return Array.isArray(certificate?.tags) && certificate.tags.includes(tag)
}

function isMockSystemRelayCA(certificate) {
  return hasMockCertificateTag(certificate, SYSTEM_RELAY_CA_TAG)
}

function isMockSystemManagedRelayListenerPayload(payload) {
  return payload?.usage === 'relay_tunnel' && payload?.certificate_type === 'internal_ca'
}

export async function fetchCertificates(agentId) {
  if (isDev) { await sleep(); return [...(mockCertsByAgent[agentId] || [])] }
  const { data } = await api.get(`/agents/${encodeURIComponent(agentId)}/certificates`)
  return data.certificates || []
}

export async function createCertificate(agentId, payload) {
  if (isDev) {
    await sleep()
    const isSystemManagedRelayListener = isMockSystemManagedRelayListenerPayload(payload)
    const tags = Array.isArray(payload.tags) ? [...payload.tags] : []
    if (isSystemManagedRelayListener && !tags.includes(SYSTEM_RELAY_TUNNEL_TAG)) {
      tags.push(SYSTEM_RELAY_TUNNEL_TAG)
    }
    const item = {
      ...payload,
      tags,
      id: ++mockCertIdCounter,
      status: payload.certificate_type === 'uploaded' || isSystemManagedRelayListener ? 'active' : 'pending',
      last_issue_at: payload.certificate_type === 'uploaded' || isSystemManagedRelayListener ? new Date().toISOString() : '',
      last_error: ''
    }
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
    if (idx !== -1) {
      if (isMockSystemRelayCA(list[idx])) {
        return list[idx]
      }
      const next = { ...list[idx], ...payload }
      if (isMockSystemManagedRelayListenerPayload(next)) {
        const tags = Array.isArray(next.tags) ? [...next.tags] : []
        if (!tags.includes(SYSTEM_RELAY_TUNNEL_TAG)) {
          tags.push(SYSTEM_RELAY_TUNNEL_TAG)
        }
        next.tags = tags
        next.status = 'active'
        next.last_issue_at = next.last_issue_at || new Date().toISOString()
        next.last_error = ''
      }
      list[idx] = next
      return list[idx]
    }
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
    if (idx !== -1 && isMockSystemRelayCA(list[idx])) return null
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

export async function fetchAllAgentsRelayListeners(agentIds) {
  if (isDev) {
    await sleep()
    return agentIds.map((agentId) => ({
      agentId,
      listeners: (mockRelayListenersByAgent[agentId] || []).map((item) => normalizeMockRelayListenerRecord(item))
    }))
  }
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

const mockRelayListenersByAgent = {
  local: [
    {
      id: 1,
      agent_id: 'local',
      name: 'relay-a',
      bind_hosts: ['0.0.0.0', '127.0.0.1'],
      listen_port: 7443,
      public_host: 'relay-a.example.com',
      public_port: 7443,
      enabled: true,
      certificate_id: 3,
      certificate_source: 'auto_relay_ca',
      trust_mode_source: 'auto',
      tls_mode: 'pin_and_ca',
      transport_mode: 'tls_tcp',
      allow_transport_fallback: true,
      obfs_mode: 'early_window_v2',
      pin_set: [{ type: 'spki_sha256', value: 'derived-pin-a' }],
      trusted_ca_certificate_ids: [2],
      allow_self_signed: true,
      tags: ['relay', 'shared'],
      revision: 1
    },
    {
      id: 5,
      agent_id: 'local',
      name: 'relay-wg-local',
      bind_hosts: ['0.0.0.0'],
      listen_port: 51820,
      public_host: 'wg-relay.example.com',
      enabled: true,
      certificate_id: null,
      certificate_source: 'auto_relay_ca',
      trust_mode_source: 'auto',
      tls_mode: 'pin_and_ca',
      transport_mode: 'wireguard',
      wireguard_profile_id: 1,
      allow_transport_fallback: false,
      obfs_mode: 'off',
      pin_set: [],
      trusted_ca_certificate_ids: [],
      allow_self_signed: true,
      tags: ['relay', 'wg'],
      revision: 1
    }
  ],
  'edge-1': [
    {
      id: 2,
      agent_id: 'edge-1',
      name: 'relay-b',
      bind_hosts: ['0.0.0.0'],
      listen_port: 8443,
      public_host: 'relay-b.example.com',
      enabled: true,
      certificate_id: 2,
      certificate_source: 'auto_relay_ca',
      trust_mode_source: 'auto',
      tls_mode: 'pin_and_ca',
      transport_mode: 'quic',
      allow_transport_fallback: true,
      obfs_mode: 'off',
      pin_set: [{ type: 'spki_sha256', value: 'derived-pin-b' }],
      trusted_ca_certificate_ids: [4],
      allow_self_signed: true,
      tags: ['relay', 'shared'],
      revision: 3
    },
    {
      id: 4,
      agent_id: 'edge-1',
      name: 'relay-d',
      bind_hosts: ['0.0.0.0'],
      listen_port: 9443,
      public_host: 'relay-d.example.com',
      enabled: true,
      certificate_id: 2,
      certificate_source: 'auto_relay_ca',
      trust_mode_source: 'auto',
      tls_mode: 'pin_and_ca',
      transport_mode: 'tls_tcp',
      allow_transport_fallback: true,
      obfs_mode: 'off',
      pin_set: [{ type: 'spki_sha256', value: 'derived-pin-d' }],
      trusted_ca_certificate_ids: [4],
      allow_self_signed: true,
      tags: ['relay', 'shared'],
      revision: 1
    }
  ]
}

let mockRelayListenerIdCounter = 5

function findMockRelayListenerCertificate(agentId, certificateId) {
  const certificates = mockCertsByAgent[agentId] || []
  if (certificateId != null) {
    const selected = certificates.find((cert) => Number(cert.id) === Number(certificateId))
    if (selected) return selected
  }
  return certificates.find((cert) => cert.usage === 'relay_tunnel' && cert.certificate_type === 'internal_ca')
    || certificates.find((cert) => cert.usage === 'relay_tunnel')
    || certificates[0]
    || null
}

function findMockRelayCA(agentId) {
  const certificates = mockCertsByAgent[agentId] || []
  return certificates.find((cert) => isMockSystemRelayCA(cert) || cert.usage === 'relay_ca')
    || (mockCertsByAgent.local || []).find((cert) => isMockSystemRelayCA(cert) || cert.usage === 'relay_ca')
    || null
}

function buildMockAutoPin(certificate) {
  const source = String(certificate?.domain || certificate?.id || 'relay').toLowerCase()
  const suffix = source
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .split('-')
    .filter(Boolean)
    .slice(-2)
    .join('-') || 'relay'
  return [{
    type: 'spki_sha256',
    value: `derived-pin-${suffix}`
  }]
}

function normalizeRelayPort(raw) {
  if (raw == null || String(raw).trim() === '') return null
  const value = Number(raw)
  if (!Number.isInteger(value) || value < 1 || value > 65535) return null
  return value
}

function normalizeRelayBindHosts(rawBindHosts, legacyListenHost) {
  const source = Array.isArray(rawBindHosts)
    ? rawBindHosts
    : [rawBindHosts ?? legacyListenHost ?? '0.0.0.0']
  const deduped = []
  const seen = new Set()
  for (const item of source) {
    const host = String(item || '').trim()
    if (!host || seen.has(host)) continue
    seen.add(host)
    deduped.push(host)
  }
  return deduped.length ? deduped : ['0.0.0.0']
}

function normalizeRelayTransportMode(value) {
  if (value === 'quic' || value === 'wireguard') return value
  return 'tls_tcp'
}

function normalizeRelayObfsMode(value, transportMode) {
  if (transportMode !== 'tls_tcp') return 'off'
  return value === 'early_window_v2' ? 'early_window_v2' : 'off'
}

function normalizeMockRelayListenerRecord(record = {}) {
  const {
    bind_hosts: rawBindHosts,
    listen_host: legacyListenHost,
    public_host: rawPublicHost,
    public_port: rawPublicPort,
    ...rest
  } = record
  const transportMode = normalizeRelayTransportMode(record.transport_mode)
  const normalized = {
    ...rest,
    bind_hosts: normalizeRelayBindHosts(rawBindHosts, legacyListenHost),
    transport_mode: transportMode,
    allow_transport_fallback: record.allow_transport_fallback !== false,
    obfs_mode: normalizeRelayObfsMode(record.obfs_mode, transportMode)
  }
  const publicHost = String(rawPublicHost || '').trim()
  const publicPort = normalizeRelayPort(rawPublicPort)
  if (publicHost) normalized.public_host = publicHost
  if (publicPort != null) normalized.public_port = publicPort
  return normalized
}

function normalizeMockRelayListenerPayload(agentId, payload = {}) {
  payload = normalizeRelayListenerPayloadForTransport(payload)
  const normalizedRecord = normalizeMockRelayListenerRecord(payload)
  const certificateSource = payload.certificate_source === 'existing_certificate' ? 'existing_certificate' : 'auto_relay_ca'
  const trustModeSource = payload.trust_mode_source === 'custom' ? 'custom' : 'auto'
  const transportMode = normalizeRelayTransportMode(payload.transport_mode)
  const hasPublicPortInput = payload.public_port != null && String(payload.public_port).trim() !== ''
  if (hasPublicPortInput && normalizeRelayPort(payload.public_port) == null) {
    throw new Error('public_port must be an integer between 1 and 65535')
  }
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
  const certificate = certificateSource === 'existing_certificate' && payload.certificate_id == null
    ? null
    : findMockRelayListenerCertificate(agentId, payload.certificate_id)
  const relayCA = findMockRelayCA(agentId)
  const certificateId = certificate
    ? Number(certificate.id)
    : (payload.certificate_id == null ? null : Number(payload.certificate_id))
  const tlsMode = String(payload.tls_mode || 'pin_and_ca')
  if (!['pin_only', 'ca_only', 'pin_or_ca', 'pin_and_ca'].includes(tlsMode)) {
    throw new Error('tls_mode must be pin_only, ca_only, pin_or_ca, or pin_and_ca')
  }
  if (payload.enabled !== false && certificateSource === 'existing_certificate' && certificateId == null) {
    throw new Error('certificate_id is required when relay listener is enabled')
  }
  if (trustModeSource === 'custom') {
    if (tlsMode === 'pin_only' && !pinSet.length) {
      throw new Error('pin_only requires pin_set')
    }
    if (tlsMode === 'ca_only' && !trustedCa.length) {
      throw new Error('ca_only requires trusted_ca_certificate_ids')
    }
    if (tlsMode === 'pin_and_ca' && (!pinSet.length || !trustedCa.length)) {
      throw new Error('pin_and_ca requires both pin_set and trusted_ca_certificate_ids')
    }
    if (tlsMode === 'pin_or_ca' && !pinSet.length && !trustedCa.length) {
      throw new Error('pin_or_ca requires pin_set or trusted_ca_certificate_ids')
    }
  }
  const derivedPinSet = trustModeSource === 'auto'
    ? buildMockAutoPin(certificate)
    : pinSet
  const derivedTrustedCa = trustModeSource === 'auto'
    ? (relayCA ? [Number(relayCA.id)] : [])
    : trustedCa
  return {
    ...normalizedRecord,
    certificate_id: certificateId,
    certificate_source: certificateSource,
    trust_mode_source: trustModeSource,
    transport_mode: transportMode,
    allow_transport_fallback: transportMode === 'wireguard' ? false : payload.allow_transport_fallback !== false,
    obfs_mode: transportMode === 'tls_tcp'
      ? normalizeRelayObfsMode(payload.obfs_mode, transportMode)
      : 'off',
    pin_set: derivedPinSet,
    trusted_ca_certificate_ids: derivedTrustedCa,
    tls_mode: trustModeSource === 'auto' ? 'pin_and_ca' : tlsMode,
    allow_self_signed: trustModeSource === 'auto'
      ? true
      : payload.allow_self_signed === true
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
    return (mockRelayListenersByAgent[agentId] || []).map((item) => normalizeMockRelayListenerRecord(item))
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
        ...normalizeMockRelayListenerRecord(listener),
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
    const normalizedPayload = normalizeMockRelayListenerPayload(agentId, payload)
    const item = {
      id: ++mockRelayListenerIdCounter,
      agent_id: agentId,
      revision: Date.now(),
      ...normalizedPayload
    }
    mockRelayListenersByAgent[agentId] = mockRelayListenersByAgent[agentId] || []
    mockRelayListenersByAgent[agentId].push(item)
    return normalizeMockRelayListenerRecord(item)
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
    const normalizedPayload = normalizeMockRelayListenerPayload(agentId, { ...list[idx], ...payload })
    list[idx] = normalizeMockRelayListenerRecord({ ...list[idx], ...normalizedPayload })
    return normalizeMockRelayListenerRecord(list[idx])
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

const mockWireGuardProfilesByAgent = {
  local: [
    {
      id: 1,
      agent_id: 'local',
      name: 'local-wg',
      mode: 'generic_wireguard',
      private_key: 'xxxxx',
      listen_port: 51820,
      public_endpoint: 'local.example.com:51820',
      addresses: ['10.8.0.1/24'],
      peers: [
        {
          name: 'edge-peer',
          public_key: 'mock-public-key-edge',
          preshared_key: 'xxxxx',
          endpoint: 'edge.example.com:51820',
          allowed_ips: ['10.8.0.2/32'],
          persistent_keepalive_seconds: 25
        }
      ],
      dns: ['1.1.1.1'],
      mtu: 1420,
      enabled: true,
      tags: ['wg', 'local'],
      revision: 1
    }
  ],
  'edge-1': [
    {
      id: 2,
      agent_id: 'edge-1',
      name: 'edge-wg',
      mode: 'generic_wireguard',
      private_key: 'xxxxx',
      listen_port: 51821,
      public_endpoint: 'edge-1.example.com:51821',
      addresses: ['10.9.0.1/24'],
      peers: [],
      dns: [],
      mtu: 1420,
      enabled: true,
      tags: ['wg', 'edge'],
      revision: 1
    }
  ]
}

let mockWireGuardProfileIdCounter = 2

const mockWireGuardClientsByProfile = {
  local: {
    1: [
      {
        id: 1,
        name: 'phone',
        address: '10.8.0.2/32',
        public_key: 'mock-client-public-key-phone',
        private_key: 'mock-client-private-key-phone',
        preshared_key: 'mock-client-psk-phone',
        dns: ['1.1.1.1'],
        allowed_ips: ['0.0.0.0/0', '::/0'],
        enabled: true,
        revision: 1
      }
    ]
  },
  'edge-1': {
    2: []
  }
}

let mockWireGuardClientIdCounter = 1

function normalizeStringList(value) {
  if (!Array.isArray(value)) return []
  return value
    .map((item) => String(item || '').trim())
    .filter(Boolean)
}

function isMockIPv4Address(value) {
  const parts = String(value || '').split('.')
  return parts.length === 4 && parts.every((part) => {
    if (!/^\d+$/.test(part)) return false
    const octet = Number(part)
    return Number.isInteger(octet) && octet >= 0 && octet <= 255
  })
}

function isMockIPv6Address(value) {
  const raw = String(value || '').trim()
  if (!raw.includes(':')) return false
  try {
    new URL(`http://[${raw}]/`)
    return true
  } catch {
    return false
  }
}

function isMockIPAddress(value) {
  return isMockIPv4Address(value) || isMockIPv6Address(value)
}

function validateMockWireGuardPrefixes(values, field) {
  for (const value of values) {
    const raw = String(value || '').trim()
    const slashIndex = raw.lastIndexOf('/')
    if (slashIndex <= 0 || slashIndex === raw.length - 1) {
      throw new Error(`${field} must be CIDR`)
    }
    const address = raw.slice(0, slashIndex)
    const bitsText = raw.slice(slashIndex + 1)
    if (!/^\d+$/.test(bitsText)) {
      throw new Error(`${field} must be CIDR`)
    }
    const bits = Number(bitsText)
    if (isMockIPv4Address(address)) {
      if (!Number.isInteger(bits) || bits < 0 || bits > 32) throw new Error(`${field} must be CIDR`)
      continue
    }
    if (isMockIPv6Address(address)) {
      if (!Number.isInteger(bits) || bits < 0 || bits > 128) throw new Error(`${field} must be CIDR`)
      continue
    }
    throw new Error(`${field} must be CIDR`)
  }
}

function validateMockWireGuardDNSAddrs(values) {
  for (const value of values) {
    if (!isMockIPAddress(value)) throw new Error('dns must be IP addresses')
  }
}

function normalizeMockWireGuardPeer(peer = {}) {
  return {
    name: String(peer.name || '').trim(),
    public_key: String(peer.public_key || '').trim(),
    preshared_key: String(peer.preshared_key || '').trim(),
    endpoint: String(peer.endpoint || '').trim(),
    allowed_ips: normalizeStringList(peer.allowed_ips),
    persistent_keepalive_seconds: peer.persistent_keepalive_seconds == null || peer.persistent_keepalive_seconds === ''
      ? null
      : Number(peer.persistent_keepalive_seconds)
  }
}

function allocateMockWireGuardAddress(agentId) {
  const used = new Set()
  for (const profile of mockWireGuardProfilesByAgent[agentId] || []) {
    for (const address of normalizeStringList(profile.addresses)) {
      const match = /^10\.8\.(\d{1,3})\.1\/24$/.exec(address)
      if (!match) continue
      const subnet = Number(match[1])
      if (Number.isInteger(subnet) && subnet >= 0 && subnet <= 255) used.add(subnet)
    }
  }
  for (let subnet = 0; subnet <= 255; subnet += 1) {
    if (!used.has(subnet)) return `10.8.${subnet}.1/24`
  }
  return '10.8.0.1/24'
}

function findMockWireGuardProfile(agentId, profileId) {
  return (mockWireGuardProfilesByAgent[agentId] || []).find((profile) => String(profile.id) === String(profileId))
}

function profileSubnetPrefix(profile = {}) {
  const address = normalizeStringList(profile.addresses)[0] || '10.8.0.1/24'
  const match = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.\d{1,3}\/\d{1,2}$/.exec(address)
  return match ? `${match[1]}.${match[2]}.${match[3]}` : '10.8.0'
}

function nextMockWireGuardClientAddress(agentId, profileId) {
  const profile = findMockWireGuardProfile(agentId, profileId)
  const prefix = profileSubnetPrefix(profile)
  const used = new Set((mockWireGuardClientsByProfile[agentId]?.[profileId] || [])
    .map((client) => {
      const match = new RegExp(`^${prefix.replace(/\./g, '\\.')}\\.(\\d{1,3})/32$`).exec(String(client.address || ''))
      return match ? Number(match[1]) : null
    })
    .filter((item) => Number.isInteger(item)))
  for (let host = 2; host <= 254; host += 1) {
    if (!used.has(host)) return `${prefix}.${host}/32`
  }
  return `${prefix}.2/32`
}

function normalizeMockWireGuardClient(agentId, profileId, client = {}) {
  const id = Number(client.id || ++mockWireGuardClientIdCounter)
  const address = String(client.address || '').trim() || nextMockWireGuardClientAddress(agentId, profileId)
  const allowedIPs = normalizeStringList(client.allowed_ips)
  const dns = normalizeStringList(client.dns)
  validateMockWireGuardPrefixes(allowedIPs.length ? allowedIPs : [address], 'allowed_ips')
  validateMockWireGuardDNSAddrs(dns)
  return {
    ...client,
    id: Number.isInteger(id) && id > 0 ? id : ++mockWireGuardClientIdCounter,
    name: String(client.name || '').trim() || `client-${id}`,
    address,
    public_key: String(client.public_key || `mock-client-public-key-${id}`).trim(),
    private_key: String(client.private_key || `mock-client-private-key-${id}`).trim(),
    preshared_key: String(client.preshared_key || `mock-client-psk-${id}`).trim(),
    dns,
    allowed_ips: allowedIPs.length ? allowedIPs : [address],
    enabled: client.enabled !== false,
    revision: Number(client.revision || Date.now())
  }
}

function normalizeMockWireGuardClientInput(payload = {}, profile = {}) {
  const allowedIPs = normalizeStringList(payload.allowed_ips)
  const dns = Object.prototype.hasOwnProperty.call(payload, 'dns')
    ? normalizeStringList(payload.dns)
    : normalizeStringList(profile.dns)
  validateMockWireGuardPrefixes(allowedIPs, 'allowed_ips')
  validateMockWireGuardDNSAddrs(dns)
  return {
    name: String(payload.name || '').trim(),
    allowed_ips: allowedIPs,
    dns,
    enabled: payload.enabled !== false
  }
}

function publicMockWireGuardClient(client = {}) {
  const { private_key, preshared_key, ...safeClient } = client
  return safeClient
}

function upsertMockWireGuardClientPeer(profile = {}, client = {}) {
  if (!profile) return
  const publicKey = String(client.public_key || '').trim()
  if (!publicKey) return
  profile.peers = (Array.isArray(profile.peers) ? profile.peers : [])
    .filter((peer) => String(peer.public_key || '').trim() !== publicKey)
  if (client.enabled === false) return
  profile.peers.push(normalizeMockWireGuardPeer({
    name: client.name,
    public_key: publicKey,
    preshared_key: client.preshared_key,
    allowed_ips: [client.address]
  }))
}

function reconcileMockWireGuardGeneratedClientPeers(agentId, profile = {}) {
  if (!profile) return profile
  for (const client of mockWireGuardClientsByProfile[agentId]?.[profile.id] || []) {
    upsertMockWireGuardClientPeer(profile, client)
  }
  return profile
}

function normalizeMockWireGuardProfile(agentId, profile = {}) {
  const id = Number(profile.id || ++mockWireGuardProfileIdCounter)
  return {
    ...profile,
    id: Number.isInteger(id) && id > 0 ? id : ++mockWireGuardProfileIdCounter,
    agent_id: String(profile.agent_id || agentId),
    name: String(profile.name || '').trim(),
    mode: 'generic_wireguard',
    private_key: String(profile.private_key || '').trim(),
    listen_port: profile.listen_port == null || profile.listen_port === '' ? null : Number(profile.listen_port),
    public_endpoint: String(profile.public_endpoint || '').trim(),
    addresses: normalizeStringList(profile.addresses),
    peers: Array.isArray(profile.peers) ? profile.peers.map((peer) => normalizeMockWireGuardPeer(peer)) : [],
    dns: normalizeStringList(profile.dns),
    mtu: profile.mtu == null || profile.mtu === '' ? null : Number(profile.mtu),
    enabled: profile.enabled !== false,
    tags: normalizeStringList(profile.tags),
    revision: Number(profile.revision || Date.now())
  }
}

function redactedMockWireGuardURI(uri) {
  const raw = String(uri || '').trim()
  if (!raw) return ''
  try {
    const parsed = new URL(raw)
    if (parsed.username) parsed.username = 'xxxxx'
    if (parsed.searchParams.has('psk')) parsed.searchParams.set('psk', 'xxxxx')
    return parsed.toString()
  } catch {
    return raw.replace(/^wireguard:\/\/[^@]+@/i, 'wireguard://xxxxx@')
  }
}

function parseMockWireGuardURIQuery(raw = '') {
  const out = {}
  for (const part of String(raw || '').replace(/^\?/, '').split('&')) {
    if (!part) continue
    const [rawKey, ...rawValueParts] = part.split('=')
    let key = ''
    let value = rawValueParts.join('=')
    try {
      key = decodeURIComponent(rawKey)
    } catch {
      continue
    }
    try {
      value = decodeURIComponent(value)
    } catch {
      // Keep the raw value if the preview URI contains a malformed escape.
    }
    out[String(key || '').trim().toLowerCase()] = value
  }
  return out
}

function parseMockWireGuardURI(uri) {
  const raw = String(uri || '').trim()
  const parsed = new URL(raw)
  if (parsed.protocol !== 'wireguard:') {
    throw new Error('wireguard URI scheme must be wireguard')
  }
  const query = parseMockWireGuardURIQuery(parsed.search)
  const addresses = normalizeStringList((query.address || query.addresses || '').split(','))
  const allowedIPs = normalizeStringList((query.allowedips || query.allowed_ips || '').split(','))
  const dns = normalizeStringList((query.dns || '').split(','))
  const publicKey = String(query.publickey || query.peer_public_key || '').trim()
  const mtu = Number(query.mtu || 0)
  if (!parsed.username || !parsed.hostname || !parsed.port || !publicKey || addresses.length === 0) {
    throw new Error('invalid wireguard URI')
  }
  if (query.reserved != null) {
    throw new Error('wireguard URI reserved is not supported')
  }
  return {
    name: decodeURIComponent(parsed.hash.replace(/^#/, '') || ''),
    private_key: parsed.username,
    endpoint: `${parsed.hostname}:${parsed.port}`,
    public_key: publicKey,
    preshared_key: String(query.psk || '').trim(),
    addresses,
    allowed_ips: allowedIPs.length ? allowedIPs : ['0.0.0.0/0', '::/0'],
    dns,
    mtu: Number.isInteger(mtu) && mtu > 0 ? mtu : null
  }
}

export async function fetchWireGuardProfiles(agentId) {
  if (isDev) {
    await sleep()
    ensureDevRelayAgentExists(agentId)
    return (mockWireGuardProfilesByAgent[agentId] || []).map((profile) => normalizeMockWireGuardProfile(agentId, profile))
  }
  const { data } = await api.get(`/agents/${encodeURIComponent(agentId)}/wireguard-profiles`)
  return data.profiles || []
}

export async function createWireGuardProfile(agentId, payload) {
  if (isDev) {
    await sleep()
    ensureDevRelayAgentExists(agentId)
    const addresses = normalizeStringList(payload.addresses)
    const profile = normalizeMockWireGuardProfile(agentId, {
      ...payload,
      id: ++mockWireGuardProfileIdCounter,
      agent_id: agentId,
      addresses: addresses.length ? addresses : [allocateMockWireGuardAddress(agentId)],
      revision: Date.now()
    })
    mockWireGuardProfilesByAgent[agentId] = mockWireGuardProfilesByAgent[agentId] || []
    mockWireGuardProfilesByAgent[agentId].push(profile)
    return profile
  }
  const { data } = await api.post(
    `/agents/${encodeURIComponent(agentId)}/wireguard-profiles`,
    payload,
    longRunningRequest
  )
  return data.profile
}

export async function updateWireGuardProfile(agentId, id, payload) {
  if (isDev) {
    await sleep()
    ensureDevRelayAgentExists(agentId)
    const list = mockWireGuardProfilesByAgent[agentId] || []
    const idx = list.findIndex((profile) => String(profile.id) === String(id))
    if (idx === -1) return null
    list[idx] = reconcileMockWireGuardGeneratedClientPeers(agentId, normalizeMockWireGuardProfile(agentId, {
      ...list[idx],
      ...payload,
      id: list[idx].id,
      revision: Date.now()
    }))
    return list[idx]
  }
  const { data } = await api.put(
    `/agents/${encodeURIComponent(agentId)}/wireguard-profiles/${encodeURIComponent(id)}`,
    payload,
    longRunningRequest
  )
  return data.profile
}

export async function deleteWireGuardProfile(agentId, id) {
  if (isDev) {
    await sleep()
    ensureDevRelayAgentExists(agentId)
    const list = mockWireGuardProfilesByAgent[agentId] || []
    const idx = list.findIndex((profile) => String(profile.id) === String(id))
    if (idx === -1) return null
    const deleted = list.splice(idx, 1)[0]
    if (mockWireGuardClientsByProfile[agentId]) {
      delete mockWireGuardClientsByProfile[agentId][id]
    }
    return deleted
  }
  const { data } = await api.delete(
    `/agents/${encodeURIComponent(agentId)}/wireguard-profiles/${encodeURIComponent(id)}`,
    longRunningRequest
  )
  return data.profile
}

export async function fetchWireGuardClients(agentId, profileId) {
  if (isDev) {
    await sleep()
    ensureDevRelayAgentExists(agentId)
    if (!findMockWireGuardProfile(agentId, profileId)) throw new Error(`WireGuard Profile not found: ${profileId}`)
    const clients = mockWireGuardClientsByProfile[agentId]?.[profileId] || []
    return clients.map((client) => publicMockWireGuardClient(normalizeMockWireGuardClient(agentId, profileId, client)))
  }
  const { data } = await api.get(
    `/agents/${encodeURIComponent(agentId)}/wireguard-profiles/${encodeURIComponent(profileId)}/clients`
  )
  return data.clients || []
}

export async function createWireGuardClient(agentId, profileId, payload) {
  if (isDev) {
    await sleep()
    ensureDevRelayAgentExists(agentId)
    const profile = findMockWireGuardProfile(agentId, profileId)
    if (!profile) throw new Error(`WireGuard Profile not found: ${profileId}`)
    const input = normalizeMockWireGuardClientInput(payload, profile)
    const client = normalizeMockWireGuardClient(agentId, profileId, {
      ...input,
      id: ++mockWireGuardClientIdCounter,
      revision: Date.now()
    })
    mockWireGuardClientsByProfile[agentId] = mockWireGuardClientsByProfile[agentId] || {}
    mockWireGuardClientsByProfile[agentId][profileId] = mockWireGuardClientsByProfile[agentId][profileId] || []
    mockWireGuardClientsByProfile[agentId][profileId].push(client)
    upsertMockWireGuardClientPeer(profile, client)
    return publicMockWireGuardClient(client)
  }
  const { data } = await api.post(
    `/agents/${encodeURIComponent(agentId)}/wireguard-profiles/${encodeURIComponent(profileId)}/clients`,
    payload,
    longRunningRequest
  )
  return data.client
}

export async function deleteWireGuardClient(agentId, profileId, clientId) {
  if (isDev) {
    await sleep()
    ensureDevRelayAgentExists(agentId)
    const profile = findMockWireGuardProfile(agentId, profileId)
    if (!profile) throw new Error(`WireGuard Profile not found: ${profileId}`)
    const clients = mockWireGuardClientsByProfile[agentId]?.[profileId] || []
    const idx = clients.findIndex((client) => String(client.id) === String(clientId))
    if (idx === -1) return null
    const deleted = clients.splice(idx, 1)[0]
    upsertMockWireGuardClientPeer(profile, { ...deleted, enabled: false })
    return publicMockWireGuardClient(deleted)
  }
  const { data } = await api.delete(
    `/agents/${encodeURIComponent(agentId)}/wireguard-profiles/${encodeURIComponent(profileId)}/clients/${encodeURIComponent(clientId)}`,
    longRunningRequest
  )
  return data.client
}

export async function updateWireGuardClient(agentId, profileId, clientId, payload) {
  if (isDev) {
    await sleep()
    ensureDevRelayAgentExists(agentId)
    if (!Object.prototype.hasOwnProperty.call(payload || {}, 'enabled') || typeof payload.enabled !== 'boolean') {
      throw new Error('enabled must be a boolean')
    }
    const profile = findMockWireGuardProfile(agentId, profileId)
    if (!profile) throw new Error(`WireGuard Profile not found: ${profileId}`)
    const clients = mockWireGuardClientsByProfile[agentId]?.[profileId] || []
    const idx = clients.findIndex((client) => String(client.id) === String(clientId))
    if (idx === -1) throw new Error(`WireGuard Client not found: ${clientId}`)
    clients[idx] = normalizeMockWireGuardClient(agentId, profileId, {
      ...clients[idx],
      enabled: payload.enabled,
      revision: Date.now()
    })
    upsertMockWireGuardClientPeer(profile, clients[idx])
    return publicMockWireGuardClient(clients[idx])
  }
  const { data } = await api.patch(
    `/agents/${encodeURIComponent(agentId)}/wireguard-profiles/${encodeURIComponent(profileId)}/clients/${encodeURIComponent(clientId)}`,
    payload,
    longRunningRequest
  )
  return data.client
}

export async function fetchWireGuardClientConfig(agentId, profileId, clientId) {
  if (isDev) {
    await sleep()
    ensureDevRelayAgentExists(agentId)
    const profile = findMockWireGuardProfile(agentId, profileId)
    if (!profile) throw new Error(`WireGuard Profile not found: ${profileId}`)
    if (!String(profile.public_endpoint || '').trim()) {
      throw new Error('public_endpoint is required to generate client config')
    }
    const client = (mockWireGuardClientsByProfile[agentId]?.[profileId] || [])
      .find((item) => String(item.id) === String(clientId))
    if (!client) throw new Error(`WireGuard Client not found: ${clientId}`)
    const dns = normalizeStringList(client.dns)
    return [
      '[Interface]',
      `PrivateKey = ${client.private_key}`,
      `Address = ${client.address}`,
      ...(dns.length ? [`DNS = ${dns.join(', ')}`] : []),
      '',
      '[Peer]',
      `PublicKey = mock-server-public-key-${profile.id}`,
      ...(client.preshared_key ? [`PresharedKey = ${client.preshared_key}`] : []),
      `AllowedIPs = ${normalizeStringList(client.allowed_ips).join(', ') || '0.0.0.0/0, ::/0'}`,
      `Endpoint = ${profile.public_endpoint}`,
      'PersistentKeepalive = 25',
      ''
    ].join('\n')
  }
  const { data } = await api.get(
    `/agents/${encodeURIComponent(agentId)}/wireguard-profiles/${encodeURIComponent(profileId)}/clients/${encodeURIComponent(clientId)}/config`,
    { responseType: 'text' }
  )
  return data
}

export async function fetchWireGuardClientURI(agentId, profileId, clientId, reserved = '') {
  const suffix = reserved ? `?reserved=${encodeURIComponent(reserved)}` : ''
  if (isDev) {
    await sleep()
    ensureDevRelayAgentExists(agentId)
    const profile = findMockWireGuardProfile(agentId, profileId)
    if (!profile) throw new Error(`WireGuard Profile not found: ${profileId}`)
    if (!String(profile.public_endpoint || '').trim()) {
      throw new Error('public_endpoint is required to generate client URI')
    }
    const client = (mockWireGuardClientsByProfile[agentId]?.[profileId] || [])
      .find((item) => String(item.id) === String(clientId))
    if (!client) throw new Error(`WireGuard Client not found: ${clientId}`)
    const uri = new URL(`wireguard://${encodeURIComponent(client.private_key)}@${profile.public_endpoint}`)
    uri.searchParams.set('publickey', `mock-server-public-key-${profile.id}`)
    if (client.preshared_key) uri.searchParams.set('psk', client.preshared_key)
    uri.searchParams.set('address', client.address)
    uri.searchParams.set('allowedips', normalizeStringList(client.allowed_ips).join(',') || '0.0.0.0/0,::/0')
    const dns = normalizeStringList(client.dns)
    if (dns.length) uri.searchParams.set('dns', dns.join(','))
    if (profile.mtu) uri.searchParams.set('mtu', String(profile.mtu))
    if (reserved) uri.searchParams.set('reserved', reserved)
    uri.hash = client.name || `client-${client.id}`
    return uri.toString()
  }
  const { data } = await api.get(
    `/agents/${encodeURIComponent(agentId)}/wireguard-profiles/${encodeURIComponent(profileId)}/clients/${encodeURIComponent(clientId)}/uri${suffix}`,
    { responseType: 'text' }
  )
  return data
}

export async function parseWireGuardURI(uri) {
  if (isDev) {
    await sleep()
    const parsed = parseMockWireGuardURI(uri)
    return {
      ok: true,
      uri: redactedMockWireGuardURI(uri),
      profile: {
        name: parsed.name,
        endpoint: parsed.endpoint,
        public_key: parsed.public_key,
        addresses: parsed.addresses,
        allowed_ips: parsed.allowed_ips,
        dns: parsed.dns,
        mtu: parsed.mtu
      }
    }
  }
  const { data } = await api.post('/wireguard/parse-uri', { uri })
  return data
}

export async function importWireGuardURIProfile(agentId, uri, name = '') {
  if (isDev) {
    await sleep()
    ensureDevRelayAgentExists(agentId)
    const parsed = parseMockWireGuardURI(uri)
    const profile = normalizeMockWireGuardProfile(agentId, {
      id: ++mockWireGuardProfileIdCounter,
      agent_id: agentId,
      name: parsed.name || String(name || '').trim() || `wireguard-${mockWireGuardProfileIdCounter}`,
      private_key: 'xxxxx',
      listen_port: 0,
      public_endpoint: parsed.endpoint,
      addresses: parsed.addresses,
      peers: [{
        name: 'egress',
        public_key: parsed.public_key,
        preshared_key: parsed.preshared_key ? 'xxxxx' : '',
        endpoint: parsed.endpoint,
        allowed_ips: parsed.allowed_ips,
        persistent_keepalive_seconds: 0
      }],
      dns: parsed.dns,
      mtu: parsed.mtu,
      enabled: true,
      revision: Date.now()
    })
    mockWireGuardProfilesByAgent[agentId] = mockWireGuardProfilesByAgent[agentId] || []
    mockWireGuardProfilesByAgent[agentId].push(profile)
    return profile
  }
  const payload = { uri }
  if (String(name || '').trim()) payload.name = String(name || '').trim()
  const { data } = await api.post(
    `/agents/${encodeURIComponent(agentId)}/wireguard-profiles/import-uri`,
    payload,
    longRunningRequest
  )
  return data.profile
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

export async function exportBackupSelective(include) {
  if (isDev) {
    await sleep()
    return exportBackup()
  }
}

export async function importBackupPreview(file) {
  if (isDev) {
    await sleep(600)
    return importBackup(file)
  }
}

export async function fetchBackupResourceCounts() {
  if (isDev) {
    await sleep(200)
    return {
      ok: true,
      counts: {
        agents: 3,
        http_rules: 12,
        l4_rules: 4,
        relay_listeners: 2,
        certificates: 5,
        version_policies: 1
      }
    }
  }
}

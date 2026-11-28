const INITIAL_SERVICES = [
  {
    id: 'svc-emby-ready',
    name: 'Emby 家庭影院',
    entry_agent_id: 'local',
    service_agent_id: 'local',
    backend_url: 'http://127.0.0.1:8096',
    entry_url: 'https://emby.example.com',
    rule_id: 101,
    switch_phase: '',
    summary: '已就绪',
    https_ready: true,
    advanced: false,
    advanced_reason: '',
    last_error: '',
    path: { mode: 'direct', summary: '本机直连', pending: false },
    certificate: { state: 'ready', https_ready: true, not_after: '2027-02-15' },
    probes: [
      {
        agent_id: 'local',
        purpose: 'backend_http',
        status: 'http_status',
        http_status_code: 200,
        target: 'http://127.0.0.1:8096/System/Info/Public',
        checked_at: '2026-11-28T08:10:00Z',
        verification: 'fresh',
      },
    ],
    rules: [
      {
        id: 101,
        agent_id: 'local',
        frontend_url: 'https://emby.example.com',
        backends: [{ url: 'http://127.0.0.1:8096' }],
      },
    ],
    traffic: {
      state: 'available',
      accounted_bytes: 28 * 1024 * 1024 * 1024 + 412 * 1024 * 1024,
      label: '近 24 小时',
    },
    client_confirmation: {
      confirmed: true,
      confirmed_at: '2026-11-28T07:45:00Z',
      kind: 'user_confirmation',
    },
    deletion: null,
    apply: { pending: false, new_entry_applied: true, certificate_ready: true },
    created_at: '2026-11-20T10:00:00Z',
    updated_at: '2026-11-28T08:10:00Z',
  },
  {
    id: 'svc-jellyfin-switching',
    name: 'Jellyfin 客厅端',
    entry_agent_id: 'edge-1',
    service_agent_id: 'local',
    backend_url: 'http://192.168.10.24:8096',
    entry_url: 'https://jf.example.com',
    rule_id: 204,
    pending_rule_id: 205,
    pending_rule_agent_id: 'edge-2',
    switch_phase: '等待新入口生效',
    summary: '待确认',
    https_ready: false,
    advanced: false,
    advanced_reason: '',
    last_error: '',
    path: { mode: 'relay', summary: '单跳 Relay 转发', pending: true },
    certificate: { state: 'issuing', https_ready: false },
    probes: [
      {
        agent_id: 'edge-1',
        purpose: 'entry_to_service',
        status: 'http_status',
        http_status_code: 302,
        target: 'http://192.168.10.24:8096',
        checked_at: '2026-11-27T18:20:00Z',
        verification: 'expired',
      },
      {
        agent_id: 'local',
        purpose: 'backend_tcp',
        status: 'connected',
        target: '192.168.10.24:8096',
        checked_at: '2026-11-28T08:05:00Z',
        verification: 'fresh',
      },
    ],
    rules: [
      {
        id: 204,
        agent_id: 'edge-1',
        frontend_url: 'https://jf.example.com',
        backends: [{ url: 'http://192.168.10.24:8096' }],
      },
    ],
    traffic: { state: 'unavailable' },
    client_confirmation: null,
    deletion: null,
    apply: { pending: true, old_entry_may_receive: true, listener_applied: true },
    created_at: '2026-11-25T14:30:00Z',
    updated_at: '2026-11-28T08:05:00Z',
  },
  {
    id: 'svc-plex-failed',
    name: 'Plex 影音库',
    entry_agent_id: 'edge-2',
    service_agent_id: 'hk-01',
    backend_url: 'http://10.10.4.18:32400',
    entry_url: 'https://plex.example.com',
    rule_id: 308,
    switch_phase: '',
    summary: '失败',
    https_ready: false,
    advanced: false,
    advanced_reason: '',
    last_error: 'ACME HTTP-01 校验失败：域名解析未指向入口节点',
    path: { mode: 'relay', summary: '跨节点 Relay', pending: false },
    certificate: {
      state: 'error',
      https_ready: false,
      last_error: 'ACME HTTP-01 校验失败',
    },
    probes: [
      {
        agent_id: 'edge-2',
        purpose: 'backend_http',
        status: 'unreachable',
        target: 'http://10.10.4.18:32400',
        checked_at: '2026-11-28T07:55:00Z',
        error: 'dial tcp 10.10.4.18:32400: i/o timeout',
      },
    ],
    rules: [
      {
        id: 308,
        agent_id: 'edge-2',
        frontend_url: 'https://plex.example.com',
        backends: [{ url: 'http://10.10.4.18:32400' }],
      },
    ],
    traffic: { state: 'unavailable' },
    client_confirmation: null,
    deletion: null,
    apply: { pending: false },
    created_at: '2026-11-26T09:15:00Z',
    updated_at: '2026-11-28T07:55:00Z',
  },
  {
    id: 'svc-media-advanced',
    name: '多线媒体聚合入口',
    entry_agent_id: 'edge-1',
    service_agent_id: 'sg-01',
    backend_url: 'http://10.20.0.10:8096',
    entry_url: 'https://hub.example.com',
    rule_id: 412,
    switch_phase: '',
    summary: '已就绪',
    https_ready: true,
    advanced: true,
    advanced_reason: '关联规则包含多个后端和自定义路由头，请前往高级规则页调整转发配置。',
    last_error: '',
    path: { mode: 'direct', summary: '自定义规则直连', pending: false },
    certificate: { state: 'ready', https_ready: true, not_after: '2027-05-01' },
    probes: [
      {
        agent_id: 'sg-01',
        purpose: 'backend_http',
        status: 'http_status',
        http_status_code: 200,
        target: 'http://10.20.0.10:8096/health',
        checked_at: '2026-11-28T08:00:00Z',
        verification: 'fresh',
      },
    ],
    rules: [
      {
        id: 412,
        agent_id: 'edge-1',
        frontend_url: 'https://hub.example.com',
        backends: [
          { url: 'http://10.20.0.10:8096' },
          { url: 'http://10.20.0.11:8096' },
          {
            kind: 'plugin_provider',
            plugin_provider: { provider_id: 'official.emby-helper', instance_id: 'inst-emby' },
          },
        ],
      },
    ],
    traffic: {
      state: 'available',
      rx_bytes: 1850 * 1024 * 1024,
      tx_bytes: 6420 * 1024 * 1024,
      label: '近 24 小时',
    },
    client_confirmation: {
      confirmed: true,
      confirmed_at: '2026-11-27T11:20:00Z',
      kind: 'user_confirmation',
    },
    deletion: null,
    apply: { pending: false, new_entry_applied: true, certificate_ready: true },
    created_at: '2026-11-18T16:40:00Z',
    updated_at: '2026-11-28T08:00:00Z',
  },
]

function clone(value) {
  return JSON.parse(JSON.stringify(value))
}

let services = clone(INITIAL_SERVICES)
let nextRuleId = 500
const operations = new Map()

function readDevMockFlags() {
  if (typeof window === 'undefined' || !window.localStorage) return {}
  try {
    return JSON.parse(window.localStorage.getItem('panel_dev_mock_flags') || '{}')
  } catch {
    return {}
  }
}

function defaultCertificate(entryUrl) {
  const https = String(entryUrl || '').startsWith('https:')
  return https
    ? { state: 'pending', https_ready: false }
    : { state: 'none', https_ready: false }
}

function present(row) {
  const certificate = row.certificate
    ? clone(row.certificate)
    : defaultCertificate(row.entry_url)
  return {
    id: row.id,
    name: row.name || '',
    entry_agent_id: row.entry_agent_id,
    service_agent_id: row.service_agent_id,
    backend_url: row.backend_url,
    entry_url: row.entry_url,
    ...(row.relay_listen_port != null && row.relay_listen_port !== ''
      ? { relay_listen_port: row.relay_listen_port }
      : {}),
    ...(row.relay_public_host ? { relay_public_host: row.relay_public_host } : {}),
    rule_id: row.rule_id || 0,
    ...(row.pending_rule_id ? { pending_rule_id: row.pending_rule_id } : {}),
    ...(row.pending_rule_agent_id ? { pending_rule_agent_id: row.pending_rule_agent_id } : {}),
    switch_phase: row.switch_phase || '',
    summary: row.summary || '待确认',
    https_ready: Boolean(row.https_ready ?? certificate.https_ready),
    advanced: Boolean(row.advanced),
    advanced_reason: row.advanced_reason || '',
    last_error: row.last_error || '',
    path: row.path ? clone(row.path) : { mode: 'pending', summary: '待确认', pending: true },
    certificate,
    probes: Array.isArray(row.probes) ? clone(row.probes) : [],
    rules: Array.isArray(row.rules) ? clone(row.rules) : [],
    traffic: row.traffic ? clone(row.traffic) : { state: 'unavailable' },
    client_confirmation: row.client_confirmation ? clone(row.client_confirmation) : null,
    deletion: row.deletion ? clone(row.deletion) : null,
    apply: row.apply ? clone(row.apply) : { pending: true },
    created_at: row.created_at,
    updated_at: row.updated_at,
  }
}

function remember(operation, key, body, value) {
  if (!key) return
  operations.set(`${operation}\n${key}`, { body: JSON.stringify(body ?? null), value: clone(value) })
}

function replay(operation, key, body) {
  if (!key) return null
  const saved = operations.get(`${operation}\n${key}`)
  if (!saved) return null
  if (saved.body !== JSON.stringify(body ?? null)) {
    const error = new Error('idempotency key was already used with a different request')
    error.status = 409
    throw error
  }
  return clone(saved.value)
}

function notFoundError() {
  const error = new Error('access service not found')
  error.status = 404
  return error
}

export function resetAccessServicesDevMock() {
  services = clone(INITIAL_SERVICES)
  nextRuleId = 500
  operations.clear()
}

export async function fetchAccessServices() {
  if (readDevMockFlags().emptyAccessServices === true) return []
  return services.map((row) => present(row))
}

export async function fetchAccessService(id) {
  if (readDevMockFlags().emptyAccessServices === true) throw notFoundError()
  const row = services.find((item) => item.id === id)
  if (!row) throw notFoundError()
  return present(row)
}

export async function createAccessService(payload, idempotencyKey) {
  const replayed = replay('create', idempotencyKey, payload)
  if (replayed) return replayed
  const now = new Date().toISOString()
  const ruleId = nextRuleId++
  const row = {
    id: newId(),
    ...payload,
    rule_id: ruleId,
    summary: '待确认',
    path: { mode: 'pending', summary: '待确认', pending: true },
    certificate: defaultCertificate(payload?.entry_url),
    rules: [
      {
        id: ruleId,
        agent_id: payload?.entry_agent_id || '',
        frontend_url: payload?.entry_url || '',
        backends: payload?.backend_url ? [{ url: payload.backend_url }] : [],
      },
    ],
    apply: { pending: true },
    created_at: now,
    updated_at: now,
  }
  services.unshift(row)
  const view = present(row)
  remember('create', idempotencyKey, payload, view)
  return view
}

export async function updateAccessService(id, payload, idempotencyKey) {
  const replayed = replay(`update:${id}`, idempotencyKey, payload)
  if (replayed) return replayed
  const row = services.find((item) => item.id === id)
  if (!row) throw notFoundError()
  const prevHttps = String(row.entry_url || '').startsWith('https:')
  Object.assign(row, payload, { updated_at: new Date().toISOString() })
  const nextHttps = String(row.entry_url || '').startsWith('https:')
  if (prevHttps !== nextHttps) {
    row.certificate = defaultCertificate(row.entry_url)
    row.https_ready = Boolean(row.certificate.https_ready)
  }
  if (!row.advanced && Array.isArray(row.rules) && row.rules.length === 1) {
    row.rules[0] = {
      ...row.rules[0],
      agent_id: row.entry_agent_id,
      frontend_url: row.entry_url,
      backends: row.backend_url ? [{ url: row.backend_url }] : [],
    }
  }
  const view = present(row)
  remember(`update:${id}`, idempotencyKey, payload, view)
  return view
}

export async function correctAccessService(id, payload, idempotencyKey) {
  const replayed = replay(`correct:${id}`, idempotencyKey, payload)
  if (replayed) return replayed
  const row = services.find((item) => item.id === id)
  if (!row) throw notFoundError()
  const prevHttps = String(row.entry_url || '').startsWith('https:')
  Object.assign(row, payload, {
    summary: '待确认',
    last_error: '',
    apply: { pending: true },
    updated_at: new Date().toISOString(),
  })
  const nextHttps = String(row.entry_url || '').startsWith('https:')
  if (prevHttps !== nextHttps || row.certificate?.state === 'error') {
    row.certificate = defaultCertificate(row.entry_url)
    row.https_ready = Boolean(row.certificate.https_ready)
  }
  if (!row.advanced && Array.isArray(row.rules) && row.rules.length === 1) {
    row.rules[0] = {
      ...row.rules[0],
      agent_id: row.entry_agent_id,
      frontend_url: row.entry_url,
      backends: row.backend_url ? [{ url: row.backend_url }] : [],
    }
  }
  const view = present(row)
  remember(`correct:${id}`, idempotencyKey, payload, view)
  return view
}

export async function retryAccessService(id, idempotencyKey) {
  const replayed = replay(`retry:${id}`, idempotencyKey, {})
  if (replayed) return replayed
  const row = services.find((item) => item.id === id)
  if (!row) throw notFoundError()
  row.summary = row.summary === '失败' ? '待确认' : (row.summary || '待确认')
  row.apply = { ...(row.apply || {}), pending: true }
  row.updated_at = new Date().toISOString()
  const view = present(row)
  remember(`retry:${id}`, idempotencyKey, {}, view)
  return view
}

export async function deleteAccessService(id, idempotencyKey) {
  const replayed = replay(`delete:${id}`, idempotencyKey, {})
  if (replayed) return replayed
  const row = services.find((item) => item.id === id)
  if (!row) throw notFoundError()
  row.deletion = {
    pending: true,
    stopped_entries: [{ agent_id: row.entry_agent_id, rule_id: row.rule_id || 0, url: row.entry_url }],
    retained: [
      { kind: 'certificate', reason: '共享证书和不是本服务单独拥有的证书会保留' },
      { kind: 'relay_listener', reason: '仍被引用或不是本服务创建的监听器会保留' },
      { kind: 'agent', reason: '节点会保留' },
    ],
    note: '删除尚未在节点生效',
  }
  row.summary = '已保存、待生效'
  row.apply = { ...(row.apply || {}), pending: true }
  row.updated_at = new Date().toISOString()
  const view = present(row)
  remember(`delete:${id}`, idempotencyKey, {}, view)
  return view
}

export async function confirmAccessServiceClient(id, payload, idempotencyKey) {
  const replayed = replay(`confirm:${id}`, idempotencyKey, payload)
  if (replayed) return replayed
  const row = services.find((item) => item.id === id)
  if (!row) throw notFoundError()
  row.client_confirmation = {
    confirmed: payload?.confirmed === true,
    confirmed_at: payload?.confirmed ? new Date().toISOString() : '',
    note: payload?.note || '',
    kind: 'user_confirmation',
  }
  row.updated_at = new Date().toISOString()
  const view = present(row)
  remember(`confirm:${id}`, idempotencyKey, payload, view)
  return view
}

export async function retireAccessService(id, idempotencyKey) {
  const replayed = replay(`retire:${id}`, idempotencyKey, {})
  if (replayed) return replayed
  const row = services.find((item) => item.id === id)
  if (!row) throw notFoundError()
  row.switch_phase = '退役旧入口'
  row.updated_at = new Date().toISOString()
  const view = present(row)
  remember(`retire:${id}`, idempotencyKey, {}, view)
  return view
}

export async function abandonAccessService(id, idempotencyKey) {
  const replayed = replay(`abandon:${id}`, idempotencyKey, {})
  if (replayed) return replayed
  const row = services.find((item) => item.id === id)
  if (!row) throw notFoundError()
  row.switch_phase = ''
  row.pending_rule_id = 0
  row.pending_rule_agent_id = ''
  row.updated_at = new Date().toISOString()
  const view = present(row)
  remember(`abandon:${id}`, idempotencyKey, {}, view)
  return view
}

function newId() {
  if (globalThis.crypto?.randomUUID) return globalThis.crypto.randomUUID()
  return `svc-${Date.now().toString(16)}`
}

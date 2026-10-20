const services = []
const operations = new Map()

function clone(value) {
  return JSON.parse(JSON.stringify(value))
}

function present(row) {
  const https = String(row.entry_url || '').startsWith('https:')
  return {
    id: row.id,
    name: row.name || '',
    entry_agent_id: row.entry_agent_id,
    service_agent_id: row.service_agent_id,
    backend_url: row.backend_url,
    entry_url: row.entry_url,
    rule_id: row.rule_id || 0,
    switch_phase: row.switch_phase || '',
    summary: row.summary || '待确认',
    https_ready: false,
    advanced: Boolean(row.advanced),
    advanced_reason: row.advanced_reason || '',
    last_error: row.last_error || '',
    path: row.path || { mode: 'pending', summary: '待确认', pending: true },
    certificate: https
      ? { state: 'pending', https_ready: false }
      : { state: 'none', https_ready: false },
    probes: Array.isArray(row.probes) ? row.probes : [],
    rules: Array.isArray(row.rules) ? row.rules : [],
    traffic: row.traffic || { state: 'unavailable' },
    client_confirmation: row.client_confirmation || null,
    deletion: row.deletion || null,
    apply: row.apply || { pending: true },
    created_at: row.created_at,
    updated_at: row.updated_at,
  }
}

function remember(operation, key, body, value) {
  operations.set(`${operation}\n${key}`, { body: JSON.stringify(body ?? null), value: clone(value) })
}

function replay(operation, key, body) {
  const saved = operations.get(`${operation}\n${key}`)
  if (!saved) return null
  if (saved.body !== JSON.stringify(body ?? null)) {
    const error = new Error('idempotency key was already used with a different request')
    error.status = 409
    throw error
  }
  return clone(saved.value)
}

export async function fetchAccessServices() {
  return services.map((row) => present(row))
}

export async function fetchAccessService(id) {
  const row = services.find((item) => item.id === id)
  if (!row) {
    const error = new Error('access service not found')
    error.status = 404
    throw error
  }
  return present(row)
}

export async function createAccessService(payload, idempotencyKey) {
  const replayed = replay('create', idempotencyKey, payload)
  if (replayed) return replayed
  const now = new Date().toISOString()
  const row = {
    id: newId(),
    ...payload,
    summary: '待确认',
    path: { mode: 'pending', summary: '待确认', pending: true },
    created_at: now,
    updated_at: now,
  }
  services.push(row)
  const view = present(row)
  remember('create', idempotencyKey, payload, view)
  return view
}

export async function updateAccessService(id, payload, idempotencyKey) {
  const replayed = replay(`update:${id}`, idempotencyKey, payload)
  if (replayed) return replayed
  const row = services.find((item) => item.id === id)
  if (!row) {
    const error = new Error('access service not found')
    error.status = 404
    throw error
  }
  Object.assign(row, payload, { updated_at: new Date().toISOString() })
  const view = present(row)
  remember(`update:${id}`, idempotencyKey, payload, view)
  return view
}

export async function correctAccessService(id, payload, idempotencyKey) {
  return updateAccessService(id, { ...payload, summary: '待确认' }, idempotencyKey)
}

export async function retryAccessService(id, idempotencyKey) {
  const row = services.find((item) => item.id === id)
  if (!row) {
    const error = new Error('access service not found')
    error.status = 404
    throw error
  }
  row.summary = row.summary || '待确认'
  row.updated_at = new Date().toISOString()
  const view = present(row)
  remember(`retry:${id}`, idempotencyKey, {}, view)
  return view
}

export async function deleteAccessService(id, idempotencyKey) {
  const replayed = replay(`delete:${id}`, idempotencyKey, {})
  if (replayed) return replayed
  const row = services.find((item) => item.id === id)
  if (!row) {
    const error = new Error('access service not found')
    error.status = 404
    throw error
  }
  row.deletion = {
    pending: true,
    stopped_entries: [{ agent_id: row.entry_agent_id, rule_id: row.rule_id || 0, url: row.entry_url }],
    retained: [
      { kind: 'certificate', reason: '共享证书和不是本服务单独拥有的证书会保留' },
      { kind: 'agent', reason: '节点会保留' },
    ],
    note: '删除尚未在节点生效',
  }
  row.summary = '已保存、待生效'
  row.updated_at = new Date().toISOString()
  const view = present(row)
  remember(`delete:${id}`, idempotencyKey, {}, view)
  return view
}

export async function confirmAccessServiceClient(id, payload, idempotencyKey) {
  const row = services.find((item) => item.id === id)
  if (!row) {
    const error = new Error('access service not found')
    error.status = 404
    throw error
  }
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
  const row = services.find((item) => item.id === id)
  if (!row) {
    const error = new Error('access service not found')
    error.status = 404
    throw error
  }
  row.switch_phase = '退役旧入口'
  row.updated_at = new Date().toISOString()
  const view = present(row)
  remember(`retire:${id}`, idempotencyKey, {}, view)
  return view
}

export async function abandonAccessService(id, idempotencyKey) {
  const row = services.find((item) => item.id === id)
  if (!row) {
    const error = new Error('access service not found')
    error.status = 404
    throw error
  }
  row.switch_phase = ''
  row.updated_at = new Date().toISOString()
  const view = present(row)
  remember(`abandon:${id}`, idempotencyKey, {}, view)
  return view
}

function newId() {
  if (globalThis.crypto?.randomUUID) return globalThis.crypto.randomUUID()
  return `svc-${Date.now().toString(16)}`
}

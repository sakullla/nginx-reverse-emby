const HEALTH_PATHS = new Set(['/health', '/healthz', '/__nre/health'])
const HOP_BY_HOP_HEADERS = new Set([
  'connection',
  'keep-alive',
  'proxy-authenticate',
  'proxy-authorization',
  'te',
  'trailer',
  'transfer-encoding',
  'upgrade'
])
const CLOUDFLARE_REQUEST_HEADERS = new Set([
  'cf-connecting-ip',
  'cf-ipcountry',
  'cf-ray',
  'cf-visitor',
  'cf-worker',
  'cdn-loop'
])
const SENSITIVE_RESPONSE_HEADERS = new Set([
  'authorization',
  'proxy-authorization',
  'x-panel-token',
  'x-register-token',
  'x-agent-token',
  'x-nre-worker-token'
])

export default {
  async fetch(request, env, ctx) {
    return handleRequest(request, env, ctx)
  }
}

export async function handleRequest(request, env) {
  const url = new URL(request.url)
  let config
  try {
    config = validateEnv(env)
  } catch (err) {
    return jsonResponse(500, {
      ok: false,
      message: err.message
    })
  }

  if (HEALTH_PATHS.has(url.pathname)) {
    return jsonResponse(200, {
      ok: true,
      service: 'nre-cloudflare-worker',
      upstream: config.masterUrl
    })
  }

  if (!['http:', 'https:'].includes(url.protocol)) {
    return jsonResponse(400, { ok: false, message: 'unsupported request scheme' })
  }

  try {
    const upstreamRequest = buildUpstreamRequest(request, config)
    const response = await fetch(upstreamRequest)
    return sanitizeResponse(response)
  } catch {
    return jsonResponse(502, {
      ok: false,
      message: 'upstream request failed'
    })
  }
}

export function validateEnv(env = {}) {
  const masterUrl = normalizeMasterUrl(env.NRE_MASTER_URL)
  const token = String(env.NRE_WORKER_TOKEN || '').trim()

  if (!masterUrl) {
    throw new Error('NRE_MASTER_URL must be a valid https URL')
  }
  if (!token) {
    throw new Error('NRE_WORKER_TOKEN is required')
  }

  return { masterUrl, token }
}

export function normalizeMasterUrl(raw) {
  const value = String(raw || '').trim()
  if (!value) return ''

  try {
    const parsed = new URL(value)
    if (parsed.protocol !== 'https:' || !parsed.hostname) return ''
    parsed.hash = ''
    parsed.search = ''
    parsed.pathname = parsed.pathname.replace(/\/+$/, '')
    if (parsed.pathname.endsWith('/panel-api')) {
      parsed.pathname = parsed.pathname.slice(0, -'/panel-api'.length)
    }
    if (parsed.pathname.endsWith('/api')) {
      parsed.pathname = parsed.pathname.slice(0, -'/api'.length)
    }
    return parsed.toString().replace(/\/+$/, '')
  } catch {
    return ''
  }
}

export function buildUpstreamRequest(request, config) {
  const incomingURL = new URL(request.url)
  const upstreamURL = new URL(config.masterUrl)
  upstreamURL.pathname = joinPaths(upstreamURL.pathname, incomingURL.pathname)
  upstreamURL.search = incomingURL.search

  const headers = copyRequestHeaders(request.headers)
  headers.set('X-Panel-Token', config.token)
  headers.set('X-NRE-Worker', 'cloudflare')
  headers.set('X-Forwarded-Host', incomingURL.host)
  headers.set('X-Forwarded-Proto', incomingURL.protocol.replace(':', ''))

  const init = {
    method: request.method,
    headers,
    redirect: 'manual'
  }

  if (!['GET', 'HEAD'].includes(request.method.toUpperCase())) {
    init.body = request.body
    init.duplex = 'half'
  }

  return new Request(upstreamURL.toString(), init)
}

function copyRequestHeaders(source) {
  const headers = new Headers()
  for (const [name, value] of source.entries()) {
    const lower = name.toLowerCase()
    if (HOP_BY_HOP_HEADERS.has(lower)) continue
    if (CLOUDFLARE_REQUEST_HEADERS.has(lower)) continue
    if (SENSITIVE_RESPONSE_HEADERS.has(lower)) continue
    if (lower === 'host' || lower === 'content-length') continue
    headers.append(name, value)
  }
  return headers
}

function sanitizeResponse(response) {
  const headers = new Headers(response.headers)
  for (const name of SENSITIVE_RESPONSE_HEADERS) {
    headers.delete(name)
  }
  for (const name of HOP_BY_HOP_HEADERS) {
    headers.delete(name)
  }
  return new Response(response.body, {
    status: response.status,
    statusText: response.statusText,
    headers
  })
}

function joinPaths(basePath, requestPath) {
  const base = String(basePath || '').replace(/\/+$/, '')
  const next = String(requestPath || '/')
  if (!base) return next.startsWith('/') ? next : `/${next}`
  return `${base}/${next.replace(/^\/+/, '')}`
}

function jsonResponse(status, payload) {
  return new Response(JSON.stringify(payload), {
    status,
    headers: {
      'Content-Type': 'application/json; charset=utf-8',
      'Cache-Control': 'no-store'
    }
  })
}

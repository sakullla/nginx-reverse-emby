import { api, longRunningRequest } from './client'

const TERMINAL_STATES = new Set(['succeeded', 'failed', 'cancelled'])
const OPERATION_STATES = new Set(['accepted', 'running', 'blocked', 'succeeded', 'failed', 'cancelled'])

export const PKI_CONFIRMATION_ACTION = Object.freeze({
  revoke: 'revoke',
  forceRotate: 'force_rotate',
  rotateCA: 'ca_rotate',
  emergencyRotateCA: 'emergency_ca_rotate',
  activate: 'activate'
})

function text(value) {
  return String(value ?? '').trim()
}

function canonicalOperationState(value) {
  const state = text(value).toLowerCase()
  if (state === 'pending' || state === 'accepted' || state === 'queued') return 'accepted'
  if (state === 'applying' || state === 'in_progress') return 'running'
  if (state === 'complete' || state === 'completed' || state === 'success') return 'succeeded'
  return OPERATION_STATES.has(state) ? state : 'accepted'
}

export function safePkiOperationStatusURL(value) {
  const candidate = text(value)
  return /^\/(?:panel-api|api)\/pki\/operations\/[^/?#]+$/.test(candidate) ? candidate : ''
}

export function normalizePkiOperation(payload = {}) {
  const envelope = payload && typeof payload === 'object' ? payload : {}
  const raw = envelope.operation && typeof envelope.operation === 'object'
    ? { ...envelope, ...envelope.operation }
    : envelope
  const id = text(raw.id || raw.operation_id || envelope.operation_id)
  const state = canonicalOperationState(raw.state || raw.status || envelope.status)
  const lastError = text(raw.last_error || raw.error || raw.message)
  const statusURL = safePkiOperationStatusURL(
    envelope.status_url || raw.status_url || (id ? `/panel-api/pki/operations/${encodeURIComponent(id)}` : '')
  )

  return {
    id,
    operation_id: id,
    status_url: statusURL,
    kind: text(raw.kind),
    target_type: text(raw.target_type),
    target_id: text(raw.target_id),
    state,
    phase: text(raw.phase),
    created_at: raw.created_at || '',
    updated_at: raw.updated_at || '',
    last_error: lastError,
    result: raw.result && typeof raw.result === 'object' ? { ...raw.result } : null,
    terminal: TERMINAL_STATES.has(state)
  }
}

function resource(data, key, fallback) {
  if (data && Object.prototype.hasOwnProperty.call(data, key)) return data[key]
  return fallback
}

export async function fetchPkiOverview() {
  const { data } = await api.get('/pki/overview')
  return resource(data, 'overview', {}) || {}
}

export async function fetchPkiAuthorities() {
  const { data } = await api.get('/pki/authorities')
  const value = resource(data, 'authorities', [])
  return Array.isArray(value) ? value : []
}

export async function fetchPkiIdentities() {
  const { data } = await api.get('/pki/identities')
  const value = resource(data, 'identities', [])
  return Array.isArray(value) ? value : []
}

export async function fetchPkiCertificates() {
  const { data } = await api.get('/pki/certificates')
  const value = resource(data, 'certificates', [])
  return Array.isArray(value) ? value : []
}

export async function fetchPkiAlerts() {
  const { data } = await api.get('/pki/alerts')
  const value = resource(data, 'alerts', [])
  return Array.isArray(value) ? value : []
}

export function pkiEventParams(filters = {}) {
  const params = {}
  const fields = [
    ['type', filters.type],
    ['identity_id', filters.identity_id ?? filters.identityID],
    ['serial', filters.serial],
    ['operator_id', filters.operator_id ?? filters.operatorID],
    ['source', filters.source],
    ['result', filters.result],
    ['ca_generation', filters.ca_generation ?? filters.caGeneration],
    ['from', filters.from],
    ['to', filters.to]
  ]
  for (const [key, value] of fields) {
    const normalized = text(value)
    if (normalized) params[key] = normalized
  }
  return params
}

export async function fetchPkiEvents(filters = {}) {
  const params = pkiEventParams(filters)
  const { data } = await api.get('/pki/events', { params })
  const value = resource(data, 'events', [])
  return Array.isArray(value) ? value : []
}

export async function createPkiEnrollmentToken({ scope = 'new_agent', boundAgentId = '' } = {}) {
  const { data } = await api.post('/pki/enrollment-tokens', {
    scope: text(scope),
    bound_agent_id: text(boundAgentId)
  })
  const value = resource(data, 'enrollment_token', null)
  return value && typeof value === 'object' ? { ...value } : null
}

export async function issuePkiConfirmation(action, targetID = '') {
  const { data } = await api.post('/pki/confirmations', {
    action: text(action),
    target_id: text(targetID)
  })
  const value = resource(data, 'confirmation', null)
  return value && typeof value === 'object' ? { ...value } : null
}

async function postPkiAction(path, body = {}, config = longRunningRequest) {
  const { data } = await api.post(path, body, config)
  return normalizePkiOperation(data)
}

function actionBody({ reason = '', confirmationNonce = '', passphrase = '', force = false } = {}) {
  const body = {
    reason: text(reason),
    confirmation_nonce: text(confirmationNonce)
  }
  if (passphrase) body.passphrase = String(passphrase)
  if (force) body.force = true
  return body
}

export function revokePkiIdentity(identityID, options = {}) {
  const id = text(identityID)
  if (!id) return Promise.reject(new Error('internal PKI identity is required'))
  return postPkiAction(`/pki/identities/${encodeURIComponent(id)}/revoke`, actionBody(options))
}

export function forceRotatePkiIdentity(identityID, options = {}) {
  const id = text(identityID)
  if (!id) return Promise.reject(new Error('internal PKI identity is required'))
  return postPkiAction(`/pki/identities/${encodeURIComponent(id)}/force-rotate`, actionBody(options))
}

export function rotatePkiAuthority(options = {}) {
  return postPkiAction('/pki/authorities/rotate', actionBody(options))
}

export function emergencyRotatePkiAuthority(options = {}) {
  return postPkiAction('/pki/authorities/emergency-rotate', actionBody(options))
}

export function activatePkiMigration(options = {}) {
  return postPkiAction('/pki/activation', actionBody(options))
}

export function exportProtectedPki(passphrase) {
  return postPkiAction('/pki/backups/export', actionBody({ passphrase }))
}

export async function importProtectedPki({ archive, passphrase, reason = '', confirmationNonce = '', force = false } = {}) {
  if (!archive) throw new Error('protected PKI backup archive is required')
  const body = new FormData()
  body.append('archive', archive)
  body.append('passphrase', String(passphrase || ''))
  body.append('reason', text(reason))
  body.append('confirmation_nonce', text(confirmationNonce))
  body.append('force', String(force === true))
  const { data } = await api.post('/pki/backups/import', body, longRunningRequest)
  return normalizePkiOperation(data)
}

export async function fetchPkiOperationStatus(operationOrURL) {
  const candidate = typeof operationOrURL === 'object'
    ? operationOrURL?.status_url || operationOrURL?.id || operationOrURL?.operation_id
    : operationOrURL
  const safeURL = safePkiOperationStatusURL(candidate)
  let path = ''
  if (safeURL) {
    path = safeURL.replace(/^\/(?:panel-api|api)/, '')
  } else {
    const id = text(candidate)
    if (!id || /[/?#]/.test(id)) throw new Error('internal PKI operation reference is invalid')
    path = `/pki/operations/${encodeURIComponent(id)}`
  }
  const { data } = await api.get(path)
  return normalizePkiOperation(data)
}

export function protectedArchiveBlob(operation) {
  const archive = operation?.result?.archive
  if (!archive) return null
  if (archive instanceof Blob) return archive
  if (archive instanceof Uint8Array || Array.isArray(archive)) {
    return new Blob([Uint8Array.from(archive)], { type: 'application/octet-stream' })
  }
  if (typeof archive !== 'string' || typeof atob !== 'function') return null
  const decoded = atob(archive)
  const bytes = Uint8Array.from(decoded, character => character.charCodeAt(0))
  return new Blob([bytes], { type: 'application/octet-stream' })
}

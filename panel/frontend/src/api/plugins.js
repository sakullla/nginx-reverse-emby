import { api, longRunningRequest } from './client'
import { redactPluginData, redactPluginProjection } from './pluginSecurity'

const pluginRoot = '/plugins'

function identity(value, label) {
  const result = String(value || '').trim()
  if (!result) throw new Error(`${label} is required`)
  return encodeURIComponent(result)
}

function pluginPath(pluginID, suffix = '') {
  return `${pluginRoot}/${identity(pluginID, 'plugin id')}${suffix}`
}

function projectPublishedEntry(entry) {
  if (!entry || typeof entry !== 'object' || Array.isArray(entry)) return null
  const ruleID = Number(entry.rule_id)
  const agentID = String(entry.agent_id || '').trim()
  const frontendURL = String(entry.frontend_url || '').trim()
  if (!Number.isInteger(ruleID) || ruleID <= 0 || !agentID || !frontendURL) return null
  const projected = {
    rule_id: ruleID,
    agent_id: agentID,
    frontend_url: frontendURL,
    enabled: entry.enabled === true,
    accessible: entry.accessible === true
  }
  const instanceID = String(entry.instance_id || '').trim()
  if (instanceID) projected.instance_id = instanceID
  return projected
}

function projectPluginPublishedEntries(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return value
  const raw = Array.isArray(value.published_entries) ? value.published_entries : []
  value.published_entries = raw.map(projectPublishedEntry).filter(Boolean)
  return value
}

export async function fetchPlugins() {
  const { data } = await api.get(pluginRoot)
  return Array.isArray(data?.plugins) ? data.plugins.map((item) => redactPluginProjection(item)) : []
}

export async function fetchPluginDetail(pluginID) {
  const { data } = await api.get(pluginPath(pluginID), longRunningRequest)
  return projectPluginPublishedEntries(redactPluginProjection(data))
}

export async function fetchPluginOperations(pluginID) {
  const { data } = await api.get(pluginPath(pluginID, '/operations'))
  return Array.isArray(data?.operations) ? data.operations.map((item) => redactPluginProjection(item)) : []
}

export const PLUGIN_OPERATION_POLL_INTERVAL_MS = 500
export const PLUGIN_OPERATION_WAIT_TIMEOUT_MS = 120000

function waitForNextPluginOperationPoll(delay, signal) {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(new Error('plugin operation wait was cancelled'))
      return
    }
    const onAbort = () => {
      globalThis.clearTimeout(timer)
      reject(new Error('plugin operation wait was cancelled'))
    }
    const timer = globalThis.setTimeout(() => {
      signal?.removeEventListener('abort', onAbort)
      resolve()
    }, delay)
    signal?.addEventListener('abort', onAbort, { once: true })
  })
}

export async function waitForPluginOperation(pluginID, operationID, options = {}) {
  const id = String(operationID || '').trim()
  if (!id) throw new Error('plugin operation id is required')
  const timeout = Number(options.timeoutMs) > 0 ? Number(options.timeoutMs) : PLUGIN_OPERATION_WAIT_TIMEOUT_MS
  const interval = Number(options.pollIntervalMs) > 0 ? Number(options.pollIntervalMs) : PLUGIN_OPERATION_POLL_INTERVAL_MS
  const deadline = Date.now() + timeout
  let lastStatus = 'not found'
  do {
    const operations = await fetchPluginOperations(pluginID)
    const operation = operations.find((item) => String(item?.id || '').trim() === id)
    if (operation) {
      lastStatus = String(operation.status || '').trim().toLowerCase() || 'unknown'
      if (lastStatus === 'succeeded') return operation
      if (operation.completed_at || ['failed', 'cancelled', 'superseded'].includes(lastStatus)) {
        throw new Error(operation.error || `plugin operation ${lastStatus}`)
      }
    }
    if (Date.now() >= deadline) break
    await waitForNextPluginOperationPoll(interval, options.signal)
  } while (Date.now() < deadline)
  throw new Error(`plugin operation did not complete before timeout (last status: ${lastStatus})`)
}

export const PLUGIN_PACKAGE_DETAIL_TIMEOUT_MS = 180000

export async function fetchPluginPackageDetail(selection) {
  const { data } = await api.post(`${pluginRoot}/package-detail`, selection, { timeout: PLUGIN_PACKAGE_DETAIL_TIMEOUT_MS })
  return redactPluginProjection(data?.package || {})
}

export async function installPlugin(selection) {
  const { data } = await api.post(`${pluginRoot}/install`, selection, longRunningRequest)
  return redactPluginProjection(data?.plugin || {})
}

export async function runPluginAction(pluginID, action, payload = {}) {
  const allowed = new Set(['enable', 'disable', 'rollback', 'configure', 'upgrade', 'uninstall', 'publish', 'unpublish'])
  if (!allowed.has(action)) throw new Error('plugin action is invalid')
  const { data } = await api.post(pluginPath(pluginID, `/${action}`), payload, longRunningRequest)
  return projectPluginPublishedEntries(redactPluginProjection(data?.result))
}

export const enablePlugin = (pluginID) => runPluginAction(pluginID, 'enable')
export const disablePlugin = (pluginID) => runPluginAction(pluginID, 'disable')
export const rollbackPlugin = (pluginID, confirmedPermissions = []) => runPluginAction(pluginID, 'rollback', { confirmed_permissions: confirmedPermissions })
export const configurePlugin = (pluginID, payload) => runPluginAction(pluginID, 'configure', payload)
export async function publishPlugin(pluginID, payload) {
  const request = payload && typeof payload === 'object' && !Array.isArray(payload) ? payload : {}
  const targets = Array.isArray(request.targets)
    ? [...new Set(request.targets.map((target) => String(target || '').trim()).filter(Boolean))]
    : []
  if (targets.length !== 1) throw new Error('exactly one target is required')
  if (!String(request.frontend_url || '').trim()) throw new Error('frontend url is required')
  return runPluginAction(pluginID, 'publish', payload)
}
export async function unpublishPlugin(pluginID, payload) {
  const request = payload && typeof payload === 'object' && !Array.isArray(payload) ? payload : {}
  const targets = Array.isArray(request.targets)
    ? [...new Set(request.targets.map((target) => String(target || '').trim()).filter(Boolean))]
    : []
  if (targets.length !== 1) throw new Error('exactly one target is required')
  const ruleID = Number(request.rule_id)
  if (!Number.isInteger(ruleID) || ruleID <= 0) throw new Error('rule id is required')
  return runPluginAction(pluginID, 'unpublish', { targets, rule_id: ruleID })
}
export const upgradePlugin = (pluginID, selection) => runPluginAction(pluginID, 'upgrade', selection)
export const uninstallPlugin = (pluginID) => runPluginAction(pluginID, 'uninstall', { drained: true })

export async function deletePluginInstance(pluginID, instanceID) {
  const { data } = await api.delete(pluginPath(pluginID, `/instances/${identity(instanceID, 'instance id')}`), longRunningRequest)
  return data?.deleted === true
}

export function newPluginActionKey() {
  if (globalThis.crypto?.randomUUID) return `ui:${globalThis.crypto.randomUUID()}`
  const bytes = new Uint8Array(24)
  globalThis.crypto?.getRandomValues?.(bytes)
  return `ui:${Array.from(bytes, (value) => value.toString(16).padStart(2, '0')).join('')}`
}

export async function invokePluginDynamicAction(pluginID, instanceID, actionID, targetID, confirmed, idempotencyKey = newPluginActionKey()) {
  const path = pluginPath(pluginID, `/instances/${identity(instanceID, 'instance id')}/actions/${identity(actionID, 'action id')}`)
  const { data } = await api.post(path, { target_id: String(targetID || '').trim(), confirmed: confirmed === true }, { headers: { 'Idempotency-Key': idempotencyKey } })
  return redactPluginProjection(data)
}

export async function fetchPluginLogs(pluginID, instanceID, { agentID = '', cursor = '', limit = 50, signal } = {}) {
  const path = pluginPath(pluginID, `/instances/${identity(instanceID, 'instance id')}/logs`)
	const { data } = await api.get(path, { params: { agent_id: agentID || undefined, cursor: cursor || undefined, limit }, signal })
  return redactPluginData({ entries: Array.isArray(data?.entries) ? data.entries : [], next_cursor: data?.next_cursor || '' })
}

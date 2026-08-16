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

export async function fetchPlugins() {
  const { data } = await api.get(pluginRoot)
  return Array.isArray(data?.plugins) ? data.plugins.map((item) => redactPluginProjection(item)) : []
}

export async function fetchPluginDetail(pluginID) {
  const { data } = await api.get(pluginPath(pluginID), longRunningRequest)
  return redactPluginProjection(data)
}

export async function fetchPluginOperations(pluginID) {
  const { data } = await api.get(pluginPath(pluginID, '/operations'))
  return Array.isArray(data?.operations) ? data.operations.map((item) => redactPluginProjection(item)) : []
}

export async function fetchPluginPackageDetail(selection) {
  const { data } = await api.post(`${pluginRoot}/package-detail`, selection, longRunningRequest)
  return redactPluginProjection(data?.package || {})
}

export async function installPlugin(selection) {
  const { data } = await api.post(`${pluginRoot}/install`, selection, longRunningRequest)
  return redactPluginProjection(data?.plugin || {})
}

export async function runPluginAction(pluginID, action, payload = {}) {
  const allowed = new Set(['enable', 'disable', 'rollback', 'configure', 'upgrade', 'uninstall'])
  if (!allowed.has(action)) throw new Error('plugin action is invalid')
  const { data } = await api.post(pluginPath(pluginID, `/${action}`), payload, longRunningRequest)
  return redactPluginProjection(data?.result)
}

export const enablePlugin = (pluginID) => runPluginAction(pluginID, 'enable')
export const disablePlugin = (pluginID) => runPluginAction(pluginID, 'disable')
export const rollbackPlugin = (pluginID, confirmedPermissions = []) => runPluginAction(pluginID, 'rollback', { confirmed_permissions: confirmedPermissions })
export const configurePlugin = (pluginID, payload) => runPluginAction(pluginID, 'configure', payload)
export const upgradePlugin = (pluginID, selection) => runPluginAction(pluginID, 'upgrade', selection)
export const uninstallPlugin = (pluginID) => runPluginAction(pluginID, 'uninstall', { drained: true })

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

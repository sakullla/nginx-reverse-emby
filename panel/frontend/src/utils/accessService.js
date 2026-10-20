export const ACCESS_SERVICE_TAG_PREFIX = 'nre-access-service:'

const RETAINED_BEFORE_DELETE = [
  { kind: 'certificate', reason: '共享证书和不是本服务单独拥有的证书会保留' },
  { kind: 'relay_listener', reason: '仍被引用或不是本服务创建的监听器会保留' },
  { kind: 'agent', reason: '节点会保留' },
]

export function newIdempotencyKey() {
  if (globalThis.crypto?.randomUUID) return globalThis.crypto.randomUUID()
  const bytes = new Uint8Array(16)
  if (globalThis.crypto?.getRandomValues) globalThis.crypto.getRandomValues(bytes)
  if (bytes.some((value) => value !== 0)) {
    return Array.from(bytes, (value) => value.toString(16).padStart(2, '0')).join('')
  }
  return `ui-${Date.now().toString(16)}-${Math.random().toString(16).slice(2)}`
}

export function reuseIdempotencyKey(session, operation, body) {
  const serialized = JSON.stringify(body ?? null)
  const current = session?.[operation]
  if (current?.key && current.body === serialized) return session
  return {
    ...(session || {}),
    [operation]: { key: newIdempotencyKey(), body: serialized },
  }
}

export function accessServiceIdFromTags(tags) {
  const list = Array.isArray(tags) ? tags : []
  const tag = list.find((item) => String(item).startsWith(ACCESS_SERVICE_TAG_PREFIX))
  return tag ? String(tag).slice(ACCESS_SERVICE_TAG_PREFIX.length) : ''
}

export function ruleServiceLink(rule) {
  const id = accessServiceIdFromTags(rule?.tags)
  if (!id) return null
  return { id, to: `/services?id=${encodeURIComponent(id)}` }
}

export function advancedRuleHref(rule) {
  const params = new URLSearchParams()
  if (rule?.agent_id) params.set('agentId', String(rule.agent_id))
  if (rule?.id != null && rule.id !== '') params.set('search', `#id=${rule.id}`)
  const query = params.toString()
  return query ? `/rules?${query}` : '/rules'
}

export function isHttpUrl(value) {
  try {
    const url = new URL(String(value || '').trim())
    return (url.protocol === 'http:' || url.protocol === 'https:') && Boolean(url.hostname)
  } catch {
    return false
  }
}

export function safeHttpHref(value) {
  const text = String(value || '').trim()
  return isHttpUrl(text) ? text : ''
}

function knownAgentIds(agents) {
  return new Set((Array.isArray(agents) ? agents : []).map((agent) => String(agent?.id ?? '')))
}

export function validateServiceDraft(draft, agents = []) {
  const errors = {}
  if (!isHttpUrl(draft?.backend_url)) {
    errors.backend_url = '后端 URL 必须是带主机名的 http 或 https 地址'
  }
  if (!isHttpUrl(draft?.entry_url)) {
    errors.entry_url = '入口地址必须是带主机名的 http 或 https 地址'
  }
  const known = knownAgentIds(agents)
  if (!draft?.service_agent_id || !known.has(String(draft.service_agent_id))) {
    errors.service_agent_id = '服务侧节点不存在'
  }
  if (!draft?.entry_agent_id || !known.has(String(draft.entry_agent_id))) {
    errors.entry_agent_id = '入口节点不存在'
  }
  const portText = draft?.relay_listen_port
  if (portText != null && String(portText).trim() !== '') {
    const port = Number(portText)
    if (!Number.isInteger(port) || port < 1 || port > 65535) {
      errors.relay_listen_port = '监听端口无效'
    }
  }
  return errors
}

export function servicePayload(draft) {
  const payload = {
    name: String(draft?.name || '').trim(),
    backend_url: String(draft?.backend_url || '').trim(),
    service_agent_id: String(draft?.service_agent_id || '').trim(),
    entry_agent_id: String(draft?.entry_agent_id || '').trim(),
    entry_url: String(draft?.entry_url || '').trim(),
  }
  const portText = draft?.relay_listen_port
  if (portText != null && String(portText).trim() !== '') {
    payload.relay_listen_port = Number(portText)
  }
  const host = String(draft?.relay_public_host || '').trim()
  if (host) payload.relay_public_host = host
  return payload
}

function forwardingSnapshot(value) {
  return {
    backend_url: String(value?.backend_url || '').trim(),
    entry_url: String(value?.entry_url || '').trim(),
    entry_agent_id: String(value?.entry_agent_id || '').trim(),
    service_agent_id: String(value?.service_agent_id || '').trim(),
    relay_listen_port: value?.relay_listen_port == null || String(value.relay_listen_port).trim() === ''
      ? ''
      : String(Number(value.relay_listen_port)),
    relay_public_host: String(value?.relay_public_host || '').trim(),
  }
}

export function forwardingChanged(before, draft) {
  return JSON.stringify(forwardingSnapshot(before)) !== JSON.stringify(forwardingSnapshot(draft))
}

export function simplifiedForwardingChanged(before, draft) {
  const left = forwardingSnapshot(before)
  const right = forwardingSnapshot(draft)
  return left.backend_url !== right.backend_url
    || left.entry_url !== right.entry_url
    || left.entry_agent_id !== right.entry_agent_id
    || left.service_agent_id !== right.service_agent_id
}

export function isNameOnlyChange(service, draft) {
  if (!service) return false
  const nameChanged = String(draft?.name || '').trim() !== String(service?.name || '').trim()
  return nameChanged && !forwardingChanged(service, draft)
}

export function errorRequestsRelayInput(message) {
  const text = String(message || '')
  return text.includes('relay_listen_port') || text.includes('relay_public_host')
}

export function pathText(path) {
  const summary = String(path?.summary || '').trim()
  const mode = String(path?.mode || '').trim()
  if (summary && mode) return `${summary}（${mode}）`
  return summary || mode || '控制面尚未返回路径'
}

function formatPresentBytes(value) {
  const number = typeof value === 'number' ? value : Number(value)
  if (!Number.isFinite(number) || number < 0) return '无数据'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let size = number
  let unitIndex = 0
  while (size >= 1024 && unitIndex < units.length - 1) {
    size /= 1024
    unitIndex += 1
  }
  if (unitIndex === 0) return `${Math.round(size)} ${units[unitIndex]}`
  return `${size.toFixed(size >= 10 ? 1 : 2)} ${units[unitIndex]}`
}

function hasPresentByte(value, key) {
  return value != null && Object.prototype.hasOwnProperty.call(value, key) && value[key] != null && value[key] !== ''
}

// Missing service bytes stay "无数据". Do not call trafficStats formatters:
// those coerce a missing sample to 0.
export function serviceTrafficText(traffic) {
  if (!traffic || typeof traffic !== 'object') return { text: '无数据', detail: '' }
  const state = String(traffic.state || '')
  if (state === '' || state === 'unavailable') return { text: '无数据', detail: '' }
  const hasAccounted = hasPresentByte(traffic, 'accounted_bytes')
  const hasRx = hasPresentByte(traffic, 'rx_bytes')
  const hasTx = hasPresentByte(traffic, 'tx_bytes')
  if (!hasAccounted && !hasRx && !hasTx) {
    return { text: '无数据', detail: String(traffic.label || '') }
  }
  const parts = []
  if (hasAccounted) parts.push(formatPresentBytes(traffic.accounted_bytes))
  if (!hasAccounted && hasRx) parts.push(`收 ${formatPresentBytes(traffic.rx_bytes)}`)
  if (!hasAccounted && hasTx) parts.push(`发 ${formatPresentBytes(traffic.tx_bytes)}`)
  return { text: parts.join(' / '), detail: String(traffic.label || '') }
}

export function certificateText(certificate) {
  const state = String(certificate?.state || 'none')
  if (state === 'none') return { label: '无证书', detail: '' }
  if (state === 'error') return { label: '失败', detail: String(certificate?.last_error || '') }
  if (state === 'ready') {
    const expiry = String(certificate?.not_after || '').trim()
    return { label: expiry ? `到期 ${expiry}` : '已就绪', detail: '' }
  }
  if (state === 'issuing') return { label: '签发中', detail: '' }
  return { label: '未就绪', detail: '' }
}

export function probeText(probe) {
  const status = String(probe?.status || 'untested')
  const code = Number(probe?.http_status_code)
  const withCode = status === 'http_status' && Number.isInteger(code) && code > 0
  return {
    status,
    text: withCode ? `${status} ${code}` : status,
    agentId: String(probe?.agent_id || ''),
    target: String(probe?.target || ''),
    checkedAt: String(probe?.checked_at || ''),
    purpose: String(probe?.purpose || ''),
    error: String(probe?.error || ''),
  }
}

function uniqueTexts(values) {
  const seen = new Set()
  const result = []
  for (const value of values) {
    const text = String(value || '').trim()
    if (!text || seen.has(text)) continue
    seen.add(text)
    result.push(text)
  }
  return result
}

export function incompleteSteps(service) {
  const steps = []
  const summary = String(service?.summary || '').trim()
  if (summary && summary !== '已就绪') steps.push(summary)
  if (service?.switch_phase) steps.push(service.switch_phase)
  if (service?.path?.pending || service?.path?.mode === 'pending') {
    steps.push(`路径 ${pathText(service.path)}`)
  }
  if (service?.apply?.pending) steps.push('待生效')
  if (service?.apply?.old_entry_may_receive) steps.push('旧入口仍可能接收新请求')
  const certificate = certificateText(service?.certificate)
  if (certificate.label === '失败' || certificate.label === '签发中' || certificate.label === '未就绪') {
    steps.push(`证书${certificate.label}`)
  }
  if (service?.last_error) steps.push(service.last_error)
  return uniqueTexts(steps)
}

export function ruleBackendLabels(rule) {
  const backends = Array.isArray(rule?.backends) ? rule.backends : []
  return backends.map((backend) => {
    const provider = backend?.plugin_provider
    if (backend?.kind === 'plugin_provider' || provider) {
      const name = String(provider?.provider_id || '插件后端')
      const instance = String(provider?.instance_id || '').trim()
      return instance ? `${name} · ${instance}` : name
    }
    return String(backend?.url || '').trim()
  }).filter(Boolean)
}

export function deletionImpact(service) {
  if (service?.deletion) {
    return {
      pending: Boolean(service.deletion.pending),
      stopped: Array.isArray(service.deletion.stopped_entries) ? service.deletion.stopped_entries : [],
      retained: Array.isArray(service.deletion.retained) ? service.deletion.retained : [],
      note: String(service.deletion.note || ''),
    }
  }
  const stopped = []
  const rules = Array.isArray(service?.rules) ? service.rules : []
  for (const rule of rules) {
    stopped.push({
      agent_id: rule.agent_id || service?.entry_agent_id || '',
      rule_id: rule.id,
      url: rule.frontend_url || service?.entry_url || '',
    })
  }
  if (service?.pending_rule_id && !stopped.some((entry) => entry.rule_id === service.pending_rule_id)) {
    stopped.push({
      agent_id: service.pending_rule_agent_id || '',
      rule_id: service.pending_rule_id,
      url: service.entry_url || '',
    })
  }
  if (!stopped.length && service?.entry_url) {
    stopped.push({
      agent_id: service.entry_agent_id || '',
      rule_id: service.rule_id || '',
      url: service.entry_url,
    })
  }
  return { pending: false, stopped, retained: RETAINED_BEFORE_DELETE, note: '' }
}

export function accessErrorMessage(error) {
  return error?.response?.data?.message || error?.message || '请求失败'
}

export function accessErrorService(error) {
  const service = error?.response?.data?.service
  return service && typeof service === 'object' ? service : null
}

export function draftFromService(service) {
  return {
    name: service?.name || '',
    backend_url: service?.backend_url || '',
    service_agent_id: service?.service_agent_id || '',
    entry_agent_id: service?.entry_agent_id || '',
    entry_url: service?.entry_url || '',
    relay_listen_port: service?.relay_listen_port ?? '',
    relay_public_host: service?.relay_public_host || '',
  }
}

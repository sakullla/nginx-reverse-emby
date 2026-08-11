const REDACTED = '[REDACTED]'
const sensitiveKey = /(?:^|[_-])(authorization|cookie|credential|password|private[_-]?key|secret|token|api[_-]?key|value)(?:$|[_-])/i
const allowedSchemaTypes = new Set(['string', 'number', 'integer', 'boolean'])

export function sanitizePluginText(value) {
  return String(value ?? '')
    .replace(/Bearer\s+[^\s,;]+/gi, 'Bearer [REDACTED]')
    .replace(/([a-z][a-z0-9+.-]*:\/\/[^\s/:@]+:)[^\s/@]+@/gi, '$1[REDACTED]@')
    .replace(/((?:authorization|cookie|credential|password|private[_-]?key|secret|token|api[_-]?key)\s*[:=]\s*)[^\s,;]+/gi, '$1[REDACTED]')
    .replace(/-----BEGIN [^-]*PRIVATE KEY-----[\s\S]*?-----END [^-]*PRIVATE KEY-----/gi, REDACTED)
}

export function redactPluginData(value, key = '', depth = 0) {
  if (sensitiveKey.test(String(key))) return REDACTED
  if (depth > 12) return '[TRUNCATED]'
  if (typeof value === 'string') return sanitizePluginText(value)
  if (Array.isArray(value)) return value.slice(0, 500).map((item) => redactPluginData(item, '', depth + 1))
  if (!value || typeof value !== 'object') return value
  return Object.fromEntries(Object.entries(value).map(([name, item]) => [name, redactPluginData(item, name, depth + 1)]))
}

function sanitizePluginStructure(value, depth = 0) {
  if (depth > 12) return '[TRUNCATED]'
  if (typeof value === 'string') return sanitizePluginText(value)
  if (Array.isArray(value)) return value.slice(0, 500).map((item) => sanitizePluginStructure(item, depth + 1))
  if (!value || typeof value !== 'object') return value
  return Object.fromEntries(Object.entries(value).map(([name, item]) => [name, sanitizePluginStructure(item, depth + 1)]))
}

function pruneSecretPointer(config, pointer) {
  if (!config || typeof config !== 'object' || typeof pointer !== 'string' || !pointer.startsWith('/')) return
  const segments = pointer.slice(1).split('/').map((segment) => segment.replaceAll('~1', '/').replaceAll('~0', '~'))
  let parent = config
  for (const segment of segments.slice(0, -1)) {
    if (!parent || typeof parent !== 'object' || !Object.hasOwn(parent, segment)) return
    parent = parent[segment]
  }
  const leaf = segments.at(-1)
  if (parent && typeof parent === 'object' && leaf !== undefined) {
    if (Array.isArray(parent)) {
      if (!/^(0|[1-9]\d*)$/.test(leaf) || Number(leaf) >= parent.length) return
      parent[Number(leaf)] = null
    }
    else delete parent[leaf]
  }
}

function safeSecretPointer(pointer) {
  if (typeof pointer !== 'string' || pointer.length < 1 || pointer.length > 1024 || !pointer.startsWith('/') || /[\u0000-\u001f\u007f]/.test(pointer)) return false
  for (let index = 0; index < pointer.length; index += 1) {
    if (pointer[index] === '~' && !['0', '1'].includes(pointer[index + 1])) return false
  }
  return true
}

function normalizeSecretFieldStates(fields) {
  if (!Array.isArray(fields)) return []
  const normalized = []
  const seen = new Set()
  for (const field of fields.slice(0, 500)) {
    if (!field || typeof field !== 'object' || Array.isArray(field) || !safeSecretPointer(field.pointer) || typeof field.present !== 'boolean' || seen.has(field.pointer)) continue
    seen.add(field.pointer)
    normalized.push({ pointer: field.pointer, present: field.present })
  }
  return normalized
}

function stripWriteOnlySchemaValues(schema) {
  if (!schema || typeof schema !== 'object') return
  if (schema.writeOnly === true) {
    delete schema.default
    delete schema.const
    delete schema.examples
  }
  if (schema.properties && typeof schema.properties === 'object') {
    for (const field of Object.values(schema.properties)) stripWriteOnlySchemaValues(field)
  }
  if (schema.items && typeof schema.items === 'object') stripWriteOnlySchemaValues(schema.items)
}

// Plugin read DTOs contain security metadata whose names are intentionally
// descriptive (for example `secret_fields` and a schema property named
// `token`). Keep that structure intact, sanitize text values, and prune only
// broker-owned config values identified by the server's JSON pointers.
export function redactPluginProjection(value) {
  const result = sanitizePluginStructure(value)
  const schema = result?.package?.config_schema || result?.config_schema
  stripWriteOnlySchemaValues(schema)
  const instances = Array.isArray(result?.instances) ? result.instances : []
  const sourceInstances = Array.isArray(value?.instances) ? value.instances : []
  for (const [index, instance] of instances.entries()) {
    const source = sourceInstances[index] || {}
    instance.secret_fields = normalizeSecretFieldStates(source.secret_fields)
    instance.pending_secret_fields = normalizeSecretFieldStates(source.pending_secret_fields)
    for (const field of instance.secret_fields) pruneSecretPointer(instance.config, field.pointer)
    for (const field of instance.pending_secret_fields) pruneSecretPointer(instance.pending_config, field.pointer)
  }
  return result
}

export function safePluginJSON(value) {
  return JSON.stringify(redactPluginData(value), null, 2)
}

function safeDefault(field) {
  if (!Object.hasOwn(field, 'default')) return undefined
  const value = field.default
  if (field.type === 'string' && typeof value === 'string') return value
  if (field.type === 'boolean' && typeof value === 'boolean') return value
  if (field.type === 'number' && typeof value === 'number' && Number.isFinite(value)) return value
  if (field.type === 'integer' && Number.isInteger(value)) return value
  return undefined
}

export function normalizePluginConfigSchema(schema) {
  if (!schema || typeof schema !== 'object' || Array.isArray(schema) || schema.type !== 'object') return []
  const required = new Set(Array.isArray(schema.required) ? schema.required.filter((name) => typeof name === 'string') : [])
  const properties = schema.properties && typeof schema.properties === 'object' && !Array.isArray(schema.properties)
    ? schema.properties
    : {}
  return Object.entries(properties).flatMap(([name, field]) => {
    if (!field || typeof field !== 'object' || Array.isArray(field)) return []
    if (!allowedSchemaTypes.has(field.type) || field.$ref || field.contentMediaType === 'text/html') return []
    const secret = field.writeOnly === true
    const normalized = {
      name,
      type: field.type,
      title: sanitizePluginText(field.title || name),
      description: sanitizePluginText(field.description || ''),
      required: required.has(name),
      secret,
      enum: Array.isArray(field.enum) ? field.enum.filter((item) => ['string', 'number', 'boolean'].includes(typeof item)).slice(0, 100) : [],
      minimum: Number.isFinite(field.minimum) ? field.minimum : undefined,
      maximum: Number.isFinite(field.maximum) ? field.maximum : undefined
    }
    const defaultValue = secret ? undefined : safeDefault(field)
    if (defaultValue !== undefined) normalized.default = defaultValue
    return [normalized]
  })
}

export function safePluginExport(detail, operations) {
  const structuredDetail = detail && typeof detail === 'object' && (
    Object.hasOwn(detail, 'plugin') || Object.hasOwn(detail, 'package') || Array.isArray(detail.instances)
  )
  return {
    exported_at: new Date().toISOString(),
    plugin: structuredDetail ? redactPluginProjection(detail) : redactPluginData(detail),
    operations: redactPluginData(operations)
  }
}

export function pluginRiskNotices(packageDetail = {}, source = {}) {
  const permissions = Array.isArray(packageDetail.permissions) ? packageDetail.permissions.map(String) : []
  const manifest = packageDetail.manifest || {}
  const notices = []
  if (source.kind && source.kind !== 'official') notices.push('非官方来源：安装或升级前必须复核签名身份、权限差异与来源风险。')
  if (permissions.some((item) => /docker/i.test(item))) notices.push('Docker 权限属于宿主级高风险能力；插件可能控制容器与宿主资源。')
  if (permissions.some((item) => /unsandboxed/i.test(item)) || manifest.runtime?.sandbox === false) notices.push('该插件请求非沙箱运行授权，故障或越权可能影响宿主。')
  if (/waf/i.test(manifest.id || '') || (manifest.extension_points || []).some((item) => /waf/i.test(item))) notices.push('WAF fail-closed 会在插件不可用时阻断直接依赖流量，请确认可用性预算。')
  if (/rate/i.test(manifest.id || '') || permissions.some((item) => /rate/i.test(item))) notices.push('限速状态按节点本地维护；多 Agent 部署不会自动形成全局共享计数器。')
  notices.push('边界：插件不能注入宿主前端代码；业务插件实现与第三方控制面扩展不在本面板加载范围内。')
  return notices
}

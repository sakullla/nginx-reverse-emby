const REDACTED = '[REDACTED]'
const sensitiveKey = /(?:^|[_-])(authorization|cookie|credential|password|private[_-]?key|secret|token|api[_-]?key|value)(?:$|[_-])/i

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

function escapePointerToken(token) {
  return String(token).replaceAll('~', '~0').replaceAll('/', '~1')
}

function schemaRequiredNames(schema) {
  return new Set(Array.isArray(schema.required) ? schema.required.filter((name) => typeof name === 'string') : [])
}

function schemaProperties(schema) {
  return schema && typeof schema === 'object' && !Array.isArray(schema)
    && schema.properties && typeof schema.properties === 'object' && !Array.isArray(schema.properties)
    ? schema.properties
    : {}
}

function schemaEnumOptions(field) {
  if (!Array.isArray(field.enum)) return []
  return field.enum
    .filter((item) => typeof item === 'string' || typeof item === 'number')
    .slice(0, 100)
    .map((item) => ({ value: item, label: String(item) }))
}

function synthesizeComponent(name, field, pointerPrefix, required) {
  if (!field || typeof field !== 'object' || Array.isArray(field)) return []
  if (field.$ref || field.contentMediaType === 'text/html') return []
  const pointer = `${pointerPrefix}/${escapePointerToken(name)}`
  const identity = {
    id: name,
    label: sanitizePluginText(field.title || name),
    description: sanitizePluginText(field.description || '')
  }
  const annotation = {}
  if (field.readOnly === true) annotation.read_only = true
  // A JSON Schema node may omit `type` yet still carry object/array keywords.
  const type = field.type || (field.properties ? 'object' : field.items ? 'array' : undefined)
  switch (type) {
    case 'object':
      return [{ type: 'section', ...identity, ...annotation, children: synthesizeObjectProperties(field, pointer) }]
    case 'array': {
      const items = field.items
      if (items && typeof items === 'object' && !Array.isArray(items) && (items.type === 'object' || items.properties)) {
        return [{ type: 'array', ...identity, ...annotation, binding: pointer, required, children: synthesizeObjectProperties(items, '') }]
      }
      return [{ type: 'array', ...identity, ...annotation, binding: pointer, required }]
    }
    case 'boolean': {
      const component = { type: 'toggle', ...identity, ...annotation, binding: pointer, required }
      component.default = field.default === undefined ? false : field.default
      return [component]
    }
    case 'number':
    case 'integer': {
      if (Array.isArray(field.enum) && field.enum.length) {
        const component = { type: 'select', ...identity, ...annotation, binding: pointer, required, options: schemaEnumOptions(field) }
        if (field.default !== undefined) component.default = field.default
        return [component]
      }
      const component = { type: 'number', ...identity, ...annotation, binding: pointer, required }
      if (Number.isFinite(field.minimum)) component.minimum = field.minimum
      if (Number.isFinite(field.maximum)) component.maximum = field.maximum
      if (Number.isFinite(field.multipleOf)) component.step = field.multipleOf
      if (field.default !== undefined) component.default = field.default
      return [component]
    }
    case 'string': {
      if (field.writeOnly === true) return [{ type: 'secret', ...identity, ...annotation, binding: pointer, required }]
      if (Array.isArray(field.enum) && field.enum.length) {
        const component = { type: 'select', ...identity, ...annotation, binding: pointer, required, options: schemaEnumOptions(field) }
        if (field.default !== undefined) component.default = field.default
        return [component]
      }
      const component = { type: 'text', ...identity, ...annotation, binding: pointer, required }
      if (Number.isFinite(field.minLength)) component.min_length = field.minLength
      if (Number.isFinite(field.maxLength)) component.max_length = field.maxLength
      if (typeof field.pattern === 'string') component.pattern = field.pattern
      if (field.default !== undefined) component.default = field.default
      return [component]
    }
    default:
      // `null` and absent `type` are both accepted by the backend vocabulary.
      // Render them read-only so the field stays visible and its broker-owned
      // value round-trips without being editable or silently dropped.
      return [{ type: 'text', ...identity, ...annotation, read_only: true, binding: pointer, required }]
  }
}

function synthesizeObjectProperties(schema, pointerPrefix) {
  if (!schema || typeof schema !== 'object' || Array.isArray(schema)) return []
  const required = schemaRequiredNames(schema)
  const properties = schemaProperties(schema)
  return Object.entries(properties).flatMap(([name, field]) => synthesizeComponent(name, field, pointerPrefix, required.has(name)))
}

// Recursively synthesize a host-renderable declarative component tree from a
// validated config_schema when a plugin ships no ui_schema. The result reuses
// the same fixed component vocabulary as the declarative renderer, so nested
// objects and arrays round-trip through the same edit/submit path.
export function schemaToUIComponents(schema) {
  if (!schema || typeof schema !== 'object' || Array.isArray(schema) || schema.type !== 'object') return []
  return synthesizeObjectProperties(schema, '')
}

const ANNOTATION_STRIPPED = Symbol('annotation-stripped')

function stripAnnotationConfigValue(schema, value, annotation) {
  if (schema && typeof schema === 'object' && !Array.isArray(schema) && schema[annotation] === true) return ANNOTATION_STRIPPED
  if (Array.isArray(value)) {
    const itemsSchema = schema && typeof schema === 'object' && !Array.isArray(schema) ? schema.items : undefined
    return value.map((item) => {
      const cleaned = stripAnnotationConfigValue(itemsSchema, item, annotation)
      return cleaned === ANNOTATION_STRIPPED ? null : cleaned
    })
  }
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    const properties = schema && typeof schema === 'object' && !Array.isArray(schema) ? schema.properties : undefined
    const out = {}
    for (const [key, child] of Object.entries(value)) {
      const childSchema = properties && typeof properties === 'object' && !Array.isArray(properties) ? properties[key] : undefined
      const cleaned = stripAnnotationConfigValue(childSchema, child, annotation)
      if (cleaned !== ANNOTATION_STRIPPED) out[key] = cleaned
    }
    return out
  }
  return value
}

// Remove writeOnly plaintext from a config value before it reaches the
// declarative renderer, so secret plaintext can never be echoed back in the
// submit payload. Secrets travel exclusively via secret_replacements.
export function stripWriteOnlyConfigValues(schema, config) {
  return stripAnnotationConfigValue(schema, config, 'writeOnly')
}

// Remove readOnly (broker-owned, display-only) values from a config value
// before it is persisted through a configure operation, so the server's
// readOnly rejection never fires for values the client merely echoed back.
export function stripReadOnlyConfigValues(schema, config) {
  return stripAnnotationConfigValue(schema, config, 'readOnly')
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

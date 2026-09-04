import { describe, expect, it, vi } from 'vitest'
import {
  pluginRiskNotices,
  redactPluginData,
  redactPluginProjection,
  safePluginExport,
  sanitizePluginText,
  schemaToUIComponents,
  enrichDeclarativeUIDocument,
  collectDeclarativeConstraintErrors,
  stripHostInjectedConfigValues,
  stripReadOnlyConfigValues,
  stripWriteOnlyConfigValues
} from './pluginSecurity'

describe('plugin UI security boundary', () => {
  it('redacts credentials from nested logs, errors and exports', () => {
    const payload = {
      token: 'raw-token',
      nested: { message: 'authorization=Bearer abc123', endpoint: 'https://user:pass@example.test/path' },
      list: [{ password: 'raw-password' }]
    }
    const redacted = redactPluginData(payload)
    expect(JSON.stringify(redacted)).not.toContain('raw-token')
    expect(JSON.stringify(redacted)).not.toContain('abc123')
    expect(JSON.stringify(redacted)).not.toContain('raw-password')
    expect(sanitizePluginText('cookie=session-secret')).toBe('cookie=[REDACTED]')
    expect(sanitizePluginText('failure {"password":"two word secret","token":"abc123"}')).not.toMatch(/two word secret|abc123/)
    vi.useFakeTimers().setSystemTime(new Date('2026-08-11T00:00:00Z'))
    expect(JSON.stringify(safePluginExport(payload, []))).not.toContain('raw-token')
    vi.useRealTimers()
  })

  it('synthesizes scalar fields, constraints and secret markers into components', () => {
    const components = schemaToUIComponents({
      type: 'object',
      required: ['mode', 'api_token'],
      properties: {
        mode: { type: 'string', title: '模式', enum: ['observe', 'block'] },
        api_token: { type: 'string', writeOnly: true, default: 'must-not-render' },
        injected: { type: 'string', contentMediaType: 'text/html', default: '<script>bad()</script>' },
        remote: { $ref: 'https://plugins.example/schema.json' },
        threshold: { type: 'number', minimum: 1, maximum: 100, multipleOf: 1 },
        note: { type: 'string', minLength: 2, maxLength: 20, pattern: '^[a-z]+$' }
      }
    })
    expect(components.map((component) => component.id)).toEqual(['mode', 'api_token', 'threshold', 'note'])
    expect(components[0]).toMatchObject({ type: 'radio', binding: '/mode', required: true, options: [{ value: 'observe', label: 'observe' }, { value: 'block', label: 'block' }] })
    expect(components[1]).toMatchObject({ type: 'secret', binding: '/api_token', required: true })
    expect(components[2]).toMatchObject({ type: 'number', binding: '/threshold', minimum: 1, maximum: 100, step: 1 })
    expect(components[3]).toMatchObject({ type: 'text', binding: '/note', min_length: 2, max_length: 20, pattern: '^[a-z]+$' })
  })

  it('recursively synthesizes nested objects and arrays', () => {
    const components = schemaToUIComponents({
      type: 'object',
      properties: {
        credentials: { type: 'object', properties: { token: { type: 'string', writeOnly: true } } },
        upstreams: { type: 'array', items: { type: 'object', properties: { host: { type: 'string' }, port: { type: 'number' } } } },
        tags: { type: 'array', items: { type: 'string' } }
      }
    })
    expect(components).toEqual([
      {
        type: 'section', id: 'credentials', label: 'credentials', description: '',
        children: [{ type: 'secret', id: 'token', label: 'token', description: '', binding: '/credentials/token', required: false }]
      },
      {
        type: 'array', id: 'upstreams', label: 'upstreams', description: '', binding: '/upstreams', required: false, min_items: 0,
        children: [
          { type: 'text', id: 'host', label: 'host', description: '', binding: '/host', required: false },
          { type: 'number', id: 'port', label: 'port', description: '', binding: '/port', required: false }
        ]
      },
      { type: 'array', id: 'tags', label: 'tags', description: '', binding: '/tags', required: false, min_items: 0, item_type: 'string' }
    ])
  })

  it('keeps scalar array item types and constraints from the config schema', () => {
    const components = schemaToUIComponents({
      type: 'object',
      properties: {
        ports: { type: 'array', minItems: 1, uniqueItems: true, items: { type: 'integer', minimum: 1, maximum: 65535, multipleOf: 1 } },
        flags: { type: 'array', items: { type: 'boolean' } },
        names: { type: 'array', items: { type: 'string', minLength: 2, maxLength: 8, pattern: '^[a-z]+$' } }
      }
    })
    expect(components[0]).toMatchObject({ type: 'array', item_type: 'integer', item_minimum: 1, item_maximum: 65535, item_step: 1, min_items: 1, unique_items: true })
    expect(components[1]).toMatchObject({ type: 'array', item_type: 'boolean' })
    expect(components[2]).toMatchObject({ type: 'array', item_type: 'string', item_min_length: 2, item_max_length: 8, item_pattern: '^[a-z]+$' })
  })

  it('rejoins declarative array and field projections with config schema constraints', () => {
    const document = enrichDeclarativeUIDocument({
      schema_version: 1,
      components: [
        { type: 'text', id: 'name', binding: '/name' },
        { type: 'array', id: 'ports', binding: '/ports' },
        { type: 'array', id: 'routes', binding: '/routes', children: [{ type: 'number', id: 'weight', binding: '/weight' }] }
      ]
    }, {
      type: 'object',
      properties: {
        name: { type: 'string', minLength: 2, pattern: '^[a-z]+$' },
        ports: { type: 'array', uniqueItems: true, items: { type: 'integer', minimum: 1, maximum: 65535 } },
        routes: { type: 'array', maxItems: 4, items: { type: 'object', properties: { weight: { type: 'integer', multipleOf: 2 } } } }
      }
    })
    expect(document.components[0]).toMatchObject({ min_length: 2, pattern: '^[a-z]+$' })
    expect(document.components[1]).toMatchObject({ min_items: 0, unique_items: true, item_type: 'integer', item_minimum: 1, item_maximum: 65535 })
    expect(document.components[2]).toMatchObject({ min_items: 0, max_items: 4 })
    expect(document.components[2].children[0]).toMatchObject({ integer: true, step: 2 })
  })

  it('keeps hostInjected arrays off the fill-in UI even when they have a schema default', () => {
    const components = schemaToUIComponents({
      type: 'object',
      required: ['records'],
      properties: {
        records: {
          type: 'array',
          hostInjected: true,
          default: [],
          items: { type: 'object', properties: { body: { type: 'string', title: '正文' } } }
        },
        note: { type: 'string', title: '备注' }
      }
    })
    expect(components.map((component) => component.id)).toEqual(['note'])
    expect(JSON.stringify(components)).not.toContain('正文')
  })

  it('keeps a required schema array empty unless minItems requires entries', () => {
    const [apps] = schemaToUIComponents({
      type: 'object',
      required: ['apps'],
      properties: {
        apps: { type: 'array', maxItems: 128, items: { type: 'object', properties: { image: { type: 'string' } } } }
      }
    })
    expect(apps).toMatchObject({ required: true, min_items: 0, max_items: 128, default: [] })
    expect(collectDeclarativeConstraintErrors([apps], { apps: [] })).toEqual([])

    const [nonEmptyApps] = schemaToUIComponents({
      type: 'object',
      required: ['apps'],
      properties: {
        apps: { type: 'array', minItems: 1, items: { type: 'object', properties: { image: { type: 'string' } } } }
      }
    })
    expect(collectDeclarativeConstraintErrors([nonEmptyApps], { apps: [] })).toEqual([
      { pointer: '/apps', message: '至少 1 项' }
    ])
  })

  it('synthesizes extended vocabulary: long enums stay select, enum arrays become multiselect, map objects become keyvalue', () => {
    const components = schemaToUIComponents({
      type: 'object',
      properties: {
        short_enum: { type: 'string', enum: ['a', 'b', 'c'] },
        long_enum: { type: 'string', enum: ['a', 'b', 'c', 'd', 'e'] },
        numeric_enum: { type: 'string', enum: [1, 2] },
        flags: { type: 'array', items: { type: 'string', enum: ['fast', 'safe'] }, default: ['fast'] },
        labels: { type: 'object', additionalProperties: true },
        closed_map: { type: 'object', additionalProperties: false },
        fixed: { type: 'object', properties: { name: { type: 'string' } } }
      }
    })
    expect(components[0]).toMatchObject({ type: 'radio', binding: '/short_enum' })
    expect(components[1]).toMatchObject({ type: 'select', binding: '/long_enum' })
    expect(components[2]).toMatchObject({ type: 'select', binding: '/numeric_enum' })
    expect(components[3]).toMatchObject({ type: 'multiselect', binding: '/flags', default: ['fast'], options: [{ value: 'fast', label: 'fast' }, { value: 'safe', label: 'safe' }] })
    expect(components[4]).toMatchObject({ type: 'keyvalue', binding: '/labels' })
    expect(components[5]).toMatchObject({ type: 'section', id: 'closed_map', children: [] })
    expect(components[6]).toMatchObject({ type: 'section', id: 'fixed' })
  })

  it('omits hostInjected properties, keeps unmarked strings as text, and uses textarea for long strings', () => {
    const components = schemaToUIComponents({
      type: 'object',
      required: ['generation', 'mode', 'upstreams'],
      properties: {
        generation: { type: 'string', hostInjected: true, title: 'Generation' },
        secret_ref: { type: 'string', hostInjected: true },
        resource_group_ref: { type: 'string', hostInjected: true },
        mode: { type: 'string', title: '模式' },
        note: { type: 'string', maxLength: 512 },
        leftover: { type: 'string', hostInjected: false },
        mislabelled: { type: 'string', hostInjected: 'true' },
        upstreams: { type: 'string', title: '上游', maxLength: 16384, description: '按行填写，留空使用默认上游。' }
      }
    })
    expect(components.map((component) => component.id)).toEqual(['mode', 'note', 'leftover', 'mislabelled', 'upstreams'])
    expect(components[0]).toMatchObject({ type: 'text', binding: '/mode', required: true })
    expect(components[1]).toMatchObject({ type: 'text', binding: '/note', max_length: 512 })
    expect(components[2]).toMatchObject({ type: 'text', binding: '/leftover' })
    expect(components[3]).toMatchObject({ type: 'text', binding: '/mislabelled' })
    expect(components[4]).toMatchObject({
      type: 'textarea',
      binding: '/upstreams',
      required: true,
      max_length: 16384,
      label: '上游',
      description: '按行填写，留空使用默认上游。'
    })
  })

  it('synthesizes rule_ref as a select that can bind visible host HTTP rules', () => {
    const components = schemaToUIComponents({
      type: 'object',
      required: ['rule_ref'],
      properties: {
        image: { type: 'string', title: '镜像', maxLength: 512 },
        rule_ref: { type: 'string', title: '规则', minLength: 1, maxLength: 128 },
        rule_reference: { type: 'string', title: '其它引用' }
      }
    })
    expect(components.map((component) => component.id)).toEqual(['image', 'rule_ref', 'rule_reference'])
    expect(components[0]).toMatchObject({ type: 'text', binding: '/image', max_length: 512 })
    expect(components[2]).toMatchObject({ type: 'text', binding: '/rule_reference' })
    const ruleRef = components[1]
    expect(ruleRef).toMatchObject({ type: 'select', id: 'rule_ref', label: '规则', binding: '/rule_ref', required: true, options_source: 'http_rule' })
    expect(Array.isArray(ruleRef.options)).toBe(true)
    const visibleHttpRules = [
      { value: 'https://media.example.com', label: 'media' },
      { value: 'https://tv.example.com', label: 'tv' }
    ]
    ruleRef.options = visibleHttpRules
    expect(ruleRef).toMatchObject({ type: 'select', binding: '/rule_ref', options: visibleHttpRules })
  })

  it('omits nested hostInjected properties and synthesizes nested rule_ref as a host HTTP rule select', () => {
    const components = schemaToUIComponents({
      type: 'object',
      properties: {
        generation: { type: 'string' },
        apps: {
          type: 'array',
          items: {
            type: 'object',
            required: ['id', 'image', 'rule_ref', 'generation'],
            properties: {
              id: { type: 'string', hostInjected: true },
              image: { type: 'string', title: '镜像', maxLength: 512 },
              rule_ref: { type: 'string', title: '规则', minLength: 1, maxLength: 128 },
              generation: { type: 'string', hostInjected: true },
              secret_refs: { type: 'array', hostInjected: true, items: { type: 'string' } },
              auto_update: { type: 'boolean', title: '自动更新' }
            }
          }
        }
      }
    })
    expect(components.map((component) => component.id)).toEqual(['generation', 'apps'])
    expect(components[0]).toMatchObject({ type: 'text', binding: '/generation' })
    expect(components[1].type).toBe('array')
    expect(components[1].children.map((child) => child.id)).toEqual(['image', 'rule_ref', 'auto_update'])
    expect(components[1].children[0]).toMatchObject({ type: 'text', binding: '/image', max_length: 512, required: true })
    expect(components[1].children[1]).toMatchObject({ type: 'select', binding: '/rule_ref', required: true, label: '规则', options_source: 'http_rule' })
    expect(Array.isArray(components[1].children[1].options)).toBe(true)
    expect(components[1].children[2]).toMatchObject({ type: 'toggle', binding: '/auto_update' })
  })

  it('collects constraint errors through grid containers and new component types', () => {
    const components = [
      { type: 'grid', id: 'pair', columns: 2, children: [
        { type: 'radio', id: 'mode', label: 'Mode', binding: '/mode', required: true, options: [{ value: 'a', label: 'A' }] },
        { type: 'multiselect', id: 'flags', label: 'Flags', binding: '/flags', required: true, options: [{ value: 'x', label: 'X' }] }
      ] },
      { type: 'keyvalue', id: 'labels', label: 'Labels', binding: '/labels', required: true }
    ]
    const errors = collectDeclarativeConstraintErrors(components, {})
    expect(errors).toEqual([
      { pointer: '/mode', message: '此项为必填' },
      { pointer: '/flags', message: '此项为必填' },
      { pointer: '/labels', message: '此项为必填' }
    ])
    expect(collectDeclarativeConstraintErrors(components, { mode: 'a', flags: ['x'], labels: { k: 'v' } })).toEqual([])
  })

  it('strips hostInjected values from form models and submit payloads', () => {
    const schema = {
      type: 'object',
      required: ['generation', 'mode', 'upstreams'],
      properties: {
        generation: { type: 'string', hostInjected: true },
        secret_ref: { type: 'string', hostInjected: true },
        resource_group_ref: { type: 'string', hostInjected: true },
        mode: { type: 'string' },
        leftover: { type: 'string', hostInjected: false },
        mislabelled: { type: 'string', hostInjected: 'true' },
        token: { type: 'string', writeOnly: true },
        status: { type: 'string', readOnly: true },
        apps: {
          type: 'array',
          items: {
            type: 'object',
            required: ['id', 'image', 'generation'],
            properties: {
              id: { type: 'string', hostInjected: true },
              image: { type: 'string' },
              generation: { type: 'string', hostInjected: true },
              secret_refs: { type: 'array', hostInjected: true, items: { type: 'string' } }
            }
          }
        }
      }
    }
    const config = {
      generation: 'rpc-id-1',
      secret_ref: 'vault:secret',
      resource_group_ref: 'rg-1',
      mode: 'observe',
      leftover: 'keep-false',
      mislabelled: 'keep-string-true',
      token: 'plaintext-token',
      status: 'broker-owned',
      apps: [{ id: 'app-1', image: 'nginx', generation: 'rpc-id-1', secret_refs: ['vault:a'] }]
    }
    const withoutHost = {
      mode: 'observe',
      leftover: 'keep-false',
      mislabelled: 'keep-string-true',
      token: 'plaintext-token',
      status: 'broker-owned',
      apps: [{ image: 'nginx' }]
    }
    expect(stripHostInjectedConfigValues(schema, config)).toEqual(withoutHost)
    expect(stripWriteOnlyConfigValues(schema, config)).toEqual({
      mode: 'observe',
      leftover: 'keep-false',
      mislabelled: 'keep-string-true',
      status: 'broker-owned',
      apps: [{ image: 'nginx' }]
    })
    expect(stripReadOnlyConfigValues(schema, config)).toEqual({
      mode: 'observe',
      leftover: 'keep-false',
      mislabelled: 'keep-string-true',
      token: 'plaintext-token',
      apps: [{ image: 'nginx' }]
    })
  })

  it('strips writeOnly plaintext from config while keeping public values', () => {
    const schema = {
      type: 'object',
      properties: {
        mode: { type: 'string' },
        credentials: { type: 'object', properties: { token: { type: 'string', writeOnly: true }, region: { type: 'string' } } },
        endpoints: { type: 'array', items: { type: 'object', properties: { host: { type: 'string' }, secret: { type: 'string', writeOnly: true } } } }
      }
    }
    const config = {
      mode: 'observe',
      credentials: { token: 'plaintext-token', region: 'us' },
      endpoints: [{ host: 'a', secret: 'plaintext-a' }, { host: 'b', secret: 'plaintext-b' }]
    }
    expect(stripWriteOnlyConfigValues(schema, config)).toEqual({
      mode: 'observe',
      credentials: { region: 'us' },
      endpoints: [{ host: 'a' }, { host: 'b' }]
    })
  })

  it('strips readOnly values from config while keeping client-owned values', () => {
    const schema = {
      type: 'object',
      properties: {
        mode: { type: 'string' },
        status: { type: 'string', readOnly: true },
        metadata: { type: 'object', properties: { status: { type: 'string', readOnly: true }, note: { type: 'string' } } },
        endpoints: { type: 'array', items: { type: 'object', properties: { host: { type: 'string' }, state: { type: 'string', readOnly: true } } } }
      }
    }
    const config = {
      mode: 'observe',
      status: 'broker-owned',
      metadata: { status: 'broker', note: 'mine' },
      endpoints: [{ host: 'a', state: 'up' }, { host: 'b', state: 'down' }]
    }
    expect(stripReadOnlyConfigValues(schema, config)).toEqual({
      mode: 'observe',
      metadata: { note: 'mine' },
      endpoints: [{ host: 'a' }, { host: 'b' }]
    })
  })

  it('synthesizes numeric enums as a select and keeps defaults', () => {
    const components = schemaToUIComponents({
      type: 'object',
      properties: {
        level: { type: 'integer', title: '级别', enum: [1, 2, 3], default: 2 },
        priority: { type: 'number', enum: [0.5, 1, 1.5] }
      }
    })
    expect(components[0]).toMatchObject({ type: 'select', binding: '/level', default: 2, options: [{ value: 1, label: '1' }, { value: 2, label: '2' }, { value: 3, label: '3' }] })
    expect(components[1]).toMatchObject({ type: 'select', binding: '/priority', options: [{ value: 0.5, label: '0.5' }, { value: 1, label: '1' }, { value: 1.5, label: '1.5' }] })
  })

  it('renders null and unknown-type fields as read-only text instead of dropping them', () => {
    const components = schemaToUIComponents({
      type: 'object',
      properties: {
        note: { type: 'null', title: 'Null note' },
        untyped: { title: 'No type' }
      }
    })
    expect(components.map((component) => component.id)).toEqual(['note', 'untyped'])
    expect(components[0]).toMatchObject({ type: 'text', binding: '/note', read_only: true })
    expect(components[1]).toMatchObject({ type: 'text', binding: '/untyped', read_only: true })
  })

  it('seeds boolean defaults to false and preserves explicit defaults', () => {
    const components = schemaToUIComponents({
      type: 'object',
      properties: {
        enabled: { type: 'boolean' },
        debug: { type: 'boolean', default: true }
      }
    })
    expect(components[0]).toMatchObject({ type: 'toggle', binding: '/enabled', default: false })
    expect(components[1]).toMatchObject({ type: 'toggle', binding: '/debug', default: true })
  })

  it('preserves structured DTO metadata while pruning only broker-owned values', () => {
    const projected = redactPluginProjection({
      package: { config_schema: { type: 'object', properties: {
        token: { type: 'string', title: '普通 token 字段' },
        credential: { type: 'string', writeOnly: true, default: 'package-secret' }
      } } },
      instances: [{
        config: { token: 'routing-token', credential: 'must-not-survive', absent: 'also-must-not-survive' },
        secret_fields: [
          { pointer: '/credential', present: true, value: 'must-be-dropped' },
          { pointer: '/absent', present: false },
          { pointer: 'unsafe', present: true },
          { pointer: '/wrong-type', present: 'true' }
        ]
      }]
    })
    expect(projected.package.config_schema.properties.token.title).toBe('普通 token 字段')
    expect(projected.package.config_schema.properties.credential).not.toHaveProperty('default')
    expect(projected.instances[0].secret_fields).toEqual([
      { pointer: '/credential', present: true },
      { pointer: '/absent', present: false }
    ])
    expect(projected.instances[0].config).toEqual({ token: 'routing-token' })
    const exported = safePluginExport(projected, [])
    expect(exported.plugin.package.config_schema.properties.token.title).toBe('普通 token 字段')
    expect(exported.plugin.instances[0].secret_fields).toEqual([
      { pointer: '/credential', present: true },
      { pointer: '/absent', present: false }
    ])
  })

  it('collects visible required, range, length and pattern errors', () => {
    const components = schemaToUIComponents({
      type: 'object',
      required: ['name', 'port', 'note', 'upstreams'],
      properties: {
        name: { type: 'string', title: '名称', minLength: 2 },
        port: { type: 'number', minimum: 1, maximum: 65535 },
        note: { type: 'string', pattern: '^[a-z]+$' },
        extra: { type: 'string', minLength: 3 },
        upstreams: { type: 'array', items: { type: 'object', required: ['host'], properties: { host: { type: 'string' } } } }
      }
    })
    const extra = components.find((component) => component.id === 'extra')
    extra.visible_when = { field: '/mode', op: 'eq', value: 'advanced' }
    expect(collectDeclarativeConstraintErrors(components, {
      name: 'a',
      port: 0,
      note: 'NOPE',
      extra: 'x',
      upstreams: [{}]
    }).map((item) => `${item.pointer}:${item.message}`)).toEqual([
      '/name:至少 2 个字符',
      '/port:不能小于 1',
      '/note:格式不匹配',
      '/upstreams/0/host:此项为必填'
    ])
  })

  it('treats a present required secret as valid until it is cleared', () => {
    const components = [{ type: 'secret', id: 'token', label: 'Token', binding: '/token', required: true }]
    expect(collectDeclarativeConstraintErrors(components, {}, { secretFields: [{ pointer: '/token', present: true }] })).toEqual([])
    expect(collectDeclarativeConstraintErrors(components, {}, {
      secretFields: [{ pointer: '/token', present: true }],
      secretReplacements: { '/token': null }
    })).toEqual([{ pointer: '/token', message: '此项为必填' }])
    expect(collectDeclarativeConstraintErrors(components, {}, { secretFields: [] })).toEqual([{ pointer: '/token', message: '此项为必填' }])
  })

  it('states relevant runtime risks and the no-package-frontend boundary', () => {
    const notices = pluginRiskNotices({ permissions: ['docker.manage', 'runtime.unsandboxed'], manifest: { id: 'official.waf', extension_points: ['http.waf'] } }, { kind: 'custom' })
    expect(notices.join(' ')).toMatch(/非官方|Docker|非沙箱|fail-closed|不能注入宿主前端代码/)
  })
})

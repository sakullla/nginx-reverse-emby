import { describe, expect, it, vi } from 'vitest'
import {
  pluginRiskNotices,
  redactPluginData,
  redactPluginProjection,
  safePluginExport,
  sanitizePluginText,
  schemaToUIComponents,
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
    expect(components[0]).toMatchObject({ type: 'select', binding: '/mode', required: true, options: [{ value: 'observe', label: 'observe' }, { value: 'block', label: 'block' }] })
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
        type: 'array', id: 'upstreams', label: 'upstreams', description: '', binding: '/upstreams', required: false,
        children: [
          { type: 'text', id: 'host', label: 'host', description: '', binding: '/host', required: false },
          { type: 'number', id: 'port', label: 'port', description: '', binding: '/port', required: false }
        ]
      },
      { type: 'array', id: 'tags', label: 'tags', description: '', binding: '/tags', required: false }
    ])
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

  it('states relevant runtime risks and the no-package-frontend boundary', () => {
    const notices = pluginRiskNotices({ permissions: ['docker.manage', 'runtime.unsandboxed'], manifest: { id: 'official.waf', extension_points: ['http.waf'] } }, { kind: 'custom' })
    expect(notices.join(' ')).toMatch(/非官方|Docker|非沙箱|fail-closed|不能注入宿主前端代码/)
  })
})

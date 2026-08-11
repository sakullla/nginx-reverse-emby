import { describe, expect, it, vi } from 'vitest'
import {
  normalizePluginConfigSchema,
  pluginRiskNotices,
  redactPluginData,
  redactPluginProjection,
  safePluginExport,
  sanitizePluginText
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

  it('allows only host declarative scalar fields and marks secrets write-only', () => {
    const fields = normalizePluginConfigSchema({
      type: 'object',
      required: ['mode', 'api_token'],
      properties: {
        mode: { type: 'string', enum: ['observe', 'block'], default: 'observe' },
        api_token: { type: 'string', writeOnly: true, default: 'must-not-render' },
        injected: { type: 'string', contentMediaType: 'text/html', default: '<script>bad()</script>' },
        remote: { $ref: 'https://plugins.example/schema.json' }
      },
      ui: '<script>bad()</script>'
    })
    expect(fields.map((field) => field.name)).toEqual(['mode', 'api_token'])
    expect(fields[1]).toMatchObject({ secret: true, required: true })
    expect(fields[1]).not.toHaveProperty('default')
  })

  it('preserves structured DTO metadata while pruning only broker-owned values', () => {
    const projected = redactPluginProjection({
      package: { config_schema: { type: 'object', properties: {
        token: { type: 'string', title: '普通 token 字段' },
        credential: { type: 'string', writeOnly: true, default: 'package-secret' }
      } } },
      instances: [{
        config: { token: 'routing-token', credential: 'must-not-survive' },
        secret_fields: ['/credential']
      }]
    })
    expect(projected.package.config_schema.properties.token.title).toBe('普通 token 字段')
    expect(projected.package.config_schema.properties.credential).not.toHaveProperty('default')
    expect(projected.instances[0].secret_fields).toEqual(['/credential'])
    expect(projected.instances[0].config).toEqual({ token: 'routing-token' })
    const exported = safePluginExport(projected, [])
    expect(exported.plugin.package.config_schema.properties.token.title).toBe('普通 token 字段')
    expect(exported.plugin.instances[0].secret_fields).toEqual(['/credential'])
  })

  it('states relevant runtime risks and the no-package-frontend boundary', () => {
    const notices = pluginRiskNotices({ permissions: ['docker.manage', 'runtime.unsandboxed'], manifest: { id: 'official.waf', extension_points: ['http.waf'] } }, { kind: 'custom' })
    expect(notices.join(' ')).toMatch(/非官方|Docker|非沙箱|fail-closed|不能注入宿主前端代码/)
  })
})

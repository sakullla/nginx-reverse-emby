import { describe, expect, it } from 'vitest'
import { buildDiagnosticConclusion } from './diagnosticConclusion.js'

function buildSummary(overrides = {}) {
  return {
    sent: 3,
    succeeded: 3,
    failed: 0,
    loss_rate: 0,
    avg_latency_ms: 18,
    min_latency_ms: 10,
    max_latency_ms: 22,
    quality: '极佳',
    ...overrides
  }
}

describe('buildDiagnosticConclusion', () => {
  it('summarizes a fully successful diagnosis in one sentence', () => {
    const conclusion = buildDiagnosticConclusion({
      kind: 'http',
      state: 'completed',
      error: '',
      result: { summary: buildSummary() }
    })

    expect(conclusion.tone).toBe('success')
    expect(conclusion.headline).toContain('HTTP 反代链路正常')
    expect(conclusion.headline).toContain('3 次探测全部成功')
    expect(conclusion.detail).toContain('18 ms')
    expect(conclusion.suggestions).toEqual([])
  })

  it('labels L4 success conclusions', () => {
    const conclusion = buildDiagnosticConclusion({
      kind: 'l4_tcp',
      state: 'completed',
      result: { summary: buildSummary({ sent: 2, succeeded: 2, avg_latency_ms: 9 }) }
    })

    expect(conclusion.tone).toBe('success')
    expect(conclusion.headline).toContain('L4 转发链路正常')
  })

  it('attributes a failed diagnosis to failed backends and lists suggestions', () => {
    const conclusion = buildDiagnosticConclusion({
      kind: 'http',
      state: 'completed',
      result: {
        summary: buildSummary({ sent: 3, succeeded: 1, failed: 2, loss_rate: 0.7, quality: '较差' }),
        backends: [
          {
            backend: 'http://origin.example.test/healthz [127.0.0.1:8096]',
            address: '127.0.0.1:8096',
            summary: buildSummary({ sent: 3, succeeded: 1, failed: 2, loss_rate: 0.7, quality: '较差' })
          }
        ],
        samples: [
          {
            attempt: 1,
            success: false,
            backend: 'http://origin.example.test/healthz',
            error: 'dial tcp 127.0.0.1:8096: connect: connection refused'
          }
        ]
      }
    })

    expect(conclusion.tone).toBe('danger')
    expect(conclusion.headline).toContain('后端探测失败')
    expect(conclusion.detail).toContain('http://origin.example.test/healthz')
    expect(conclusion.detail).toContain('connection refused')
    expect(conclusion.suggestions.length).toBeGreaterThan(0)
    expect(conclusion.suggestions.join(' ')).toContain('后端')
  })

  it('attributes failures to a resolved child backend when the parent summary has no samples', () => {
    const conclusion = buildDiagnosticConclusion({
      kind: 'http',
      state: 'completed',
      result: {
        summary: buildSummary({ sent: 2, succeeded: 0, failed: 2, loss_rate: 1, quality: '不可用' }),
        backends: [
          {
            backend: 'https://origin.example.test',
            summary: buildSummary({ sent: 0, succeeded: 0, failed: 0, avg_latency_ms: 0, quality: '不可用' }),
            children: [
              {
                backend: 'https://origin.example.test [192.0.2.245:443]',
                address: '192.0.2.245:443',
                summary: buildSummary({ sent: 2, succeeded: 0, failed: 2, loss_rate: 1, quality: '不可用' })
              }
            ]
          }
        ]
      }
    })

    expect(conclusion.tone).toBe('danger')
    expect(conclusion.headline).toContain('后端探测失败')
    expect(conclusion.detail).toContain('192.0.2.245:443')
  })

  it('attributes a failed diagnosis to Relay paths when a path failed', () => {
    const conclusion = buildDiagnosticConclusion({
      kind: 'l4_tcp',
      state: 'completed',
      result: {
        summary: buildSummary({ sent: 2, succeeded: 0, failed: 2, loss_rate: 1, quality: '不可用' }),
        relay_paths: [
          { path: [1, 2], success: false, error: 'relay dial timeout' },
          { path: [3], success: true, latency_ms: 12 }
        ]
      }
    })

    expect(conclusion.tone).toBe('danger')
    expect(conclusion.headline).toContain('Relay 路径失败')
    expect(conclusion.detail).toContain('中继器 #1 → 中继器 #2')
    expect(conclusion.detail).toContain('relay dial timeout')
    expect(conclusion.suggestions.length).toBeGreaterThan(0)
    expect(conclusion.suggestions.join(' ')).toContain('Relay')
  })

  it('reports 无法定位 when the summary has no samples', () => {
    const conclusion = buildDiagnosticConclusion({
      kind: 'http',
      state: 'completed',
      result: { summary: buildSummary({ sent: 0, succeeded: 0, failed: 0, avg_latency_ms: 0, quality: '不可用' }) }
    })

    expect(conclusion.tone).toBe('warning')
    expect(conclusion.headline).toContain('无法定位')
    expect(conclusion.detail).toContain('数据不足')
    expect(conclusion.suggestions.length).toBeGreaterThan(0)
  })

  it('reports 无法定位 when a failure cannot be attributed to any backend', () => {
    const conclusion = buildDiagnosticConclusion({
      kind: 'http',
      state: 'completed',
      result: { summary: buildSummary({ sent: 2, succeeded: 0, failed: 2, loss_rate: 1, quality: '不可用' }) }
    })

    expect(conclusion.tone).toBe('warning')
    expect(conclusion.headline).toContain('无法定位')
    expect(conclusion.detail).toContain('诊断存在失败样本')
    expect(conclusion.detail).toContain('没有可定位到具体后端或 Relay 路径的数据')
    expect(conclusion.suggestions.length).toBeGreaterThan(0)
  })

  it('keeps 诊断已失败 wording when the diagnostic task itself failed', () => {
    const conclusion = buildDiagnosticConclusion({
      kind: 'http',
      state: 'failed',
      result: { summary: buildSummary({ sent: 2, succeeded: 0, failed: 2, loss_rate: 1, quality: '不可用' }) }
    })

    expect(conclusion.tone).toBe('warning')
    expect(conclusion.headline).toContain('无法定位')
    expect(conclusion.detail).toContain('诊断已失败')
    expect(conclusion.suggestions.length).toBeGreaterThan(0)
  })

  it('keeps the success conclusion when every probe sample succeeded', () => {
    const conclusion = buildDiagnosticConclusion({
      kind: 'http',
      state: 'completed',
      result: {
        summary: buildSummary(),
        relay_paths: [{ path: [1], success: false, error: 'dial timeout' }]
      }
    })

    expect(conclusion.tone).toBe('success')
    expect(conclusion.headline).toContain('HTTP 反代链路正常')
    expect(conclusion.detail).toContain('1 条 Relay 路径探测失败')
    expect(conclusion.suggestions).toEqual([])
  })

  it('preserves the raw task error message', () => {
    const conclusion = buildDiagnosticConclusion({
      kind: 'http',
      state: 'failed',
      error: 'agent offline: task session closed',
      result: null
    })

    expect(conclusion.tone).toBe('danger')
    expect(conclusion.headline).toContain('诊断执行失败')
    expect(conclusion.detail).toBe('agent offline: task session closed')
    expect(conclusion.suggestions.length).toBeGreaterThan(0)
  })
})

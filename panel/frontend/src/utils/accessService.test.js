import { describe, expect, it } from 'vitest'
import { formatBytes } from './trafficStats.js'
import {
  advancedRuleHref,
  certificateText,
  deletionImpact,
  incompleteSteps,
  isNameOnlyChange,
  pathText,
  probeText,
  reuseIdempotencyKey,
  ruleBackendLabels,
  ruleServiceLink,
  serviceTrafficText,
  validateServiceDraft,
} from './accessService.js'

const agents = [
  { id: 'edge', name: '入口节点' },
  { id: 'origin', name: '源站节点' },
]

function draft(overrides = {}) {
  return {
    name: '家庭媒体',
    backend_url: 'http://10.1.0.8:8096',
    service_agent_id: 'origin',
    entry_agent_id: 'edge',
    entry_url: 'https://media.example.com',
    ...overrides,
  }
}

describe('access service presentation', () => {
  it('keeps missing traffic empty instead of formatting it as zero', () => {
    expect(formatBytes(null)).toBe('0 B')
    expect(serviceTrafficText({ state: 'unavailable' })).toEqual({ text: '无数据', detail: '' })
    expect(serviceTrafficText({
      state: 'http_rule',
      label: '该规则在当前周期的用量',
      rx_bytes: null,
      tx_bytes: null,
    })).toEqual({ text: '无数据', detail: '该规则在当前周期的用量' })
    expect(serviceTrafficText({
      state: 'entry_agent',
      label: '入口节点流量',
      accounted_bytes: 2048,
    }).text).toBe('2.00 KiB')
  })

  it('shows certificate expiry, absence, or failure without inventing a date', () => {
    expect(certificateText({ state: 'none' })).toEqual({ label: '无证书', detail: '' })
    expect(certificateText({ state: 'error', last_error: 'dns failed' })).toEqual({ label: '失败', detail: 'dns failed' })
    expect(certificateText({ state: 'ready', not_after: '2030-01-02T00:00:00Z' }).label).toBe('到期 2030-01-02T00:00:00Z')
    expect(certificateText({ state: 'issuing' }).label).toBe('签发中')
  })

  it('renders probe codes and control-plane path text without deciding the route', () => {
    expect(probeText({ status: 'http_status', http_status_code: 401, agent_id: 'edge' }).text).toBe('http_status 401')
    expect(probeText({ status: 'http_status', http_status_code: 403 }).text).toBe('http_status 403')
    expect(probeText({ status: 'http_status', http_status_code: 405 }).text).toBe('http_status 405')
    expect(probeText({ status: 'untested' }).text).toBe('untested')
    expect(pathText(undefined)).toBe('控制面尚未返回路径')
    expect(pathText({ mode: 'pending', summary: '待确认', pending: true })).toBe('待确认（pending）')
  })

  it('does not treat a rule or an online flag as access completion', () => {
    const steps = incompleteSteps({
      summary: '待确认',
      rules: [{ id: 1, frontend_url: 'https://media.example.com' }],
      agent_online: true,
      path: { mode: 'pending', summary: '待确认', pending: true },
      switch_phase: '等待新入口生效',
    })
    expect(steps).toContain('待确认')
    expect(steps).toContain('等待新入口生效')
    expect(steps.join(' ')).not.toContain('已就绪')
    expect(steps.join(' ')).not.toContain('接入完成')
  })

  it('lists entries that would stop and shared resources that stay', () => {
    const impact = deletionImpact({
      entry_url: 'https://media.example.com',
      entry_agent_id: 'edge',
      rules: [{ id: 4, agent_id: 'edge', frontend_url: 'https://media.example.com' }],
      pending_rule_id: 8,
      pending_rule_agent_id: 'origin',
    })
    expect(impact.stopped.map((entry) => entry.url)).toEqual(['https://media.example.com', 'https://media.example.com'])
    expect(impact.stopped.map((entry) => entry.rule_id)).toEqual([4, 8])
    expect(impact.retained.map((item) => item.kind)).toEqual(['certificate', 'relay_listener', 'agent'])
  })

  it('uses the control-plane deletion record once delete has started', () => {
    const impact = deletionImpact({
      deletion: {
        pending: true,
        stopped_entries: [{ agent_id: 'edge', rule_id: 4, url: 'https://old.example' }],
        retained: [{ kind: 'agent', reason: '节点会保留' }],
        note: '删除尚未在节点生效',
      },
    })
    expect(impact.pending).toBe(true)
    expect(impact.stopped[0].url).toBe('https://old.example')
    expect(impact.note).toBe('删除尚未在节点生效')
  })

  it('treats a name edit as name-only and keeps invalid fields from clearing the draft', () => {
    const service = draft()
    expect(isNameOnlyChange(service, draft({ name: '新名称' }))).toBe(true)
    expect(isNameOnlyChange(service, draft({ backend_url: 'http://10.9.0.3:8096' }))).toBe(false)
    const errors = validateServiceDraft(draft({
      backend_url: 'ftp://files.example',
      service_agent_id: 'missing',
    }), agents)
    expect(errors.backend_url).toBeTruthy()
    expect(errors.service_agent_id).toBeTruthy()
    expect(errors.entry_url).toBeUndefined()
    expect(errors.entry_agent_id).toBeUndefined()
  })

  it('links service-created rules back to the service and shows the rule backends', () => {
    expect(ruleServiceLink({ tags: ['web', 'nre-access-service:svc-1'] }).to).toBe('/services?id=svc-1')
    expect(ruleServiceLink({ tags: ['web'] })).toBeNull()
    expect(advancedRuleHref({ id: 9, agent_id: 'edge' })).toContain('/rules?')
    expect(advancedRuleHref({ id: 9, agent_id: 'edge' })).toContain('agentId=edge')
    expect(ruleBackendLabels({
      backends: [
        { url: 'http://real.example/library' },
        { kind: 'plugin_provider', plugin_provider: { instance_id: 'inst', provider_id: 'emby' } },
      ],
    })).toEqual(['http://real.example/library', 'emby · inst'])
  })

  it('reuses an idempotency key only for the same request body', () => {
    const first = reuseIdempotencyKey({}, 'create', { backend_url: 'http://10.1.0.8:8096' })
    const same = reuseIdempotencyKey(first, 'create', { backend_url: 'http://10.1.0.8:8096' })
    const changed = reuseIdempotencyKey(same, 'create', { backend_url: 'http://10.9.0.3:8096' })
    expect(same.create.key).toBe(first.create.key)
    expect(changed.create.key).not.toBe(first.create.key)
  })
})

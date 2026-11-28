import { describe, expect, it } from 'vitest'
import { formatBytes } from './trafficStats.js'
import {
  advancedRuleHref,
  certificateText,
  certificateTone,
  deletionImpact,
  filterAccessServices,
  incompleteSteps,
  errorRequestsRelayInput,
  isNameOnlyChange,
  pathText,
  simplifiedForwardingChanged,
  probeText,
  probeTone,
  resolveServiceSubmitAction,
  retainedResourceKindLabel,
  reuseIdempotencyKey,
  ruleBackendLabels,
  ruleServiceLink,
  serviceStatusBucket,
  serviceSummaryTone,
  serviceTrafficText,
  summarizeServices,
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
    const saved = probeText({
      status: 'http_status',
      http_status_code: 401,
      agent_id: 'edge',
      target: 'http://ready.example/emby',
      checked_at: '2026-10-20T01:02:03Z',
      verification: 'expired',
    })
    expect(saved.text).toBe('http_status 401')
    expect(saved.target).toBe('http://ready.example/emby')
    expect(saved.checkedAt).toBe('2026-10-20T01:02:03Z')
    expect(saved.verification).toBe('expired')
    const connected = probeText({
      status: 'connected',
      target: '203.0.113.10:9443',
      checked_at: '2026-10-20T01:02:04Z',
      verification: 'expired',
    })
    expect(connected.text).toBe('connected')
    expect(connected.verification).toBe('expired')
    expect(`${saved.text} ${connected.text}`).not.toContain('接入完成')
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
    const stale = incompleteSteps({
      summary: '待验证',
      rules: [{ id: 1, frontend_url: 'https://media.example.com' }],
      probes: [{ status: 'connected', verification: 'expired', target: '203.0.113.10:9443' }],
    })
    expect(stale).toContain('待验证')
    expect(stale.join(' ')).not.toContain('已就绪')
    expect(stale.join(' ')).not.toContain('接入完成')
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
    expect(errorRequestsRelayInput('relay_public_host must be a concrete host')).toBe(true)
    expect(simplifiedForwardingChanged(service, draft({ relay_public_host: '203.0.113.10' }))).toBe(false)
    expect(simplifiedForwardingChanged(service, draft({ backend_url: 'http://10.9.0.3:8096' }))).toBe(true)
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

  it('maps summary, certificate, probe tones and retained resource kind labels consistently', () => {
    expect(serviceSummaryTone('已就绪')).toBe('success')
    expect(serviceSummaryTone('失败')).toBe('danger')
    expect(serviceSummaryTone('待确认')).toBe('warning')
    expect(serviceSummaryTone('已保存、待生效')).toBe('warning')
    expect(serviceSummaryTone('切换中')).toBe('warning')
    expect(serviceSummaryTone('')).toBe('neutral')
    expect(serviceSummaryTone(null)).toBe('neutral')

    expect(certificateTone({ state: 'ready' })).toBe('success')
    expect(certificateTone({ state: 'error' })).toBe('danger')
    expect(certificateTone({ state: 'issuing' })).toBe('primary')
    expect(certificateTone({ state: 'none' })).toBe('neutral')
    expect(certificateTone(undefined)).toBe('neutral')
    expect(certificateTone({ state: 'pending' })).toBe('warning')

    expect(probeTone({ status: 'connected', verification: 'expired' })).toBe('warning')
    expect(probeTone({ status: 'failed', error: 'timeout' })).toBe('danger')
    expect(probeTone({ status: 'connected' })).toBe('success')
    expect(probeTone({ status: 'http_status', http_status_code: 401 })).toBe('primary')
    expect(probeTone({ status: 'untested' })).toBe('neutral')
    expect(probeTone(null)).toBe('neutral')
    expect(probeTone({ status: 'dial_error' })).toBe('warning')

    expect(retainedResourceKindLabel('certificate')).toBe('证书')
    expect(retainedResourceKindLabel('relay_listener')).toBe('监听器')
    expect(retainedResourceKindLabel('agent')).toBe('节点')
    expect(retainedResourceKindLabel('custom_kind')).toBe('custom_kind')
    expect(retainedResourceKindLabel('')).toBe('资源')
  })

  it('buckets, summarizes, and filters access services across agent, keyword, and status', () => {
    const list = [
      {
        id: 'svc-ready',
        name: '家庭影院',
        entry_agent_id: 'edge',
        service_agent_id: 'origin',
        entry_url: 'https://emby.example.com',
        backend_url: 'http://10.1.0.8:8096',
        summary: '已就绪',
      },
      {
        id: 'svc-pending',
        name: '相册同步',
        entry_agent_id: 'hk',
        service_agent_id: 'edge',
        entry_url: 'https://photos.example.com',
        backend_url: 'http://10.1.0.9:2283',
        summary: '待确认',
      },
      {
        id: 'svc-verify',
        name: '音乐站',
        entry_agent_id: 'hk',
        service_agent_id: 'nas',
        entry_url: 'https://music.example.com',
        backend_url: 'http://10.1.0.10:4533',
        summary: '待验证',
      },
      {
        id: 'svc-failed',
        name: '失败站点',
        entry_agent_id: 'jp',
        service_agent_id: 'origin',
        entry_url: 'https://broken.example.com',
        backend_url: 'http://10.1.0.11:8080',
        summary: '失败',
      },
    ]

    expect(serviceStatusBucket(list[0])).toBe('ready')
    expect(serviceStatusBucket(list[1])).toBe('pending')
    expect(serviceStatusBucket(list[2])).toBe('pending')
    expect(serviceStatusBucket(list[3])).toBe('failed')
    expect(serviceStatusBucket(null)).toBe('pending')

    expect(summarizeServices(list)).toEqual({ total: 4, ready: 1, pending: 2, failed: 1 })
    expect(summarizeServices([])).toEqual({ total: 0, ready: 0, pending: 0, failed: 0 })

    // All-agent sentinel returns full list
    expect(filterAccessServices(list, { agentId: '__all__', q: '', status: '' })).toHaveLength(4)
    // Agent matches either entry_agent_id or service_agent_id
    expect(filterAccessServices(list, { agentId: 'edge' }).map((s) => s.id)).toEqual(['svc-ready', 'svc-pending'])
    expect(filterAccessServices(list, { agentId: 'origin' }).map((s) => s.id)).toEqual(['svc-ready', 'svc-failed'])
    // Status bucket filter
    expect(filterAccessServices(list, { status: 'ready' }).map((s) => s.id)).toEqual(['svc-ready'])
    expect(filterAccessServices(list, { status: 'pending' }).map((s) => s.id)).toEqual(['svc-pending', 'svc-verify'])
    expect(filterAccessServices(list, { status: 'failed' }).map((s) => s.id)).toEqual(['svc-failed'])
    // Keyword matches name, entry_url, backend_url, id (case-insensitive)
    expect(filterAccessServices(list, { q: 'EMBY' }).map((s) => s.id)).toEqual(['svc-ready'])
    expect(filterAccessServices(list, { q: '2283' }).map((s) => s.id)).toEqual(['svc-pending'])
    expect(filterAccessServices(list, { q: '音乐' }).map((s) => s.id)).toEqual(['svc-verify'])
    expect(filterAccessServices(list, { q: 'svc-failed' }).map((s) => s.id)).toEqual(['svc-failed'])
    // Combined filters
    expect(filterAccessServices(list, { agentId: 'edge', status: 'pending', q: 'photos' }).map((s) => s.id)).toEqual(['svc-pending'])
    expect(filterAccessServices(list, { agentId: 'edge', status: 'failed' })).toEqual([])
  })

  it('resolves create, update, correct, and blocked_advanced submit actions automatically', () => {
    const base = { id: 'svc-1', ...draft(), summary: '已就绪', advanced: false, last_error: '' }

    // Create when no current service
    expect(resolveServiceSubmitAction(null, draft())).toBe('create')
    expect(resolveServiceSubmitAction(undefined, draft())).toBe('create')

    // Normal ready service: name edit and forwarding edit both use update
    expect(resolveServiceSubmitAction(base, draft({ name: '改名' }))).toBe('update')
    expect(resolveServiceSubmitAction(base, draft({ backend_url: 'http://10.2.0.9:8096' }))).toBe('update')

    // Failed / pending / errored service: name-only stays update; forwarding edit uses correct
    const failed = { ...base, summary: '失败', last_error: '证书签发失败' }
    expect(resolveServiceSubmitAction(failed, draft({ name: '仅改名' }))).toBe('update')
    expect(resolveServiceSubmitAction(failed, draft({ entry_url: 'https://new.example.com' }))).toBe('correct')

    const pendingConfirm = { ...base, summary: '待确认', last_error: '' }
    expect(resolveServiceSubmitAction(pendingConfirm, draft({ entry_agent_id: 'origin' }))).toBe('correct')

    // Supplying single-hop relay fields when last_error asks for relay input uses correct
    const needRelay = { ...base, summary: '待确认', last_error: 'relay_public_host must be a concrete host' }
    expect(resolveServiceSubmitAction(needRelay, draft({ name: '改名不补中继' }))).toBe('update')
    expect(resolveServiceSubmitAction(needRelay, draft({ relay_public_host: '203.0.113.10' }))).toBe('correct')
    expect(resolveServiceSubmitAction(needRelay, draft({ relay_listen_port: 19443 }))).toBe('correct')

    // Advanced service: only name edit allowed (update); forwarding edit blocked; never correct
    const advanced = { ...base, summary: '失败', advanced: true, last_error: 'relay_public_host missing' }
    expect(resolveServiceSubmitAction(advanced, draft({ name: '高级改名' }))).toBe('update')
    expect(resolveServiceSubmitAction(advanced, draft({ backend_url: 'http://10.9.0.1:8096' }))).toBe('blocked_advanced')
    expect(resolveServiceSubmitAction(advanced, draft({ entry_agent_id: 'origin' }))).toBe('blocked_advanced')
  })
})

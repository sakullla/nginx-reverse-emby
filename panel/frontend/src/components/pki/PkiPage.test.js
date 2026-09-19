// @vitest-environment jsdom

import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { computed, ref } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import { parse as parseSfc } from '@vue/compiler-sfc'
import postcss from 'postcss'
import PkiPage from '../../pages/PkiPage.vue'
import agentPickerSource from '../AgentPicker.vue?raw'
import baseModalSource from '../base/BaseModal.vue?raw'
import createAgentPickerSource from '../common/CreateAgentPicker.vue?raw'
import globalSearchSource from '../GlobalSearch.vue?raw'
import versionsPageSource from '../../pages/VersionsPage.vue?raw'
import indexDocumentSource from '../../../index.html?raw'

const modalUtilitiesSource = readFileSync(resolve('src/styles/utilities.css'), 'utf8')

const pki = vi.hoisted(() => ({
  overview: vi.fn(),
  authorities: vi.fn(),
  identities: vi.fn(),
  certificates: vi.fn(),
  alerts: vi.fn(),
  events: vi.fn(),
  enrollment: vi.fn(),
  confirmation: vi.fn(),
  revoke: vi.fn(),
  forceRotate: vi.fn(),
  rotateCA: vi.fn(),
  emergencyCA: vi.fn(),
  exportBackup: vi.fn(),
  importBackup: vi.fn()
}))

const tracked = vi.hoisted(() => ({
  operations: [],
  track: vi.fn(),
  refresh: vi.fn(),
  forget: vi.fn()
}))

const agents = vi.hoisted(() => {
  const { ref } = require('vue')
  return { data: ref([]) }
})

vi.mock('../../hooks/useAgents', () => ({
  useAgents: () => ({ data: agents.data })
}))

vi.mock('../../api/pki', () => ({
  PKI_CONFIRMATION_ACTION: {
    revoke: 'revoke',
    forceRotate: 'force_rotate',
    rotateCA: 'ca_rotate',
    emergencyRotateCA: 'emergency_ca_rotate'
  },
  fetchPkiOverview: pki.overview,
  fetchPkiAuthorities: pki.authorities,
  fetchPkiIdentities: pki.identities,
  fetchPkiCertificates: pki.certificates,
  fetchPkiAlerts: pki.alerts,
  fetchPkiEvents: pki.events,
  createPkiEnrollmentToken: pki.enrollment,
  issuePkiConfirmation: pki.confirmation,
  revokePkiIdentity: pki.revoke,
  forceRotatePkiIdentity: pki.forceRotate,
  rotatePkiAuthority: pki.rotateCA,
  emergencyRotatePkiAuthority: pki.emergencyCA,
  exportProtectedPki: pki.exportBackup,
  importProtectedPki: pki.importBackup,
  protectedArchiveBlob: vi.fn(() => null)
}))

vi.mock('../../hooks/usePkiOperations', () => ({
  usePkiOperations: () => ({
    operations: ref(tracked.operations),
    errors: computed(() => ({})),
    track: tracked.track,
    refresh: tracked.refresh,
    forget: tracked.forget
  }),
  resetPkiOperationMemory: vi.fn(),
  recordPkiOperation: vi.fn((operation) => {
    tracked.operations = [operation, ...tracked.operations]
    return operation
  })
}))

function createTestRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'home', component: { template: '<div />' } },
      { path: '/certs', name: 'certs', component: { template: '<div />' } },
      { path: '/pki', name: 'pki', component: { template: '<div />' } },
    ],
  })
}

async function mountPage() {
  const router = createTestRouter()
  await router.push('/pki')
  await router.isReady()
  document.body.innerHTML = '<div id="app"></div>'
  return mount(PkiPage, {
    attachTo: document.getElementById('app'),
    global: {
      plugins: [router],
      stubs: {
        RouterLink: { template: '<a><slot /></a>' },
        // Keep Teleport real so BaseModal still mounts, but attachTo body above
        // makes dialog queryable through document/wrapper root.
      }
    }
  })
}

function buttonByText(wrapper, label) {
  // Prefer page-level buttons (including teleported BaseModal footer buttons).
  const fromDoc = Array.from(document.body.querySelectorAll('button')).find(button => button.textContent.includes(label))
  if (fromDoc) return {
    exists: () => true,
    attributes: (name) => fromDoc.getAttribute(name) ?? (name in fromDoc ? String(fromDoc[name]) : undefined),
    text: () => fromDoc.textContent || '',
    async trigger(eventName) {
      fromDoc.dispatchEvent(new MouseEvent(eventName, { bubbles: true, cancelable: true }))
    },
    element: fromDoc,
  }
  return wrapper.findAll('button').find(button => button.text().includes(label))
}

function findInPage(selector) {
  // BaseModal teleports to <body>; query the live document after attachTo.
  return document.body.querySelector(selector)
}

function setInputValue(selector, value) {
  const el = findInPage(selector)
  if (!el) throw new Error(`Missing element: ${selector}`)
  el.value = value
  el.dispatchEvent(new Event('input', { bubbles: true }))
  el.dispatchEvent(new Event('change', { bubbles: true }))
}

function stylesFromSfc(source, filename) {
  const { descriptor } = parseSfc(source, { filename })
  return postcss.parse(descriptor.styles.map(style => style.content).join('\n'))
}

function declarationsFor(root, selector, property, mediaQuery) {
  const values = []
  root.walkRules((rule) => {
    const selectors = rule.selector.split(',').map(value => value.trim())
    if (!selectors.includes(selector)) return
    if (mediaQuery && (rule.parent.type !== 'atrule' || rule.parent.params !== mediaQuery)) return
    rule.walkDecls(property, declaration => values.push(declaration.value))
  })
  return values
}

describe('PkiPage behavior boundary', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.clearAllMocks()
    tracked.operations = []
    pki.overview.mockResolvedValue({
      pki_domain_id: 'domain-1',
      pki_epoch: 4,
      security_revision: 11,
      upgrade_state: 'tunnel_mtls_only',
      runtime_status: 'healthy',
      identity_count: 1,
      certificate_count: 1
    })
    pki.authorities.mockResolvedValue([{
      id: 'ca-2', generation: 2, status: 'active', fingerprint_sha256: 'ca-fingerprint', not_after: '2036-01-01T00:00:00Z'
    }])
    pki.identities.mockResolvedValue([{
      id: 'identity-1', kind: 'agent', agent_id: 'agent-1', state: 'active', current_certificate_id: 'cert-1', rotation_phase: 'idle'
    }])
    pki.certificates.mockResolvedValue([{
      id: 'cert-1', identity_id: 'identity-1', purpose: 'client_auth', ca_generation: 2,
      serial_hex: '01ab', public_key_fingerprint_sha256: 'cert-fingerprint', status: 'active',
      not_before: '2026-08-01T00:00:00Z', not_after: '2026-11-01T00:00:00Z', next_action: 'renew at one-third lifetime'
    }])
    pki.alerts.mockResolvedValue([])
    pki.events.mockResolvedValue([])
    agents.data.value = [
      { id: 'agent-1', name: '香港边缘节点' },
      { id: 'agent-2', name: '东京备用节点' },
      { id: 'local', name: '本机 Agent', is_local: true }
    ]
    pki.enrollment.mockResolvedValue({ token: 'one-time-secret', scope: 'new_agent', expires_at: '2026-08-03T02:00:00Z' })
    pki.confirmation.mockResolvedValue({ nonce: 'nonce-1', action: 'revoke', target_id: 'identity-1' })
    pki.revoke.mockResolvedValue({ id: 'op-revoke', state: 'accepted', kind: 'revoke' })
  })

  it('keeps mounted dialogs inside the mobile viewport and safe area', async () => {
    const wrapper = await mountPage()
    await flushPromises()
    await wrapper.find('[data-test="identity-revoke"]').trigger('click')
    await flushPromises()

    const dialog = findInPage('[data-test="action-dialog"] [role="dialog"]')
    expect(dialog).toBeTruthy()
    expect(dialog.classList.contains('modal')).toBe(true)

    const viewport = new DOMParser()
      .parseFromString(indexDocumentSource, 'text/html')
      .querySelector('meta[name="viewport"]')
    const viewportDirectives = viewport.content.split(',').map(value => value.trim())
    expect(viewportDirectives).toContain('viewport-fit=cover')

    const modalStyles = stylesFromSfc(baseModalSource, 'BaseModal.vue')
    expect(declarationsFor(modalStyles, '.modal', 'max-height', '(max-width: 640px)')).toEqual([
      'calc(100vh - var(--space-8))',
      'calc(100dvh - var(--space-8))'
    ])
    expect(declarationsFor(modalStyles, '.modal-backdrop', 'padding-bottom', '(max-width: 640px)'))
      .toEqual(['max(var(--space-4), env(safe-area-inset-bottom, 0px))'])
    expect(declarationsFor(modalStyles, '.modal', 'max-height', '(max-width: 375px) and (max-height: 812px)'))
      .toEqual(['100vh', '100dvh'])

    const sharedModalStyles = postcss.parse(modalUtilitiesSource)
    expect(declarationsFor(sharedModalStyles, '.modal', 'max-height')).toEqual([
      'min(90vh, 920px)',
      'min(90dvh, 920px)',
      'calc(100dvh - 5.5rem - env(safe-area-inset-bottom, 0px))'
    ])
    expect(declarationsFor(sharedModalStyles, '.modal-overlay', 'padding-bottom'))
      .toContain('max(clamp(0.75rem, 2vw, 1.5rem), env(safe-area-inset-bottom, 0px))')

    const overlayContracts = [
      [globalSearchSource, 'GlobalSearch.vue', '.global-search-panel', undefined, ['80vh', '80dvh']],
      [agentPickerSource, 'AgentPicker.vue', '.agent-picker__dropdown', '(max-width: 640px)', ['70vh', '70dvh']],
      [createAgentPickerSource, 'CreateAgentPicker.vue', '.create-agent-picker', undefined, [
        'min(520px, calc(100vh - var(--space-8)))',
        'min(520px, calc(100dvh - var(--space-8)))'
      ]],
      [versionsPageSource, 'VersionsPage.vue', '.modal', undefined, [
        'calc(100vh - var(--space-8))',
        'calc(100dvh - var(--space-8))'
      ]]
    ]
    for (const [source, filename, selector, mediaQuery, expected] of overlayContracts) {
      expect(declarationsFor(stylesFromSfc(source, filename), selector, 'max-height', mediaQuery), filename)
        .toEqual(expected)
    }
  })

  it('renders the PKI boundary, lifecycle fields, and shared owner labels', async () => {
    pki.identities.mockResolvedValue([
      {
        id: 'identity-1', kind: 'agent', agent_id: 'agent-1', state: 'active', current_certificate_id: 'cert-1', rotation_phase: 'idle'
      },
      {
        id: 'identity-local', kind: 'agent', agent_id: 'local', state: 'active', current_certificate_id: 'cert-local', rotation_phase: 'idle'
      }
    ])
    pki.certificates.mockResolvedValue([
      {
        id: 'cert-1', identity_id: 'identity-1', purpose: 'client_auth', ca_generation: 2,
        serial_hex: '01ab', public_key_fingerprint_sha256: 'cert-fingerprint', status: 'active',
        not_before: '2026-08-01T00:00:00Z', not_after: '2026-11-01T00:00:00Z', next_action: 'renew at one-third lifetime'
      },
      {
        id: 'cert-local', identity_id: 'identity-local', purpose: 'client_auth', ca_generation: 2,
        serial_hex: '0a0a', public_key_fingerprint_sha256: 'local-fingerprint', status: 'active',
        not_before: '2026-08-01T00:00:00Z', not_after: '2026-11-01T00:00:00Z'
      }
    ])
    pki.alerts.mockResolvedValue([{
      id: 'alert-1', level: 'warning', kind: 'renewal_due', object_type: 'identity', object_id: 'identity-1',
      reason: 'certificate nearing one-third lifetime', last_seen: '2026-08-03T01:00:00Z'
    }])
    pki.events.mockResolvedValue([{
      id: 'event-1', type: 'rotate', object_type: 'identity', object_id: 'identity-1', result: 'success',
      source: 'panel', occurred_at: '2026-08-03T01:05:00Z', reason: 'scheduled renew'
    }])

    const wrapper = await mountPage()
    await flushPromises()

    expect(wrapper.text()).toContain('当前为内部 PKI 域')
    expect(wrapper.text()).toContain('内部 PKI')
    expect(wrapper.text()).toContain('香港边缘节点')
    expect(wrapper.text()).toContain('本机 Agent')
    expect(wrapper.find('[data-test="identity-row"]').text()).toContain('客户端认证')
    expect(wrapper.find('[data-test="identity-row"]').text()).not.toContain('identity-1')
    expect(wrapper.find('[data-test="identity-row"]').text()).not.toContain('agent-1')
    expect(wrapper.text()).toContain('到期前自动续签')
    expect(wrapper.text()).toContain('即将续签')

    await wrapper.find('[data-test="identity-row"]').trigger('click')
    await flushPromises()
    const inspect = findInPage('[data-test="identity-inspect"]')
    expect(inspect?.textContent).toContain('client_auth')
    expect(inspect?.textContent).toContain('CA generation 2')
    expect(inspect?.textContent).toContain('01ab')
    expect(inspect?.textContent).toContain('cert-fingerprint')
    expect(inspect?.textContent).toContain('renew at one-third lifetime')
  })

  it('sorts status-sensitive resources and limits every non-CA list to five rows per page', async () => {
    pki.authorities.mockResolvedValue(Array.from({ length: 7 }, (_, index) => {
      const generation = index + 1
      return {
        id: `ca-${generation}`,
        generation,
        status: generation === 7 ? 'active' : 'revoked',
        fingerprint_sha256: `ca-${generation}-fingerprint`,
        not_before: generation === 6 ? '2026-07-01T00:00:00Z' : `2026-08-0${generation}T00:00:00Z`,
        not_after: generation === 6 ? '2036-07-01T00:00:00Z' : `2036-08-0${generation}T00:00:00Z`
      }
    }))
    pki.identities.mockResolvedValue(Array.from({ length: 7 }, (_, index) => {
      const number = index + 1
      return {
        id: `identity-${number}`,
        kind: 'agent',
        agent_id: `agent-${number}`,
        state: number === 7 ? 'active' : 'revoked',
        current_certificate_id: `cert-${number}`,
        revoked_at: number === 7 ? null : `2026-08-0${number}T01:00:00Z`
      }
    }))
    pki.certificates.mockResolvedValue(Array.from({ length: 7 }, (_, index) => {
      const number = index + 1
      return {
        id: `cert-${number}`,
        identity_id: `identity-${number}`,
        purpose: 'client_auth',
        ca_generation: number,
        serial_hex: `0${number}`,
        status: number === 7 ? 'active' : 'revoked',
        not_before: `2026-08-0${number}T00:00:00Z`,
        not_after: `2026-11-0${number}T00:00:00Z`
      }
    }))
    pki.alerts.mockResolvedValue(Array.from({ length: 7 }, (_, index) => {
      const number = index + 1
      return {
        id: `alert-${number}`,
        kind: `alert-${number}`,
        object_type: 'certificate',
        object_id: `cert-${number}`,
        level: number === 7 ? 'critical' : 'warning',
        reason: `reason-${number}`,
        last_seen: `2026-08-0${number}T02:00:00Z`
      }
    }))
    pki.events.mockResolvedValue(Array.from({ length: 7 }, (_, index) => {
      const number = index + 1
      return {
        id: `event-${number}`,
        type: `event-${number}`,
        object_type: 'certificate',
        object_id: `cert-${number}`,
        result: 'success',
        source: 'panel',
        occurred_at: `2026-08-0${number}T03:00:00Z`
      }
    }))
    tracked.operations = Array.from({ length: 7 }, (_, index) => {
      const number = index + 1
      return {
        id: `operation-${number}`,
        kind: 'ca_rotate',
        state: number === 7 ? 'failed' : 'succeeded',
        terminal: true,
        updated_at: `2026-08-0${number}T04:00:00Z`
      }
    })

    const wrapper = await mountPage()
    await flushPromises()

    expect(wrapper.findAll('[data-test="authority-row"]')).toHaveLength(5)
    expect(wrapper.findAll('[data-test="authority-row"]')[0].text()).toContain('第 7 代')
    expect(wrapper.findAll('[data-test="authority-row"]')[1].text()).toContain('第 5 代')
    expect(wrapper.text()).not.toContain('第 1 代')
    expect(wrapper.text()).not.toContain('第 6 代')

    expect(wrapper.findAll('[data-test="identity-row"]')).toHaveLength(5)
    expect(wrapper.findAll('[data-test="identity-row"]')[0].text()).toContain('agent-7')
    expect(wrapper.findAll('[data-test="identity-row"]')[0].text()).not.toContain('identity-7')
    expect(wrapper.findAll('[data-test="alert-row"]')).toHaveLength(5)
    expect(wrapper.findAll('[data-test="alert-row"]')[0].text()).toContain('alert-7')
    expect(wrapper.findAll('[data-test="event-row"]')).toHaveLength(5)
    expect(wrapper.findAll('[data-test="event-row"]')[0].text()).toContain('event-7')
    expect(wrapper.findAll('[data-test="operation-row"]')).toHaveLength(5)
    expect(wrapper.findAll('[data-test="operation-row"]')[0].text()).toContain('已失败')

    await wrapper.find('[data-test="identity-pagination"]').findAll('button')[1].trigger('click')
    expect(wrapper.findAll('[data-test="identity-row"]')).toHaveLength(2)
    await wrapper.find('[data-test="alert-pagination"]').findAll('button')[1].trigger('click')
    expect(wrapper.findAll('[data-test="alert-row"]')).toHaveLength(2)
    await wrapper.find('[data-test="event-pagination"]').findAll('button')[1].trigger('click')
    expect(wrapper.findAll('[data-test="event-row"]')).toHaveLength(2)
    await wrapper.find('[data-test="operation-pagination"]').findAll('button')[1].trigger('click')
    expect(wrapper.findAll('[data-test="operation-row"]')).toHaveLength(2)
  })

  it('disables invalid identity actions and explains automatic migration activation', async () => {
    pki.identities.mockResolvedValue([{
      id: 'identity-revoked', kind: 'agent', agent_id: 'agent-1', state: 'revoked', current_certificate_id: 'cert-revoked'
    }])
    pki.certificates.mockResolvedValue([{
      id: 'cert-revoked', identity_id: 'identity-revoked', purpose: 'client_auth', ca_generation: 1,
      status: 'revoked', revoked_at: '2026-08-05T00:00:00Z'
    }])

    const wrapper = await mountPage()
    await flushPromises()

    const row = wrapper.find('[data-test="identity-row"]')
    const rotate = row.find('[data-test="identity-force-rotate"]')
    const revoke = row.find('[data-test="identity-revoke"]')
    expect(rotate.exists()).toBe(true)
    expect(revoke.exists()).toBe(true)
    expect(rotate.attributes('disabled')).toBeDefined()
    expect(revoke.attributes('disabled')).toBeDefined()
    expect(buttonByText(wrapper, '迁移激活')).toBeUndefined()

    pki.identities.mockResolvedValue([{
      id: 'identity-enrollment-required', kind: 'agent', agent_id: 'agent-2', state: 'enrollment_required'
    }])
    pki.certificates.mockResolvedValue([])
    const enrollmentWrapper = await mountPage()
    await flushPromises()

    const enrollmentRow = enrollmentWrapper.find('[data-test="identity-row"]')
    expect(enrollmentRow.find('[data-test="identity-force-rotate"]').attributes('disabled')).toBeDefined()
    expect(enrollmentRow.find('[data-test="identity-revoke"]').attributes('disabled')).toBeUndefined()

    pki.overview.mockResolvedValue({
      pki_domain_id: 'domain-1',
      pki_epoch: 4,
      security_revision: 11,
      upgrade_state: 'migration_required',
      runtime_status: 'healthy'
    })
    const migrationWrapper = await mountPage()
    await flushPromises()
    expect(buttonByText(migrationWrapper, '迁移激活')).toBeUndefined()
    expect(migrationWrapper.find('[data-test="automatic-activation-notice"]').text()).toContain('就绪后自动激活')
  })

  it('obtains a target-bound nonce before submitting revoke', async () => {
    const wrapper = await mountPage()
    await flushPromises()

    await wrapper.find('[data-test="identity-revoke"]').trigger('click')
    await flushPromises()
    setInputValue('[data-test="action-reason"]', 'certificate compromised')
    setInputValue('[data-test="action-confirmation"]', 'identity-1')
    findInPage('[data-test="action-dialog"] form')?.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    await flushPromises()

    expect(pki.confirmation).toHaveBeenCalledWith('revoke', 'identity-1')
    expect(pki.revoke).toHaveBeenCalledWith('identity-1', {
      reason: 'certificate compromised',
      confirmationNonce: 'nonce-1'
    })
    expect(pki.confirmation.mock.invocationCallOrder[0]).toBeLessThan(pki.revoke.mock.invocationCallOrder[0])
    expect(tracked.track).toHaveBeenCalledWith(expect.objectContaining({ id: 'op-revoke' }))
  })

  it('obtains an identity-bound nonce before forcing endpoint rotation', async () => {
    pki.forceRotate.mockResolvedValue({ id: 'op-force-rotate', state: 'accepted', kind: 'force_rotate' })
    const wrapper = await mountPage()
    await flushPromises()

    await wrapper.find('[data-test="identity-force-rotate"]').trigger('click')
    await flushPromises()
    setInputValue('[data-test="action-reason"]', 'renew endpoint credential now')
    setInputValue('[data-test="action-confirmation"]', 'identity-1')
    findInPage('[data-test="action-dialog"] form')?.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    await flushPromises()

    expect(pki.confirmation).toHaveBeenCalledWith('force_rotate', 'identity-1')
    expect(pki.forceRotate).toHaveBeenCalledWith('identity-1', {
      reason: 'renew endpoint credential now',
      confirmationNonce: 'nonce-1'
    })
    expect(pki.confirmation.mock.invocationCallOrder[0]).toBeLessThan(pki.forceRotate.mock.invocationCallOrder[0])
  })

  it('obtains a domain-bound nonce before normal CA rotation', async () => {
    pki.rotateCA.mockResolvedValue({ id: 'op-ca-rotate', state: 'accepted', kind: 'ca_rotate' })
    const wrapper = await mountPage()
    await flushPromises()

    await buttonByText(wrapper, '日常 CA 轮转').trigger('click')
    await flushPromises()
    setInputValue('[data-test="action-reason"]', 'scheduled authority maintenance')
    setInputValue('[data-test="action-confirmation"]', 'ROTATE CA')
    findInPage('[data-test="action-dialog"] form')?.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    await flushPromises()

    expect(pki.confirmation).toHaveBeenCalledWith('ca_rotate', 'domain')
    expect(pki.rotateCA).toHaveBeenCalledWith({
      reason: 'scheduled authority maintenance',
      confirmationNonce: 'nonce-1'
    })
    expect(pki.confirmation.mock.invocationCallOrder[0]).toBeLessThan(pki.rotateCA.mock.invocationCallOrder[0])
  })

  it('does not request a nonce or mutation when a high-risk dialog is cancelled', async () => {
    const wrapper = await mountPage()
    await flushPromises()

    await buttonByText(wrapper, '紧急 CA 轮转').trigger('click')
    await flushPromises()
    const cancel = Array.from(document.body.querySelectorAll('button')).find(btn => btn.textContent.includes('取消'))
    cancel?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()

    expect(pki.confirmation).not.toHaveBeenCalled()
    expect(pki.emergencyCA).not.toHaveBeenCalled()
  })

  it('locks a submitted high-risk action and uses its original reason after a delayed nonce', async () => {
    let resolveConfirmation
    pki.confirmation.mockReturnValue(new Promise(resolve => { resolveConfirmation = resolve }))
    const wrapper = await mountPage()
    await flushPromises()

    await wrapper.find('[data-test="identity-revoke"]').trigger('click')
    await flushPromises()
    const dialog = findInPage('[data-test="action-dialog"]')
    const reason = findInPage('[data-test="action-reason"]')
    const confirmation = findInPage('[data-test="action-confirmation"]')
    setInputValue('[data-test="action-reason"]', 'original compromise reason')
    setInputValue('[data-test="action-confirmation"]', 'identity-1')
    dialog?.querySelector('form')?.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    await flushPromises()

    expect(reason?.disabled).toBe(true)
    expect(confirmation?.disabled).toBe(true)
    const cancel = Array.from(document.body.querySelectorAll('button')).find(btn => btn.textContent.includes('取消'))
    expect(cancel?.disabled).toBe(true)
    setInputValue('[data-test="action-reason"]', 'mutated while pending')
    dialog?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    expect(findInPage('[data-test="action-dialog"]')).toBeTruthy()

    resolveConfirmation({ nonce: 'delayed-nonce', action: 'revoke', target_id: 'identity-1' })
    await flushPromises()

    expect(pki.revoke).toHaveBeenCalledWith('identity-1', {
      reason: 'original compromise reason',
      confirmationNonce: 'delayed-nonce'
    })
    expect(findInPage('[data-test="action-dialog"]')).toBeFalsy()
  })

  it('clears export passphrases after a failed request without persisting or echoing them', async () => {
    pki.exportBackup.mockRejectedValue(new Error('service unavailable'))
    const wrapper = await mountPage()
    await flushPromises()

    const secret = 'request-only-passphrase'
    const form = wrapper.findAll('form.backup-card')[0]
    await form.find('[data-test="export-passphrase"]').setValue(secret)
    await form.findAll('input[type="password"]')[1].setValue(secret)
    await form.trigger('submit')
    await flushPromises()

    expect(pki.exportBackup).toHaveBeenCalledWith(secret)
    expect(form.find('[data-test="export-passphrase"]').element.value).toBe('')
    expect(form.findAll('input[type="password"]')[1].element.value).toBe('')
    expect(localStorage.getItem('nre.pki.operations.v1') || '').not.toContain(secret)
    expect(wrapper.text()).not.toContain(secret)
  })

  it('tracks a protected import and clears its request-only inputs and file selection', async () => {
    pki.importBackup.mockResolvedValue({ id: 'op-import', state: 'accepted', kind: 'protected_import' })
    const wrapper = await mountPage()
    await flushPromises()

    const form = wrapper.find('form.backup-card--import')
    const archive = new File(['encrypted archive'], 'backup.nre-pki', { type: 'application/octet-stream' })
    const fileInput = form.find('[data-test="import-archive"]')
    Object.defineProperty(fileInput.element, 'files', { configurable: true, value: [archive] })
    await fileInput.trigger('change')
    await form.find('[data-test="import-passphrase"]').setValue('import-request-secret')
    await form.find('[data-test="import-reason"]').setValue('planned restore')
    await form.trigger('submit')
    await flushPromises()

    expect(pki.importBackup).toHaveBeenCalledWith({
      archive,
      passphrase: 'import-request-secret',
      reason: 'planned restore'
    })
    expect(tracked.track).toHaveBeenCalledWith(expect.objectContaining({ id: 'op-import' }))
    expect(form.find('[data-test="import-passphrase"]').element.value).toBe('')
    expect(form.find('[data-test="import-reason"]').element.value).toBe('')
    expect(localStorage.getItem('nre.pki.operations.v1') || '').not.toContain('import-request-secret')
    expect(wrapper.text()).not.toContain('import-request-secret')

    await form.find('[data-test="import-passphrase"]').setValue('second-secret')
    await form.find('[data-test="import-reason"]').setValue('retry without file')
    await form.trigger('submit')
    expect(pki.importBackup).toHaveBeenCalledTimes(1)
  })

  it('clears a failed protected import password and file selection without tracking or echoing secrets', async () => {
    pki.importBackup.mockRejectedValue(new Error('restore failed'))
    const wrapper = await mountPage()
    await flushPromises()

    const form = wrapper.find('form.backup-card--import')
    const archive = new File(['encrypted archive'], 'failed.nre-pki', { type: 'application/octet-stream' })
    const fileInput = form.find('[data-test="import-archive"]')
    Object.defineProperty(fileInput.element, 'files', { configurable: true, value: [archive] })
    await fileInput.trigger('change')
    const secret = 'failed-import-secret'
    await form.find('[data-test="import-passphrase"]').setValue(secret)
    await form.find('[data-test="import-reason"]').setValue('failed restore')
    await form.trigger('submit')
    await flushPromises()

    expect(pki.importBackup).toHaveBeenCalledTimes(1)
    expect(tracked.track).not.toHaveBeenCalled()
    expect(form.find('[data-test="import-passphrase"]').element.value).toBe('')
    expect(localStorage.getItem('nre.pki.operations.v1') || '').not.toContain(secret)
    expect(wrapper.text()).not.toContain(secret)

    await form.find('[data-test="import-passphrase"]').setValue('retry-secret')
    await form.trigger('submit')
    expect(pki.importBackup).toHaveBeenCalledTimes(1)
  })
})

// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { computed, ref } from 'vue'
import PkiPage from '../../pages/PkiPage.vue'

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
  })
}))

function mountPage() {
  return mount(PkiPage, {
    global: {
      stubs: {
        RouterLink: { template: '<a><slot /></a>' }
      }
    }
  })
}

function buttonByText(wrapper, label) {
  return wrapper.findAll('button').find(button => button.text().includes(label))
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
    pki.enrollment.mockResolvedValue({ token: 'one-time-secret', scope: 'new_agent', expires_at: '2026-08-03T02:00:00Z' })
    pki.confirmation.mockResolvedValue({ nonce: 'nonce-1', action: 'revoke', target_id: 'identity-1' })
    pki.revoke.mockResolvedValue({ id: 'op-revoke', state: 'accepted', kind: 'revoke' })
  })

  it('renders public/internal separation and the lifecycle identity fields', async () => {
    const wrapper = mountPage()
    await flushPromises()

    expect(wrapper.text()).toContain('与公网证书分域')
    expect(wrapper.text()).toContain('内部 PKI')
    expect(wrapper.text()).toContain('identity-1')
    expect(wrapper.text()).toContain('client_auth')
    expect(wrapper.text()).toContain('CA generation 2')
    expect(wrapper.text()).toContain('01ab')
    expect(wrapper.text()).toContain('cert-fingerprint')
    expect(wrapper.text()).toContain('renew at one-third lifetime')
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

    const wrapper = mountPage()
    await flushPromises()

    expect(wrapper.findAll('[data-test="authority-row"]')).toHaveLength(5)
    expect(wrapper.findAll('[data-test="authority-row"]')[0].text()).toContain('Generation 7')
    expect(wrapper.findAll('[data-test="authority-row"]')[1].text()).toContain('Generation 5')
    expect(wrapper.text()).not.toContain('Generation 1')
    expect(wrapper.text()).not.toContain('Generation 6')

    expect(wrapper.findAll('[data-test="identity-row"]')).toHaveLength(5)
    expect(wrapper.findAll('[data-test="identity-row"]')[0].text()).toContain('identity-7')
    expect(wrapper.findAll('[data-test="alert-row"]')).toHaveLength(5)
    expect(wrapper.findAll('[data-test="alert-row"]')[0].text()).toContain('alert-7')
    expect(wrapper.findAll('[data-test="event-row"]')).toHaveLength(5)
    expect(wrapper.findAll('[data-test="event-row"]')[0].text()).toContain('event-7')
    expect(wrapper.findAll('[data-test="operation-row"]')).toHaveLength(5)
    expect(wrapper.findAll('[data-test="operation-row"]')[0].text()).toContain('operation-7')

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

    const wrapper = mountPage()
    await flushPromises()

    const row = wrapper.find('[data-test="identity-row"]')
    const actions = row.findAll('button')
    expect(actions).toHaveLength(2)
    expect(actions[0].attributes('disabled')).toBeDefined()
    expect(actions[1].attributes('disabled')).toBeDefined()
    expect(buttonByText(wrapper, '迁移激活')).toBeUndefined()

    pki.identities.mockResolvedValue([{
      id: 'identity-enrollment-required', kind: 'agent', agent_id: 'agent-2', state: 'enrollment_required'
    }])
    pki.certificates.mockResolvedValue([])
    const enrollmentWrapper = mountPage()
    await flushPromises()

    const enrollmentActions = enrollmentWrapper.find('[data-test="identity-row"]').findAll('button')
    expect(enrollmentActions[0].attributes('disabled')).toBeDefined()
    expect(enrollmentActions[1].attributes('disabled')).toBeUndefined()

    pki.overview.mockResolvedValue({
      pki_domain_id: 'domain-1',
      pki_epoch: 4,
      security_revision: 11,
      upgrade_state: 'migration_required',
      runtime_status: 'healthy'
    })
    const migrationWrapper = mountPage()
    await flushPromises()
    expect(buttonByText(migrationWrapper, '迁移激活')).toBeUndefined()
    expect(migrationWrapper.find('[data-test="automatic-activation-notice"]').text()).toContain('就绪后自动激活')
  })

  it('shows an enrollment token once and clears it when the dialog closes', async () => {
    const wrapper = mountPage()
    await flushPromises()

    await buttonByText(wrapper, '创建登记令牌').trigger('click')
    await buttonByText(wrapper, '生成令牌').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="enrollment-secret"]').text()).toContain('one-time-secret')
    expect(localStorage.getItem('one-time-secret')).toBeNull()

    await buttonByText(wrapper, '我已保存并关闭').trigger('click')
    expect(wrapper.find('[data-test="enrollment-secret"]').exists()).toBe(false)
    expect(wrapper.html()).not.toContain('one-time-secret')
  })

  it('keeps a pending enrollment dialog open until its one-time token can be handled', async () => {
    let resolveEnrollment
    pki.enrollment.mockReturnValue(new Promise(resolve => { resolveEnrollment = resolve }))
    const wrapper = mountPage()
    await flushPromises()

    await buttonByText(wrapper, '创建登记令牌').trigger('click')
    await buttonByText(wrapper, '生成令牌').trigger('click')

    const dialog = wrapper.find('[data-test="enrollment-dialog"]')
    expect(buttonByText(wrapper, '取消').attributes('disabled')).toBeDefined()
    await dialog.trigger('click')
    expect(wrapper.find('[data-test="enrollment-dialog"]').exists()).toBe(true)

    resolveEnrollment({ token: 'pending-one-time-secret', scope: 'new_agent', expires_at: '2026-08-03T02:00:00Z' })
    await flushPromises()
    expect(wrapper.find('[data-test="enrollment-secret"]').text()).toContain('pending-one-time-secret')

    await buttonByText(wrapper, '我已保存并关闭').trigger('click')
    expect(wrapper.html()).not.toContain('pending-one-time-secret')
  })

  it('obtains a target-bound nonce before submitting revoke', async () => {
    const wrapper = mountPage()
    await flushPromises()

    await buttonByText(wrapper, '撤销').trigger('click')
    await wrapper.find('[data-test="action-reason"]').setValue('certificate compromised')
    await wrapper.find('[data-test="action-confirmation"]').setValue('identity-1')
    await wrapper.find('[data-test="action-dialog"] form').trigger('submit')
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
    const wrapper = mountPage()
    await flushPromises()

    await buttonByText(wrapper, '强制换证').trigger('click')
    await wrapper.find('[data-test="action-reason"]').setValue('renew endpoint credential now')
    await wrapper.find('[data-test="action-confirmation"]').setValue('identity-1')
    await wrapper.find('[data-test="action-dialog"] form').trigger('submit')
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
    const wrapper = mountPage()
    await flushPromises()

    await buttonByText(wrapper, '日常 CA 轮转').trigger('click')
    await wrapper.find('[data-test="action-reason"]').setValue('scheduled authority maintenance')
    await wrapper.find('[data-test="action-confirmation"]').setValue('ROTATE CA')
    await wrapper.find('[data-test="action-dialog"] form').trigger('submit')
    await flushPromises()

    expect(pki.confirmation).toHaveBeenCalledWith('ca_rotate', 'domain')
    expect(pki.rotateCA).toHaveBeenCalledWith({
      reason: 'scheduled authority maintenance',
      confirmationNonce: 'nonce-1'
    })
    expect(pki.confirmation.mock.invocationCallOrder[0]).toBeLessThan(pki.rotateCA.mock.invocationCallOrder[0])
  })

  it('does not request a nonce or mutation when a high-risk dialog is cancelled', async () => {
    const wrapper = mountPage()
    await flushPromises()

    await buttonByText(wrapper, '紧急 CA 轮转').trigger('click')
    await buttonByText(wrapper, '取消').trigger('click')

    expect(pki.confirmation).not.toHaveBeenCalled()
    expect(pki.emergencyCA).not.toHaveBeenCalled()
  })

  it('locks a submitted high-risk action and uses its original reason after a delayed nonce', async () => {
    let resolveConfirmation
    pki.confirmation.mockReturnValue(new Promise(resolve => { resolveConfirmation = resolve }))
    const wrapper = mountPage()
    await flushPromises()

    await buttonByText(wrapper, '撤销').trigger('click')
    const dialog = wrapper.find('[data-test="action-dialog"]')
    const reason = dialog.find('[data-test="action-reason"]')
    const confirmation = dialog.find('[data-test="action-confirmation"]')
    await reason.setValue('original compromise reason')
    await confirmation.setValue('identity-1')
    await dialog.find('form').trigger('submit')

    expect(reason.attributes('disabled')).toBeDefined()
    expect(confirmation.attributes('disabled')).toBeDefined()
    expect(buttonByText(wrapper, '取消').attributes('disabled')).toBeDefined()
    await reason.setValue('mutated while pending')
    await dialog.trigger('click')
    expect(wrapper.find('[data-test="action-dialog"]').exists()).toBe(true)

    resolveConfirmation({ nonce: 'delayed-nonce', action: 'revoke', target_id: 'identity-1' })
    await flushPromises()

    expect(pki.revoke).toHaveBeenCalledWith('identity-1', {
      reason: 'original compromise reason',
      confirmationNonce: 'delayed-nonce'
    })
    expect(wrapper.find('[data-test="action-dialog"]').exists()).toBe(false)
  })

  it('clears export passphrases after a failed request without persisting or echoing them', async () => {
    pki.exportBackup.mockRejectedValue(new Error('service unavailable'))
    const wrapper = mountPage()
    await flushPromises()

    const secret = 'request-only-passphrase'
    const form = wrapper.findAll('form.backup-form')[0]
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
    const wrapper = mountPage()
    await flushPromises()

    const form = wrapper.find('form.backup-form--import')
    const archive = new File(['encrypted archive'], 'backup.nre-pki', { type: 'application/octet-stream' })
    const fileInput = form.find('[data-test="import-archive"]')
    Object.defineProperty(fileInput.element, 'files', { configurable: true, value: [archive] })
    await fileInput.trigger('change')
    await form.find('[data-test="import-passphrase"]').setValue('import-request-secret')
    await form.find('[data-test="import-reason"]').setValue('planned restore')
    await form.find('[data-test="import-confirmation"]').setValue('IMPORT')
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
    expect(form.find('[data-test="import-confirmation"]').element.value).toBe('')
    expect(localStorage.getItem('nre.pki.operations.v1') || '').not.toContain('import-request-secret')
    expect(wrapper.text()).not.toContain('import-request-secret')

    await form.find('[data-test="import-passphrase"]').setValue('second-secret')
    await form.find('[data-test="import-reason"]').setValue('retry without file')
    await form.find('[data-test="import-confirmation"]').setValue('IMPORT')
    await form.trigger('submit')
    expect(pki.importBackup).toHaveBeenCalledTimes(1)
  })

  it('clears a failed protected import password and file selection without tracking or echoing secrets', async () => {
    pki.importBackup.mockRejectedValue(new Error('restore failed'))
    const wrapper = mountPage()
    await flushPromises()

    const form = wrapper.find('form.backup-form--import')
    const archive = new File(['encrypted archive'], 'failed.nre-pki', { type: 'application/octet-stream' })
    const fileInput = form.find('[data-test="import-archive"]')
    Object.defineProperty(fileInput.element, 'files', { configurable: true, value: [archive] })
    await fileInput.trigger('change')
    const secret = 'failed-import-secret'
    await form.find('[data-test="import-passphrase"]').setValue(secret)
    await form.find('[data-test="import-reason"]').setValue('failed restore')
    await form.find('[data-test="import-confirmation"]').setValue('IMPORT')
    await form.trigger('submit')
    await flushPromises()

    expect(pki.importBackup).toHaveBeenCalledTimes(1)
    expect(tracked.track).not.toHaveBeenCalled()
    expect(form.find('[data-test="import-passphrase"]').element.value).toBe('')
    expect(form.find('[data-test="import-confirmation"]').element.value).toBe('')
    expect(localStorage.getItem('nre.pki.operations.v1') || '').not.toContain(secret)
    expect(wrapper.text()).not.toContain(secret)

    await form.find('[data-test="import-passphrase"]').setValue('retry-secret')
    await form.find('[data-test="import-confirmation"]').setValue('IMPORT')
    await form.trigger('submit')
    expect(pki.importBackup).toHaveBeenCalledTimes(1)
  })
})

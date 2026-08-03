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
  activate: vi.fn(),
  exportBackup: vi.fn(),
  importBackup: vi.fn()
}))

const tracked = vi.hoisted(() => ({ track: vi.fn(), refresh: vi.fn(), forget: vi.fn() }))

vi.mock('../../api/pki', () => ({
  PKI_CONFIRMATION_ACTION: {
    revoke: 'revoke',
    forceRotate: 'force_rotate',
    rotateCA: 'ca_rotate',
    emergencyRotateCA: 'emergency_ca_rotate',
    activate: 'activate'
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
  activatePkiMigration: pki.activate,
  exportProtectedPki: pki.exportBackup,
  importProtectedPki: pki.importBackup,
  protectedArchiveBlob: vi.fn(() => null)
}))

vi.mock('../../hooks/usePkiOperations', () => ({
  usePkiOperations: () => ({
    operations: ref([]),
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
    pki.overview.mockResolvedValue({
      pki_domain_id: 'domain-1',
      pki_epoch: 4,
      security_revision: 11,
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
})

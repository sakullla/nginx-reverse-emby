// @vitest-environment node

import { beforeEach, describe, expect, it, vi } from 'vitest'

const requests = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('../api/client', () => ({ api: requests, longRunningRequest: { timeout: 0 } }))

const pki = await import('../api/pki.js')

describe('internal PKI API consumer', () => {
  beforeEach(() => {
    requests.get.mockReset()
    requests.post.mockReset()
  })

  it('keeps internal resources under the dedicated /pki namespace', async () => {
    requests.get
      .mockResolvedValueOnce({ data: { overview: { pki_domain_id: 'domain-1' } } })
      .mockResolvedValueOnce({ data: { certificates: [{ id: 'cert-1' }] } })

    await expect(pki.fetchPkiOverview()).resolves.toEqual({ pki_domain_id: 'domain-1' })
    await expect(pki.fetchPkiCertificates()).resolves.toEqual([{ id: 'cert-1' }])

    expect(requests.get).toHaveBeenNthCalledWith(1, '/pki/overview')
    expect(requests.get).toHaveBeenNthCalledWith(2, '/pki/certificates')
  })

  it('passes canonical audit filters without adding secrets to the URL', async () => {
    requests.get.mockResolvedValueOnce({ data: { events: [] } })

    await pki.fetchPkiEvents({
      type: 'certificate_revoked',
      identityID: 'identity-1',
      caGeneration: 2,
      result: 'succeeded',
      passphrase: 'must-not-leak'
    })

    expect(requests.get).toHaveBeenCalledWith('/pki/events', { params: {
      type: 'certificate_revoked',
      identity_id: 'identity-1',
      ca_generation: '2',
      result: 'succeeded'
    } })
  })

  it.each([
    ['pending', 'accepted', false],
    ['running', 'running', false],
    ['blocked', 'blocked', false],
    ['succeeded', 'succeeded', true],
    ['failed', 'failed', true]
  ])('normalizes %s as an independent PKI operation state', (state, expected, terminal) => {
    expect(pki.normalizePkiOperation({
      operation_id: 'op-1',
      status_url: '/panel-api/pki/operations/op-1',
      operation: { id: 'op-1', state }
    })).toMatchObject({ id: 'op-1', state: expected, terminal })
  })

  it('accepts only relative PKI operation status URLs', async () => {
    requests.get.mockResolvedValue({ data: { operation: { id: 'op-1', state: 'running' } } })

    await pki.fetchPkiOperationStatus('/panel-api/pki/operations/op-1')
    expect(requests.get).toHaveBeenCalledWith('/pki/operations/op-1')

    await expect(pki.fetchPkiOperationStatus('https://attacker.test/pki/operations/op-1'))
      .rejects.toThrow('internal PKI operation reference is invalid')
    expect(requests.get).toHaveBeenCalledTimes(1)
  })

  it('binds confirmation and revoke requests to the explicit action and target', async () => {
    requests.post
      .mockResolvedValueOnce({ data: { confirmation: { nonce: 'nonce-1', action: 'revoke', target_id: 'identity-1' } } })
      .mockResolvedValueOnce({ data: {
        operation_id: 'op-revoke',
        status_url: '/panel-api/pki/operations/op-revoke',
        operation: { id: 'op-revoke', state: 'pending', kind: 'revoke' }
      } })

    const confirmation = await pki.issuePkiConfirmation('revoke', 'identity-1')
    const operation = await pki.revokePkiIdentity('identity-1', {
      reason: 'identity compromised',
      confirmationNonce: confirmation.nonce
    })

    expect(requests.post).toHaveBeenNthCalledWith(1, '/pki/confirmations', {
      action: 'revoke',
      target_id: 'identity-1'
    })
    expect(requests.post).toHaveBeenNthCalledWith(2, '/pki/identities/identity-1/revoke', {
      reason: 'identity compromised',
      confirmation_nonce: 'nonce-1'
    }, { timeout: 0 })
    expect(operation).toMatchObject({ id: 'op-revoke', state: 'accepted' })
  })

  it('uses the backend canonical scope for bound reenrollment tokens', async () => {
    requests.post.mockResolvedValueOnce({ data: {
      enrollment_token: {
        token: 'one-time-token',
        scope: 'bound_reenrollment',
        bound_agent_id: 'agent-1'
      }
    } })

    await pki.createPkiEnrollmentToken({
      scope: 'bound_reenrollment',
      boundAgentId: 'agent-1'
    })

    expect(requests.post).toHaveBeenCalledWith('/pki/enrollment-tokens', {
      scope: 'bound_reenrollment',
      bound_agent_id: 'agent-1'
    })
  })

  it('sends protected import passphrases only in a multipart request body', async () => {
    requests.post.mockResolvedValueOnce({ data: {
      operation_id: 'op-import',
      operation: { id: 'op-import', state: 'succeeded', kind: 'protected_import' }
    } })
    const archive = new Blob(['encrypted'], { type: 'application/octet-stream' })

    await pki.importProtectedPki({ archive, passphrase: 'single-request-secret', reason: 'migration' })

    const [url, body, config] = requests.post.mock.calls[0]
    expect(url).toBe('/pki/backups/import')
    expect(config).toEqual({ timeout: 0 })
    expect(body).toBeInstanceOf(FormData)
    expect(body.get('passphrase')).toBe('single-request-secret')
    expect(url).not.toContain('single-request-secret')
  })
})

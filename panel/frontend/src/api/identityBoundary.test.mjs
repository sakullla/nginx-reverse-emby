// @vitest-environment jsdom

import { QueryClient } from '@tanstack/vue-query'
import { effectScope } from 'vue'
import { beforeEach, describe, expect, it } from 'vitest'
import { clearCredentials, setSessionToken } from './authState'
import { bindCredentialIdentityBoundary } from './identityBoundary'
import { recordPkiOperation, resetPkiOperationMemory, usePkiOperations } from '../hooks/usePkiOperations'
import { recordAcceptedOperation, resetOperations, useOperationsStore } from '../stores/operations'

describe('credential identity boundary', () => {
  beforeEach(() => {
    clearCredentials()
    localStorage.clear()
    resetOperations()
    resetPkiOperationMemory(localStorage)
  })

  it('removes administrator query data before a restricted identity can render', () => {
    const queryClient = new QueryClient()
    queryClient.setQueryData(['agents'], [{ id: 'hidden-agent' }])
    const unbind = bindCredentialIdentityBoundary(queryClient)

    setSessionToken('restricted-session')

    expect(queryClient.getQueryData(['agents'])).toBeUndefined()
    unbind()
  })

  it('removes persisted operation details without deleting identity-independent preferences', () => {
    setSessionToken('administrator-session')
    recordAcceptedOperation({
      operation_id: 'admin-operation',
      status_url: '/panel-api/operations/admin-operation',
      agent_id: 'hidden-agent',
      apply_status: 'failed',
      error_message: 'administrator-only error'
    })
    recordPkiOperation({
      id: 'admin-pki-operation',
      status_url: '/panel-api/pki/operations/admin-pki-operation',
      target_id: 'hidden-identity',
      state: 'running',
      last_error: 'administrator-only PKI error'
    }, localStorage)
    localStorage.setItem('nre.theme', 'dark')
    const queryClient = new QueryClient()
    const unbind = bindCredentialIdentityBoundary(queryClient)

    setSessionToken('restricted-session')

    expect(useOperationsStore().operations.value).toEqual([])
    expect(localStorage.getItem('nre.operations.v1')).toBeNull()
    expect(localStorage.getItem('nre.pki.operations.v1')).toBeNull()
    expect(localStorage.getItem('nre.theme')).toBe('dark')
    const scope = effectScope(true)
    let pkiOperations
    scope.run(() => {
      pkiOperations = usePkiOperations({ pollInterval: -1, refreshOnRestore: false, storage: localStorage }).operations
    })
    expect(pkiOperations.value).toEqual([])
    scope.stop()
    unbind()
  })
})

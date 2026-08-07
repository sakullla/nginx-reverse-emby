// @vitest-environment node

import { QueryClient } from '@tanstack/vue-query'
import { beforeEach, describe, expect, it } from 'vitest'
import { clearCredentials, setSessionToken } from './authState'
import { bindCredentialIdentityBoundary } from './identityBoundary'

describe('credential identity boundary', () => {
  beforeEach(() => {
    clearCredentials()
  })

  it('removes administrator query data before a restricted identity can render', () => {
    const queryClient = new QueryClient()
    queryClient.setQueryData(['agents'], [{ id: 'hidden-agent' }])
    const unbind = bindCredentialIdentityBoundary(queryClient)

    setSessionToken('restricted-session')

    expect(queryClient.getQueryData(['agents'])).toBeUndefined()
    unbind()
  })
})

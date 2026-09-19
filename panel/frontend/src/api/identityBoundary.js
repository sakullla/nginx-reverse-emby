import { onCredentialIdentityChange } from './authState'
import { resetPkiOperationMemory } from '../hooks/usePkiOperations'
import { resetOperations } from '../stores/operations'

export function bindCredentialIdentityBoundary(queryClient) {
  return onCredentialIdentityChange(() => {
    queryClient.clear()
    resetOperations()
    resetPkiOperationMemory()
  })
}

import { onCredentialIdentityChange } from './authState'

export function bindCredentialIdentityBoundary(queryClient) {
  return onCredentialIdentityChange(() => queryClient.clear())
}

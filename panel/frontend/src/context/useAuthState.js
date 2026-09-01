import { computed } from 'vue'
import {
  authToken,
  clearAuthToken,
  clearCredentials,
  credentialVersion,
  sessionToken,
  setAuthToken,
  setSessionToken
} from '../api/authState'

export function useAuthState() {
  return {
    token: authToken,
    sessionToken,
    hasToken: computed(() => !!authToken.value),
    credentialVersion,
    setToken: setAuthToken,
    setSessionToken,
    clearToken: clearAuthToken,
    clearCredentials
  }
}

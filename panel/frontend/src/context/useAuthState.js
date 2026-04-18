import { computed } from 'vue'
import { authToken, clearAuthToken, setAuthToken } from '../api/authState'

export function useAuthState() {
  return {
    token: authToken,
    hasToken: computed(() => !!authToken.value),
    setToken: setAuthToken,
    clearToken: clearAuthToken
  }
}

import { ref } from 'vue'
import { onAuthChange } from '../utils/authEvents'
import { setApiTokenRef } from '../api'

// Shared reactive auth state — single source of truth for auth token
const _token = ref(localStorage.getItem('panel_token'))

// Keep api's token ref in sync so 401 interceptor can update our reactive state
setApiTokenRef((v) => { _token.value = v })

// Sync 401 interceptor clears with our reactive state
onAuthChange((token) => { _token.value = token })

export function useAuthState() {
  return {
    token: _token,
    hasToken: _token,
    setToken(token) {
      _token.value = token
    },
    clearToken() {
      _token.value = null
    }
  }
}

// Plain module (no Vue imports) for cross-cutting auth state sync
// Both api/index.js and context/useAuthState.js import from here to avoid circular deps
const _callbacks = []
export function onAuthChange(cb) { _callbacks.push(cb) }
export function notifyAuthChange(token) { _callbacks.forEach(cb => cb(token)) }

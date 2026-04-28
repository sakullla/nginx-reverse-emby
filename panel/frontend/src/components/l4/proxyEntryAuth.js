export function buildProxyEntryAuthPayload(currentAuth, editedAuth) {
  const current = currentAuth || {}
  const edited = editedAuth || {}
  const currentEnabled = current.enabled === true
  const currentUsername = String(current.username || '').trim()
  const currentPassword = String(current.password || '')
  const editedEnabled = edited.enabled === true
  const editedUsername = String(edited.username || '').trim()
  const editedPassword = String(edited.password || '')

  if (!editedEnabled) {
    return { enabled: false, username: '', password: '' }
  }
  if (currentEnabled && currentPassword === '' && editedPassword === '') {
    if (editedUsername === currentUsername) {
      return undefined
    }
    throw new Error('Proxy entry password is redacted; re-enter the password before saving auth changes.')
  }
  return {
    enabled: true,
    username: editedUsername,
    password: editedPassword,
  }
}

const REDACTED_PROXY_PASSWORD = 'xxxxx'

function parseProxyURL(raw) {
  try {
    return new URL(raw)
  } catch {
    return null
  }
}

function hasRedactedProxyPassword(raw) {
  const parsed = parseProxyURL(raw)
  return parsed?.password === REDACTED_PROXY_PASSWORD
}

export function buildOutboundProxyPayload(currentURL, editedURL) {
  const current = String(currentURL || '').trim()
  const edited = String(editedURL || '').trim()

  if (current === edited) {
    return hasRedactedProxyPassword(current) ? {} : { outbound_proxy_url: edited }
  }
  if (hasRedactedProxyPassword(current) && hasRedactedProxyPassword(edited)) {
    throw new Error('Proxy password is redacted; re-enter the password before saving changes.')
  }
  return { outbound_proxy_url: edited }
}

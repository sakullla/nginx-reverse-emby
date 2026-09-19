export function parseFrontendURL(value) {
  const raw = String(value || '').trim()
  if (!raw) return { raw: '', href: '', https: true, host: '' }
  const explicitHTTP = /^http:\/\//i.test(raw)
  const explicitHTTPS = /^https:\/\//i.test(raw)
  const withScheme = /^[a-z][a-z0-9+.-]*:\/\//i.test(raw) ? raw : `https://${raw}`
  try {
    const url = new URL(withScheme)
    if (url.protocol !== 'http:' && url.protocol !== 'https:') {
      return { raw, href: '', https: !explicitHTTP, host: '' }
    }
    const path = `${url.pathname || ''}${url.search || ''}`
    const suffix = path === '/' ? '' : path
    const https = explicitHTTP ? false : explicitHTTPS ? true : url.protocol !== 'http:'
    return {
      raw,
      href: `${https ? 'https' : 'http'}://${url.host}${suffix}`,
      https,
      host: url.host
    }
  } catch {
    return { raw, href: '', https: !explicitHTTP, host: '' }
  }
}

export function buildFrontendURL(value, https) {
  const parsed = parseFrontendURL(value)
  if (!parsed.host) return ''
  const useHTTPS = https == null ? parsed.https : Boolean(https)
  const separator = parsed.href.indexOf('/', parsed.href.indexOf('://') + 3)
  const suffix = separator >= 0 ? parsed.href.slice(separator) : ''
  return `${useHTTPS ? 'https' : 'http'}://${parsed.host}${suffix}`
}

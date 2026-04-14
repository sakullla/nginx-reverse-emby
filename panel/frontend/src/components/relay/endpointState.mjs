function normalizePort(raw) {
  const text = String(raw ?? '').trim()
  if (!text) return null
  if (!/^\d+$/.test(text)) return null
  const port = Number(text)
  if (!Number.isInteger(port) || port < 1 || port > 65535) return null
  return port
}

export function parsePublicEndpoint(value) {
  const input = String(value ?? '').trim()
  if (!input) {
    return {
      publicHost: '',
      publicPort: null,
      isValid: true
    }
  }

  if (input.startsWith('[')) {
    const closing = input.indexOf(']')
    if (closing <= 1) {
      return {
        publicHost: '',
        publicPort: null,
        isValid: false
      }
    }

    const host = input.slice(1, closing).trim()
    const rest = input.slice(closing + 1).trim()
    if (!rest) {
      return {
        publicHost: host,
        publicPort: null,
        isValid: Boolean(host)
      }
    }
    if (!rest.startsWith(':')) {
      return {
        publicHost: '',
        publicPort: null,
        isValid: false
      }
    }

    const port = normalizePort(rest.slice(1))
    return {
      publicHost: host,
      publicPort: port,
      isValid: Boolean(host) && port != null
    }
  }

  const parts = input.split(':')
  if (parts.length === 1) {
    return {
      publicHost: parts[0].trim(),
      publicPort: null,
      isValid: Boolean(parts[0].trim())
    }
  }

  if (parts.length > 2) {
    return {
      publicHost: input,
      publicPort: null,
      isValid: true
    }
  }

  if (parts.length !== 2) {
    return {
      publicHost: '',
      publicPort: null,
      isValid: false
    }
  }

  const host = parts[0].trim()
  const port = normalizePort(parts[1])
  return {
    publicHost: host,
    publicPort: port,
    isValid: Boolean(host) && port != null
  }
}

export function buildPublicEndpoint(state = {}) {
  const host = String(state?.public_host ?? '').trim()
  const port = normalizePort(state?.public_port)
  if (!host) return ''
  if (port == null) return host
  if (host.includes(':')) return `[${host}]:${port}`
  return `${host}:${port}`
}

export function normalizeBindHosts(value) {
  const rows = Array.isArray(value)
    ? value
    : String(value ?? '').split(/\r?\n/)
  const deduped = []
  const seen = new Set()
  for (const row of rows) {
    const host = String(row ?? '').trim()
    if (!host || seen.has(host)) continue
    seen.add(host)
    deduped.push(host)
  }
  return deduped
}

export function buildBindHostsText(bindHosts) {
  return normalizeBindHosts(bindHosts).join('\n')
}

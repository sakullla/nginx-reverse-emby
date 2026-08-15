export function pluginProviderRef(backend) {
  if (backend?.kind !== 'plugin_provider') return null
  const instanceId = String(backend?.plugin_provider?.instance_id || '').trim()
  const providerId = String(backend?.plugin_provider?.provider_id || '').trim()
  if (!instanceId || !providerId) return null
  return { instanceId, providerId }
}

export function providerCatalogKey(value) {
  const instanceId = String(value?.instance_id ?? value?.instanceId ?? '').trim()
  const providerId = String(value?.provider_id ?? value?.providerId ?? '').trim()
  return `${instanceId}\u0000${providerId}`
}

export function describeHTTPBackend(backend, providerCatalog = []) {
  const provider = pluginProviderRef(backend)
  if (provider) {
    const catalogEntry = providerCatalog.find((item) => providerCatalogKey(item) === providerCatalogKey(provider))
    return {
      kind: 'provider',
      label: String(catalogEntry?.display_name || provider.providerId),
      detail: provider.instanceId,
      state: String(catalogEntry?.state || ''),
      generation: String(catalogEntry?.ready_generation || '')
    }
  }
  const url = String(backend?.url || '').trim()
  return url ? { kind: 'url', label: url, detail: '', state: '', generation: '' } : null
}

export function describeHTTPBackends(rule, providerCatalog = []) {
  if (!Array.isArray(rule?.backends)) return []
  return rule.backends.map((backend) => describeHTTPBackend(backend, providerCatalog)).filter(Boolean)
}

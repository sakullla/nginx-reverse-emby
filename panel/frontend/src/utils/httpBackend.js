export function pluginProviderRef(backend) {
  if (backend?.kind !== 'plugin_provider') return null
  const instanceId = String(backend?.plugin_provider?.instance_id || '').trim()
  const providerId = String(backend?.plugin_provider?.provider_id || '').trim()
  if (!instanceId || !providerId) return null
  return { instanceId, providerId }
}

export function providerCatalogKey(value, fallbackAgentId = '') {
  const agentId = String(value?.agent_id ?? value?.agentId ?? fallbackAgentId).trim()
  const instanceId = String(value?.instance_id ?? value?.instanceId ?? '').trim()
  const providerId = String(value?.provider_id ?? value?.providerId ?? '').trim()
  return `${agentId}\u0000${instanceId}\u0000${providerId}`
}

export function describeHTTPBackend(backend, providerCatalog = [], options = {}) {
  const provider = pluginProviderRef(backend)
  if (provider) {
    const agentId = String(options.agentId || '').trim()
    const catalogStatus = options.catalogStatus == null || options.catalogStatus === 'ready' ? 'ready' : 'unknown'
    const catalogEntry = providerCatalog.find((item) => (
      providerCatalogKey(item, agentId) === providerCatalogKey(provider, agentId)
    ))
    return {
      kind: 'provider',
      label: String(catalogEntry?.display_name || provider.providerId),
      detail: provider.instanceId,
      state: catalogStatus === 'ready' ? String(catalogEntry?.state || 'unavailable') : 'unknown',
      generation: String(catalogEntry?.ready_generation || '')
    }
  }
  const url = String(backend?.url || '').trim()
  return url ? { kind: 'url', label: url, detail: '', state: '', generation: '' } : null
}

export function describeHTTPBackends(rule, providerCatalog = [], catalogStatus = 'ready') {
  if (!Array.isArray(rule?.backends)) return []
  const options = { agentId: rule?.agent_id, catalogStatus }
  return rule.backends.map((backend) => describeHTTPBackend(backend, providerCatalog, options)).filter(Boolean)
}

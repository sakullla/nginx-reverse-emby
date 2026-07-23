/**
 * Normalize option-source payloads that may be either:
 * - a flat resource list (single-agent fetch), or
 * - an all-agents grouping array: [{ agentId, <itemsKey>: [...] }, ...]
 *
 * Returns a flat array of resources suitable for collecting tags / select options.
 */
export function flattenAgentGroupedItems(payload, itemsKey) {
  if (!Array.isArray(payload) || !payload.length) return []
  const key = String(itemsKey || '')
  if (!key) return payload

  // All-agents helpers return groups shaped like { agentId, rules|l4Rules|... }.
  const looksGrouped = payload.some(
    (entry) => entry
      && typeof entry === 'object'
      && Object.prototype.hasOwnProperty.call(entry, key)
      && Array.isArray(entry[key])
  )
  if (!looksGrouped) return payload

  const flat = []
  for (const group of payload) {
    const items = group?.[key]
    if (!Array.isArray(items)) continue
    for (const item of items) flat.push(item)
  }
  return flat
}

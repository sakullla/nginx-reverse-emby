/** Sentinel meaning "all agents" for list-page filters and selection memory. */
export const ALL_AGENTS_FILTER = '__all__'

export function isAllAgentsFilter(value) {
  return value === ALL_AGENTS_FILTER
}

/**
 * Normalize a stored/selected agent filter.
 * - blank/null/undefined → null
 * - `__all__` (or historical aliases) → ALL_AGENTS_FILTER
 * - otherwise trimmed string id
 */
export function normalizeAgentFilter(value) {
  if (value == null) return null
  const trimmed = String(value).trim()
  if (!trimmed) return null
  if (trimmed === ALL_AGENTS_FILTER || trimmed === 'all' || trimmed === '*') {
    return ALL_AGENTS_FILTER
  }
  return trimmed
}

// Shared metric formatting helpers for agent monitoring UI.
// Used by AgentMonitorCard and AgentDetailPage to keep percentages, bytes,
// rates and CPU/memory/disk display consistent.

import { formatBytes as formatBytesBase } from './trafficStats.js'

export { formatBytesBase as formatBytes }

export function percent(value) {
  if (value === null || value === undefined || value === '') return '—'
  return Number.isFinite(Number(value)) ? `${Number(value).toFixed(1)}%` : '—'
}

export function clamp(value) {
  if (value === null || value === undefined || value === '') return 0
  const n = Number(value)
  if (!Number.isFinite(n)) return 0
  return Math.min(100, Math.max(0, n))
}

/**
 * Map a percentage-like value to a semantic tone.
 * Thresholds mirror AgentMonitorCard's original behavior:
 *   > 85% => danger
 *   >= 70% => warning
 *   otherwise => success
 * Missing or non-finite values => neutral.
 */
export function barTone(value) {
  if (value === null || value === undefined || value === '') return 'neutral'
  const n = Number(value)
  if (!Number.isFinite(n)) return 'neutral'
  if (n > 85) return 'danger'
  if (n >= 70) return 'warning'
  return 'success'
}

export function rate(value) {
  if (value === null || value === undefined || value === '') return '—'
  const n = Number(value)
  if (!Number.isFinite(n)) return '—'
  return `${formatBytesBase(n)}/s`
}

export function cpuUsage(source = {}) {
  const used = metricNumber(source.cpu_used_cores)
  const total = metricNumber(source.cpu_total_cores)
  if (Number.isFinite(used) && Number.isFinite(total) && total > 0) {
    return `${used.toFixed(1)} / ${total.toFixed(0)} 核`
  }
  if (Number.isFinite(used)) return `${used.toFixed(1)} 核`
  return percent(source.cpu_usage_percent)
}

function metricNumber(value) {
  if (value === null || value === undefined || value === '') return NaN
  return Number(value)
}

export function bytesPair(usedValue, totalValue) {
  if (usedValue === null || usedValue === undefined || usedValue === '') return '—'
  const used = Number(usedValue)
  const total = Number(totalValue)
  if (Number.isFinite(used) && Number.isFinite(total) && total > 0) {
    return `${formatBytesBase(used)} / ${formatBytesBase(total)}`
  }
  if (Number.isFinite(used)) return formatBytesBase(used)
  return '—'
}

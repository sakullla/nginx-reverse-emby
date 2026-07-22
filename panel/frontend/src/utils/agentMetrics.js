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

export function cpuUsage(source = {}, opts = {}) {
  const used = metricNumber(source.cpu_used_cores)
  const total = metricNumber(source.cpu_total_cores)
  if (Number.isFinite(used) && Number.isFinite(total) && total > 0) {
    return opts.compact
      ? `${used.toFixed(1)}/${total.toFixed(0)}核`
      : `${used.toFixed(1)} / ${total.toFixed(0)} 核`
  }
  if (Number.isFinite(used)) {
    return opts.compact ? `${used.toFixed(1)}核` : `${used.toFixed(1)} 核`
  }
  return percent(source.cpu_usage_percent)
}

function metricNumber(value) {
  if (value === null || value === undefined || value === '') return NaN
  return Number(value)
}

/**
 * Format used/total bytes.
 * When opts.compact is true and both sides share a unit, collapse to
 * "4.00 / 16.0 GiB" so list tiles avoid mid-unit line breaks.
 */
export function bytesPair(usedValue, totalValue, opts = {}) {
  if (usedValue === null || usedValue === undefined || usedValue === '') return '—'
  const used = Number(usedValue)
  const total = Number(totalValue)
  if (Number.isFinite(used) && Number.isFinite(total) && total > 0) {
    if (opts.compact) {
      const usedParts = splitBytes(used)
      const totalParts = splitBytes(total)
      if (usedParts.unit === totalParts.unit) {
        return `${usedParts.amount}/${totalParts.amount} ${usedParts.unit}`
      }
      return `${formatBytesBase(used)}/${formatBytesBase(total)}`
    }
    return `${formatBytesBase(used)} / ${formatBytesBase(total)}`
  }
  if (Number.isFinite(used)) return formatBytesBase(used)
  return '—'
}

function splitBytes(value) {
  const formatted = formatBytesBase(value)
  const match = /^(.+)\s+(\S+)$/.exec(formatted)
  if (!match) return { amount: formatted, unit: '' }
  return { amount: match[1], unit: match[2] }
}

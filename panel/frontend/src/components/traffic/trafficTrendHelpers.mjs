export function dateInputToRFC3339(value, endOfDay = false) {
  if (!value) return ''
  const text = String(value).trim()
  if (!text) return ''
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(text)
  if (!match) {
    const date = new Date(text)
    return Number.isNaN(date.getTime()) ? '' : date.toISOString()
  }
  const [, year, month, day] = match
  const hours = endOfDay ? 23 : 0
  const minutes = endOfDay ? 59 : 0
  const seconds = endOfDay ? 59 : 0
  const milliseconds = endOfDay ? 999 : 0
  return new Date(Date.UTC(Number(year), Number(month) - 1, Number(day), hours, minutes, seconds, milliseconds)).toISOString()
}

export function previousPeriodRange(from, to) {
  const start = parseDateInputStart(from)
  const end = parseDateInputEnd(to)
  if (!start || !end || end < start) return { from: '', to: '' }
  const durationMs = end.getTime() - start.getTime() + 1
  const prevTo = new Date(start.getTime() - 1)
  const prevFrom = new Date(prevTo.getTime() - durationMs + 1)
  return {
    from: prevFrom.toISOString(),
    to: prevTo.toISOString()
  }
}

export function alignSeriesByPosition(currentPoints, comparisonPoints) {
  if (!Array.isArray(currentPoints)) return []
  if (!Array.isArray(comparisonPoints) || comparisonPoints.length === 0) {
    return currentPoints.map(() => null)
  }
  return currentPoints.map((_, index) => {
    const point = comparisonPoints[index]
    return point ? Number(point.accounted_bytes) || 0 : null
  })
}

export function hostTotalForLast24h(points, now = new Date()) {
  if (!Array.isArray(points)) return 0
  const end = now instanceof Date ? now.getTime() : new Date(now).getTime()
  if (!Number.isFinite(end)) return 0
  const start = end - 24 * 60 * 60 * 1000
  return points.reduce((sum, p) => {
    const bucketTime = new Date(p?.bucket_start || '').getTime()
    if (!Number.isFinite(bucketTime) || bucketTime < start || bucketTime > end) return sum
    return sum + (Number(p.accounted_bytes) || 0)
  }, 0)
}

function parseDateInputStart(value) {
  const iso = dateInputToRFC3339(value)
  if (!iso) return null
  const date = new Date(iso)
  return Number.isNaN(date.getTime()) ? null : date
}

function parseDateInputEnd(value) {
  const iso = dateInputToRFC3339(value, true)
  if (!iso) return null
  const date = new Date(iso)
  return Number.isNaN(date.getTime()) ? null : date
}

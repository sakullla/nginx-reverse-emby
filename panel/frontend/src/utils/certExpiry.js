const DAY_MS = 24 * 60 * 60 * 1000
const HOUR_MS = 60 * 60 * 1000

function pluralDays(days) {
  return `${days} 天`
}

/**
 * Derive expiry presentation for a certificate `not_after` timestamp.
 * Returns null when the timestamp is missing/unparseable.
 *
 * @param {string|number|Date|null|undefined} notAfter
 * @param {number} [nowMs] injectable clock for tests
 * @returns {{ expired: boolean, daysLeft: number, remainingLabel: string, tone: string, dateLabel: string } | null}
 */
export function certExpiryInfo(notAfter, nowMs = Date.now()) {
  if (!notAfter) return null
  const ts = notAfter instanceof Date ? notAfter.getTime() : Date.parse(notAfter)
  if (Number.isNaN(ts)) return null

  let dateLabel = ''
  try {
    dateLabel = new Date(ts).toLocaleString('zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
    })
  } catch {
    dateLabel = String(notAfter)
  }

  const diffMs = ts - nowMs
  if (diffMs <= 0) {
    const daysAgo = Math.floor(-diffMs / DAY_MS)
    return {
      expired: true,
      daysLeft: 0,
      remainingLabel: daysAgo > 0 ? `已过期 ${pluralDays(daysAgo)}` : '已过期',
      tone: 'danger',
      dateLabel,
    }
  }

  const daysLeft = Math.floor(diffMs / DAY_MS)
  let remainingLabel
  if (daysLeft >= 1) {
    remainingLabel = `剩余 ${pluralDays(daysLeft)}`
  } else {
    const hoursLeft = Math.floor(diffMs / HOUR_MS)
    remainingLabel = hoursLeft >= 1 ? `剩余 ${hoursLeft} 小时` : '即将到期'
  }

  let tone = 'success'
  if (daysLeft <= 7) tone = 'danger'
  else if (daysLeft <= 30) tone = 'warning'

  return { expired: false, daysLeft, remainingLabel, tone, dateLabel }
}

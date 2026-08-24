import { ref } from 'vue'

const DEFAULT_TIMEZONE = 'UTC'
const EMPTY = '—'

export const panelTimeZone = ref(DEFAULT_TIMEZONE)

export function setPanelTimeZone(value) {
  const next = String(value || '').trim()
  panelTimeZone.value = next || DEFAULT_TIMEZONE
}

export function getPanelTimeZone() {
  return panelTimeZone.value || DEFAULT_TIMEZONE
}

export function formatPanelDateTime(value, empty = EMPTY) {
  if (value == null || value === '') return empty
  const date = value instanceof Date ? value : new Date(value)
  if (Number.isNaN(date.getTime())) return String(value)
  const formatted = formatInTimeZone(date, getPanelTimeZone())
  if (formatted) return formatted
  return formatInTimeZone(date, DEFAULT_TIMEZONE) || String(value)
}

function formatInTimeZone(date, timeZone) {
  try {
    const parts = new Intl.DateTimeFormat('en-GB', {
      timeZone,
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hour12: false,
      hourCycle: 'h23'
    }).formatToParts(date)
    const pick = (type) => parts.find((part) => part.type === type)?.value || ''
    const year = pick('year')
    const month = pick('month')
    const day = pick('day')
    const hour = pick('hour')
    const minute = pick('minute')
    const second = pick('second')
    if (!year || !month || !day || !hour || !minute || !second) return ''
    return `${year}/${month}/${day} ${hour}:${minute}:${second}`
  } catch {
    return ''
  }
}

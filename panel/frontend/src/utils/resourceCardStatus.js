/** Shared status tone/label maps for resource list cards. */

export const SYNC_STATUS_TONE = {
  active: 'success',
  pending: 'warning',
  failed: 'danger',
  disabled: 'neutral',
}

export const SYNC_STATUS_LABEL = {
  active: '生效中',
  pending: '待同步',
  failed: '同步失败',
  disabled: '已禁用',
}

export function syncStatusTone(status) {
  return SYNC_STATUS_TONE[status] || 'neutral'
}

export function syncStatusLabel(status) {
  return SYNC_STATUS_LABEL[status] || '未知'
}

/** Simple enabled/disabled resources such as Relay listeners. */
export function enabledStatusTone(enabled) {
  return enabled ? 'success' : 'neutral'
}

export function enabledStatusLabel(enabled) {
  return enabled ? '生效中' : '已禁用'
}

/**
 * Cert lifecycle → BaseListCard status (four-tone only).
 * Badge copy stays resource-specific; issuing maps to warning, not primary.
 */
export function certCardStatusTone(cert) {
  if (!cert?.enabled) return 'neutral'
  if (cert.status === 'issuing') return 'warning'
  if (cert.status === 'active') return 'success'
  if (cert.status === 'pending') return 'warning'
  if (cert.status === 'error') return 'danger'
  return 'neutral'
}

export function certCardStatusLabel(cert) {
  if (!cert?.enabled) return '已禁用'
  if (cert.status === 'active') return '生效中'
  if (cert.status === 'pending') return '待签发'
  if (cert.status === 'issuing') return '签发中'
  if (cert.status === 'error') return '签发失败'
  return '未知'
}

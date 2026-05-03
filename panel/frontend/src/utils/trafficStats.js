function normalizeBytes(value) {
  const number = Number(value)
  return Number.isFinite(number) ? Math.max(0, number) : 0
}

function normalizePositiveInteger(value, fallback) {
  const number = Number(value)
  return Number.isInteger(number) && number > 0 ? number : fallback
}

function normalizeNullableBytes(value) {
  if (value == null || value === '') return null
  const number = Number(value)
  return Number.isFinite(number) && number >= 0 ? number : null
}

function normalizeNullablePositiveInteger(value) {
  if (value == null || value === '') return null
  const number = Number(value)
  return Number.isInteger(number) && number > 0 ? number : null
}

export function normalizeTrafficBucket(value) {
  return {
    rx_bytes: normalizeBytes(value?.rx_bytes),
    tx_bytes: normalizeBytes(value?.tx_bytes)
  }
}

export function normalizeTrafficSummaryBucket(value) {
  const bucket = normalizeTrafficBucket(value)
  return {
    ...bucket,
    accounted_bytes: Number.isFinite(Number(value?.accounted_bytes))
      ? normalizeBytes(value.accounted_bytes)
      : bucket.rx_bytes + bucket.tx_bytes
  }
}

export function accountedBytes(bytes, direction = 'both') {
  const bucket = normalizeTrafficBucket(bytes)
  switch (String(direction || 'both').toLowerCase()) {
    case 'rx':
      return bucket.rx_bytes
    case 'tx':
      return bucket.tx_bytes
    case 'max':
      return Math.max(bucket.rx_bytes, bucket.tx_bytes)
    case 'both':
    default:
      return bucket.rx_bytes + bucket.tx_bytes
  }
}

export function normalizeTrafficPolicy(policy = {}) {
  const direction = ['rx', 'tx', 'both', 'max'].includes(String(policy.direction || '').toLowerCase())
    ? String(policy.direction).toLowerCase()
    : 'both'
  const cycleStartDay = normalizePositiveInteger(policy.cycle_start_day, 1)
  return {
    direction,
    cycle_start_day: cycleStartDay <= 28 ? cycleStartDay : 1,
    monthly_quota_bytes: normalizeNullableBytes(policy.monthly_quota_bytes),
    block_when_exceeded: policy.block_when_exceeded === true,
    hourly_retention_days: normalizePositiveInteger(policy.hourly_retention_days, 180),
    daily_retention_months: normalizePositiveInteger(policy.daily_retention_months, 24),
    monthly_retention_months: normalizeNullablePositiveInteger(policy.monthly_retention_months)
  }
}

export function bucketForObject(stats, mapName, id) {
  const key = String(id)
  return normalizeTrafficBucket(stats?.traffic?.[mapName]?.[key])
}

export function summaryBucketForObject(summary, mapName, id) {
  const key = String(id)
  const buckets = Array.isArray(summary?.[mapName]) ? summary[mapName] : []
  return normalizeTrafficSummaryBucket(buckets.find((bucket) => String(bucket?.scope_id) === key))
}

export function formatBytes(value) {
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let size = normalizeBytes(value)
  let unitIndex = 0
  while (size >= 1024 && unitIndex < units.length - 1) {
    size /= 1024
    unitIndex += 1
  }
  if (unitIndex === 0) return `${Math.round(size)} ${units[unitIndex]}`
  return `${size.toFixed(size >= 10 ? 1 : 2)} ${units[unitIndex]}`
}

export function formatQuota(value, unlimitedLabel = 'Unlimited') {
  const bytes = normalizeNullableBytes(value)
  return bytes == null ? unlimitedLabel : formatBytes(bytes)
}

export function normalizeTrafficTrendPoint(point = {}, direction = 'both') {
  const bucket = normalizeTrafficBucket(point)
  return {
    bucket_start: String(point.bucket_start || ''),
    rx_bytes: bucket.rx_bytes,
    tx_bytes: bucket.tx_bytes,
    accounted_bytes: Number.isFinite(Number(point.accounted_bytes))
      ? normalizeBytes(point.accounted_bytes)
      : accountedBytes(bucket, direction)
  }
}

export function normalizeTrafficTrendPoints(points = [], direction = 'both') {
  return Array.isArray(points)
    ? points.map((point) => normalizeTrafficTrendPoint(point, direction))
    : []
}

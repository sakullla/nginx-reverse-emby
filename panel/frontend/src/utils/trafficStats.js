function normalizeBytes(value) {
  const number = Number(value)
  return Number.isFinite(number) ? Math.max(0, number) : 0
}

export function normalizeTrafficBucket(value) {
  return {
    rx_bytes: normalizeBytes(value?.rx_bytes),
    tx_bytes: normalizeBytes(value?.tx_bytes)
  }
}

export function bucketForObject(stats, mapName, id) {
  const key = String(id)
  return normalizeTrafficBucket(stats?.traffic?.[mapName]?.[key])
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

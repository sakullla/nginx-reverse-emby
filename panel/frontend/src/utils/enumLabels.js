// 共享枚举 → { label, tone } 映射，供表格组件统一渲染徽章。
// 未知枚举值统一兜底：原值大写 + neutral，调用方无需额外判断。

const STATUS_MAP = {
  active: { label: '生效中', tone: 'success' },
  pending: { label: '待同步', tone: 'warning' },
  failed: { label: '同步失败', tone: 'danger' },
  disabled: { label: '已禁用', tone: 'neutral' },
}

const PROTOCOL_MAP = {
  tcp: { label: 'TCP', tone: 'primary' },
  udp: { label: 'UDP', tone: 'warning' },
}

const TRANSPORT_MAP = {
  quic: { label: 'QUIC', tone: 'primary' },
  tls: { label: 'TLS/TCP', tone: 'success' },
  wireguard: { label: 'WireGuard', tone: 'warning' },
  tcp: { label: 'TCP', tone: 'neutral' },
}

const LB_MAP = {
  adaptive: { label: 'ADP', tone: 'primary' },
  round_robin: { label: 'RR', tone: 'primary' },
  random: { label: 'RND', tone: 'primary' },
}

const UNKNOWN = { label: '', tone: 'neutral' }

function lookup(map, key) {
  if (!key) return UNKNOWN
  return map[key] || { label: String(key).toUpperCase(), tone: 'neutral' }
}

export function getStatusBadge(status) {
  return lookup(STATUS_MAP, status)
}

export function getProtocolBadge(protocol) {
  const normalized = String(protocol || '').toLowerCase()
  return lookup(PROTOCOL_MAP, normalized)
}

export function getTransportBadge(mode) {
  const normalized = String(mode || '').toLowerCase()
  return lookup(TRANSPORT_MAP, normalized)
}

export function getLBLabel(strategy) {
  return lookup(LB_MAP, strategy) || { label: 'ADP', tone: 'primary' }
}

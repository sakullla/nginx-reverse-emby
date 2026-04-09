export function getDefaultTuning(protocol = 'tcp') {
  const isUdp = protocol === 'udp'
  return {
    listen: {
      reuseport: isUdp,
      backlog: null,
      so_keepalive: false,
      tcp_nodelay: true,
    },
    proxy: {
      connect_timeout: '10s',
      idle_timeout: isUdp ? '20s' : '10m',
      buffer_size: '16k',
      udp_proxy_requests: null,
      udp_proxy_responses: null,
    },
    upstream: {
      max_conns: 0,
      max_fails: 3,
      fail_timeout: '30s',
    },
    limit_conn: {
      key: '$binary_remote_addr',
      count: null,
      zone_size: '10m',
    },
    proxy_protocol: {
      decode: false,
      send: false,
    },
  }
}

export function mergeTuning(saved, protocol) {
  const defaults = getDefaultTuning(protocol)
  if (!saved || typeof saved !== 'object') return defaults
  return {
    listen: { ...defaults.listen, ...(saved.listen || {}) },
    proxy: { ...defaults.proxy, ...(saved.proxy || {}) },
    upstream: { ...defaults.upstream, ...(saved.upstream || {}) },
    limit_conn: { ...defaults.limit_conn, ...(saved.limit_conn || {}) },
    proxy_protocol: { ...defaults.proxy_protocol, ...(saved.proxy_protocol || {}) },
  }
}

export function resetTuningForProtocol(_currentTuning, protocol) {
  return mergeTuning(null, protocol)
}

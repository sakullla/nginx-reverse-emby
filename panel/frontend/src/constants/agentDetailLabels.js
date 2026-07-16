export const agentDetailLabels = {
  // Navigation
  backToAgents: '返回节点管理',
  notFoundTitle: '节点不存在或已删除',

  // Status bar metadata
  meta: {
    address: '地址',
    lastSeen: '最后活跃',
    version: '版本',
    platform: '平台',
    arch: '架构',
  },

  // Metric labels
  metrics: {
    cpu: 'CPU',
    memory: '内存',
    disk: '磁盘',
    network: '网络',
    httpRules: 'HTTP 规则',
    l4Rules: 'L4 规则',
    certificates: '证书',
    relayListeners: 'Relay 监听',
    syncStatus: '同步状态',
  },

  // Secondary band under primary health/sync summary
  secondaryMetrics: '资源与业务',

  // Section titles
  sections: {
    rules: '规则列表',
    certificates: '证书列表',
    relayListeners: '监听列表',
    traffic: '流量统计',
    trafficHealth: '健康概览',
    trafficAnalysisModal: '总流量分析',
    trafficManagementModal: '剩余/额度管理',
    systemInfo: '系统信息',
    syncEvents: '同步事件',
  },

  // Operations
  actions: {
    deleteAgent: '删除节点',
    collapseSummary: '折叠节点信息',
    expandSummary: '展开节点信息',
  },

  // Empty states
  empty: {
    rules: '该节点暂无规则',
    certificates: '该节点暂无证书',
    relayListeners: '该节点暂无监听',
  },

  // Sync event block
  sync: {
    failedTitle: '同步失败',
    failedHint: '最近一次配置同步未成功，错误信息如下：',
    status: '同步状态',
    message: '同步消息',
    time: '同步时间',
  },

  // System info card titles
  systemCards: {
    package: '运行包',
    identity: '节点身份',
    sync: '同步状态',
  },

  // Misc
  ruleType: {
    http: 'HTTP',
    l4: 'L4',
  },
  ruleEnabled: '启用',
  ruleDisabled: '禁用',
  ruleBackendLabel: '后端',
  certStatus: {
    active: '生效中',
    pending: '待签发',
    issuing: '签发中',
    error: '签发失败',
    disabled: '已禁用',
    unknown: '未知',
  },
  // Inspection meta labels (not expiry — last_issue_at is issue time)
  certIssuedAt: '签发',
  listenerTransport: {
    quic: 'QUIC',
    wireguard: 'WireGuard',
    tls_tcp: 'TLS/TCP',
  },

  // DDNS domain configuration & display (R1/R4). Backend status enum is
  // ok | error | disabled | idle (storage.DdnsStatus). Field names mirror the
  // backend AgentSummary / monitor payload snake_case: last_seen_ipv4,
  // last_seen_ipv6, ddns_domain, ddns_status. No credential lives here (R7).
  ddns: {
    configAction: 'DDNS 域名',
    configButtonTitle: '配置 DDNS 域名',
    configModalTitle: '配置 DDNS 域名',
    configModalSubtitle: '为 NAT 节点解析动态域名；Cloudflare 凭证由主控统一保管，不在此填写',
    summaryLabel: 'DDNS',
    summaryUnconfigured: '未配置',
    saveSuccess: 'DDNS 配置已保存',
    domainRequired: '启用 IPv4 或 IPv6 时需填写域名',
    metaDomain: '域名',
    metaIpv4: 'IPv4',
    metaIpv6: 'IPv6',
    metaStatus: '解析状态',
    statusLabel: {
      ok: '已解析',
      error: '解析失败',
      disabled: '未启用',
      idle: '待解析',
    },
  },
}

const DDNS_STATUS_TONE = {
  ok: 'success',
  error: 'danger',
  disabled: 'neutral',
  idle: 'warning',
}

// ddnsStatusBadge maps the backend DdnsStatus.Status enum to a BaseBadge
// { label, tone }. Unknown / empty status yields a neutral placeholder so the
// detail page can always render a status row.
export function ddnsStatusBadge(status) {
  const key = String(status || '').trim()
  if (!key) return { label: '—', tone: 'neutral' }
  return {
    label: agentDetailLabels.ddns.statusLabel[key] || key,
    tone: DDNS_STATUS_TONE[key] || 'neutral',
  }
}

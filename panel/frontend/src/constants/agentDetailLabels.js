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
}

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

  // Section titles
  sections: {
    rules: '规则列表',
    certificates: '证书列表',
    relayListeners: '监听列表',
    traffic: '流量统计',
    systemInfo: '系统信息',
    syncEvents: '同步事件',
  },

  // Operations
  actions: {
    applyConfig: '推送配置',
    copyJoinCommand: '复制注册命令',
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
}

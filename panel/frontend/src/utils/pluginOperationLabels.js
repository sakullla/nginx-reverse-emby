export function pluginOperationKindLabel(kind) {
  return ({ install: '安装', upgrade: '升级', configure: '配置更新', enable: '启用', disable: '停用', rollback: '回滚', uninstall: '卸载', publish: '发布', unpublish: '取消发布' })[kind] || kind || '插件操作'
}

export function pluginOperationStatusLabel(status) {
  return ({ pending: '等待执行', running: '执行中', succeeded: '已完成', failed: '失败', cancelled: '已取消', superseded: '已被新操作替代' })[status] || status || '未知状态'
}

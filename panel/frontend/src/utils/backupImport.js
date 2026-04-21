export const BACKUP_DOWNLOAD_REVOKE_DELAY_MS = 30000
export const BACKUP_SENSITIVE_WARNING =
  '备份包未加密，包含节点 token、证书私钥等敏感材料，请仅在受信任环境中保存与传输。'
export const BACKUP_IMPORT_CONFIRMATION_MESSAGE =
  '导入备份会修改当前系统中的节点、规则和证书配置。确认继续导入备份吗？'

export function getBackupSummaryTotals(summary) {
  return {
    imported: Number(summary?.imported || 0),
    skipped_conflict: Number(summary?.skipped_conflict || 0),
    skipped_invalid: Number(summary?.skipped_invalid || 0),
    skipped_missing_material: Number(summary?.skipped_missing_material || 0)
  }
}

export function formatBackupReportItem(item) {
  const resourceLabels = {
    agent: '节点',
    http_rule: 'HTTP 规则',
    l4_rule: 'L4 规则',
    certificate: '证书',
    relay_listener: 'Relay 监听器',
    version_policy: '版本策略',
    system: '系统'
  }
  const resource = resourceLabels[item?.resource] || '项目'
  const identifier = item?.identifier ? ` ${item.identifier}` : ''
  const detail = item?.detail ? `: ${item.detail}` : ''
  return `${resource}${identifier}${detail}`
}

export function getBackupDownloadRevokeDelayMs() {
  return BACKUP_DOWNLOAD_REVOKE_DELAY_MS
}

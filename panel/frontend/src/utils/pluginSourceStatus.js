// 仓库源状态视图：页面与市场弹窗共用，避免原始 API 结果枚举直接进入界面。
// 未知结果值不原样透出，统一回落到中文兜底文案。

const REFRESH_RESULT_VIEWS = {
  succeeded: { label: '刷新成功', tone: 'current' },
  success: { label: '刷新成功', tone: 'current' },
  failed: { label: '刷新失败', tone: 'error' },
  failure: { label: '刷新失败', tone: 'error' },
  error: { label: '刷新失败', tone: 'error' },
  running: { label: '刷新中', tone: 'pending' },
  pending: { label: '等待刷新', tone: 'pending' },
  cancelled: { label: '已取消', tone: 'pending' },
  canceled: { label: '已取消', tone: 'pending' },
  skipped: { label: '已跳过', tone: 'pending' }
}

export function refreshResultView(value) {
  const key = String(value || '').trim().toLowerCase()
  return REFRESH_RESULT_VIEWS[key] || null
}

export function repositorySourceStatus(source, options = {}) {
  if (options.refreshing) return { label: '刷新中', tone: 'pending' }
  if (source?.last_error) return { label: '刷新失败', tone: 'error' }
  const result = refreshResultView(source?.last_result)
  if (source?.current_resolved_oid) {
    if (!result || result.label === '刷新成功') return { label: '当前可用', tone: 'current' }
    return { label: `当前 · ${result.label}`, tone: result.tone }
  }
  return result ? { ...result } : { label: '等待首次刷新', tone: 'pending' }
}

export function repositoryStatusBadgeTone(status) {
  if (status?.tone === 'current') return 'success'
  if (status?.tone === 'error') return 'danger'
  if (status?.tone === 'pending') return 'warning'
  return 'neutral'
}

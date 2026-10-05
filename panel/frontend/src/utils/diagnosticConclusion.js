const KIND_LABELS = {
  http: 'HTTP 反代',
  l4_tcp: 'L4 转发'
}

/**
 * 基于既有诊断任务数据生成中文结论与下一步建议。
 * 输入: { kind, state, error, result }
 * 输出: { tone, headline, detail, suggestions[] }
 * 不新增探测行为，仅消费 task.result.summary / relay_paths / backends / samples。
 */
export function buildDiagnosticConclusion(input = {}) {
  const taskError = String(input.error || '').trim()
  if (taskError) return taskErrorConclusion(taskError)

  const result = input.result || null
  const summary = result?.summary || null
  const sent = toCount(summary?.sent)

  if (!summary || sent <= 0) return insufficientDataConclusion()

  const failed = resolveFailedCount(summary, sent)
  const failedRelayPaths = collectFailedRelayPaths(result)
  const failedBackends = collectFailedBackends(result)

  if (failed === 0 && input.state !== 'failed') {
    return successConclusion(input.kind, summary, failedRelayPaths)
  }
  if (failedRelayPaths.length > 0) return relayFailureConclusion(failedRelayPaths)
  if (failedBackends.length > 0) return backendFailureConclusion(failedBackends, result)
  return unattributedConclusion(input.state)
}

function successConclusion(kind, summary, failedRelayPaths = []) {
  const sent = toCount(summary?.sent)
  const subject = KIND_LABELS[kind] ? `${KIND_LABELS[kind]}链路` : '链路'
  const latency = Number(summary?.avg_latency_ms)
  let detail = Number.isFinite(latency) && latency > 0
    ? `平均延迟 ${latency} ms，未发现失败样本。`
    : '未发现失败样本。'
  if (failedRelayPaths.length > 0) {
    detail += `另有 ${failedRelayPaths.length} 条 Relay 路径探测失败，当前使用的路径正常。`
  }
  return {
    tone: 'success',
    headline: `${subject}正常：${sent} 次探测全部成功`,
    detail,
    suggestions: []
  }
}

function relayFailureConclusion(failedPaths) {
  return {
    tone: 'danger',
    headline: `Relay 路径失败：${failedPaths.length} 条路径不可用`,
    detail: relayFailureDetail(failedPaths),
    suggestions: [
      '检查 Relay 监听器是否在线，以及各跳节点之间的网络连通性',
      '在 Relay 页面确认涉及的监听端口配置正确，然后重新运行诊断'
    ]
  }
}

function relayFailureDetail(paths) {
  const labels = paths.slice(0, 3).map((path, index) => {
    const ids = Array.isArray(path?.path) ? path.path : []
    const label = ids.length
      ? ids.map(id => `中继器 #${id}`).join(' → ')
      : `路径 ${index + 1}`
    const error = String(path?.error || '').trim()
    return error ? `${label}（${error}）` : label
  })
  const extra = paths.length > 3 ? `，另有 ${paths.length - 3} 条` : ''
  return `失败路径：${labels.join('；')}${extra}。`
}

function backendFailureConclusion(backends, result) {
  return {
    tone: 'danger',
    headline: `后端探测失败：${backends.length} 个后端不可用`,
    detail: backendFailureDetail(backends, result),
    suggestions: [
      '检查失败后端的地址、端口与协议是否正确',
      '确认后端服务已启动，且能被节点直接访问'
    ]
  }
}

function backendFailureDetail(backends, result) {
  const labels = backends.slice(0, 3).map(backendLabel)
  const extra = backends.length > 3 ? `，共 ${backends.length} 个` : ''
  const parts = [`失败后端：${labels.join('、')}${extra}。`]
  const errors = failedSampleErrors(result)
  if (errors.length) parts.push(`错误示例：${errors.join('；')}。`)
  return parts.join('')
}

function backendLabel(backend) {
  const name = String(backend?.backend || '').trim()
  const address = String(backend?.address || '').trim()
  if (!name) return address || '未知后端'
  if (!address || name.includes(address)) return name
  return `${name}（${address}）`
}

function insufficientDataConclusion() {
  return {
    tone: 'warning',
    headline: '无法定位：诊断没有产生有效数据',
    detail: '本次诊断没有有效探测样本，数据不足，无法判断失败环节。',
    suggestions: [
      '确认目标节点在线后重新运行诊断',
      '检查规则后端地址与 Relay 配置是否完整'
    ]
  }
}

function unattributedConclusion(state) {
  const detail = state === 'completed'
    ? '诊断存在失败样本，但结果中没有可定位到具体后端或 Relay 路径的数据，无法判断失败环节。'
    : '诊断已失败，但结果中没有可定位到具体后端或 Relay 路径的数据，无法判断失败环节。'
  return {
    tone: 'warning',
    headline: '无法定位：缺少可归因的失败信息',
    detail,
    suggestions: [
      '展开探测样本，结合错误信息排查失败环节',
      '确认规则配置无误后重新运行诊断'
    ]
  }
}

function taskErrorConclusion(error) {
  return {
    tone: 'danger',
    headline: '诊断执行失败',
    detail: error,
    suggestions: [
      '确认目标节点在线并保持心跳，然后重新运行诊断',
      '若问题持续，查看节点日志中的任务失败详情'
    ]
  }
}

function collectFailedRelayPaths(result) {
  const paths = Array.isArray(result?.relay_paths) ? result.relay_paths : []
  return paths.filter(path => path && path.success === false)
}

function collectFailedBackends(result) {
  const collected = []
  const visit = (backend) => {
    if (!backend) return
    const summary = backend.summary || null
    if (toCount(summary?.failed) > 0) {
      collected.push(backend)
      return
    }
    if (toCount(summary?.sent) <= 0) {
      for (const child of backend.children || []) visit(child)
    }
  }
  for (const backend of result?.backends || []) visit(backend)
  return collected
}

function failedSampleErrors(result) {
  const errors = []
  for (const sample of result?.samples || []) {
    if (!sample || sample.success) continue
    const message = String(sample.error || '').trim()
    if (!message || errors.includes(message)) continue
    errors.push(message)
    if (errors.length >= 2) break
  }
  return errors
}

function resolveFailedCount(summary, sent) {
  const explicit = Number(summary?.failed)
  if (Number.isFinite(explicit)) return Math.max(0, explicit)
  return Math.max(0, sent - toCount(summary?.succeeded))
}

function toCount(value) {
  const num = Number(value)
  return Number.isFinite(num) && num > 0 ? num : 0
}

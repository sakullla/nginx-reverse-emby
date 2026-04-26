function isHttpsUrl(value) {
  try {
    const parsed = new URL(String(value || '').trim())
    return parsed.protocol === 'https:' && !!parsed.host
  } catch {
    return false
  }
}

export function validateWorkerDeployInput(input = {}) {
  const errors = {}
  const workerName = String(input.workerName || '').trim()
  const masterUrl = String(input.masterUrl || '').trim()
  const token = String(input.token || '').trim()
  const packageRecord = input.packageRecord
  if (!workerName) errors.workerName = '请输入 Worker 名称'
  if (!isHttpsUrl(masterUrl)) errors.masterUrl = '请输入有效的 https Master URL'
  if (!token) errors.token = '请输入 Worker 访问令牌'
  if (!packageRecord || packageRecord.platform !== 'cloudflare_worker' || packageRecord.kind !== 'worker_script') {
    errors.packageRecord = '请选择 Cloudflare Worker 脚本包'
  }
  return errors
}

export function buildWorkerDeployModel(input = {}) {
  const errors = validateWorkerDeployInput(input)
  if (Object.keys(errors).length) {
    const err = new Error('invalid worker deploy input')
    err.errors = errors
    throw err
  }
  const workerName = String(input.workerName || '').trim()
  const masterUrl = String(input.masterUrl || '').trim().replace(/\/+$/, '')
  const token = String(input.token || '').trim()
  const pkg = input.packageRecord
  return {
    workerName,
    scriptUrl: pkg.download_url,
    sha256: pkg.sha256,
    env: {
      NRE_MASTER_URL: masterUrl,
      NRE_WORKER_TOKEN: token
    },
    command: [
      'wrangler deploy',
      '--name', workerName,
      '--compatibility-date 2026-04-26',
      pkg.download_url
    ].join(' ')
  }
}

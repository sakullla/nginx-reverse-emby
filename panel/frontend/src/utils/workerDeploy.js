function isHttpsUrl(value) {
  try {
    const parsed = new URL(String(value || '').trim())
    return parsed.protocol === 'https:' && !!parsed.host
  } catch {
    return false
  }
}

function isSha256(value) {
  return /^[a-f0-9]{64}$/i.test(String(value || '').trim())
}

function isSafeWorkerName(value) {
  return /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(value)
}

export function validateWorkerDeployInput(input = {}) {
  const errors = {}
  const workerName = String(input.workerName || '').trim()
  const masterUrl = String(input.masterUrl || '').trim()
  const token = String(input.token || '').trim()
  const packageRecord = input.packageRecord
  if (!workerName) errors.workerName = '请输入 Worker 名称'
  else if (!isSafeWorkerName(workerName)) {
    errors.workerName = 'Worker 名称仅支持小写字母、数字和连字符，且需以字母或数字开头和结尾'
  }
  if (!isHttpsUrl(masterUrl)) errors.masterUrl = '请输入有效的 https Master URL'
  if (!token) errors.token = '请输入 Worker 访问令牌'
  if (
    !packageRecord ||
    packageRecord.platform !== 'cloudflare_worker' ||
    packageRecord.arch !== 'script' ||
    packageRecord.kind !== 'worker_script'
  ) {
    errors.packageRecord = '请选择 Cloudflare Worker 脚本包'
  } else if (!isHttpsUrl(packageRecord.download_url) || !isSha256(packageRecord.sha256)) {
    errors.packageRecord = '请选择有效的 Cloudflare Worker 脚本包'
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
  const commandArgs = [
    'wrangler deploy',
    '--name',
    workerName,
    '--compatibility-date 2026-04-26',
    pkg.download_url
  ]
  return {
    workerName,
    scriptUrl: pkg.download_url,
    sha256: pkg.sha256,
    env: {
      NRE_MASTER_URL: masterUrl,
      NRE_WORKER_TOKEN: token
    },
    commandArgs,
    command: commandArgs.join(' ')
  }
}

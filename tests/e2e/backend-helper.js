const fs = require('fs')
const net = require('net')
const os = require('os')
const path = require('path')
const { spawn } = require('child_process')
const { request: playwrightRequest, expect } = require('@playwright/test')

const REPO_ROOT = path.resolve(__dirname, '..', '..')

function getFreePort() {
  return new Promise((resolve, reject) => {
    const server = net.createServer()
    server.listen(0, '127.0.0.1', () => {
      const address = server.address()
      server.close(() => resolve(address.port))
    })
    server.on('error', reject)
  })
}

async function waitForServer(baseURL, timeoutMs = 10000) {
  const startedAt = Date.now()
  const context = await playwrightRequest.newContext({ baseURL })

  while (Date.now() - startedAt < timeoutMs) {
    try {
      const response = await context.get('/api/info')
      if (response.ok()) {
        await context.dispose()
        return
      }
    } catch (error) {
      // ignore until timeout
    }
    await new Promise((resolve) => setTimeout(resolve, 100))
  }

  await context.dispose()
  throw new Error(`Timed out waiting for backend ${baseURL}`)
}

async function startBackend(options = {}) {
  const port = await getFreePort()
  const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), 'nginx-reverse-emby-backend-'))
  const dataRoot = path.join(tempRoot, 'data')
  const agentRulesDir = path.join(dataRoot, 'agent_rules')
  fs.mkdirSync(agentRulesDir, { recursive: true })

  const applyLog = path.join(tempRoot, 'mock-apply.log')
  const applyScript = path.join(tempRoot, 'mock-apply.js')
  fs.writeFileSync(
    applyScript,
    [
      "const fs = require('fs')",
      'const logFile = process.env.MOCK_APPLY_LOG',
      "fs.appendFileSync(logFile, JSON.stringify({ at: new Date().toISOString() }) + '\\n')",
      "if (process.env.MOCK_APPLY_FAIL === '1') {",
      "  process.stderr.write('mock apply failed')",
      '  process.exit(2)',
      '}'
    ].join('\n'),
    'utf8'
  )

  const env = {
    ...process.env,
    API_TOKEN: options.apiToken || 'admin',
    MASTER_REGISTER_TOKEN: options.registerToken || 'register-token',
    MASTER_LOCAL_AGENT_NAME: 'local-master',
    PANEL_BACKEND_HOST: '127.0.0.1',
    PANEL_BACKEND_PORT: String(port),
    PANEL_DATA_ROOT: dataRoot,
    PANEL_RULES_JSON: path.join(dataRoot, 'proxy_rules.json'),
    PANEL_AGENTS_JSON: path.join(dataRoot, 'agents.json'),
    PANEL_AGENT_RULES_DIR: agentRulesDir,
    PANEL_APPLY_COMMAND: process.execPath,
    PANEL_APPLY_ARGS: JSON.stringify([applyScript]),
    MOCK_APPLY_LOG: applyLog,
    MOCK_APPLY_FAIL: options.failApply ? '1' : '0',
    AGENT_HEARTBEAT_TIMEOUT_MS: String(options.heartbeatTimeoutMs || 1200),
    AGENT_POLL_INTERVAL_MS: '150',
    PANEL_AUTO_APPLY: options.autoApply === false ? '0' : '1'
  }

  const child = spawn(process.execPath, ['panel/backend/server.js'], {
    cwd: REPO_ROOT,
    env,
    stdio: ['ignore', 'pipe', 'pipe']
  })

  let logs = ''
  child.stdout.on('data', (chunk) => {
    logs += chunk.toString()
  })
  child.stderr.on('data', (chunk) => {
    logs += chunk.toString()
  })

  const baseURL = `http://127.0.0.1:${port}`
  await waitForServer(baseURL)

  const api = await playwrightRequest.newContext({
    baseURL,
    extraHTTPHeaders: {
      'X-Panel-Token': env.API_TOKEN
    }
  })

  const publicApi = await playwrightRequest.newContext({ baseURL })

  async function stop() {
    await api.dispose()
    await publicApi.dispose()
    if (!child.killed) {
      child.kill()
      await new Promise((resolve) => child.once('exit', resolve))
    }
    fs.rmSync(tempRoot, { recursive: true, force: true })
  }

  return {
    api,
    publicApi,
    baseURL,
    env,
    tempRoot,
    logs: () => logs,
    readApplyLog: () => (fs.existsSync(applyLog) ? fs.readFileSync(applyLog, 'utf8').trim().split(/\r?\n/).filter(Boolean) : []),
    stop
  }
}

async function expectOk(response) {
  const payload = await response.json()
  expect(response.ok(), JSON.stringify(payload)).toBeTruthy()
  return payload
}

module.exports = {
  startBackend,
  expectOk
}

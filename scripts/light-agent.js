#!/usr/bin/env node
'use strict'

const fs = require('fs')
const os = require('os')
const path = require('path')
const http = require('http')
const https = require('https')
const { spawnSync } = require('child_process')

const MASTER_URL = trimSlash(process.env.MASTER_PANEL_URL || '')
const REGISTER_TOKEN = process.env.MASTER_REGISTER_TOKEN || process.env.REGISTER_TOKEN || ''
const AGENT_NAME = process.env.AGENT_NAME || os.hostname()
const AGENT_TOKEN = process.env.AGENT_TOKEN || process.env.AGENT_API_TOKEN || ''
const AGENT_VERSION = process.env.AGENT_VERSION || '1'
const AGENT_TAGS = (process.env.AGENT_TAGS || '')
  .split(',')
  .map((item) => item.trim())
  .filter(Boolean)
const AGENT_URL = trimSlash(process.env.AGENT_PUBLIC_URL || '')
const HEARTBEAT_INTERVAL_MS = Number(process.env.AGENT_HEARTBEAT_INTERVAL_MS || '10000')
const RULES_JSON = process.env.RULES_JSON || path.resolve(process.cwd(), 'proxy_rules.json')
const L4_RULES_JSON = process.env.L4_RULES_JSON || path.resolve(process.cwd(), 'l4_rules.json')
const MANAGED_CERTS_JSON =
  process.env.MANAGED_CERTS_JSON || path.resolve(process.cwd(), 'managed_certificates.json')
const STATE_FILE = process.env.AGENT_STATE_FILE || path.resolve(process.cwd(), 'agent-state.json')
const APPLY_COMMAND = process.env.APPLY_COMMAND || ''
const NGINX_STATUS_URL = process.env.NGINX_STATUS_URL || 'http://127.0.0.1:18080/nginx_status'
const AGENT_CAPABILITIES = [...new Set(
  (process.env.AGENT_CAPABILITIES || 'http_rules,local_acme,cert_install,l4')
    .split(',')
    .map((item) => item.trim())
    .filter(Boolean)
)]

function trimSlash(value) {
  return String(value || '').trim().replace(/\/+$/, '')
}

function nowIso() {
  return new Date().toISOString()
}

function ensureParent(file) {
  fs.mkdirSync(path.dirname(file), { recursive: true })
}

function readJson(file, fallback) {
  try {
    if (!fs.existsSync(file)) return fallback
    return JSON.parse(fs.readFileSync(file, 'utf8'))
  } catch {
    return fallback
  }
}

function writeJson(file, value) {
  ensureParent(file)
  fs.writeFileSync(file, JSON.stringify(value, null, 2), 'utf8')
}

function safeLogDetails(details) {
  if (details === undefined || details === null || details === '') return ''
  if (typeof details === 'string') return details
  try {
    return JSON.stringify(details)
  } catch {
    return String(details)
  }
}

function log(level, message, details) {
  const line = `[agent][${nowIso()}][${String(level || 'info').toUpperCase()}] ${message}${
    details === undefined || details === null || details === ''
      ? ''
      : ` ${safeLogDetails(details)}`
  }`

  const writer = level === 'error' ? console.error : console.log
  writer(line)
}

function truncateText(value, maxLength = 1000) {
  const text = String(value || '').trim()
  if (!text || text.length <= maxLength) return text
  return `${text.slice(0, maxLength)}...`
}

function requestJson(method, urlString, payload, headers = {}) {
  return new Promise((resolve, reject) => {
    const url = new URL(urlString)
    const transport = url.protocol === 'https:' ? https : http
    const body = payload ? Buffer.from(JSON.stringify(payload), 'utf8') : null

    const req = transport.request(
      url,
      {
        method,
        headers: {
          Accept: 'application/json',
          ...(body
            ? {
                'Content-Type': 'application/json',
                'Content-Length': String(body.length)
              }
            : {}),
          ...headers
        }
      },
      (res) => {
        let raw = ''
        res.setEncoding('utf8')
        res.on('data', (chunk) => {
          raw += chunk
        })
        res.on('end', () => {
          let parsed = {}
          if (raw) {
            try {
              parsed = JSON.parse(raw)
            } catch {
              parsed = { raw }
            }
          }
          if (res.statusCode < 200 || res.statusCode >= 300) {
            reject(new Error(parsed.details || parsed.message || `HTTP ${res.statusCode}`))
            return
          }
          resolve(parsed)
        })
      }
    )

    req.on('error', reject)
    req.setTimeout(10000, () => {
      req.destroy(new Error('request timeout'))
    })
    if (body) req.write(body)
    req.end()
  })
}

function parseStubStatus(data) {
  const lines = String(data || '').split('\n')
  const activeMatch = (lines[0] || '').match(/\d+/)
  const requestsLine = (lines[2] || '').trim().split(/\s+/)
  return {
    activeConnections: activeMatch ? activeMatch[0] : '0',
    totalRequests: requestsLine.length >= 3 ? requestsLine[2] : '0',
    status: '正常'
  }
}

function fetchNginxStats() {
  if (!NGINX_STATUS_URL) {
    return Promise.resolve({
      totalRequests: '0',
      status: '未配置 nginx_status'
    })
  }

  return new Promise((resolve) => {
    const url = new URL(NGINX_STATUS_URL)
    const transport = url.protocol === 'https:' ? https : http
    transport
      .get(url, (res) => {
        let raw = ''
        res.setEncoding('utf8')
        res.on('data', (chunk) => {
          raw += chunk
        })
        res.on('end', () => {
          try {
            resolve(parseStubStatus(raw))
          } catch (err) {
            resolve({
              totalRequests: '0',
              status: '获取失败',
              error: err.message
            })
          }
        })
      })
      .on('error', (err) => {
        resolve({
          totalRequests: '0',
          status: '连接失败',
          error: err.message
        })
      })
  })
}

function loadState() {
  return {
    current_revision: 0,
    last_apply_revision: 0,
    last_apply_status: null,
    last_apply_message: '',
    ...readJson(STATE_FILE, {})
  }
}

function saveState(state) {
  writeJson(STATE_FILE, state)
}

async function registerAgent() {
  if (!REGISTER_TOKEN) return
  log('info', 'registering agent to master', {
    master: MASTER_URL,
    name: AGENT_NAME,
    tags: AGENT_TAGS
  })
  await requestJson(
    'POST',
    `${MASTER_URL}/panel-api/agents/register`,
    {
      name: AGENT_NAME,
      agent_url: AGENT_URL,
      agent_token: AGENT_TOKEN,
      version: AGENT_VERSION,
      tags: AGENT_TAGS,
      capabilities: AGENT_CAPABILITIES,
      mode: 'pull',
      register_token: REGISTER_TOKEN
    },
    {
      'X-Register-Token': REGISTER_TOKEN
    }
  )
  log('info', 'agent registered successfully')
}

function applyRules() {
  if (!APPLY_COMMAND) {
    throw new Error('APPLY_COMMAND is not configured')
  }
  log('info', 'applying synced rules', {
    rules_file: RULES_JSON,
    apply_command: APPLY_COMMAND
  })
  const result = spawnSync('/bin/sh', ['-lc', APPLY_COMMAND], {
    encoding: 'utf8',
    env: {
      ...process.env,
      RULES_JSON,
      L4_RULES_JSON,
      MANAGED_CERTS_JSON
    }
  })
  if (result.error) throw result.error
  if (result.status !== 0) {
    throw new Error(
      truncateText(result.stderr || result.stdout || `exit code ${result.status}`)
    )
  }
  return truncateText([result.stdout, result.stderr].filter(Boolean).join('\n'))
}

async function heartbeat(state) {
  const stats = await fetchNginxStats()
  return requestJson(
    'POST',
    `${MASTER_URL}/panel-api/agents/heartbeat`,
    {
      name: AGENT_NAME,
      agent_url: AGENT_URL,
      version: AGENT_VERSION,
      tags: AGENT_TAGS,
      capabilities: AGENT_CAPABILITIES,
      current_revision: state.current_revision,
      last_apply_revision: state.last_apply_revision,
      last_apply_status: state.last_apply_status,
      last_apply_message: state.last_apply_message,
      stats
    },
    {
      'X-Agent-Token': AGENT_TOKEN
    }
  )
}

async function runOnce() {
  const state = loadState()
  log('info', 'sending heartbeat', {
    current_revision: state.current_revision,
    last_apply_status: state.last_apply_status
  })
  const response = await heartbeat(state)
  log('info', 'heartbeat acknowledged', {
    desired_revision: response.sync?.desired_revision ?? state.current_revision,
    current_revision: state.current_revision,
    has_update: !!response.sync?.has_update
  })

  if (response.sync?.has_update && Array.isArray(response.sync.rules)) {
    const nextRevision = Number(response.sync.desired_revision) || state.current_revision
    log('info', 'received rules update from master', {
      desired_revision: nextRevision,
      rules_count: response.sync.rules.length,
      rules_file: RULES_JSON
    })
    writeJson(RULES_JSON, response.sync.rules)
    writeJson(L4_RULES_JSON, Array.isArray(response.sync.l4_rules) ? response.sync.l4_rules : [])
    writeJson(
      MANAGED_CERTS_JSON,
      Array.isArray(response.sync.certificates) ? response.sync.certificates : []
    )
    try {
      const applyOutput = applyRules()
      state.current_revision = nextRevision
      state.last_apply_revision = nextRevision
      state.last_apply_status = 'success'
      state.last_apply_message = truncateText(
        applyOutput || `Applied successfully at ${nowIso()}`
      )
      log('info', 'rules applied successfully', {
        current_revision: state.current_revision,
        message: state.last_apply_message
      })
    } catch (err) {
      state.last_apply_revision = nextRevision
      state.last_apply_status = 'error'
      state.last_apply_message = truncateText(String(err.message || err))
      log('error', 'failed to apply synced rules', {
        desired_revision: nextRevision,
        message: state.last_apply_message
      })
    }
    saveState(state)
    log('info', 'reporting apply result to master', {
      current_revision: state.current_revision,
      last_apply_status: state.last_apply_status
    })
    await heartbeat(state)
  } else {
    saveState(state)
  }

  return response.heartbeat_interval_ms || HEARTBEAT_INTERVAL_MS
}

async function main() {
  if (!MASTER_URL) throw new Error('MASTER_PANEL_URL is required')
  if (!AGENT_TOKEN) throw new Error('AGENT_TOKEN or AGENT_API_TOKEN is required')

  log('info', 'starting lightweight agent', {
    master: MASTER_URL,
    name: AGENT_NAME,
    interval_ms: HEARTBEAT_INTERVAL_MS,
    rules_file: RULES_JSON,
    l4_rules_file: L4_RULES_JSON,
    managed_certs_file: MANAGED_CERTS_JSON,
    state_file: STATE_FILE
  })
  await registerAgent()

  const loop = async () => {
    try {
      const nextInterval = await runOnce()
      setTimeout(loop, nextInterval)
    } catch (err) {
      log('error', 'heartbeat loop failed', String(err.message || err))
      setTimeout(loop, HEARTBEAT_INTERVAL_MS)
    }
  }

  loop()
}

main().catch((err) => {
  log('error', 'fatal', String(err.message || err))
  process.exit(1)
})

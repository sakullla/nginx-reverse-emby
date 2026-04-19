import assert from 'node:assert/strict'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import http from 'node:http'
import { spawn } from 'node:child_process'
import { setTimeout as delay } from 'node:timers/promises'
import { fileURLToPath } from 'node:url'
import { gzipSync } from 'node:zlib'

import {
  getBackupSummaryTotals,
  formatBackupReportItem,
  getBackupDownloadRevokeDelayMs,
  BACKUP_SENSITIVE_WARNING,
  BACKUP_IMPORT_CONFIRMATION_MESSAGE
} from '../panel/frontend/src/utils/backupImport.js'
import { createBackupImportDevMock } from '../panel/frontend/src/api/backupDevMock.js'

function writeTarString(buffer, value, start, length) {
  Buffer.from(String(value || ''), 'utf8').copy(buffer, start, 0, length)
}

function writeTarOctal(buffer, value, start, length) {
  const octal = Math.max(0, Number(value) || 0).toString(8)
  Buffer.from(octal.padStart(length - 1, '0') + '\0', 'ascii').copy(buffer, start, 0, length)
}

function buildTarHeader(name, size) {
  const header = Buffer.alloc(512, 0)
  writeTarString(header, name, 0, 100)
  writeTarOctal(header, 0o644, 100, 8)
  writeTarOctal(header, 0, 108, 8)
  writeTarOctal(header, 0, 116, 8)
  writeTarOctal(header, size, 124, 12)
  writeTarOctal(header, Math.floor(Date.now() / 1000), 136, 12)
  header.fill(0x20, 148, 156)
  header[156] = '0'.charCodeAt(0)
  writeTarString(header, 'ustar', 257, 6)
  writeTarString(header, '00', 263, 2)
  let checksum = 0
  for (const byte of header) checksum += byte
  writeTarOctal(header, checksum, 148, 8)
  return header
}

function buildTarGz(entries) {
  const chunks = []
  for (const entry of entries) {
    const body = Buffer.isBuffer(entry.body) ? entry.body : Buffer.from(String(entry.body || ''), 'utf8')
    chunks.push(buildTarHeader(entry.name, body.length))
    chunks.push(body)
    const remainder = body.length % 512
    if (remainder) {
      chunks.push(Buffer.alloc(512 - remainder, 0))
    }
  }
  chunks.push(Buffer.alloc(1024, 0))
  return gzipSync(Buffer.concat(chunks))
}

function createMultipartBody(fieldName, fileName, content) {
  const boundary = `----codex-${Date.now().toString(16)}`
  const head = Buffer.from(
    `--${boundary}\r\n` +
      `Content-Disposition: form-data; name="${fieldName}"; filename="${fileName}"\r\n` +
      `Content-Type: application/gzip\r\n\r\n`,
    'utf8'
  )
  const tail = Buffer.from(`\r\n--${boundary}--\r\n`, 'utf8')
  return {
    boundary,
    body: Buffer.concat([head, content, tail])
  }
}

function request({ port, method, pathName, headers = {}, body = null }) {
  return new Promise((resolve, reject) => {
    const req = http.request(
      {
        host: '127.0.0.1',
        port,
        method,
        path: pathName,
        headers
      },
      (res) => {
        const chunks = []
        res.on('data', (chunk) => chunks.push(chunk))
        res.on('end', () => {
          resolve({
            statusCode: res.statusCode || 0,
            headers: res.headers,
            body: Buffer.concat(chunks)
          })
        })
      }
    )
    req.on('error', reject)
    if (body) req.write(body)
    req.end()
  })
}

async function waitForServer(port, child) {
  for (let index = 0; index < 50; index += 1) {
    if (child.exitCode !== null) {
      throw new Error(`backend exited early with code ${child.exitCode}`)
    }
    try {
      const response = await request({ port, method: 'GET', pathName: '/api/info' })
      if (response.statusCode === 200) return
    } catch {}
    await delay(100)
  }
  throw new Error('backend did not become ready')
}

async function withServer(run) {
  const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
  const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), 'nre-backup-test-'))
  fs.mkdirSync(path.join(tempRoot, 'agent_rules'), { recursive: true })
  fs.mkdirSync(path.join(tempRoot, 'l4_agent_rules'), { recursive: true })
  fs.mkdirSync(path.join(tempRoot, 'managed_certificates'), { recursive: true })
  fs.writeFileSync(
    path.join(tempRoot, 'agents.json'),
    JSON.stringify([
      {
        id: 'existing-agent',
        name: 'edge-01',
        agent_url: 'http://edge-01.internal',
        agent_token: 'edge-token',
        capabilities: ['http_rules', 'cert_install', 'l4', 'local_acme']
      }
    ], null, 2)
  )
  fs.writeFileSync(path.join(tempRoot, 'proxy_rules.json'), '[]')
  fs.writeFileSync(path.join(tempRoot, 'managed_certificates.json'), '[]')
  fs.writeFileSync(path.join(tempRoot, 'agent_rules', 'existing-agent.json'), '[]')
  fs.writeFileSync(path.join(tempRoot, 'l4_agent_rules', 'existing-agent.json'), '[]')

  const port = 19081 + Math.floor(Math.random() * 1000)
  const child = spawn(
    process.execPath,
    ['panel/backend/server.js'],
    {
      cwd: repoRoot,
      env: {
        ...process.env,
        PANEL_BACKEND_HOST: '127.0.0.1',
        PANEL_BACKEND_PORT: String(port),
        PANEL_DATA_ROOT: tempRoot,
        API_TOKEN: 'panel-secret',
        PANEL_AUTO_APPLY: '0',
        MASTER_LOCAL_AGENT_ENABLED: '0'
      },
      stdio: ['ignore', 'pipe', 'pipe']
    }
  )

  let stderr = ''
  child.stderr.on('data', (chunk) => {
    stderr += chunk.toString('utf8')
  })

  try {
    await waitForServer(port, child)
    await run({ port, tempRoot })
  } finally {
    child.kill('SIGTERM')
    await Promise.race([
      new Promise((resolve) => child.once('exit', resolve)),
      delay(2000)
    ])
    fs.rmSync(tempRoot, { recursive: true, force: true })
    if (stderr.trim()) {
      process.stderr.write(stderr)
    }
  }
}

function buildBackupArchive() {
  const archive = buildTarGz([
    {
      name: 'manifest.json',
      body: JSON.stringify({
        package_version: 1,
        source_architecture: 'main-legacy',
        exported_at: new Date().toISOString(),
        counts: {
          agents: 1,
          http_rules: 1,
          l4_rules: 0,
          relay_listeners: 0,
          certificates: 1,
          version_policies: 0
        }
      })
    },
    {
      name: 'agents.json',
      body: JSON.stringify([
        {
          id: 'backup-agent',
          name: 'edge-01',
          agent_url: 'http://backup.internal',
          agent_token: 'backup-token',
          capabilities: ['http_rules', 'cert_install', 'l4', 'local_acme']
        }
      ])
    },
    {
      name: 'http_rules.json',
      body: JSON.stringify([
        {
          id: 1,
          agent_id: 'backup-agent',
          frontend_url: 'https://media.example.com',
          backend_url: 'http://127.0.0.1:8096',
          enabled: true,
          tags: [],
          proxy_redirect: true
        }
      ])
    },
    { name: 'l4_rules.json', body: '[]' },
    { name: 'relay_listeners.json', body: '[]' },
    {
      name: 'certificates.json',
      body: JSON.stringify([
        {
          id: 1,
          domain: 'pending.example.com',
          enabled: true,
          scope: 'domain',
          issuer_mode: 'master_cf_dns',
          target_agent_ids: ['backup-agent'],
          status: 'pending',
          last_issue_at: null,
          last_error: 'dns pending',
          material_hash: '',
          agent_reports: {},
          acme_info: {},
          tags: []
        }
      ])
    },
    { name: 'version_policies.json', body: '[]' }
  ])
  return archive
}

async function runBackendRegression() {
  await withServer(async ({ port, tempRoot }) => {
    const unauthorizedExport = await request({
      port,
      method: 'GET',
      pathName: '/api/system/backup/export'
    })
    assert.equal(unauthorizedExport.statusCode, 401, 'backup export must require panel token')

    const archive = buildBackupArchive()
    const multipart = createMultipartBody('file', 'backup.tar.gz', archive)
    const unauthorizedImport = await request({
      port,
      method: 'POST',
      pathName: '/api/system/backup/import',
      headers: {
        'Content-Type': `multipart/form-data; boundary=${multipart.boundary}`,
        'Content-Length': String(multipart.body.length)
      },
      body: multipart.body
    })
    assert.equal(unauthorizedImport.statusCode, 401, 'backup import must require panel token')

    const authorizedImport = await request({
      port,
      method: 'POST',
      pathName: '/api/system/backup/import',
      headers: {
        'X-Panel-Token': 'panel-secret',
        'Content-Type': `multipart/form-data; boundary=${multipart.boundary}`,
        'Content-Length': String(multipart.body.length)
      },
      body: multipart.body
    })
    assert.equal(authorizedImport.statusCode, 200, 'authorized import should succeed')

    const payload = JSON.parse(authorizedImport.body.toString('utf8'))
    if (process.env.DEBUG_BACKUP_TEST === '1') {
      console.log(JSON.stringify(payload, null, 2))
      console.log(fs.readFileSync(path.join(tempRoot, 'managed_certificates.json'), 'utf8'))
    }
    assert.equal(payload.summary.imported, 2, 'import should count rule and pending certificate')
    assert.equal(payload.summary.skipped_conflict, 1, 'existing agent should be reported as conflict')
    assert.equal(payload.summary.skipped_missing_material, 0, 'pending certificate should not be skipped')

    const importedRules = JSON.parse(
      fs.readFileSync(path.join(tempRoot, 'agent_rules', 'existing-agent.json'), 'utf8')
    )
    assert.equal(importedRules.length, 1, 'http rule should be imported onto the existing agent')
    assert.equal(importedRules[0].frontend_url, 'https://media.example.com')

    const importedCerts = JSON.parse(
      fs.readFileSync(path.join(tempRoot, 'managed_certificates.json'), 'utf8')
    )
    assert.equal(importedCerts.length, 1, 'certificate metadata should be restored without PEM material')
    assert.equal(importedCerts[0].domain, 'pending.example.com')
    assert.equal(importedCerts[0].status, 'pending')
    assert.deepEqual(importedCerts[0].target_agent_ids, ['existing-agent'])
  })
}

function runFrontendRegression() {
  const devMock = createBackupImportDevMock()
  assert.equal(devMock.summary.imported, 2, 'dev mock must match backend summary shape')
  assert.equal(devMock.summary.skipped_conflict, 1, 'dev mock conflict count must be numeric')
  assert.equal(devMock.report.imported[0].resource, 'agent', 'dev mock report must use resource field')
  assert.equal(devMock.report.skipped_conflict[0].identifier, 'https://exists.example.com')

  assert.deepEqual(
    getBackupSummaryTotals({
      imported: 3,
      skipped_conflict: 2,
      skipped_invalid: 1,
      skipped_missing_material: 4
    }),
    {
      imported: 3,
      skipped_conflict: 2,
      skipped_invalid: 1,
      skipped_missing_material: 4
    },
    'frontend summary must read numeric totals directly'
  )

  assert.equal(
    formatBackupReportItem({
      resource: 'certificate',
      identifier: 'pending.example.com',
      detail: 'certificate metadata restored without PEM material'
    }),
    '证书 pending.example.com: certificate metadata restored without PEM material',
    'frontend report formatter must use resource/identifier/detail fields'
  )

  assert.equal(getBackupDownloadRevokeDelayMs(), 30000, 'download URL revoke delay should be long enough for browser downloads')
  assert.match(BACKUP_SENSITIVE_WARNING, /私钥|token/i, 'backup warning should mention private keys or tokens')
  assert.match(BACKUP_IMPORT_CONFIRMATION_MESSAGE, /导入备份/, 'import confirmation message should describe the action')
}

function runCodeShapeRegression() {
  const serverSource = fs.readFileSync(
    path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', 'panel/backend/server.js'),
    'utf8'
  )
  const forbiddenSymbols = [
    'function createBackupImportResult(',
    'function addBackupImportItem(',
    'function buildPortableBackupBundle(',
    'function importPortableBackupBundle(',
    'function readRequestBuffer(',
    'function parseMultipartFile(',
    'function saveManagedCertificateMaterial('
  ]
  for (const symbol of forbiddenSymbols) {
    assert.equal(
      serverSource.includes(symbol),
      false,
      `dead backup implementation should be removed: ${symbol}`
    )
  }

  const duplicateBuildPortableBackupAgents = serverSource.match(/function buildPortableBackupAgents\(/g) || []
  assert.equal(duplicateBuildPortableBackupAgents.length, 1, 'buildPortableBackupAgents should have a single implementation')
}

await runBackendRegression()
runFrontendRegression()
runCodeShapeRegression()
console.log('backup regression checks passed')

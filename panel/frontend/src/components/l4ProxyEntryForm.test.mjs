import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)
const srcDir = path.resolve(__dirname, '..')

function read(relativePath) {
  return fs.readFileSync(path.resolve(srcDir, relativePath), 'utf8')
}

test('L4 RuleForm exposes proxy entry controls and payload fields', () => {
  const source = read('components/L4RuleForm.vue')
  assert.match(source, /listen_mode/)
  assert.match(source, /proxy_entry_auth/)
  assert.doesNotMatch(source, /proxy_egress_mode/)
  assert.doesNotMatch(source, /proxy_egress_url/)
})

test('Agent list edit modal exposes outbound proxy update control', () => {
  const source = read('pages/AgentsPage.vue')
  assert.match(source, /outbound_proxy_url/)
  assert.match(source, /useUpdateAgent/)
  assert.match(source, /buildOutboundProxyPayload/)
  assert.match(source, /v-if="!editingAgent\?\.is_local"\s+class="form-group"/)
})

test('frontend API exposes agent update path', () => {
  assert.match(read('api/index.js'), /updateAgent/)
  assert.match(read('api/runtime.js'), /export async function updateAgent/)
  assert.match(read('api/devMocks/data.js'), /export async function updateAgent/)
  assert.match(read('hooks/useAgents.js'), /export function useUpdateAgent/)
})

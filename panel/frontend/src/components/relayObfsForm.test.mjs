import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)

function read(name) {
  return fs.readFileSync(path.join(__dirname, name), 'utf8')
}

test('HTTP RuleForm exposes relay obfs toggle inside relay tab', () => {
  const source = read('RuleForm.vue')
  assert.match(source, /启用 Relay 隐私增强/)
  assert.match(source, /v-model="form\.relay_obfs"/)
})

test('L4 RuleForm exposes relay obfs toggle inside relay tab', () => {
  const source = read('L4RuleForm.vue')
  assert.match(source, /启用 Relay 隐私增强/)
  assert.match(source, /v-model="form\.relay_obfs"/)
})

test('API normalization keeps relay_obfs default false', () => {
  const source = fs.readFileSync(path.resolve(__dirname, '../api/index.js'), 'utf8')
  assert.match(source, /relay_obfs:\s*payload\.relay_obfs === true/)
})

test('L4 update normalization preserves omitted relay fields', () => {
  const source = fs.readFileSync(path.resolve(__dirname, '../api/index.js'), 'utf8')
  assert.match(source, /function normalizeL4RulePayload\(payload = \{\}, options = \{\}\)/)
  assert.match(source, /const includeRelayDefaults = options\.includeRelayDefaults === true/)
  assert.match(source, /createL4Rule\(agentId, payload\)[\s\S]*normalizeL4RulePayload\(payload, \{ includeRelayDefaults: true \}\)/)
  assert.match(source, /updateL4Rule\(agentId, id, payload\)[\s\S]*normalizeL4RulePayload\(payload\)/)
})

test('HTTP legacy positional overload accepts relay_obfs argument', () => {
  const source = fs.readFileSync(path.resolve(__dirname, '../api/index.js'), 'utf8')
  assert.match(source, /function normalizeHttpRulePayload\([^)]*relay_chain,\s*relay_obfs\)/)
  assert.match(source, /relay_chain,\s*relay_obfs\s*\}\)/)
})

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
  assert.match(source, /normalizedPayload\.relay_obfs = payload\.relay_obfs === true/)
  assert.match(source, /else if \(includeRelayDefaults\) \{\s*normalizedPayload\.relay_obfs = false/)
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
  assert.match(source, /function normalizeLegacyHttpRulePayload\(payloadOrFrontend, legacyArgs = \[\], options = \{\}\)/)
  assert.match(source, /const \[\s*backend_url,\s*tags,\s*enabled,\s*proxy_redirect,\s*pass_proxy_headers,\s*user_agent,\s*custom_headers,\s*relay_chain,\s*relay_obfs\s*\] = legacyArgs/)
})

test('HTTP update normalization preserves omitted relay fields for object payloads', () => {
  const source = fs.readFileSync(path.resolve(__dirname, '../api/index.js'), 'utf8')
  assert.match(source, /function normalizeHttpRulePayloadObject\(payload = \{\}, options = \{\}\)/)
  assert.match(source, /const includeRelayDefaults = options\.includeRelayDefaults === true/)
  assert.match(source, /createRule\(agentId, payloadOrFrontend, \.\.\.legacyArgs\)[\s\S]*normalizeHttpRulePayloadObject\(payloadOrFrontend, \{ includeRelayDefaults: true \}\)/)
  assert.match(source, /updateRule\(agentId, id, payloadOrFrontend, \.\.\.legacyArgs\)[\s\S]*normalizeHttpRulePayloadObject\(payloadOrFrontend, \{ includeRelayDefaults: false \}\)/)
})

test('HTTP update normalization preserves omitted relay fields for legacy positional calls', () => {
  const source = fs.readFileSync(path.resolve(__dirname, '../api/index.js'), 'utf8')
  assert.match(source, /return normalizeHttpRulePayloadObject\(\{\s*frontend_url: payloadOrFrontend,[\s\S]*relay_obfs\s*\},\s*options\)/)
  assert.match(source, /updateRule\(agentId, id, payloadOrFrontend, \.\.\.legacyArgs\)[\s\S]*normalizeLegacyHttpRulePayload\(payloadOrFrontend, legacyArgs, \{ includeRelayDefaults: false \}\)/)
})

test('HTTP legacy positional path passes options out of band', () => {
  const source = fs.readFileSync(path.resolve(__dirname, '../api/index.js'), 'utf8')
  assert.match(source, /createRule\(agentId, payloadOrFrontend, \.\.\.legacyArgs\)[\s\S]*normalizeLegacyHttpRulePayload\(payloadOrFrontend, legacyArgs, \{ includeRelayDefaults: true \}\)/)
  assert.doesNotMatch(source, /normalizeHttpRulePayload\(payloadOrFrontend, \.\.\.legacyArgs, \{ includeRelayDefaults: (true|false) \}\)/)
})

test('HTTP RuleForm clears relay obfs when relay chain becomes empty', () => {
  const source = read('RuleForm.vue')
  assert.match(source, /watch\(\(\) => form\.value\.relay_chain,\s*\(relayChain\) => \{/)
  assert.match(source, /if \(!Array\.isArray\(relayChain\) \|\| relayChain\.length === 0\) \{\s*form\.value\.relay_obfs = false/)
})

test('L4 RuleForm clears relay obfs when relay chain becomes empty', () => {
  const source = read('L4RuleForm.vue')
  assert.match(source, /watch\(\(\) => form\.value\.relay_chain,\s*\(relayChain\) => \{/)
  assert.match(source, /if \(!Array\.isArray\(relayChain\) \|\| relayChain\.length === 0\) \{\s*form\.value\.relay_obfs = false/)
})

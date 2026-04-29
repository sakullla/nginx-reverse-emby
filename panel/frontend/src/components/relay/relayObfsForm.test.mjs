import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)
const srcDir = path.resolve(__dirname, '../..')

function read(relativePath) {
  return fs.readFileSync(path.resolve(srcDir, relativePath), 'utf8')
}

function readApiRuntime() {
  return read('api/runtime.js')
}

function readApiMock() {
  return read('api/devMocks/data.js')
}

test('HTTP RuleForm exposes relay obfs toggle inside relay tab', () => {
  const source = read('components/rules/RuleForm.vue')
  assert.match(source, /启用 Relay 隐私增强/)
  assert.match(source, /v-model="form\.relay_obfs"/)
})

test('L4 RuleForm exposes relay obfs toggle inside relay tab', () => {
  const source = read('components/l4/L4RuleForm.vue')
  assert.match(source, /启用 Relay 隐私增强/)
  assert.match(source, /v-model="form\.relay_obfs"/)
})

test('API normalization keeps relay_obfs default false', () => {
  const source = readApiRuntime()
  assert.match(source, /normalizedPayload\.relay_obfs = payload\.relay_obfs === true/)
  assert.match(source, /else if \(includeRelayDefaults\) \{\s*normalizedPayload\.relay_obfs = false/)
})

test('L4 update normalization preserves omitted relay fields', () => {
  const source = readApiRuntime()
  assert.match(source, /function normalizeL4RulePayload\(payload = \{\}, options = \{\}\)/)
  assert.match(source, /const includeRelayDefaults = options\.includeRelayDefaults === true/)
  assert.match(source, /createL4Rule\(agentId, payload\)[\s\S]*normalizeL4RulePayload\(payload, \{ includeRelayDefaults: true \}\)/)
  assert.match(source, /updateL4Rule\(agentId, id, payload\)[\s\S]*normalizeL4RulePayload\(payload\)/)
})

test('HTTP legacy positional overload accepts relay_obfs argument', () => {
  const source = readApiRuntime()
  assert.match(source, /function normalizeLegacyHttpRulePayload\(payloadOrFrontend, legacyArgs = \[\], options = \{\}\)/)
  assert.match(source, /const \[\s*backend_url,\s*tags,\s*enabled,\s*proxy_redirect,\s*pass_proxy_headers,\s*user_agent,\s*custom_headers,\s*relay_chain,\s*relay_obfs\s*\] = legacyArgs/)
})

test('HTTP update normalization preserves omitted relay fields for object payloads', () => {
  const source = readApiRuntime()
  assert.match(source, /function normalizeHttpRulePayloadObject\(payload = \{\}, options = \{\}\)/)
  assert.match(source, /const includeRelayDefaults = options\.includeRelayDefaults === true/)
  assert.match(source, /createRule\(agentId, payloadOrFrontend, \.\.\.legacyArgs\)[\s\S]*normalizeHttpRulePayloadObject\(payloadOrFrontend, \{ includeRelayDefaults: true \}\)/)
  assert.match(source, /updateRule\(agentId, id, payloadOrFrontend, \.\.\.legacyArgs\)[\s\S]*normalizeHttpRulePayloadObject\(payloadOrFrontend, \{ includeRelayDefaults: false \}\)/)
})

test('HTTP update normalization preserves omitted relay fields for legacy positional calls', () => {
  const source = readApiRuntime()
  assert.match(source, /return normalizeHttpRulePayloadObject\(\{\s*frontend_url: payloadOrFrontend,[\s\S]*relay_obfs\s*\},\s*options\)/)
  assert.match(source, /updateRule\(agentId, id, payloadOrFrontend, \.\.\.legacyArgs\)[\s\S]*normalizeLegacyHttpRulePayload\(payloadOrFrontend, legacyArgs, \{ includeRelayDefaults: false \}\)/)
})

test('HTTP legacy positional path passes options out of band', () => {
  const source = readApiRuntime()
  assert.match(source, /createRule\(agentId, payloadOrFrontend, \.\.\.legacyArgs\)[\s\S]*normalizeLegacyHttpRulePayload\(payloadOrFrontend, legacyArgs, \{ includeRelayDefaults: true \}\)/)
  assert.doesNotMatch(source, /normalizeHttpRulePayload\(payloadOrFrontend, \.\.\.legacyArgs, \{ includeRelayDefaults: (true|false) \}\)/)
})

test('HTTP RuleForm clears relay obfs when relay chain becomes empty', () => {
  const source = read('components/rules/RuleForm.vue')
  assert.match(source, /watch\(\s*\[\(\) => form\.value\.relay_layers,\s*firstRelayListener\],\s*\(\[relayLayers\]\) => \{/)
  assert.match(source, /if \(\s*!Array\.isArray\(relayLayers\)\s*\|\|\s*relayLayers\.length === 0\s*\|\|\s*firstRelayListener\.value\?\.transport_mode !== 'tls_tcp'/)
})

test('L4 RuleForm clears relay obfs when relay chain becomes empty', () => {
  const source = read('components/l4/L4RuleForm.vue')
  assert.match(source, /watch\(\s*\[\(\) => form\.value\.relay_layers,\s*firstRelayListener\],\s*\(\[relayLayers\]\) => \{/)
  assert.match(source, /if \(\s*!Array\.isArray\(relayLayers\)\s*\|\|\s*relayLayers\.length === 0\s*\|\|\s*firstRelayListener\.value\?\.transport_mode !== 'tls_tcp'/)
})

test('L4 RuleForm rehydrates local form state when initialData changes', () => {
  const source = read('components/l4/L4RuleForm.vue')
  assert.match(source, /watch\(\(\) => props\.initialData,\s*\(value\) => \{/)
  assert.match(source, /form\.value = createFormState\(value\)/)
})

test('RelayListenerForm exposes relay transport fields and submit payload', () => {
  const source = read('components/relay/RelayListenerForm.vue')
  assert.match(source, /v-model='form\.transport_mode'/)
  assert.match(source, /v-model='form\.allow_transport_fallback'/)
  assert.match(source, /v-model='form\.obfs_mode'/)
  assert.match(source, /transport_mode:\s*form\.value\.transport_mode/)
  assert.match(source, /allow_transport_fallback:\s*form\.value\.transport_mode === 'quic'[\s\S]*form\.value\.allow_transport_fallback === true/)
  assert.match(source, /obfs_mode:\s*form\.value\.transport_mode === 'tls_tcp'[\s\S]*form\.value\.obfs_mode[\s\S]*:\s*'off'/)
})

test('Relay list and selector surface transport mode to users', () => {
  const relayCard = read('components/relay/RelayCard.vue')
  const relayChainInput = read('components/relay/RelayChainInput.vue')
  assert.match(relayCard, /transportLabel/)
  assert.match(relayCard, /obfsLabel/)
  assert.match(relayChainInput, /formatTransportLabel\(listener\)/)
  assert.match(relayChainInput, /formatTransportHint\(listener\)/)
})

test('HTTP RuleForm ties relay obfs to first relay listener transport', () => {
  const source = read('components/rules/RuleForm.vue')
  assert.match(source, /const selectedRelayListeners = computed\(\(\) => \{/)
  assert.match(source, /const firstRelayListener = computed\(\(\) => \{/)
  assert.match(source, /return listenerMap\.get\(Number\(layers\[0\]\[0\]\)\) \|\| null/)
  assert.match(source, /const relayObfsUnsupportedReason = computed\(\(\) => \{/)
  assert.match(source, /firstRelayListener\.value\.transport_mode !== 'tls_tcp'/)
  assert.match(source, /watch\(\s*\[\(\) => form\.value\.relay_layers,\s*firstRelayListener\]/)
  assert.match(source, /relay_obfs:\s*firstRelayListener\.value\?\.transport_mode === 'tls_tcp'[\s\S]*form\.value\.relay_obfs === true/)
})

test('L4 RuleForm ties relay obfs to first relay listener transport', () => {
  const source = read('components/l4/L4RuleForm.vue')
  assert.match(source, /const selectedRelayListeners = computed\(\(\) => \{/)
  assert.match(source, /const firstRelayListener = computed\(\(\) => \{/)
  assert.match(source, /return listenerMap\.get\(Number\(layers\[0\]\[0\]\)\) \|\| null/)
  assert.match(source, /const relayObfsUnsupportedReason = computed\(\(\) => \{/)
  assert.match(source, /firstRelayListener\.value\.transport_mode !== 'tls_tcp'/)
  assert.match(source, /watch\(\s*\[\(\) => form\.value\.relay_layers,\s*firstRelayListener\]/)
  assert.match(source, /relay_obfs:\s*form\.value\.protocol === 'tcp'[\s\S]*firstRelayListener\.value\?\.transport_mode === 'tls_tcp'[\s\S]*form\.value\.relay_obfs === true/)
})

test('L4 RuleForm keeps relay chain available for UDP rules', () => {
  const source = read('components/l4/L4RuleForm.vue')
  assert.doesNotMatch(source, /:disabled="form\.protocol === 'udp'"/)
  assert.doesNotMatch(source, /UDP 协议不支持 Relay 链路/)
  assert.doesNotMatch(source, /form\.value\.relay_chain = \[\]\s*[\r\n]+\s*form\.value\.relay_obfs = false/)
  assert.match(source, /relay_chain:\s*flattenRelayLayers\(form\.value\.relay_layers\)/)
  assert.match(source, /relay_obfs:\s*form\.value\.protocol === 'tcp'/)
})

test('Mock relay listener normalization keeps transport defaults and quic obfs off', () => {
  const source = readApiMock()
  assert.match(source, /function normalizeRelayTransportMode\(value\)/)
  assert.match(source, /transport_mode:\s*transportMode/)
  assert.match(source, /allow_transport_fallback:\s*payload\.allow_transport_fallback !== false/)
  assert.match(source, /obfs_mode:\s*transportMode === 'tls_tcp'[\s\S]*normalizeRelayObfsMode\(payload\.obfs_mode, transportMode\)[\s\S]*:\s*'off'/)
})

test('HTTP and L4 API normalization preserve adaptive load balancing strategy', () => {
  const source = readApiRuntime()
  assert.match(source, /const SUPPORTED_LOAD_BALANCING_STRATEGIES = new Set\(\['adaptive', 'round_robin', 'random'\]\)/)
  assert.match(source, /strategy:\s*normalizeLoadBalancingStrategy\(rule\.load_balancing\?\.strategy\)/)
  assert.match(source, /strategy:\s*normalizeLoadBalancingStrategy\(payload\.load_balancing\?\.strategy\)/)
})

test('Rule forms and L4 cards expose adaptive load balancing strategy', () => {
  const httpForm = read('components/rules/RuleForm.vue')
  const l4Form = read('components/l4/L4RuleForm.vue')
  const l4Item = read('components/l4/L4RuleItem.vue')

  assert.match(httpForm, /<option value="adaptive">自适应 \(Adaptive\)<\/option>/)
  assert.match(httpForm, /const SUPPORTED_HTTP_STRATEGIES = new Set\(\['adaptive', 'round_robin', 'random'\]\)/)
  assert.match(httpForm, /load_balancing:\s*\{\s*strategy: 'adaptive'\s*\}/)

  assert.match(l4Form, /<option value="adaptive">自适应 \(Adaptive\)<\/option>/)
  assert.match(l4Form, /const SUPPORTED_L4_STRATEGIES = new Set\(\['adaptive', 'round_robin', 'random'\]\)/)
  assert.match(l4Form, /strategy:\s*normalizeL4Strategy\(initialData\?\.load_balancing\?\.strategy\)/)

  assert.match(l4Item, /const LB_MAP = \{ adaptive: 'ADP', round_robin: 'RR', random: 'RND' \}/)
  assert.match(l4Item, /const LB_TITLES = \{ adaptive: '自适应 \(Adaptive\)', round_robin: '轮询 \(Round Robin\)', random: '随机 \(Random\)' \}/)
})

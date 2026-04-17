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

function countMatches(source, pattern) {
  return source.match(pattern)?.length ?? 0
}

function countText(source, text) {
  return source.split(text).length - 1
}

test('RuleDiagnosticModal shows sustained throughput only for HTTP', () => {
  const source = read('RuleDiagnosticModal.vue')
  assert.match(source, /const showHTTPAdaptiveMetrics = computed\(\(\) => isHTTP\.value\)/)
  assert.match(source, /持续吞吐/)
  assert.equal(countMatches(source, /持续吞吐/g), 3)
})

test('RuleDiagnosticModal removes legacy bandwidth copy', () => {
  const source = read('RuleDiagnosticModal.vue')
  assert.doesNotMatch(source, /评估带宽/)
  assert.doesNotMatch(source, /formatBandwidth/)
})

test('RuleDiagnosticModal requires throughput values before rendering metrics', () => {
  const source = read('RuleDiagnosticModal.vue')
  const backendGuard = 'v-if="showHTTPAdaptiveMetrics && hasThroughput(backend.adaptive?.estimated_bandwidth_bps)"'
  const childGuard = 'v-if="showHTTPAdaptiveMetrics && hasThroughput(child.adaptive?.estimated_bandwidth_bps)"'

  assert.match(source, /function hasThroughput\(value\) \{\s+return value != null\s+\}/)
  assert.equal(countMatches(source, /v-if="showHTTPAdaptiveMetrics && hasThroughput\(/g), 3)
  assert.equal(countText(source, backendGuard), 2)
  assert.equal(countText(source, childGuard), 1)
})

test('RuleDiagnosticModal keeps performance and outlier insights HTTP-only', () => {
  const source = read('RuleDiagnosticModal.vue')
  const performanceGuard = 'v-if="showHTTPAdaptiveMetrics" class="diagnostic-metric"'
  const detailPerformanceGuard = 'v-if="showHTTPAdaptiveMetrics" class="diagnostic-factor"'
  const reasonGuard = 'v-if="showHTTPAdaptiveMetrics && backend.adaptive?.reason"'

  assert.equal(countText(source, performanceGuard), 1)
  assert.equal(countText(source, detailPerformanceGuard), 2)
  assert.equal(countText(source, reasonGuard), 1)
})

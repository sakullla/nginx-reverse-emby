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

test('RuleDiagnosticModal shows sustained throughput only for HTTP', () => {
  const source = read('RuleDiagnosticModal.vue')
  assert.match(source, /const showThroughputMetrics = computed\(\(\) => isHTTP\.value\)/)
  assert.match(source, /持续吞吐/)
  assert.match(source, /v-if="showThroughputMetrics"/)
})

test('RuleDiagnosticModal removes legacy bandwidth copy', () => {
  const source = read('RuleDiagnosticModal.vue')
  assert.doesNotMatch(source, /评估带宽/)
  assert.doesNotMatch(source, /formatBandwidth/)
})

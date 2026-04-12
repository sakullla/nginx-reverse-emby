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

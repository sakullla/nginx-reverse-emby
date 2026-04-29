import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)
const frontendRoot = path.resolve(__dirname, '..')

function readFrontendFile(relativePath) {
  return fs.readFileSync(path.join(frontendRoot, relativePath), 'utf8')
}

test('index declares a favicon served from /favicon.ico', () => {
  const html = readFrontendFile('index.html')
  assert.match(
    html,
    /<link\s+rel="icon"[^>]+href="\/favicon\.ico"/,
    'expected index.html to declare a /favicon.ico favicon'
  )
})

test('search inputs expose a stable name attribute for autofill and a11y tooling', () => {
  const checks = [
    ['src/pages/CertsPage.vue', /<input[^>]*name='certificate-search'[^>]*placeholder='搜索域名 \/ 标签 \/ #id=\.\.\.'/],
    ['src/pages/RulesPage.vue', /<input[^>]*name="rule-search"[^>]*placeholder="搜索 URL \/ 标签 \/ #id=\.\.\."/],
    ['src/pages/L4RulesPage.vue', /<input[^>]*name="l4-rule-search"[^>]*placeholder="搜索协议 \/ 地址 \/ 端口 \/ 标签 \/ #id=\.\.\."/],
    ['src/components/layout/GlobalSearch.vue', /<input[^>]*name="global-search"[^>]*class="global-search-input"/],
    ['src/components/layout/TopBar.vue', /<input[^>]*name="agent-switcher-search"[^>]*class="agent-switcher__search-input"/]
  ]

  for (const [relativePath, pattern] of checks) {
    const source = readFrontendFile(relativePath)
    assert.match(source, pattern, `expected ${relativePath} to add a name attribute to its search input`)
  }
})

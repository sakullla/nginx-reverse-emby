import { describe, expect, it } from 'vitest'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)
const srcDir = path.resolve(__dirname, '..')

function read(relativePath) {
  return fs.readFileSync(path.resolve(srcDir, relativePath), 'utf8')
}

function assertCanonicalBackendsOnly(relativePath, label) {
  const source = read(relativePath)
  expect(source, `${label} should not fall back to backend_url`).not.toMatch(/backend_url/)
  expect(source, `${label} should not fall back to upstream_host`).not.toMatch(/upstream_host/)
  expect(source, `${label} should not fall back to upstream_port`).not.toMatch(/upstream_port/)
}

describe('canonical backend display helpers', () => {
  it('HTTP search and display helpers use backends only', () => {
    assertCanonicalBackendsOnly('components/GlobalSearch.vue', 'GlobalSearch')
    assertCanonicalBackendsOnly('hooks/useGlobalSearch.js', 'useGlobalSearch')
    assertCanonicalBackendsOnly('pages/RulesPage.vue', 'RulesPage')
    assertCanonicalBackendsOnly('components/rules/RuleCard.vue', 'RuleCard')
    assertCanonicalBackendsOnly('components/rules/RuleTable.vue', 'RuleTable')
  })

  it('L4 search and display helpers use backends only', () => {
    assertCanonicalBackendsOnly('pages/L4RulesPage.vue', 'L4RulesPage')
    assertCanonicalBackendsOnly('components/l4/L4RuleItem.vue', 'L4RuleItem')
    assertCanonicalBackendsOnly('pages/AgentDetailPage.vue', 'AgentDetailPage')
  })
})

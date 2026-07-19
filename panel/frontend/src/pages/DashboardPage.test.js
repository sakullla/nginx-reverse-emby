// @vitest-environment node

import { describe, expect, it } from 'vitest'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)

function readPage() {
  return fs.readFileSync(path.resolve(__dirname, 'DashboardPage.vue'), 'utf8')
}

describe('DashboardPage', () => {
  it('renders compact top quick-action cards for nodes and rule creation', () => {
    const page = readPage()
    expect(page).toContain('class="dashboard__actions dashboard__actions--cards"')
    expect(page).toContain('dashboard__action-card')
    expect(page).toContain('查看全部节点')
    expect(page).toContain('创建 HTTP 规则')
    expect(page).toContain('创建 L4 规则')
    expect(page).not.toContain('查看离线节点')
  })

  it('links rule creation to the rules/l4 pages with the default agent id', () => {
    const page = readPage()
    expect(page).toMatch(/:to="`\/rules\?agentId=\$\{defaultAgentId\}`"/)
    expect(page).toMatch(/:to="`\/l4\?agentId=\$\{defaultAgentId\}`"/)
  })

  it('disables rule creation when there is no available agent', () => {
    const page = readPage()
    expect(page).toContain('v-if="defaultAgentId"')
    expect(page).toContain('dashboard__action-card--disabled')
  })

  it('shows four compact stat cards including a certificate card', () => {
    const page = readPage()
    expect(page).toContain('label="节点健康"')
    expect(page).toContain('label="HTTP 规则"')
    expect(page).toContain('label="L4 规则"')
    expect(page).toContain('label="证书"')
    expect(page).toMatch(/size="md"/g)
    expect(page).toContain('certCount')
    expect(page).toContain('expiringCount')
    expect(page).toContain('certTone')
    expect(page).toContain('certSubLabel')
    expect(page).toContain('`/certs?agentId=${defaultAgentId}`')
  })

  it('shows a single node-health card with online / total and progress', () => {
    const page = readPage()
    expect(page).toContain('label="节点健康"')
    expect(page).toContain(':progress="onlinePercent"')
    expect(page).toMatch(/value="`\$\{onlineCount\} \/ \$\{agents\?\.length \|\| 0\}`"/)
  })

  it('keeps HTTP and L4 rule stat cards linked to their list pages', () => {
    const page = readPage()
    expect(page).toContain('label="HTTP 规则"')
    expect(page).toContain('to="/rules"')
    expect(page).toContain('label="L4 规则"')
    expect(page).toContain('to="/l4"')
  })

  it('computes a default agent id preferring the first online agent', () => {
    const page = readPage()
    expect(page).toContain('const defaultAgentId = computed(() => {')
    expect(page).toMatch(/const online = list\.find\(a => a\.status === 'online'\)/)
    expect(page).toContain("return online?.id || list[0].id")
  })

  it('uses the certificates hook for the default agent', () => {
    const page = readPage()
    expect(page).toContain("import { useCertificates } from '../hooks/useCertificates'")
    expect(page).toContain('useCertificates(defaultAgentId)')
  })

  it('does not reference excluded modules in the template or script', () => {
    const page = readPage()
    expect(page).not.toContain('versions')
    expect(page).not.toContain('relay-listeners')
    expect(page).not.toContain('Relay')
  })

  it('retains the agent table with clickable navigation', () => {
    const page = readPage()
    expect(page).toContain('<AgentTable')
    expect(page).toContain(':clickable="true"')
    expect(page).toContain('navigateToAgent')
  })
})

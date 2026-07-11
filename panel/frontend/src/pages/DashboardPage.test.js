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
  it('renders a top quick-action bar with node and rule entry points', () => {
    const page = readPage()
    expect(page).toContain('class="dashboard__actions"')
    expect(page).toContain('查看全部节点')
    expect(page).toContain('查看离线节点')
    expect(page).toContain('创建 HTTP 规则')
    expect(page).toContain('创建 L4 规则')
  })

  it('links rule creation to the rules/l4 pages with the default agent id', () => {
    const page = readPage()
    expect(page).toMatch(/:to="`\/rules\?agentId=\$\{defaultAgentId\}`"/)
    expect(page).toMatch(/:to="`\/l4\?agentId=\$\{defaultAgentId\}`"/)
  })

  it('disables rule creation when there is no available agent', () => {
    const page = readPage()
    expect(page).toContain('v-if="defaultAgentId"')
    expect(page).toContain('dashboard__action--disabled')
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

  it('does not reference excluded modules in the template or script', () => {
    const page = readPage()
    expect(page).not.toContain('versions')
    expect(page).not.toContain('certs')
    expect(page).not.toContain('relay-listeners')
    expect(page).not.toContain('Relay')
    expect(page).not.toContain('Certificate')
  })

  it('retains the agent table with clickable navigation', () => {
    const page = readPage()
    expect(page).toContain('<AgentTable')
    expect(page).toContain(':clickable="true"')
    expect(page).toContain('navigateToAgent')
  })
})

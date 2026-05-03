import { describe, expect, it } from 'vitest'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)

function readPage() {
  return fs.readFileSync(path.resolve(__dirname, 'AgentDetailPage.vue'), 'utf8')
}

describe('AgentDetailPage', () => {
  it('hides outbound proxy editing for embedded local agents', () => {
    expect(readPage()).toMatch(/v-if="!agent\.is_local"\s+class="agent-setting"/)
  })

  it('renders traffic tab when traffic stats enabled', () => {
    const source = readPage()

    expect(source).toContain('fetchAgentStats')
    expect(source).toContain('fetchSystemInfo')
    expect(source).toContain('traffic_stats_enabled')
    expect(source).toContain("trafficStatsEnabled.value ? [{ id: 'traffic', label: '流量统计' }] : []")
    expect(source).toContain('流量统计')
    expect(source).toContain('趋势')
    expect(source).toContain('月额度')
    expect(source).toContain('校准')
    expect(source).toContain('清理')
    expect(source).toContain('formatBytes')
    expect(source).toContain('rx_bytes')
    expect(source).toContain('tx_bytes')
  })

  it('hides traffic tab when traffic stats disabled', () => {
    const source = readPage()

    expect(source).toContain('traffic_stats_enabled !== false')
    expect(source).not.toContain("{ id: 'traffic', label: '流量统计' },")
  })
})

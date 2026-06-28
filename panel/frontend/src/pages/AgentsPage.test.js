import { describe, expect, it } from 'vitest'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)

function readPage() {
  return fs.readFileSync(path.resolve(__dirname, 'AgentsPage.vue'), 'utf8')
}

describe('AgentsPage', () => {
  it('uses monitor cards as the default card-like view', () => {
    expect(readPage()).toContain("view === 'monitor'")
    expect(readPage()).toContain('AgentMonitorCard')
    expect(readPage()).toContain('useAgentMonitorStream')
  })

  it('only enables monitor stream in monitor view', () => {
    const page = readPage()
    expect(page).toMatch(/useAgentMonitorStream\s*\(\s*\{[\s\S]*enabled[\s\S]*\}\s*\)/)
    expect(page).toMatch(/view\.value\s*===\s*['"]monitor['"]/)
  })

  it('hides outbound proxy editing for embedded local agents', () => {
    expect(readPage()).toMatch(/v-if="!editingAgent\?\.is_local"\s+class="form-group"/)
  })

  it('omits outbound proxy updates for embedded local agents', () => {
    expect(readPage()).toMatch(/if \(!editingAgent\.value\.is_local\) \{[\s\S]*buildOutboundProxyPayload/)
  })

  it('uses onScopeDispose to clean up pending async work and timers', () => {
    const page = readPage()
    expect(page).toContain('onScopeDispose')
    expect(page).toMatch(/clearTimeout\s*\(\s*copyTimeout\s*\)/)
  })

  it('guards systemInfo fetch callback against unmounted scope', () => {
    const page = readPage()
    expect(page).toMatch(/fetchSystemInfo\s*\(\s*\)\s*\.then\s*\(\s*info\s*=>\s*\{\s*if\s*\(\s*!\s*disposed\s*\)/)
  })
})

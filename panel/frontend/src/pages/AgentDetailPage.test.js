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
})

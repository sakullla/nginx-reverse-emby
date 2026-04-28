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
  it('hides outbound proxy editing for embedded local agents', () => {
    expect(readPage()).toMatch(/v-if="!editingAgent\?\.is_local"\s+class="form-group"/)
  })

  it('omits outbound proxy updates for embedded local agents', () => {
    expect(readPage()).toMatch(/if \(!editingAgent\.value\.is_local\) \{[\s\S]*buildOutboundProxyPayload/)
  })
})

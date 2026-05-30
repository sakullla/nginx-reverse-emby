import { describe, expect, it } from 'vitest'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)

function read(name) {
  return fs.readFileSync(path.join(__dirname, name), 'utf8')
}

describe('relay layer payloads', () => {
  it('sends HTTP relay layers without legacy relay_chain', () => {
    const source = read('RuleForm.vue')
    expect(source).toMatch(/relay_layers:\s*Array\.isArray\(form\.value\.relay_layers\)/)
    expect(source).not.toMatch(/relay_chain:\s*flattenRelayLayers\(form\.value\.relay_layers\)/)
  })

  it('sends L4 relay layers without legacy relay_chain', () => {
    const source = read('L4RuleForm.vue')
    expect(source).toMatch(/relay_layers:\s*Array\.isArray\(form\.value\.relay_layers\)/)
    expect(source).not.toMatch(/relay_chain:\s*flattenRelayLayers\(form\.value\.relay_layers\)/)
  })
})

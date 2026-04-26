import { describe, expect, it } from 'vitest'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)

function read(name) {
  return fs.readFileSync(path.join(__dirname, name), 'utf8')
}

describe('relay layer compatibility fields', () => {
  it('sends one compatible HTTP relay path in legacy relay_chain', () => {
    const source = read('RuleForm.vue')
    expect(source).toMatch(/const id = Number\(layer\[0\]\)/)
    expect(source).toMatch(/result\.push\(id\)/)
    expect(source).not.toMatch(/result\.push\(\.\.\.layer\.map/)
  })

  it('sends one compatible L4 relay path in legacy relay_chain', () => {
    const source = read('L4RuleForm.vue')
    expect(source).toMatch(/const id = Number\(layer\[0\]\)/)
    expect(source).toMatch(/result\.push\(id\)/)
    expect(source).not.toMatch(/result\.push\(\.\.\.layer\.map/)
  })
})

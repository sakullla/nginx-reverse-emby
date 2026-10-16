import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { DOC_LINKS, DOCS_BASE } from './docLinks'

// The docs-site sidebar stays the authoritative route list (see D3 in the
// technical solution), so panel links are verified against it directly.
const docsConfigPath = resolve('../../docs-site/.vitepress/config.mjs')

function documentedDocPaths() {
  const source = readFileSync(docsConfigPath, 'utf8')
  const paths = new Set()
  for (const match of source.matchAll(/link:\s*'(\/[^']*)'/g)) {
    paths.add(match[1])
  }
  return paths
}

describe('docLinks', () => {
  it('exposes absolute documentation-site URLs without file extensions', () => {
    expect(DOCS_BASE).toBe('https://sakullla.github.io/nginx-reverse-emby')
    for (const url of Object.values(DOC_LINKS)) {
      expect(url.startsWith(`${DOCS_BASE}/`)).toBe(true)
      expect(url).not.toMatch(/[?#]/)
      expect(url.endsWith('/')).toBe(false)
      expect(url.endsWith('.html')).toBe(false)
    }
  })

  it('covers the static panel help targets', () => {
    expect(Object.keys(DOC_LINKS).sort()).toEqual(
      ['agents', 'certificates', 'deploy', 'httpRules', 'l4Rules', 'relay'].sort()
    )
  })

  it('matches routes published by the documentation site', () => {
    const documented = documentedDocPaths()
    expect(documented.size).toBeGreaterThan(0)
    for (const [key, url] of Object.entries(DOC_LINKS)) {
      expect(documented.has(url.slice(DOCS_BASE.length)), `${key} must match a docs route`).toBe(true)
    }
  })
})

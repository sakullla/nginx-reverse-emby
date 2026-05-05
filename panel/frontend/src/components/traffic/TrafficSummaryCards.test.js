import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

describe('TrafficSummaryCards', () => {
  it('uses a four-column desktop grid after removing the host summary card', () => {
    const source = readFileSync(resolve(process.cwd(), 'src/components/traffic/TrafficSummaryCards.vue'), 'utf8')

    expect(source).toContain('grid-template-columns: repeat(4, 1fr);')
  })
})

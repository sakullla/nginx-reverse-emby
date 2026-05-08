import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'

const styles = readFileSync('src/styles/index.css', 'utf8')

describe('ApexCharts tooltip theme styles', () => {
  it('binds tooltip chrome to app theme tokens', () => {
    expect(styles).toContain('html[data-theme] .apexcharts-tooltip')
    expect(styles).toContain('background: var(--color-bg-surface-raised)')
    expect(styles).toContain('color: var(--color-text-primary)')
    expect(styles).toContain('html[data-theme] .apexcharts-tooltip .apexcharts-tooltip-title')
  })
})

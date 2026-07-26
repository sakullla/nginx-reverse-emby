import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

// 移动端浏览器（iOS Safari / Android Chrome）的 100vh/90vh 按“地址栏收起后的大视口”
// 计算，弹窗会伸到地址栏/底部工具栏后面。本测试守护弹窗层视口不变量：
// vh 高度必须有 dvh 回退，且底部操作区要留出安全区（小白条）。
const read = (rel) => readFileSync(fileURLToPath(new URL(rel, import.meta.url)), 'utf-8')

describe('modal mobile viewport invariants', () => {
  it('enables viewport-fit=cover so env(safe-area-inset-*) works on iOS', () => {
    const html = read('../../../index.html')
    expect(html).toMatch(/name="viewport"[^>]*viewport-fit=cover/)
  })

  it('pairs every vh-based modal max-height in utilities.css with a dvh override', () => {
    const css = read('../../styles/utilities.css')
    const vhRules = css.match(/max-height:\s*min\(90vh,\s*920px\)/g) ?? []
    const dvhRules = css.match(/max-height:\s*min\(90dvh,\s*920px\)/g) ?? []
    expect(vhRules.length).toBeGreaterThan(0)
    expect(dvhRules.length).toBe(vhRules.length)
  })

  it('keeps the shared modal backdrop clear of the bottom safe area', () => {
    const css = read('../../styles/utilities.css')
    expect(css).toMatch(/env\(safe-area-inset-bottom/)
  })

  it('pairs BaseModal mobile vh heights with dvh overrides and bottom safe-area padding', () => {
    const sfc = read('./BaseModal.vue')
    expect(sfc).toMatch(/max-height:\s*calc\(100dvh - var\(--space-8\)\)/)
    expect(sfc).toMatch(/max-height:\s*100dvh/)
    expect(sfc).toMatch(/env\(safe-area-inset-bottom/)
  })

  it('pairs standalone overlay vh heights with dvh overrides', () => {
    const overlays = [
      ['../GlobalSearch.vue', /max-height:\s*80dvh/],
      ['../AgentPicker.vue', /max-height:\s*70dvh/],
      ['../common/CreateAgentPicker.vue', /max-height:\s*min\(520px,\s*calc\(100dvh - var\(--space-8\)\)\)/],
      ['../../pages/VersionsPage.vue', /max-height:\s*calc\(100dvh - var\(--space-8\)\)/]
    ]
    for (const [rel, pattern] of overlays) {
      expect(read(rel), rel).toMatch(pattern)
    }
  })
})

import test from 'node:test'
import assert from 'node:assert/strict'

import config from './uno.config.js'

test('uno shortcuts only reference configured theme color tokens', () => {
  const colors = config.theme?.colors ?? {}

  assert.equal(colors.surface, 'var(--color-bg-surface)')
  assert.equal(colors.subtle, 'var(--color-bg-subtle)')
  assert.equal(colors.hover, 'var(--color-bg-hover)')
  assert.equal(colors.default, 'var(--color-border-default)')
})

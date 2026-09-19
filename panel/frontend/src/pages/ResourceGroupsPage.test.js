import { describe, expect, it } from 'vitest'
import router from '../router'

function isUsableCatalogPage(path) {
  const record = router.getRoutes().find((route) => route.path === path)
  if (!record) return false
  if (record.redirect) return false
  return Boolean(record.components?.default)
}

describe('ResourceGroupsPage', () => {
  it('leaves /resource-groups for the installed plugins page', () => {
    const record = router.getRoutes().find((route) => route.path === '/resource-groups')
    expect(record?.redirect).toEqual({ name: 'plugins' })
    expect(isUsableCatalogPage('/resource-groups')).toBe(false)
    expect(router.getRoutes().some((route) => route.name === 'resource-groups')).toBe(false)
  })
})

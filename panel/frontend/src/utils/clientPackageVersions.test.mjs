import { describe, expect, it } from 'vitest'
import { compareClientPackageVersions } from './clientPackageVersions.js'

describe('clientPackageVersions', () => {
  it('orders stable releases ahead of same-version prereleases', () => {
    expect(compareClientPackageVersions('1.1.0', '1.1.0-beta.1')).toBeGreaterThan(0)
    expect(compareClientPackageVersions('1.1.0-beta.1', '1.1.0')).toBeLessThan(0)
  })

  it('orders numeric prerelease identifiers numerically', () => {
    expect(compareClientPackageVersions('1.1.0-beta.10', '1.1.0-beta.2')).toBeGreaterThan(0)
  })

  it('preserves internal hyphens in prerelease identifiers', () => {
    expect(compareClientPackageVersions('1.0.0-alpha-beta', '1.0.0-alpha')).toBeGreaterThan(0)
  })
})

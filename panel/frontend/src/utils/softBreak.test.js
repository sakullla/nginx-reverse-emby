import { describe, expect, it } from 'vitest'
import { softBreakParts } from './softBreak'

describe('softBreakParts', () => {
  it('keeps short labels as a single part', () => {
    expect(softBreakParts('集群概览')).toEqual(['集群概览'])
    expect(softBreakParts('')).toEqual([])
  })

  it('offers wrap points after URL punctuation', () => {
    expect(softBreakParts('https://local.jellyfin.staging.proxy.services.internal.company.io')).toEqual([
      'https:',
      '/',
      '/',
      'local.',
      'jellyfin.',
      'staging.',
      'proxy.',
      'services.',
      'internal.',
      'company.',
      'io'
    ])
  })

  it('breaks query strings and hyphens without splitting letters', () => {
    const parts = softBreakParts('edge-1.example.com?q=1')
    expect(parts.join('')).toBe('edge-1.example.com?q=1')
    expect(parts.every((part) => !/[a-z]{2} [a-z]/i.test(part))).toBe(true)
    expect(parts).toContain('edge-')
    expect(parts).toContain('example.')
  })
})

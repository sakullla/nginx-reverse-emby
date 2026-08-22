import { describe, expect, it } from 'vitest'
import { buildFrontendURL, parseFrontendURL } from './frontendURL'

describe('frontendURL', () => {
  it('keeps host-only values and applies the HTTPS flag', () => {
    expect(parseFrontendURL('media.example.com')).toEqual({
      raw: 'media.example.com',
      href: 'https://media.example.com',
      https: true,
      host: 'media.example.com'
    })
    expect(buildFrontendURL('media.example.com', true)).toBe('https://media.example.com')
    expect(buildFrontendURL('media.example.com', false)).toBe('http://media.example.com')
  })

  it('preserves host, port, and path from a complete URL', () => {
    const raw = 'https://doh.hk.966733.xyz:9998/XEIYeThAtsNTjcjwPQhcmc5dEp5U66'
    expect(parseFrontendURL(raw)).toEqual({
      raw,
      href: raw,
      https: true,
      host: 'doh.hk.966733.xyz:9998'
    })
    expect(buildFrontendURL(raw, true)).toBe(raw)
    expect(buildFrontendURL(raw, false)).toBe('http://doh.hk.966733.xyz:9998/XEIYeThAtsNTjcjwPQhcmc5dEp5U66')
  })

  it('preserves path when the scheme is omitted', () => {
    expect(buildFrontendURL('doh.hk.966733.xyz:9998/XEIYeThAtsNTjcjwPQhcmc5dEp5U66', true))
      .toBe('https://doh.hk.966733.xyz:9998/XEIYeThAtsNTjcjwPQhcmc5dEp5U66')
  })

  it('rejects empty or invalid values', () => {
    expect(buildFrontendURL('', true)).toBe('')
    expect(buildFrontendURL('ftp://files.example.com', true)).toBe('')
    expect(parseFrontendURL('https://').host).toBe('')
  })
})

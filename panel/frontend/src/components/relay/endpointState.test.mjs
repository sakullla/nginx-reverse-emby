import { describe, expect, it } from 'vitest'
import {
  parsePublicEndpoint,
  buildPublicEndpoint,
  normalizeBindHosts,
  buildBindHostsText
} from './endpointState.mjs'

describe('relay endpoint state', () => {
  it('parses supported endpoint forms and rejects malformed brackets', () => {
    const cases = [
      ['', { publicHost: '', publicPort: null, isValid: true }],
      [' relay.example.com ', { publicHost: 'relay.example.com', publicPort: null, isValid: true }],
      ['relay.example.com:7443', { publicHost: 'relay.example.com', publicPort: 7443, isValid: true }],
      [' [2001:db8::1]:7443 ', { publicHost: '2001:db8::1', publicPort: 7443, isValid: true }],
      ['2001:db8::1', { publicHost: '2001:db8::1', publicPort: null, isValid: true }],
      ['[2001:db8::1]7443', { publicHost: '', publicPort: null, isValid: false }]
    ]

    for (const [input, expected] of cases) {
      expect(parsePublicEndpoint(input)).toEqual(expected)
    }
  })

  it('builds host and port combinations', () => {
    expect(buildPublicEndpoint({ public_host: '', public_port: null })).toBe('')
    expect(buildPublicEndpoint({ public_host: 'relay.example.com', public_port: null })).toBe('relay.example.com')
    expect(buildPublicEndpoint({ public_host: 'relay.example.com', public_port: 7443 })).toBe('relay.example.com:7443')
    expect(buildPublicEndpoint({ public_host: '2001:db8::1', public_port: 7443 })).toBe('[2001:db8::1]:7443')
  })

  it('normalizes bind hosts and serializes one host per line', () => {
    expect(normalizeBindHosts(' 0.0.0.0 \n\n 127.0.0.1 \n0.0.0.0\n relay.local ')).toEqual([
      '0.0.0.0',
      '127.0.0.1',
      'relay.local'
    ])
    expect(buildBindHostsText(['0.0.0.0', '127.0.0.1'])).toBe('0.0.0.0\n127.0.0.1')
    expect(buildBindHostsText([])).toBe('')
  })
})

import { describe, expect, it } from 'vitest'
import { buildProxyEgressURLPayload } from './proxyEgressURL'

describe('buildProxyEgressURLPayload', () => {
  it('omits unchanged redacted proxy egress URLs from update payloads', () => {
    expect(buildProxyEgressURLPayload(
      'socks://user:xxxxx@127.0.0.1:1080',
      'socks://user:xxxxx@127.0.0.1:1080'
    )).toBeUndefined()
  })

  it('rejects edited redacted proxy egress URLs instead of saving the placeholder password', () => {
    expect(() => buildProxyEgressURLPayload(
      'socks://user:xxxxx@127.0.0.1:1080',
      'socks://user:xxxxx@10.0.0.2:1080'
    )).toThrow(/re-enter/)
  })

  it('returns explicit proxy egress password changes', () => {
    expect(buildProxyEgressURLPayload(
      'socks://user:xxxxx@127.0.0.1:1080',
      'socks://user:new-secret@10.0.0.2:1080'
    )).toBe('socks://user:new-secret@10.0.0.2:1080')
  })

  it('returns an empty URL when proxy egress is cleared', () => {
    expect(buildProxyEgressURLPayload(
      'socks://user:xxxxx@127.0.0.1:1080',
      ''
    )).toBe('')
  })
})

import { describe, expect, it } from 'vitest'
import { buildOutboundProxyPayload } from './outboundProxyURL'

describe('buildOutboundProxyPayload', () => {
  it('omits unchanged redacted proxy passwords from the update payload', () => {
    expect(buildOutboundProxyPayload(
      'socks://user:xxxxx@127.0.0.1:1080',
      'socks://user:xxxxx@127.0.0.1:1080'
    )).toEqual({})
  })

  it('rejects edited redacted proxy passwords instead of saving the placeholder', () => {
    expect(() => buildOutboundProxyPayload(
      'socks://user:xxxxx@127.0.0.1:1080',
      'socks://user:xxxxx@10.0.0.2:1080'
    )).toThrow(/re-enter/)
  })

  it('keeps explicit proxy password changes in the update payload', () => {
    expect(buildOutboundProxyPayload(
      'socks://user:xxxxx@127.0.0.1:1080',
      'socks://user:new-secret@10.0.0.2:1080'
    )).toEqual({
      outbound_proxy_url: 'socks://user:new-secret@10.0.0.2:1080'
    })
  })
})

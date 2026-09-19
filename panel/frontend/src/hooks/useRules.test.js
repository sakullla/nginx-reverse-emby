import { describe, expect, it } from 'vitest'
import { formatRuleMutationError } from './useRules'

describe('formatRuleMutationError', () => {
  it('turns the master DNS target constraint into an actionable Chinese message', () => {
    const error = new Error('request failed')
    error.response = {
      data: {
        message: 'invalid argument: master_cf_dns certificates can only target the local master agent; use local_http01 for remote agents'
      }
    }

    expect(formatRuleMutationError(error).message).toBe(
      '远程节点不能使用主控 DNS 证书，请改用“节点 HTTP-01”证书，或将规则绑定到本地主控节点。'
    )
  })

  it('preserves unrelated errors', () => {
    const error = new Error('network unavailable')
    expect(formatRuleMutationError(error)).toBe(error)
  })
})

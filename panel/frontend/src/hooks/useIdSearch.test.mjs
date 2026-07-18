// @vitest-environment node

import { describe, expect, it } from 'vitest'

import { exactIdItems } from './useIdSearch.js'

describe('exact ID pagination fallback', () => {
  it('materializes a resolved record outside the current page', () => {
    const record = { id: 57, frontend_url: 'https://media.example.com' }
    expect(exactIdItems({
      search: '#id=57',
      pageItems: [{ id: 1 }],
      resolvedMatch: { agentId: 'edge-1', record },
      agentFilter: 'edge-1'
    })).toEqual([{ ...record, agent_id: 'edge-1' }])
  })
})

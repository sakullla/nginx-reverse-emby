import { afterEach, describe, expect, it } from 'vitest'
import { api } from './client'

describe('api client', () => {
  const originalAdapter = api.defaults.adapter

  afterEach(() => {
    api.defaults.adapter = originalAdapter
  })

  it('does not send application/json for FormData uploads', async () => {
    const seenConfigs = []
    api.defaults.adapter = async (config) => {
      seenConfigs.push(config)
      return {
        data: { ok: true },
        status: 200,
        statusText: 'OK',
        headers: {},
        config
      }
    }

    const formData = new FormData()
    formData.append('file', new Blob(['backup-data']), 'backup.tar.gz')

    await api.post('/system/backup/import', formData, { timeout: 0 })

    expect(seenConfigs).toHaveLength(1)
    expect(seenConfigs[0].headers.getContentType()).not.toBe('application/json')
  })
})

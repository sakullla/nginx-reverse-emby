// @vitest-environment node

import { afterEach, describe, expect, it } from 'vitest'
import { api } from './client'
import { clearCredentials, getStoredAuthToken, setAuthToken } from './authState'

describe('api client', () => {
  const originalAdapter = api.defaults.adapter

  afterEach(() => {
    api.defaults.adapter = originalAdapter
    clearCredentials()
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

  it('preserves the typed quota context from a real 429 response', async () => {
    const quota = {
      metric: 'rule_count',
      resource_group_id: 'group-a',
      current: 2,
      limit: 1,
      recovery_condition: 'delete a rule'
    }
    api.defaults.adapter = async (config) => {
      const error = new Error('Request failed with status code 429')
      error.response = {
        data: { code: 'quota_exceeded', message: 'quota exceeded', quota_context: quota },
        status: 429,
        statusText: 'Too Many Requests',
        headers: {},
        config
      }
      throw error
    }

    await expect(api.post('/rules', {})).rejects.toMatchObject({
      status: 429,
      code: 'quota_exceeded',
      context: quota
    })
  })

  it('clears the current credentials after a 401 response', async () => {
    setAuthToken('expired-panel-token')
    api.defaults.adapter = async (config) => {
      const error = new Error('Request failed with status code 401')
      error.config = config
      error.response = { data: {}, status: 401, statusText: 'Unauthorized', headers: {}, config }
      throw error
    }

    await expect(api.get('/agents')).rejects.toMatchObject({ status: 401 })
    expect(getStoredAuthToken()).toBeNull()
  })

  it('discards an old identity response without clearing replacement credentials', async () => {
    setAuthToken('administrator-token')
    let rejectRequest
    let markAdapterStarted
    const adapterStarted = new Promise(resolve => { markAdapterStarted = resolve })
    api.defaults.adapter = config => new Promise((resolve, reject) => {
      rejectRequest = () => {
        const error = new Error('Request failed with status code 401')
        error.config = config
        error.response = { data: {}, status: 401, statusText: 'Unauthorized', headers: {}, config }
        reject(error)
      }
      markAdapterStarted()
    })

    const request = api.get('/agents')
    await adapterStarted
    setAuthToken('restricted-token')
    rejectRequest()

    await expect(request).rejects.toMatchObject({ code: 'credential_identity_changed' })
    expect(getStoredAuthToken()).toBe('restricted-token')
  })
})

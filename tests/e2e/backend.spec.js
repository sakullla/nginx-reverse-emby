const { test, expect } = require('@playwright/test')
const { startBackend, expectOk } = require('./backend-helper')

test('registers a NAT agent, updates heartbeat, and marks it offline after timeout', async () => {
  const backend = await startBackend({ heartbeatTimeoutMs: 250 })

  try {
    const registerResponse = await backend.publicApi.post('/api/agents/register', {
      data: {
        register_token: 'register-token',
        name: 'nat-edge',
        agent_token: 'edge-token',
        version: '1.0.0',
        mode: 'pull',
        tags: ['edge', 'nat']
      }
    })
    const registerPayload = await expectOk(registerResponse)
    const agentId = registerPayload.agent.id

    let agentsPayload = await expectOk(await backend.api.get('/api/agents'))
    let remote = agentsPayload.agents.find((agent) => agent.id === agentId)
    expect(remote).toBeTruthy()
    expect(remote.status).toBe('offline')
    expect(remote.agent_url).toBe('')

    const heartbeatPayload = await expectOk(
      await backend.publicApi.post('/api/agents/heartbeat', {
        data: {
          name: 'nat-edge',
          agent_token: 'edge-token',
          current_revision: 0,
          version: '1.0.1',
          tags: ['edge', 'nat'],
          stats: {
            totalRequests: '42',
            status: 'ok'
          }
        }
      })
    )
    expect(heartbeatPayload.sync.has_update).toBe(false)

    agentsPayload = await expectOk(await backend.api.get('/api/agents'))
    remote = agentsPayload.agents.find((agent) => agent.id === agentId)
    expect(remote.status).toBe('online')
    expect(remote.mode).toBe('pull')
    expect(remote.agent_url).toBe('')

    const deleteLocalResponse = await backend.api.delete('/api/agents/local')
    expect(deleteLocalResponse.status()).toBe(400)

    await new Promise((resolve) => setTimeout(resolve, 450))
    agentsPayload = await expectOk(await backend.api.get('/api/agents'))
    remote = agentsPayload.agents.find((agent) => agent.id === agentId)
    expect(remote.status).toBe('offline')
  } finally {
    await backend.stop()
  }
})

test('syncs remote rule revisions through heartbeat and stores reported stats', async () => {
  const backend = await startBackend()

  try {
    const registerPayload = await expectOk(
      await backend.publicApi.post('/api/agents/register', {
        data: {
          register_token: 'register-token',
          name: 'edge-sync',
          agent_token: 'sync-token',
          mode: 'pull',
          tags: ['edge']
        }
      })
    )
    const agentId = registerPayload.agent.id

    await expectOk(
      await backend.publicApi.post('/api/agents/heartbeat', {
        data: {
          name: 'edge-sync',
          agent_token: 'sync-token',
          current_revision: 0
        }
      })
    )

    const createRulePayload = await expectOk(
      await backend.api.post(`/api/agents/${agentId}/rules`, {
        data: {
          frontend_url: 'https://edge.example.com',
          backend_url: 'http://10.0.0.2:8096',
          tags: ['edge'],
          enabled: true,
          proxy_redirect: true
        }
      })
    )
    expect(createRulePayload.rule.frontend_url).toBe('https://edge.example.com')

    let agentPayload = await expectOk(await backend.api.get(`/api/agents/${agentId}`))
    expect(agentPayload.agent.desired_revision).toBe(1)
    expect(agentPayload.agent.current_revision).toBe(0)

    const heartbeatSyncPayload = await expectOk(
      await backend.publicApi.post('/api/agents/heartbeat', {
        data: {
          name: 'edge-sync',
          agent_token: 'sync-token',
          current_revision: 0
        }
      })
    )
    expect(heartbeatSyncPayload.sync.has_update).toBe(true)
    expect(heartbeatSyncPayload.sync.rules).toHaveLength(1)
    expect(heartbeatSyncPayload.sync.desired_revision).toBe(1)

    const applyPayload = await expectOk(await backend.api.post(`/api/agents/${agentId}/apply`, { data: {} }))
    expect(applyPayload.message).toContain('heartbeat')

    agentPayload = await expectOk(await backend.api.get(`/api/agents/${agentId}`))
    expect(agentPayload.agent.desired_revision).toBe(2)
    expect(agentPayload.agent.current_revision).toBe(0)

    const heartbeatDonePayload = await expectOk(
      await backend.publicApi.post('/api/agents/heartbeat', {
        data: {
          name: 'edge-sync',
          agent_token: 'sync-token',
          current_revision: 2,
          last_apply_status: 'success',
          last_apply_message: 'done',
          stats: {
            totalRequests: '108',
            status: 'healthy'
          }
        }
      })
    )
    expect(heartbeatDonePayload.sync.has_update).toBe(false)
    expect(heartbeatDonePayload.agent.current_revision).toBe(2)

    const statsPayload = await expectOk(await backend.api.get(`/api/agents/${agentId}/stats`))
    expect(statsPayload.stats.totalRequests).toBe('108')
    expect(statsPayload.stats.status).toBe('healthy')
  } finally {
    await backend.stop()
  }
})

test('uses the mock apply command for local apply and surfaces apply failures', async () => {
  const successBackend = await startBackend()

  try {
    let payload = await expectOk(await successBackend.api.post('/api/agents/local/apply', { data: {} }))
    expect(payload.message).toBe('applied')
    expect(successBackend.readApplyLog()).toHaveLength(1)

    payload = await expectOk(
      await successBackend.api.post('/api/agents/local/rules', {
        data: {
          frontend_url: 'https://local.example.com',
          backend_url: 'http://127.0.0.1:8096',
          tags: ['local']
        }
      })
    )
    expect(payload.rule.id).toBe(1)
    expect(successBackend.readApplyLog()).toHaveLength(2)
  } finally {
    await successBackend.stop()
  }

  const failingBackend = await startBackend({ failApply: true })
  try {
    const response = await failingBackend.api.post('/api/agents/local/apply', { data: {} })
    expect(response.status()).toBe(400)
    const payload = await response.json()
    expect(payload.message).toContain('failed to sync/apply agent config')
    expect(payload.details).toContain('mock apply failed')
  } finally {
    await failingBackend.stop()
  }
})

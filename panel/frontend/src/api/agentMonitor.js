import { getStoredAuthToken } from './authState'
import { createNDJSONParser } from '../utils/agentMonitor'

export const AGENT_MONITOR_STREAM_PATH = '/panel-api/agents/monitor-stream'

export async function consumeAgentMonitorStream({ signal, onMessage, fetchImpl = globalThis.fetch } = {}) {
  if (typeof fetchImpl !== 'function') {
    throw new Error('fetch is unavailable')
  }

  const headers = {}
  const token = getStoredAuthToken()
  if (token) headers['X-Panel-Token'] = token

  const response = await fetchImpl(AGENT_MONITOR_STREAM_PATH, {
    method: 'GET',
    headers,
    signal
  })
  if (!response.ok) {
    const error = new Error(`monitor stream failed: ${response.status}`)
    error.status = response.status
    throw error
  }
  if (!response.body?.getReader) {
    throw new Error('monitor stream body is not readable')
  }

  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  const parser = createNDJSONParser(onMessage)
  try {
    while (true) {
      const { value, done } = await reader.read()
      if (done) break
      parser.push(decoder.decode(value, { stream: true }))
    }
    parser.push(decoder.decode())
    parser.flush()
  } finally {
    if (signal?.aborted) {
      await reader.cancel().catch(() => {})
    }
  }
}

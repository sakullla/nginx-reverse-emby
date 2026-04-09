import test from 'node:test'
import assert from 'node:assert/strict'

import { getDefaultTuning, resetTuningForProtocol } from './tuningState.js'

test('resetTuningForProtocol restores TCP defaults after switching back from UDP', () => {
  const udpTuning = getDefaultTuning('udp')
  udpTuning.listen.reuseport = true
  udpTuning.proxy.idle_timeout = '20s'
  udpTuning.proxy.udp_proxy_requests = 9
  udpTuning.proxy.udp_proxy_responses = 11

  const tcpTuning = resetTuningForProtocol(udpTuning, 'tcp')
  const tcpDefaults = getDefaultTuning('tcp')

  assert.deepStrictEqual(tcpTuning, tcpDefaults)
  assert.equal(tcpTuning.listen.reuseport, false)
  assert.equal(tcpTuning.proxy.idle_timeout, '10m')
  assert.equal(tcpTuning.proxy.udp_proxy_requests, null)
  assert.equal(tcpTuning.proxy.udp_proxy_responses, null)
})

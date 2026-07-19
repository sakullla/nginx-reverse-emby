import { expect, it } from 'vitest'

import { getDefaultTuning, resetTuningForProtocol } from './tuningState.js'

it('restores TCP defaults after switching back from UDP', () => {
  const udpTuning = getDefaultTuning('udp')
  udpTuning.listen.reuseport = true
  udpTuning.proxy.idle_timeout = '20s'
  udpTuning.proxy.udp_proxy_requests = 9
  udpTuning.proxy.udp_proxy_responses = 11

  const tcpTuning = resetTuningForProtocol(udpTuning, 'tcp')
  const tcpDefaults = getDefaultTuning('tcp')

  expect(tcpTuning).toEqual(tcpDefaults)
  expect(tcpTuning.listen.reuseport).toBe(false)
  expect(tcpTuning.proxy.idle_timeout).toBe('10m')
  expect(tcpTuning.proxy.udp_proxy_requests).toBeNull()
  expect(tcpTuning.proxy.udp_proxy_responses).toBeNull()
})

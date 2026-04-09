import test from 'node:test'
import assert from 'node:assert/strict'
import {
  parsePublicEndpoint,
  buildPublicEndpoint,
  normalizeBindHosts,
  buildBindHostsText
} from './endpointState.mjs'

test('parsePublicEndpoint supports empty input', () => {
  assert.deepEqual(parsePublicEndpoint(''), {
    publicHost: '',
    publicPort: null,
    isValid: true
  })
})

test('parsePublicEndpoint supports host only', () => {
  assert.deepEqual(parsePublicEndpoint(' relay.example.com '), {
    publicHost: 'relay.example.com',
    publicPort: null,
    isValid: true
  })
})

test('parsePublicEndpoint supports host:port', () => {
  assert.deepEqual(parsePublicEndpoint('relay.example.com:7443'), {
    publicHost: 'relay.example.com',
    publicPort: 7443,
    isValid: true
  })
})

test('buildPublicEndpoint builds empty / host / host:port', () => {
  assert.equal(buildPublicEndpoint({ public_host: '', public_port: null }), '')
  assert.equal(buildPublicEndpoint({ public_host: 'relay.example.com', public_port: null }), 'relay.example.com')
  assert.equal(buildPublicEndpoint({ public_host: 'relay.example.com', public_port: 7443 }), 'relay.example.com:7443')
})

test('normalizeBindHosts trims, removes empty rows, and deduplicates', () => {
  assert.deepEqual(
    normalizeBindHosts(' 0.0.0.0 \n\n 127.0.0.1 \n0.0.0.0\n relay.local '),
    ['0.0.0.0', '127.0.0.1', 'relay.local']
  )
})

test('buildBindHostsText outputs one host per row', () => {
  assert.equal(buildBindHostsText(['0.0.0.0', '127.0.0.1']), '0.0.0.0\n127.0.0.1')
  assert.equal(buildBindHostsText([]), '')
})

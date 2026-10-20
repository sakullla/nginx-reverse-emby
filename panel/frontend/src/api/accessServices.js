import { api } from './client'

function servicePath(id) {
  return `/services/${encodeURIComponent(id)}`
}

function idempotent(key, extra = {}) {
  return { ...extra, headers: { 'Idempotency-Key': key } }
}

export async function fetchAccessServices() {
  const { data } = await api.get('/services')
  return Array.isArray(data?.services) ? data.services : []
}

export async function fetchAccessService(id) {
  const { data } = await api.get(servicePath(id))
  return data
}

export async function createAccessService(payload, idempotencyKey) {
  const { data } = await api.post('/services', payload, idempotent(idempotencyKey))
  return data
}

export async function updateAccessService(id, payload, idempotencyKey) {
  const { data } = await api.put(servicePath(id), payload, idempotent(idempotencyKey))
  return data
}

export async function correctAccessService(id, payload, idempotencyKey) {
  const { data } = await api.post(`${servicePath(id)}/corrections`, payload, idempotent(idempotencyKey))
  return data
}

export async function retryAccessService(id, idempotencyKey) {
  const { data } = await api.post(`${servicePath(id)}/retry`, {}, idempotent(idempotencyKey))
  return data
}

export async function deleteAccessService(id, idempotencyKey) {
  const { data } = await api.delete(servicePath(id), idempotent(idempotencyKey, { data: {} }))
  return data
}

export async function confirmAccessServiceClient(id, payload, idempotencyKey) {
  const { data } = await api.post(`${servicePath(id)}/client-confirmation`, payload, idempotent(idempotencyKey))
  return data
}

export async function retireAccessService(id, idempotencyKey) {
  const { data } = await api.post(`${servicePath(id)}/retire`, {}, idempotent(idempotencyKey))
  return data
}

export async function abandonAccessService(id, idempotencyKey) {
  const { data } = await api.post(`${servicePath(id)}/abandon`, {}, idempotent(idempotencyKey))
  return data
}

import { api } from './client'

const sourcesPath = '/marketplace/sources'

function sourcePath(id, suffix = '') {
  const value = String(id || '').trim()
  if (!value) throw new Error('repository source id is required')
  return `${sourcesPath}/${encodeURIComponent(value)}${suffix}`
}

export async function fetchRepositorySources() {
  const { data } = await api.get(sourcesPath)
  return Array.isArray(data?.sources) ? data.sources : []
}

export async function fetchRepositorySource(id) {
  const { data } = await api.get(sourcePath(id))
  return data.source
}

export async function createRepositorySource(payload) {
  const { data } = await api.post(sourcesPath, payload)
  return data.source
}

export async function updateRepositorySource(id, payload) {
  const { data } = await api.patch(sourcePath(id), payload)
  return data.source
}

export async function deleteRepositorySource(id) {
  const { data } = await api.delete(sourcePath(id))
  return data
}

export async function refreshRepositorySource(id) {
  const { data } = await api.post(sourcePath(id, '/refresh'))
  return data.snapshot
}

export async function fetchRepositoryEntries(id) {
  return (await fetchRepositoryContents(id)).entries
}

export async function fetchRepositoryContents(id) {
  const { data } = await api.get(sourcePath(id, '/entries'))
  return {
    entries: Array.isArray(data?.entries) ? data.entries : [],
    directPlugin: data?.direct_plugin || null
  }
}

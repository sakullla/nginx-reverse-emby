import { api } from './client'
import {
  clearCredentials,
  setSessionToken
} from './authState'

const body = (response) => response.data

export async function login(username, password) {
  clearCredentials()
  const payload = await api.post('/auth/login', { username, password }).then(body)
  setSessionToken(payload.session.token)
  return payload.session
}

export async function logout() {
  try {
    return await api.post('/auth/logout').then(body)
  } finally {
    clearCredentials()
  }
}

export const fetchCurrentActor = () => api.get('/auth/me').then(body).then((data) => data.actor)
export const fetchUsers = () => api.get('/access/users').then(body).then((data) => data.users)
export const createUser = (input) => api.post('/access/users', input).then(body).then((data) => data.user)
export const updateUser = (id, input) => api.put(`/access/users/${encodeURIComponent(id)}`, input).then(body).then((data) => data.user)
export const fetchRoles = () => api.get('/access/roles').then(body).then((data) => data.roles)
export const createRole = (input) => api.post('/access/roles', input).then(body).then((data) => data.role)
export const updateRolePermissions = (id, permissions) => api.put(`/access/roles/${encodeURIComponent(id)}`, { permissions }).then(body).then((data) => data.role)
export const fetchPermissions = () => api.get('/access/permissions').then(body).then((data) => data.permissions)
export const fetchResourceGroups = () => api.get('/access/resource-groups').then(body).then((data) => data.resource_groups)
export const createResourceGroup = (input) => api.post('/access/resource-groups', input).then(body).then((data) => data.resource_group)
export const grantResourceGroup = (input) => api.post('/access/resource-group-grants', input).then(body)
export const bindResource = (input) => api.post('/access/resource-bindings', input).then(body)
export const fetchQuotaPolicies = () => api.get('/access/quota-policies').then(body).then((data) => data.quota_policies)
export const saveQuotaPolicy = (input) => api.post('/access/quota-policies', input).then(body).then((data) => data.quota_policy)
export const fetchAuditEvents = (limit = 100) => api.get('/access/audit-events', { params: { limit } }).then(body).then((data) => data.audit_events)
export const fetchSecrets = () => api.get('/access/secrets').then(body).then((data) => data.secrets)
export const createSecret = (input) => api.post('/access/secrets', input).then(body)
export const rotateSecret = (id, value) => api.post(`/access/secrets/${encodeURIComponent(id)}/rotate`, { value }).then(body).then((data) => data.secret)

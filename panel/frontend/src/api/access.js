import { api } from './client'
import {
  clearCredentials,
  clearSessionToken,
  setSessionToken
} from './authState'

const body = (response) => response.data

const SECRET_KEYS = new Set([
  'password',
  'password_hash',
  'current_password',
  'new_password'
])

function omitSecrets(value) {
  if (!value || typeof value !== 'object') return value
  if (Array.isArray(value)) return value.map(omitSecrets)
  const result = {}
  for (const [key, item] of Object.entries(value)) {
    if (SECRET_KEYS.has(key)) continue
    result[key] = omitSecrets(item)
  }
  return result
}

function withAccessError(promise) {
  return promise.catch((error) => {
    const data = error?.response?.data
    if (data && typeof data === 'object') {
      if (data.fields != null) error.fields = data.fields
      if (data.details != null) error.details = data.details
      if (error.code == null && data.code != null) error.code = data.code
    }
    throw error
  })
}

function userPath(id) {
  return `/access/users/${encodeURIComponent(id)}`
}

function resourceGroupPath(id) {
  return `/access/resource-groups/${encodeURIComponent(id)}`
}

function trimmedQuery(value) {
  return String(value ?? '').trim()
}

function optionalParams(params) {
  const next = {}
  for (const [key, value] of Object.entries(params)) {
    if (value) next[key] = value
  }
  return Object.keys(next).length ? { params: next } : undefined
}

function createUserInput(input = {}) {
  return {
    username: input.username,
    display_name: input.display_name,
    password: input.password,
    role_ids: input.role_ids
  }
}

function updateUserInput(input = {}) {
  const payload = {}
  if (Object.hasOwn(input, 'display_name')) payload.display_name = input.display_name
  if (Object.hasOwn(input, 'role_ids')) payload.role_ids = input.role_ids
  if (Object.hasOwn(input, 'disabled')) payload.disabled = input.disabled
  return payload
}

function createResourceGroupInput(input = {}) {
  return {
    name: input.name,
    description: input.description
  }
}

function updateResourceGroupInput(input = {}) {
  const payload = {}
  if (Object.hasOwn(input, 'name')) payload.name = input.name
  if (Object.hasOwn(input, 'description')) payload.description = input.description
  return payload
}

function resourceGroupGrantInput(input = {}) {
  return {
    subject_kind: input.subject_kind,
    subject_id: input.subject_id,
    resource_group_id: input.resource_group_id
  }
}

function resourceBindingInput(input = {}) {
  return {
    resource_kind: input.resource_kind,
    resource_id: input.resource_id,
    resource_group_id: input.resource_group_id
  }
}

function resourceUnbindInput(input = {}) {
  return {
    resource_kind: input.resource_kind,
    resource_id: input.resource_id
  }
}

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
export function fetchUsers(query) {
  const q = String(query?.q ?? '').trim()
  return withAccessError(api.get('/access/users', q ? { params: { q } } : undefined))
    .then(body)
    .then((data) => (Array.isArray(data.users) ? data.users : []).map(omitSecrets))
}
export function fetchUser(id) {
  return withAccessError(api.get(userPath(id)))
    .then(body)
    .then((data) => omitSecrets(data.user))
}
export function createUser(input) {
  return withAccessError(api.post('/access/users', createUserInput(input)))
    .then(body)
    .then((data) => omitSecrets(data.user))
}
export function updateUser(id, input) {
  return withAccessError(api.put(userPath(id), updateUserInput(input)))
    .then(body)
    .then((data) => omitSecrets(data.user))
}
export async function changePassword(input = {}) {
  const payload = await withAccessError(api.post('/access/me/password', {
    current_password: input.current_password,
    new_password: input.new_password
  })).then(body)
  clearSessionToken()
  return omitSecrets(payload)
}
export function resetUserPassword(id, input = {}) {
  return withAccessError(api.post(`${userPath(id)}/password`, {
    new_password: input.new_password
  })).then(body).then(omitSecrets)
}
export const fetchRoles = () => api.get('/access/roles').then(body).then((data) => data.roles)
export const createRole = (input) => api.post('/access/roles', input).then(body).then((data) => data.role)
export const updateRolePermissions = (id, permissions) => api.put(`/access/roles/${encodeURIComponent(id)}`, { permissions }).then(body).then((data) => data.role)
export const fetchPermissions = () => api.get('/access/permissions').then(body).then((data) => data.permissions)
export function fetchResourceGroups(query) {
  return withAccessError(api.get('/access/resource-groups', optionalParams({ q: trimmedQuery(query?.q) })))
    .then(body)
    .then((data) => (Array.isArray(data.resource_groups) ? data.resource_groups : []))
}
export function fetchResourceGroup(id) {
  return withAccessError(api.get(resourceGroupPath(id)))
    .then(body)
    .then((data) => data.resource_group)
}
export function createResourceGroup(input) {
  return withAccessError(api.post('/access/resource-groups', createResourceGroupInput(input)))
    .then(body)
    .then((data) => data.resource_group)
}
export function updateResourceGroup(id, input) {
  return withAccessError(api.put(resourceGroupPath(id), updateResourceGroupInput(input)))
    .then(body)
    .then((data) => data.resource_group)
}
export function deleteResourceGroup(id) {
  return withAccessError(api.delete(resourceGroupPath(id))).then(body)
}
export function fetchResourceGroupGrants() {
  return withAccessError(api.get('/access/resource-group-grants'))
    .then(body)
    .then((data) => (Array.isArray(data.resource_group_grants) ? data.resource_group_grants : []))
}
export function grantResourceGroup(input) {
  return withAccessError(api.post('/access/resource-group-grants', resourceGroupGrantInput(input))).then(body)
}
export function revokeResourceGroupGrant(input) {
  return withAccessError(api.delete('/access/resource-group-grants', { data: resourceGroupGrantInput(input) })).then(body)
}
export function fetchResources(query) {
  return withAccessError(api.get('/access/resources', optionalParams({
    kind: trimmedQuery(query?.kind),
    q: trimmedQuery(query?.q)
  })))
    .then(body)
    .then((data) => (Array.isArray(data.resources) ? data.resources : []))
}
export function bindResource(input) {
  return withAccessError(api.post('/access/resource-bindings', resourceBindingInput(input))).then(body)
}
export function unbindResource(input) {
  return withAccessError(api.delete('/access/resource-bindings', { data: resourceUnbindInput(input) })).then(body)
}
export const fetchQuotaPolicies = () => api.get('/access/quota-policies').then(body).then((data) => data.quota_policies)
export const fetchQuotaOverview = () => api.get('/access/quota-policies').then(body)
export const saveQuotaPolicy = (input) => api.post('/access/quota-policies', input).then(body).then((data) => data.quota_policy)
export const fetchAuditEvents = (limit = 100) => api.get('/access/audit-events', { params: { limit } }).then(body).then((data) => data.audit_events)
export const fetchSecrets = () => api.get('/access/secrets').then(body).then((data) => data.secrets)
export const createSecret = (input) => api.post('/access/secrets', input).then(body)
export const rotateSecret = (id, value) => api.post(`/access/secrets/${encodeURIComponent(id)}/rotate`, { value }).then(body).then((data) => data.secret)
